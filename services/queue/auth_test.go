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

func TestRequireStaffRejectsMissingGatewaySecret(t *testing.T) {
	staff := requireStaff("gw-secret", "NURSE")
	handler := staff(func(w http.ResponseWriter, r *http.Request, ctx staffContext) {
		t.Fatal("handler should not run without a valid gateway secret")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Org-Id", "org-1")
	req.Header.Set("X-User-Role", "NURSE")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestRequireStaffRejectsDisallowedRole(t *testing.T) {
	staff := requireStaff("gw-secret", "ADMIN", "OWNER")
	handler := staff(func(w http.ResponseWriter, r *http.Request, ctx staffContext) {
		t.Fatal("handler should not run for a role outside allowedRoles")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Gateway-Secret", "gw-secret")
	req.Header.Set("X-Org-Id", "org-1")
	req.Header.Set("X-User-Role", "NURSE")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestRequireStaffPassesContextThrough(t *testing.T) {
	staff := requireStaff("gw-secret", "NURSE", "ADMIN", "OWNER")
	var gotCtx staffContext
	handler := staff(func(w http.ResponseWriter, r *http.Request, ctx staffContext) {
		gotCtx = ctx
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Gateway-Secret", "gw-secret")
	req.Header.Set("X-Org-Id", "org-1")
	req.Header.Set("X-User-Role", "NURSE")
	req.Header.Set("X-User-Email", "nurse@example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if gotCtx.OrgID != "org-1" || gotCtx.Role != "NURSE" || gotCtx.Email != "nurse@example.com" {
		t.Fatalf("staffContext = %+v, want org-1/NURSE/nurse@example.com", gotCtx)
	}
}
