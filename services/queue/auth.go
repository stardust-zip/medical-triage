package main

// Trust-boundary checks for queue-service, mirroring services/triage/triage/context.py in the
// monolith: api-gateway verifies identity and forwards it as headers,
// stripping whatever the client sent (services/gateway/main.go's
// trustedDirector) — this is the one place those headers become a typed
// context, so handlers never read raw headers or trust body fields for
// identity (org_id, nurse_id, etc.).

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

type staffContext struct {
	OrgID string
	Email string
	Role  string
}

var staffRoles = map[string]bool{"OWNER": true, "ADMIN": true, "NURSE": true, "DOCTOR": true}

// requireStaff verifies the request carries a gateway-forwarded staff
// identity and that its role is one of allowedRoles — api-gateway already
// enforces this at the routing layer (services/gateway/main.go), this is
// defense-in-depth so queue-service doesn't rely solely on the gateway
// getting routing right, same as require_roles in the Python monolith.
func requireStaff(gatewaySecret string, allowedRoles ...string) func(func(http.ResponseWriter, *http.Request, staffContext)) http.HandlerFunc {
	roleAllowed := make(map[string]bool, len(allowedRoles))
	for _, role := range allowedRoles {
		roleAllowed[role] = true
	}

	return func(next func(http.ResponseWriter, *http.Request, staffContext)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Gateway-Secret") != gatewaySecret {
				writeJSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "request did not originate from api-gateway")
				return
			}

			orgID := r.Header.Get("X-Org-Id")
			role := r.Header.Get("X-User-Role")
			if orgID == "" || !staffRoles[role] {
				writeJSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid staff identity")
				return
			}
			if !roleAllowed[role] {
				writeJSONError(w, http.StatusForbidden, "FORBIDDEN", "role does not permit this action")
				return
			}

			next(w, r, staffContext{OrgID: orgID, Email: r.Header.Get("X-User-Email"), Role: role})
		}
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
