package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// newUUIDv4 generates a random (v4) UUID.
//
// ponytail: stdlib crypto/rand + a bit of formatting instead of adding
// google/uuid for one call site.
func newUUIDv4() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// --- Patient sessions ---------------------------------------------------

type anonymousSessionRequest struct {
	OrgSlug string `json:"org_slug"`
}

type anonymousSessionResponse struct {
	Token     string `json:"token"`
	SessionID string `json:"session_id"`
	ExpiresAt string `json:"expires_at"`
}

// handleAnonymousSession mints a token-bound, anonymous patient session: no
// login, no free-text patient id supplied by the client — just an org
// binding and a random session id, signed so the gateway can trust it on
// every subsequent request.
func handleAnonymousSession(cfg config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body anonymousSessionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.OrgSlug == "" {
			writeJSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "org_slug is required")
			return
		}

		org, err := cfg.identity.orgBySlug(body.OrgSlug)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, "ORG_NOT_FOUND", "unknown org_slug")
			return
		}

		sessionID := newUUIDv4()
		expiresAt := time.Now().Add(24 * time.Hour)

		token, err := signHS256(Claims{
			"typ":        "patient",
			"org_id":     org.OrgID,
			"session_id": sessionID,
			"exp":        expiresAt.Unix(),
		}, cfg.patientSessionSecret)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "TOKEN_ERROR", "could not mint session")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(anonymousSessionResponse{
			Token:     token,
			SessionID: sessionID,
			ExpiresAt: expiresAt.Format(time.RFC3339),
		})
	}
}

// requirePatientSession verifies the Authorization bearer token is a
// gateway-issued patient session, then forwards org_id/patient_session_id as
// headers the backend trusts. The client never gets to set these directly —
// trustedDirector already strips any client-supplied copies.
func requirePatientSession(cfg config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			writeUnauthorized(w, "missing patient session token")
			return
		}
		claims, err := verifyHS256(token, cfg.patientSessionSecret)
		if err != nil || claims.str("typ") != "patient" {
			writeUnauthorized(w, "invalid or expired patient session")
			return
		}

		r.Header.Set("X-Org-Id", claims.str("org_id"))
		r.Header.Set("X-Patient-Session-Id", claims.str("session_id"))
		next.ServeHTTP(w, r)
	})
}

// --- Staff sessions -------------------------------------------------------

// requireStaff verifies the Authorization bearer token is a valid Supabase
// access token, resolves it to a tenant + role via identity-service, and
// rejects the request unless the role is in allowedRoles. nurse_id and org_id
// on downstream resolve actions therefore always come from this verified
// identity, never from the request body.
func requireStaff(cfg config, next http.Handler, allowedRoles ...string) http.Handler {
	roleAllowed := make(map[string]bool, len(allowedRoles))
	for _, role := range allowedRoles {
		roleAllowed[role] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			writeUnauthorized(w, "missing staff bearer token")
			return
		}
		claims, err := verifyHS256(token, cfg.supabaseJWTSecret)
		if err != nil {
			writeUnauthorized(w, "invalid or expired token")
			return
		}
		authUserID := claims.str("sub")
		if authUserID == "" {
			writeUnauthorized(w, "token missing subject")
			return
		}

		user, err := cfg.identity.userByAuthID(authUserID)
		if err != nil {
			writeForbidden(w, "no tenant membership for this account")
			return
		}
		if !roleAllowed[user.Role] {
			writeForbidden(w, "role does not permit this action")
			return
		}

		r.Header.Set("X-Org-Id", user.OrgID)
		r.Header.Set("X-User-Id", user.UserID)
		r.Header.Set("X-User-Role", user.Role)
		r.Header.Set("X-User-Email", user.Email)
		next.ServeHTTP(w, r)
	})
}
