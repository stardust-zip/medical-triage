package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNotifyTriageResolvedPostsExpectedBody(t *testing.T) {
	var gotPath, gotSecret string
	var gotBody map[string]string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotSecret = r.Header.Get("X-Internal-Secret")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifyTriageResolved(server.URL, "sekret", "org-1", "log-1", "TIM_MACH", "NURSE_APPROVED")

	if gotPath != "/internal/triage/queue-resolved" {
		t.Fatalf("path = %q, want /internal/triage/queue-resolved", gotPath)
	}
	if gotSecret != "sekret" {
		t.Fatalf("X-Internal-Secret = %q, want sekret", gotSecret)
	}
	want := map[string]string{
		"org_id": "org-1", "triage_log_id": "log-1",
		"approved_dept": "TIM_MACH", "resolution_type": "NURSE_APPROVED",
	}
	for k, v := range want {
		if gotBody[k] != v {
			t.Fatalf("body[%q] = %q, want %q", k, gotBody[k], v)
		}
	}
}

func TestNotifyTriageResolvedSkipsEmptyTriageLogID(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifyTriageResolved(server.URL, "sekret", "org-1", "", "TIM_MACH", "NURSE_APPROVED")

	if called {
		t.Fatal("expected no request when triageLogID is empty")
	}
}
