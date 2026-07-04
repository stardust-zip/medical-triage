package main

// Trust-boundary checks for scheduling-service — same conventions as
// services/queue/auth.go and services/identity: internal callers (the
// monolith's triage pipeline) prove themselves with a shared secret, and
// patient-facing routes trust the org_id/patient-session identity
// api-gateway already verified and forwards as headers.

import (
	"encoding/json"
	"net/http"
)

func requireInternalSecret(secret string, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Secret") != secret {
			writeJSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid internal secret")
			return
		}
		next(w, r)
	})
}

type patientContext struct {
	OrgID            string
	PatientSessionID string
}

// requirePatient verifies the request carries a gateway-forwarded patient
// identity (see requirePatientSession in services/gateway/auth.go) — the
// gateway secret proves the request actually came through the gateway, so a
// caller can't reach this service directly and forge X-Org-Id itself.
func requirePatient(gatewaySecret string, next func(http.ResponseWriter, *http.Request, patientContext)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Gateway-Secret") != gatewaySecret {
			writeJSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "request did not originate from api-gateway")
			return
		}

		orgID := r.Header.Get("X-Org-Id")
		patientSessionID := r.Header.Get("X-Patient-Session-Id")
		if orgID == "" || patientSessionID == "" {
			writeJSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing patient session")
			return
		}

		next(w, r, patientContext{OrgID: orgID, PatientSessionID: patientSessionID})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"status": "error", "code": code, "message": msg})
}
