// Command gateway is TriageOS's api-gateway: the one public entry point in
// front of the still-monolithic FastAPI backend (Phase 1 of
// docs/architecture/implementation-plan.md).
//
// Responsibilities (and nothing else, yet — later phases add rate limiting,
// WS brokering, etc.):
//   - Verify the caller's bearer token (Supabase JWT for staff, or a
//     self-issued anonymous session JWT for patients).
//   - Resolve tenant + role via identity-service and forward them to the
//     backend as headers the backend can trust — stripping any
//     client-supplied versions of those same headers first, so a caller can
//     never spoof org/user identity.
//   - Mint anonymous, token-bound patient sessions (no free-text patient id).
package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
)

type config struct {
	port                 string
	backendURL           *url.URL
	identity             *identityClient
	supabaseJWTSecret    string
	patientSessionSecret string
	gatewaySharedSecret  string
	corsOrigins          []string
}

func loadConfig() config {
	backend, err := url.Parse(getenv("BACKEND_URL", "http://localhost:8001"))
	if err != nil {
		log.Fatalf("invalid BACKEND_URL: %v", err)
	}

	return config{
		port:                 getenv("PORT", "8080"),
		backendURL:           backend,
		identity:             newIdentityClient(getenv("IDENTITY_URL", "http://localhost:8082"), mustGetenv("INTERNAL_SHARED_SECRET")),
		supabaseJWTSecret:    mustGetenv("SUPABASE_JWT_SECRET"),
		patientSessionSecret: mustGetenv("PATIENT_SESSION_SECRET"),
		gatewaySharedSecret:  mustGetenv("GATEWAY_SHARED_SECRET"),
		corsOrigins:          strings.Split(getenv("CORS_ORIGINS", "http://localhost:3000"), ","),
	}
}

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
	cfg := loadConfig()

	proxy := httputil.NewSingleHostReverseProxy(cfg.backendURL)
	proxy.Director = trustedDirector(cfg, proxy.Director)

	mux := http.NewServeMux()

	// Patient-facing: mint an anonymous, token-bound session. No login.
	mux.HandleFunc("POST /api/v1/session/anonymous", handleAnonymousSession(cfg))

	// Patient-facing: require a valid patient-session token.
	mux.Handle("POST /api/v1/chat/triage", requirePatientSession(cfg, proxy))
	mux.Handle("POST /api/v1/appointments", requirePatientSession(cfg, proxy))

	// Staff-facing: require a valid Supabase JWT + resolved tenant/role.
	mux.Handle("GET /api/v1/queue/pending", requireStaff(cfg, proxy, "NURSE", "ADMIN", "OWNER"))
	mux.Handle("POST /api/v1/queue/resolve", requireStaff(cfg, proxy, "NURSE", "ADMIN", "OWNER"))
	mux.Handle("POST /api/v1/queue/check-timeouts", requireStaff(cfg, proxy, "NURSE", "ADMIN", "OWNER"))
	mux.Handle("POST /api/v1/admin/seed-red-flags", requireStaff(cfg, proxy, "ADMIN", "OWNER"))

	// Unauthenticated passthrough (meta endpoints only).
	mux.Handle("GET /", proxy)
	mux.Handle("GET /health", proxy)

	handler := withCORS(cfg, mux)

	log.Printf("api-gateway listening on :%s -> backend %s", cfg.port, cfg.backendURL)
	log.Fatal(http.ListenAndServe(":"+cfg.port, handler))
}

// trustedDirector wraps the default reverse-proxy director so every request
// has its trust-boundary headers scrubbed before anything else runs. Actual
// identity headers are added by the auth middlewares below via context, not
// here — this only guarantees a bypass of those middlewares (e.g. hitting
// "/" which has no auth) can't smuggle a forged identity through either.
func trustedDirector(cfg config, base func(*http.Request)) func(*http.Request) {
	return func(r *http.Request) {
		stripIdentityHeaders(r)
		r.Header.Set("X-Gateway-Secret", cfg.gatewaySharedSecret)
		base(r)
	}
}

func stripIdentityHeaders(r *http.Request) {
	r.Header.Del("X-Org-Id")
	r.Header.Del("X-User-Id")
	r.Header.Del("X-User-Role")
	r.Header.Del("X-User-Email")
	r.Header.Del("X-Patient-Session-Id")
	r.Header.Del("X-Gateway-Secret")
}

func withCORS(cfg config, next http.Handler) http.Handler {
	allowed := make(map[string]bool, len(cfg.corsOrigins))
	for _, o := range cfg.corsOrigins {
		allowed[strings.TrimSpace(o)] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowed[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearerToken(r *http.Request) (string, bool) {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return "", false
	}
	return strings.TrimPrefix(auth, prefix), true
}

func writeJSONError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"status":"error","code":"` + code + `","message":"` + msg + `"}`))
}

func writeUnauthorized(w http.ResponseWriter, msg string) {
	writeJSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", msg)
}

func writeForbidden(w http.ResponseWriter, msg string) {
	writeJSONError(w, http.StatusForbidden, "FORBIDDEN", msg)
}
