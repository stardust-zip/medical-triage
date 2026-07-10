package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// POST /internal/queue/items — called server-to-server by the monolith's
// triage pipeline (services/triage/triage/agent.py) when it escalates a case, never by the
// gateway/browser. Protected by X-Internal-Secret, same convention as
// identity-service's /internal/* routes.
// ---------------------------------------------------------------------------

type createItemRequest struct {
	OrgID            string  `json:"org_id"`
	PatientSessionID string  `json:"patient_session_id"`
	ClinicalSummary  string  `json:"clinical_summary"`
	SuggestedDept    *string `json:"suggested_dept"`
	TriageLogID      *string `json:"triage_log_id"`
}

func handleCreateItem(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body createItemRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
			body.OrgID == "" || body.PatientSessionID == "" || body.ClinicalSummary == "" {
			writeJSONError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"org_id, patient_session_id and clinical_summary are required")
			return
		}

		id, err := createQueueItem(r.Context(), pool, body.OrgID, body.PatientSessionID, body.ClinicalSummary, body.SuggestedDept, body.TriageLogID)
		if err != nil {
			log.Printf("create queue item failed: %v", err)
			writeJSONError(w, http.StatusServiceUnavailable, "DB_ERROR", "could not create queue item")
			return
		}

		globalHub.broadcast(body.OrgID, queueChangedMessage)
		writeJSON(w, http.StatusCreated, map[string]string{"queue_id": id})
	}
}

// ---------------------------------------------------------------------------
// GET /api/v1/queue/pending
// ---------------------------------------------------------------------------

type queueItemResponse struct {
	ID              string  `json:"id"`
	PatientID       string  `json:"patient_id"`
	ClinicalSummary string  `json:"clinical_summary"`
	SuggestedDept   *string `json:"suggested_dept"`
	Status          string  `json:"status"`
	CreatedAt       string  `json:"created_at"`
	MinutesWaiting  float64 `json:"minutes_waiting"`
	SLABreached     bool    `json:"sla_breached"`
}

func handlePendingQueue(pool *pgxpool.Pool, slaMinutes int) func(http.ResponseWriter, *http.Request, staffContext) {
	return func(w http.ResponseWriter, r *http.Request, ctx staffContext) {
		rows, err := getPendingQueue(r.Context(), pool, ctx.OrgID)
		if err != nil {
			log.Printf("fetch pending queue failed: %v", err)
			writeJSONError(w, http.StatusServiceUnavailable, "DB_ERROR", "could not fetch pending queue")
			return
		}

		now := time.Now().UTC()
		items := make([]queueItemResponse, 0, len(rows))
		for _, row := range rows {
			minutesWaiting, slaBreached := computeWaitStats(row.CreatedAt, now, slaMinutes)
			items = append(items, queueItemResponse{
				ID:              row.ID,
				PatientID:       row.PatientID,
				ClinicalSummary: row.ClinicalSummary,
				SuggestedDept:   row.SuggestedDept,
				Status:          row.Status,
				CreatedAt:       row.CreatedAt.Format(time.RFC3339),
				MinutesWaiting:  minutesWaiting,
				SLABreached:     slaBreached,
			})
		}

		writeJSON(w, http.StatusOK, map[string]any{"total": len(items), "items": items})
	}
}

// ---------------------------------------------------------------------------
// POST /api/v1/queue/resolve
// ---------------------------------------------------------------------------

type resolveRequest struct {
	QueueID        string `json:"queue_id"`
	ApprovedDept   string `json:"approved_dept"`
	ResolutionType string `json:"resolution_type"`
}

var resolutionTypes = map[string]bool{
	"AI_AUTO": true, "NURSE_APPROVED": true, "NURSE_CORRECTED": true, "DOCTOR_CORRECTED": true,
}

func handleResolveQueue(pool *pgxpool.Pool, triageServiceURL, internalSecret string) func(http.ResponseWriter, *http.Request, staffContext) {
	return func(w http.ResponseWriter, r *http.Request, ctx staffContext) {
		var body resolveRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
			body.QueueID == "" || body.ApprovedDept == "" {
			writeJSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "queue_id and approved_dept are required")
			return
		}
		if body.ResolutionType == "" {
			body.ResolutionType = "NURSE_APPROVED"
		}
		if !resolutionTypes[body.ResolutionType] {
			writeJSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid resolution_type")
			return
		}

		updated, triageLogID, err := resolveQueueItem(r.Context(), pool, ctx.OrgID, body.QueueID, body.ApprovedDept, body.ResolutionType)
		if err != nil {
			log.Printf("resolve queue item failed: %v", err)
			writeJSONError(w, http.StatusServiceUnavailable, "DB_ERROR", "could not resolve queue item")
			return
		}
		if !updated {
			writeJSONError(w, http.StatusNotFound, "NOT_FOUND", "queue item not found or already resolved")
			return
		}

		globalHub.broadcast(ctx.OrgID, queueChangedMessage)

		// Best-effort, off the request path: the nurse's resolve action
		// must never fail because triage-service is slow or down.
		if triageLogID != nil {
			go notifyTriageResolved(triageServiceURL, internalSecret, ctx.OrgID, *triageLogID, body.ApprovedDept, body.ResolutionType)
		}

		action := "Đã duyệt"
		if body.ResolutionType == "NURSE_CORRECTED" {
			action = "Đã sửa"
		}
		writeJSON(w, http.StatusOK, resolveResponse{
			Success:        true,
			QueueID:        body.QueueID,
			FinalDept:      body.ApprovedDept,
			ResolutionType: body.ResolutionType,
			Message: action + ": bệnh nhân được điều phối đến khoa " + body.ApprovedDept +
				" bởi điều dưỡng " + firstNonEmpty(ctx.Email, ctx.OrgID),
		})
	}
}

type resolveResponse struct {
	Success        bool   `json:"success"`
	QueueID        string `json:"queue_id"`
	FinalDept      string `json:"final_dept"`
	ResolutionType string `json:"resolution_type"`
	Message        string `json:"message"`
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// POST /api/v1/queue/check-timeouts — manual on-demand sweep for the
// caller's own tenant, in addition to the automatic ticker in main.go.
// ---------------------------------------------------------------------------

func handleCheckTimeouts(pool *pgxpool.Pool, slaMinutes int) func(http.ResponseWriter, *http.Request, staffContext) {
	return func(w http.ResponseWriter, r *http.Request, ctx staffContext) {
		count, err := markTimedOutItems(r.Context(), pool, ctx.OrgID, slaMinutes)
		if err != nil {
			log.Printf("timeout sweep failed: %v", err)
			writeJSONError(w, http.StatusServiceUnavailable, "DB_ERROR", "could not run SLA sweep")
			return
		}
		if count > 0 {
			globalHub.broadcast(ctx.OrgID, queueChangedMessage)
		}
		writeJSON(w, http.StatusOK, timeoutCheckResponse{
			Success:       true,
			TimedOutCount: count,
			Message:       "Timeout sweep complete",
		})
	}
}

type timeoutCheckResponse struct {
	Success       bool   `json:"success"`
	TimedOutCount int    `json:"timed_out_count"`
	Message       string `json:"message"`
}

// ---------------------------------------------------------------------------
// GET /health
// ---------------------------------------------------------------------------

func handleHealth(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "healthy"})
	}
}
