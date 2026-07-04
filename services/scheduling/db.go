package main

// Data-access layer for scheduling-service, which owns departments, doctors,
// clinics, and appointments (Phase 4 of
// docs/architecture/implementation-plan.md). Every query sets the
// app.org_id Postgres session variable first, same pattern as
// services/queue/db.go and src/agent.py's _set_org_context, so row-level
// security (db/init.sql) enforces tenant isolation.

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// errDoubleBooked means the (org_id, doctor_id, appointment_time) unique
// index rejected the insert — a real conflict, not a retry (see
// createAppointment's idempotency-key check, which runs first).
var errDoubleBooked = errors.New("doctor is already booked at that time")

const (
	constraintNoDoubleBooking = "idx_appointments_no_double_booking"
	constraintIdempotencyKey  = "idx_appointments_idempotency_key"
)

func setOrgContext(ctx context.Context, tx pgx.Tx, orgID string) error {
	_, err := tx.Exec(ctx, `SELECT set_config('app.org_id', $1, true)`, orgID)
	return err
}

type doctor struct {
	ID             string
	Name           string
	Specialty      string
	DepartmentCode string
}

func getDoctorsByDepartment(ctx context.Context, pool *pgxpool.Pool, orgID, departmentCode string) ([]doctor, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if err := setOrgContext(ctx, tx, orgID); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx,
		`SELECT id::text, name, specialty, department_code
		 FROM   doctors
		 WHERE  department_code = $1
		 ORDER  BY name
		 LIMIT  5`,
		departmentCode,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	doctors := []doctor{}
	for rows.Next() {
		var d doctor
		if err := rows.Scan(&d.ID, &d.Name, &d.Specialty, &d.DepartmentCode); err != nil {
			return nil, err
		}
		doctors = append(doctors, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return doctors, tx.Commit(ctx)
}

type clinic struct {
	Name    string
	Address string
}

func getClinicsByDepartment(ctx context.Context, pool *pgxpool.Pool, orgID, departmentCode string) ([]clinic, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if err := setOrgContext(ctx, tx, orgID); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx,
		`SELECT name, address
		 FROM   clinics
		 WHERE  department_code = $1
		 ORDER  BY name`,
		departmentCode,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	clinics := []clinic{}
	for rows.Next() {
		var c clinic
		if err := rows.Scan(&c.Name, &c.Address); err != nil {
			return nil, err
		}
		clinics = append(clinics, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return clinics, tx.Commit(ctx)
}

// createAppointment books doctorID for appointmentTime, honoring both Phase
// 4 requirements at once:
//
//   - Idempotent booking: if idempotencyKey is set and an appointment with
//     that (org_id, idempotency_key) already exists, that row is returned
//     with reused=true instead of erroring or double-inserting — a client
//     retrying the same request (e.g. after a dropped response) gets the
//     original booking back.
//   - Double-booking prevention: two appointments for the same doctor at
//     the same instant can't both succeed, enforced by a unique index so a
//     concurrent request racing past this function's own check still can't
//     slip through. That case surfaces as errDoubleBooked.
func createAppointment(
	ctx context.Context,
	pool *pgxpool.Pool,
	orgID, patientSessionID, doctorID, departmentCode, appointmentTime string,
	idempotencyKey *string,
) (id string, reused bool, err error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback(ctx)

	if err := setOrgContext(ctx, tx, orgID); err != nil {
		return "", false, err
	}

	if idempotencyKey != nil {
		if existingID, found, err := findByIdempotencyKey(ctx, tx, *idempotencyKey); err != nil {
			return "", false, err
		} else if found {
			return existingID, true, tx.Commit(ctx)
		}
	}

	err = tx.QueryRow(ctx,
		`INSERT INTO appointments
			(org_id, patient_id, doctor_id, department_code, appointment_time, idempotency_key)
		 VALUES (current_setting('app.org_id')::uuid, $1, $2::uuid, $3, $4::timestamptz, $5)
		 RETURNING id`,
		patientSessionID, doctorID, departmentCode, appointmentTime, idempotencyKey,
	).Scan(&id)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		switch pgErr.ConstraintName {
		case constraintNoDoubleBooking:
			return "", false, errDoubleBooked
		case constraintIdempotencyKey:
			// Lost a race with a concurrent retry using the same key —
			// the other request's row is the real booking, return it.
			if idempotencyKey != nil {
				if existingID, found, findErr := findByIdempotencyKey(ctx, tx, *idempotencyKey); findErr == nil && found {
					return existingID, true, tx.Commit(ctx)
				}
			}
		}
	}
	if err != nil {
		return "", false, err
	}

	return id, false, tx.Commit(ctx)
}

func findByIdempotencyKey(ctx context.Context, tx pgx.Tx, idempotencyKey string) (string, bool, error) {
	var id string
	err := tx.QueryRow(ctx,
		`SELECT id FROM appointments WHERE idempotency_key = $1`, idempotencyKey,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}
