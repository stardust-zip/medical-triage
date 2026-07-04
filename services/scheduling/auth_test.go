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

func TestRequirePatientRejectsMissingGatewaySecret(t *testing.T) {
	handler := requirePatient("gw-secret", func(w http.ResponseWriter, r *http.Request, ctx patientContext) {
		t.Fatal("handler should not run without a valid gateway secret")
	})

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Org-Id", "org-1")
	req.Header.Set("X-Patient-Session-Id", "session-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestRequirePatientRejectsMissingSessionHeaders(t *testing.T) {
	handler := requirePatient("gw-secret", func(w http.ResponseWriter, r *http.Request, ctx patientContext) {
		t.Fatal("handler should not run without a patient session")
	})

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Gateway-Secret", "gw-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestRequirePatientPassesContextThrough(t *testing.T) {
	var gotCtx patientContext
	handler := requirePatient("gw-secret", func(w http.ResponseWriter, r *http.Request, ctx patientContext) {
		gotCtx = ctx
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Gateway-Secret", "gw-secret")
	req.Header.Set("X-Org-Id", "org-1")
	req.Header.Set("X-Patient-Session-Id", "session-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if gotCtx.OrgID != "org-1" || gotCtx.PatientSessionID != "session-1" {
		t.Fatalf("patientContext = %+v, want org-1/session-1", gotCtx)
	}
}
