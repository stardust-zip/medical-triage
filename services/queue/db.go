package main

// Data-access layer for queue-service, which owns human_triage_queue (Phase
// 3 of docs/architecture/implementation-plan.md). Every query sets the
// app.org_id Postgres session variable first, same as src/agent.py's
// _set_org_context in the monolith, so row-level security (db/init.sql)
// enforces tenant isolation — never an app-layer WHERE org_id = ... filter.

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errNotFound = errors.New("not found")

type queueItem struct {
	ID              string
	PatientID       string
	ClinicalSummary string
	SuggestedDept   *string
	Status          string
	CreatedAt       time.Time
}

func setOrgContext(ctx context.Context, tx pgx.Tx, orgID string) error {
	_, err := tx.Exec(ctx, `SELECT set_config('app.org_id', $1, true)`, orgID)
	return err
}

func createQueueItem(
	ctx context.Context,
	pool *pgxpool.Pool,
	orgID, patientSessionID, clinicalSummary string,
	suggestedDept, triageLogID *string,
) (string, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	if err := setOrgContext(ctx, tx, orgID); err != nil {
		return "", err
	}

	var id string
	err = tx.QueryRow(ctx,
		`INSERT INTO human_triage_queue
			(org_id, patient_id, clinical_summary, suggested_dept, triage_log_id, status)
		 VALUES (current_setting('app.org_id')::uuid, $1, $2, $3, $4, 'PENDING')
		 RETURNING id`,
		patientSessionID, clinicalSummary, suggestedDept, triageLogID,
	).Scan(&id)
	if err != nil {
		return "", err
	}

	return id, tx.Commit(ctx)
}

func getPendingQueue(ctx context.Context, pool *pgxpool.Pool, orgID string) ([]queueItem, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if err := setOrgContext(ctx, tx, orgID); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx,
		`SELECT id, patient_id, clinical_summary, suggested_dept, status, created_at
		 FROM   human_triage_queue
		 WHERE  status = 'PENDING'
		 ORDER  BY created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []queueItem{}
	for rows.Next() {
		var it queueItem
		if err := rows.Scan(
			&it.ID, &it.PatientID, &it.ClinicalSummary, &it.SuggestedDept, &it.Status, &it.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, tx.Commit(ctx)
}

// resolveQueueItem marks queueID RESOLVED and back-fills triage_logs with the
// nurse's final decision. Items created via createQueueItem always carry a
// triage_log_id, so this updates that exact row; queue items that predate
// the triage_log_id column fall back to the monolith's original heuristic
// (most recent triage_logs row matching the AI-suggested department) so
// in-flight items aren't silently orphaned by this migration.
func resolveQueueItem(
	ctx context.Context,
	pool *pgxpool.Pool,
	orgID, queueID, approvedDept, resolutionType string,
) (bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	if err := setOrgContext(ctx, tx, orgID); err != nil {
		return false, err
	}

	var triageLogID *string
	err = tx.QueryRow(ctx,
		`UPDATE human_triage_queue
		 SET    status = 'RESOLVED'
		 WHERE  id = $1 AND status = 'PENDING'
		 RETURNING triage_log_id`,
		queueID,
	).Scan(&triageLogID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	if triageLogID != nil {
		_, err = tx.Exec(ctx,
			`UPDATE triage_logs
			 SET    final_dept = $1, resolution_type = $2::triage_resolution
			 WHERE  id = $3`,
			approvedDept, resolutionType, *triageLogID,
		)
	} else {
		_, err = tx.Exec(ctx,
			`UPDATE triage_logs
			 SET    final_dept = $1, resolution_type = $2::triage_resolution
			 WHERE  id = (
			     SELECT tl.id
			     FROM   triage_logs tl
			     WHERE  tl.ai_suggested_dept = (
			         SELECT suggested_dept FROM human_triage_queue WHERE id = $3
			     )
			     ORDER  BY tl.created_at DESC
			     LIMIT  1
			 )`,
			approvedDept, resolutionType, queueID,
		)
	}
	if err != nil {
		return false, err
	}

	return true, tx.Commit(ctx)
}

func markTimedOutItems(ctx context.Context, pool *pgxpool.Pool, orgID string, slaMinutes int) (int, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	if err := setOrgContext(ctx, tx, orgID); err != nil {
		return 0, err
	}

	tag, err := tx.Exec(ctx,
		`UPDATE human_triage_queue
		 SET    status = 'TIMEOUT'
		 WHERE  status = 'PENDING'
		   AND  created_at < NOW() - ($1 || ' minutes')::INTERVAL`,
		slaMinutes,
	)
	if err != nil {
		return 0, err
	}

	return int(tag.RowsAffected()), tx.Commit(ctx)
}

// listOrgIDs backs the internal SLA-sweep ticker, which has no single
// tenant's request to scope itself to. organizations has no RLS policy
// (identity-service owns it and it isn't itself tenant-scoped data), so this
// is a safe plain read.
func listOrgIDs(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT id FROM organizations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
