// Command gateway is TriageOS's api-gateway: the one public entry point.
// Verifies self-issued session JWTs (staff and patient), forwards trusted
// identity headers to backend services, strips any client-supplied copies.
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
	queueServiceURL      *url.URL
	schedulingServiceURL *url.URL
	identity             *identityClient
	staffSessionSecret   string
	patientSessionSecret string
	gatewaySharedSecret  string
	corsOrigins          []string
}

func loadConfig() config {
	backend, err := url.Parse(getenv("BACKEND_URL", "http://localhost:8001"))
	if err != nil {
		log.Fatalf("invalid BACKEND_URL: %v", err)
	}
	queueService, err := url.Parse(getenv("QUEUE_SERVICE_URL", "http://localhost:8083"))
	if err != nil {
		log.Fatalf("invalid QUEUE_SERVICE_URL: %v", err)
	}
	schedulingService, err := url.Parse(getenv("SCHEDULING_SERVICE_URL", "http://localhost:8084"))
	if err != nil {
		log.Fatalf("invalid SCHEDULING_SERVICE_URL: %v", err)
	}

	return config{
		port:                 getenv("PORT", "8080"),
		backendURL:           backend,
		queueServiceURL:      queueService,
		schedulingServiceURL: schedulingService,
		identity:             newIdentityClient(getenv("IDENTITY_URL", "http://localhost:8082"), mustGetenv("INTERNAL_SHARED_SECRET")),
		staffSessionSecret:   mustGetenv("STAFF_SESSION_SECRET"),
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
	proxy.Director = withGatewaySecret(cfg, proxy.Director)

	// queue-service owns human_triage_queue as of Phase 3 — the monolith no
	// longer serves these routes at all (see src/api.py).
	queueProxy := httputil.NewSingleHostReverseProxy(cfg.queueServiceURL)
	queueProxy.Director = withGatewaySecret(cfg, queueProxy.Director)

	// scheduling-service owns departments/doctors/clinics/appointments as of
	// Phase 4 — same deal, the monolith no longer serves this route either.
	schedulingProxy := httputil.NewSingleHostReverseProxy(cfg.schedulingServiceURL)
	schedulingProxy.Director = withGatewaySecret(cfg, schedulingProxy.Director)

	mux := http.NewServeMux()

	// Patient-facing: mint an anonymous, token-bound session. No login.
	mux.HandleFunc("POST /api/v1/session/anonymous", handleAnonymousSession(cfg))

	// Staff-facing: email+password login, mints a self-issued session JWT.
	mux.HandleFunc("POST /api/v1/auth/staff/login", handleStaffLogin(cfg))

	// Patient-facing: require a valid patient-session token.
	mux.Handle("POST /api/v1/chat/triage", requirePatientSession(cfg, proxy))
	mux.Handle("POST /api/v1/appointments", requirePatientSession(cfg, schedulingProxy))

	// Staff-facing: require a valid staff session token.
	mux.Handle("GET /api/v1/queue/pending", requireStaff(cfg, queueProxy, "NURSE", "ADMIN", "OWNER"))
	mux.Handle("POST /api/v1/queue/resolve", requireStaff(cfg, queueProxy, "NURSE", "ADMIN", "OWNER"))
	mux.Handle("POST /api/v1/queue/check-timeouts", requireStaff(cfg, queueProxy, "NURSE", "ADMIN", "OWNER"))
	mux.Handle("POST /api/v1/admin/seed-red-flags", requireStaff(cfg, proxy, "ADMIN", "OWNER"))

	// Nurse dashboard live updates. Browsers can't set a custom Authorization
	// header on a WebSocket handshake, so the staff token travels as a query
	// param here instead — requireStaffWS is the same checks as requireStaff,
	// just reading the token from a different place.
	mux.Handle("GET /ws/queue", requireStaffWS(cfg, queueProxy, "NURSE", "ADMIN", "OWNER"))

	// Unauthenticated passthrough (meta endpoints only).
	mux.Handle("GET /", proxy)
	mux.Handle("GET /health", proxy)

	// stripClientIdentityHeaders must run before any auth middleware or
	// route handler sees the request — see its doc comment below for why.
	handler := withCORS(cfg, stripClientIdentityHeaders(mux))

	log.Printf("api-gateway listening on :%s -> backend %s", cfg.port, cfg.backendURL)
	log.Fatal(http.ListenAndServe(":"+cfg.port, handler))
}

// stripClientIdentityHeaders removes any identity headers the caller sent
// themselves, before the request reaches routing or auth. This has to run
// as the outermost wrapper around the whole mux, not inside a proxy's
// Director: a Director only fires once a request has already matched a
// route and passed that route's auth middleware — running the strip there
// would wipe out the *trusted* values requireStaff/requirePatientSession
// just set, not just client-forged ones (see auth_test.go's regression test
// for the bug this used to be). Doing it here instead means every route,
// including ones with no auth at all (e.g. "/"), can never have a forged
// X-Org-Id etc. reach a backend.
func stripClientIdentityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del("X-Org-Id")
		r.Header.Del("X-User-Id")
		r.Header.Del("X-User-Role")
		r.Header.Del("X-User-Email")
		r.Header.Del("X-Patient-Session-Id")
		r.Header.Del("X-Gateway-Secret")
		next.ServeHTTP(w, r)
	})
}

// withGatewaySecret wraps a reverse-proxy Director to attach the shared
// secret every downstream service checks (see requireGatewaySecret in
// src/context.py and requireInternalSecret's staff-route counterpart in
// services/queue/auth.go) — proving a request actually came through this
// gateway rather than hitting a service directly.
func withGatewaySecret(cfg config, base func(*http.Request)) func(*http.Request) {
	return func(r *http.Request) {
		r.Header.Set("X-Gateway-Secret", cfg.gatewaySharedSecret)
		base(r)
	}
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
