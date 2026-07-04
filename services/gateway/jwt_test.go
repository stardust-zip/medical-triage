package main

import (
	"testing"
	"time"
)

func TestSignAndVerifyHS256(t *testing.T) {
	token, err := signHS256(Claims{
		"sub": "user-123",
		"exp": time.Now().Add(time.Hour).Unix(),
	}, "secret")
	if err != nil {
		t.Fatalf("signHS256 returned error: %v", err)
	}

	claims, err := verifyHS256(token, "secret")
	if err != nil {
		t.Fatalf("verifyHS256 returned error: %v", err)
	}

	if claims.str("sub") != "user-123" {
		t.Fatalf("sub claim = %q, want user-123", claims.str("sub"))
	}
}

func TestVerifyHS256RejectsWrongSecret(t *testing.T) {
	token, err := signHS256(Claims{
		"sub": "user-123",
		"exp": time.Now().Add(time.Hour).Unix(),
	}, "secret")
	if err != nil {
		t.Fatalf("signHS256 returned error: %v", err)
	}

	if _, err := verifyHS256(token, "other-secret"); err == nil {
		t.Fatal("verifyHS256 accepted token signed with another secret")
	}
}

func TestVerifyHS256RejectsExpiredToken(t *testing.T) {
	token, err := signHS256(Claims{
		"sub": "user-123",
		"exp": time.Now().Add(-time.Hour).Unix(),
	}, "secret")
	if err != nil {
		t.Fatalf("signHS256 returned error: %v", err)
	}

	if _, err := verifyHS256(token, "secret"); err == nil {
		t.Fatal("verifyHS256 accepted expired token")
	}
}
