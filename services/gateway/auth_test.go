package main

import (
	"net/http/httptest"
	"testing"
)

func TestBearerTokenParsesAuthorizationHeader(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer token-123")

	token, ok := bearerToken(req)
	if !ok {
		t.Fatal("bearerToken did not parse a valid bearer token")
	}
	if token != "token-123" {
		t.Fatalf("token = %q, want token-123", token)
	}
}

func TestBearerTokenRejectsNonBearerHeader(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Basic abc")

	if _, ok := bearerToken(req); ok {
		t.Fatal("bearerToken accepted a non-bearer authorization header")
	}
}
