// Command identity is TriageOS's identity-service: owns organizations,
// users, roles, and login (email + password_hash). Every route here is
// internal-only, gated by a shared secret the gateway sends on every call.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func mustGetenv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("missing required env var %s", key)
	}
	return v
}

func main() {
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, mustGetenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()

	secret := mustGetenv("INTERNAL_SHARED_SECRET")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth(pool))
	mux.Handle("GET /internal/organizations/by-slug/{slug}", requireInternalSecret(secret, handleOrgBySlug(pool)))
	mux.Handle("POST /internal/organizations", requireInternalSecret(secret, handleCreateOrganization(pool)))
	mux.Handle("POST /internal/users", requireInternalSecret(secret, handleInviteUser(pool)))
	mux.Handle("POST /internal/auth/login", requireInternalSecret(secret, handleLogin(pool)))

	port := getenv("PORT", "8082")
	log.Printf("identity-service listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

func requireInternalSecret(secret string, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Secret") != secret {
			writeJSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid internal secret")
			return
		}
		next(w, r)
	})
}

func handleHealth(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "healthy"})
	}
}

func handleOrgBySlug(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		o, err := orgBySlug(r.Context(), pool, slug)
		if errors.Is(err, errNotFound) {
			writeJSONError(w, http.StatusNotFound, "NOT_FOUND", "unknown organization slug")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"org_id": o.ID, "name": o.Name})
	}
}

type createOrganizationRequest struct {
	Name          string `json:"name"`
	Slug          string `json:"slug"`
	OwnerPassword string `json:"owner_password"`
	OwnerEmail    string `json:"owner_email"`
}

func handleCreateOrganization(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body createOrganizationRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
			body.Name == "" || body.Slug == "" || body.OwnerPassword == "" || body.OwnerEmail == "" {
			writeJSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name, slug, owner_password, owner_email are required")
			return
		}

		o, u, err := createOrganization(r.Context(), pool, body.Name, body.Slug, body.OwnerPassword, body.OwnerEmail)
		if err != nil {
			writeJSONError(w, http.StatusConflict, "CREATE_FAILED", err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{
			"org_id": o.ID, "owner_user_id": u.ID,
		})
	}
}

type inviteUserRequest struct {
	OrgID    string `json:"org_id"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

var validRoles = map[string]bool{"OWNER": true, "ADMIN": true, "NURSE": true, "DOCTOR": true}

func handleInviteUser(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body inviteUserRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
			body.OrgID == "" || body.Email == "" || body.Password == "" || !validRoles[body.Role] {
			writeJSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "org_id, email, password and a valid role are required")
			return
		}

		u, err := inviteUser(r.Context(), pool, body.OrgID, body.Email, body.Password, body.Role)
		if err != nil {
			writeJSONError(w, http.StatusConflict, "INVITE_FAILED", err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"user_id": u.ID})
	}
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func handleLogin(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body loginRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" || body.Password == "" {
			writeJSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "email and password are required")
			return
		}

		u, err := authenticateUser(r.Context(), pool, body.Email, body.Password)
		if errors.Is(err, errNotFound) || errors.Is(err, errInvalidPassword) {
			writeJSONError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid email or password")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"user_id": u.ID, "org_id": u.OrgID, "role": u.Role, "email": u.Email,
		})
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
