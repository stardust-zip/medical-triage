package main

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHashPasswordRoundTrips(t *testing.T) {
	hash, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if hash == "correct horse battery staple" {
		t.Fatal("hashPassword returned the plaintext password unchanged")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("correct horse battery staple")) != nil {
		t.Fatal("correct password did not verify against its own hash")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("wrong password")) == nil {
		t.Fatal("wrong password verified against a hash it doesn't belong to")
	}
}
