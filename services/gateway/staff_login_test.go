package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeIdentityServer(t *testing.T, loginStatus int, body map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(loginStatus)
		_ = json.NewEncoder(w).Encode(body)
	}))
}

func TestStaffLoginMintsTokenResolvableByRequireStaff(t *testing.T) {
	identitySrv := fakeIdentityServer(t, http.StatusOK, map[string]string{
		"user_id": "user-1", "org_id": "org-1", "role": "NURSE", "email": "nurse@example.com",
	})
	defer identitySrv.Close()

	cfg := config{
		identity:           newIdentityClient(identitySrv.URL, "internal-secret"),
		staffSessionSecret: "staff-secret",
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/staff/login", strings.NewReader(`{"email":"nurse@example.com","password":"correct"}`))
	rec := httptest.NewRecorder()
	handleStaffLogin(cfg)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var loginResp staffLoginResponse
	if err := json.NewDecoder(rec.Body).Decode(&loginResp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if loginResp.Token == "" || loginResp.Role != "NURSE" {
		t.Fatalf("unexpected login response: %+v", loginResp)
	}

	// The minted token must actually authenticate a staff-only route.
	var gotOrgID, gotUserID, gotRole string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOrgID = r.Header.Get("X-Org-Id")
		gotUserID = r.Header.Get("X-User-Id")
		gotRole = r.Header.Get("X-User-Role")
		w.WriteHeader(http.StatusOK)
	})
	handler := requireStaff(cfg, inner, "NURSE", "ADMIN", "OWNER")

	authedReq := httptest.NewRequest(http.MethodGet, "/api/v1/queue/pending", nil)
	authedReq.Header.Set("Authorization", "Bearer "+loginResp.Token)
	authedRec := httptest.NewRecorder()
	handler.ServeHTTP(authedRec, authedReq)

	if authedRec.Code != http.StatusOK {
		t.Fatalf("authed request status = %d, want %d", authedRec.Code, http.StatusOK)
	}
	if gotOrgID != "org-1" || gotUserID != "user-1" || gotRole != "NURSE" {
		t.Fatalf("forwarded headers = org=%q user=%q role=%q", gotOrgID, gotUserID, gotRole)
	}
}

func TestStaffLoginRejectsInvalidCredentials(t *testing.T) {
	identitySrv := fakeIdentityServer(t, http.StatusUnauthorized, map[string]string{
		"status": "error", "code": "INVALID_CREDENTIALS",
	})
	defer identitySrv.Close()

	cfg := config{
		identity:           newIdentityClient(identitySrv.URL, "internal-secret"),
		staffSessionSecret: "staff-secret",
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/staff/login", strings.NewReader(`{"email":"nurse@example.com","password":"wrong"}`))
	rec := httptest.NewRecorder()
	handleStaffLogin(cfg)(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
