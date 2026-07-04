package main

// Minimal HS256 JWT sign/verify.
//
// ponytail: hand-rolled instead of pulling in golang-jwt — we only ever need
// HS256 sign+verify+exp-check for two token kinds (Supabase-issued staff
// tokens, self-issued patient-session tokens). That's ~50 lines of stdlib
// crypto/hmac + encoding/json, so a dependency isn't worth adding. If a
// second algorithm (RS256/JWKS rotation) is ever needed, switch to
// github.com/golang-jwt/jwt/v5 rather than growing this by hand.

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var errInvalidToken = errors.New("invalid or expired token")

// Claims is the decoded JWT payload. We only care about a handful of keys,
// so a plain map keeps this generic across the two token shapes we issue.
type Claims map[string]any

func (c Claims) str(key string) string {
	v, _ := c[key].(string)
	return v
}

func b64Encode(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func b64Decode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// signHS256 mints a JWT with header {"alg":"HS256","typ":"JWT"} and the given
// claims, signed with secret.
func signHS256(claims Claims, secret string) (string, error) {
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := b64Encode(headerJSON) + "." + b64Encode(claimsJSON)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	sig := mac.Sum(nil)

	return signingInput + "." + b64Encode(sig), nil
}

// verifyHS256 checks the signature and "exp" claim of token against secret
// and returns the decoded claims.
func verifyHS256(token string, secret string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errInvalidToken
	}
	signingInput := parts[0] + "." + parts[1]

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	expectedSig := mac.Sum(nil)

	gotSig, err := b64Decode(parts[2])
	if err != nil {
		return nil, errInvalidToken
	}
	if subtle.ConstantTimeCompare(expectedSig, gotSig) != 1 {
		return nil, errInvalidToken
	}

	payload, err := b64Decode(parts[1])
	if err != nil {
		return nil, errInvalidToken
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, errInvalidToken
	}

	if exp, ok := claims["exp"].(float64); ok {
		if time.Now().Unix() > int64(exp) {
			return nil, errInvalidToken
		}
	}

	return claims, nil
}
