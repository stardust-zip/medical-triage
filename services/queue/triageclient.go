package main

// Outbound call to triage-service's /internal/triage/queue-resolved, the
// REST realization of the queue.resolved event (§5.3 of
// docs/architecture/implementation-plan.md) — deferred a real broker since
// today this has exactly one consumer.

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

func notifyTriageResolved(
	triageServiceURL, internalSecret, orgID, triageLogID, approvedDept, resolutionType string,
) {
	if triageLogID == "" {
		return // pre-Phase-3 item with no triage_log_id: nothing to back-fill
	}

	body, _ := json.Marshal(map[string]string{
		"org_id":          orgID,
		"triage_log_id":   triageLogID,
		"approved_dept":   approvedDept,
		"resolution_type": resolutionType,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		triageServiceURL+"/internal/triage/queue-resolved", bytes.NewReader(body))
	if err != nil {
		log.Printf("notifyTriageResolved: build request failed: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Secret", internalSecret)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("notifyTriageResolved: request failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		log.Printf("notifyTriageResolved: triage-service returned %d", resp.StatusCode)
	}
}
