package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireInternalSecretRejectsMissingSecret(t *testing.T) {
	called := false
	handler := requireInternalSecret("secret", func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rec, req)

	if called {
		t.Fatal("handler was called without the internal secret")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestRequireInternalSecretAllowsMatchingSecret(t *testing.T) {
	handler := requireInternalSecret("secret", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Internal-Secret", "secret")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}
