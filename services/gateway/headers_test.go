package main

// Regression test for a real bug caught while extracting queue-service
// (Phase 3): stripping identity headers inside the reverse-proxy Director
// ran *after* requireStaff/requirePatientSession had already set the
// trusted X-Org-Id etc., so it wiped out the real values instead of only
// blocking client-forged ones — every gateway-proxied request forwarded an
// empty tenant/user context to the backend. See main.go's
// stripClientIdentityHeaders doc comment for the fix.

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"
)

func TestTrustedIdentityHeadersSurviveToBackend(t *testing.T) {
	var gotOrgID, gotSecret string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOrgID = r.Header.Get("X-Org-Id")
		gotSecret = r.Header.Get("X-Gateway-Secret")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config{gatewaySharedSecret: "gw-secret"}
	proxy := httputil.NewSingleHostReverseProxy(backendURL)
	proxy.Director = withGatewaySecret(cfg, proxy.Director)

	// Mimics requireStaff: set the verified identity, then forward to proxy.
	authThenProxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Org-Id", "org-123")
		proxy.ServeHTTP(w, r)
	})

	handler := stripClientIdentityHeaders(authThenProxy)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Org-Id", "forged-by-client")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotOrgID != "org-123" {
		t.Fatalf("backend received X-Org-Id = %q, want the trusted org-123 (not empty, not the client-forged value)", gotOrgID)
	}
	if gotSecret != "gw-secret" {
		t.Fatalf("backend received X-Gateway-Secret = %q, want gw-secret", gotSecret)
	}
}

func TestClientCannotForgeIdentityOnUnauthenticatedRoute(t *testing.T) {
	var gotOrgID string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOrgID = r.Header.Get("X-Org-Id")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config{gatewaySharedSecret: "gw-secret"}
	proxy := httputil.NewSingleHostReverseProxy(backendURL)
	proxy.Director = withGatewaySecret(cfg, proxy.Director)

	// No auth middleware at all here — mirrors the unauthenticated "/" route.
	handler := stripClientIdentityHeaders(proxy)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Org-Id", "forged-by-client")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotOrgID != "" {
		t.Fatalf("backend received X-Org-Id = %q, want empty (client-forged header must not reach an unauthenticated route)", gotOrgID)
	}
}
