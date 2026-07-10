package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type identityClient struct {
	baseURL string
	secret  string
	http    *http.Client
}

func newIdentityClient(baseURL, secret string) *identityClient {
	return &identityClient{
		baseURL: baseURL,
		secret:  secret,
		http:    &http.Client{Timeout: 5 * time.Second},
	}
}

func (c *identityClient) do(method, path string, body any, out any) (int, error) {
	var bodyReader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		bodyReader = bytes.NewReader(b)
	} else {
		bodyReader = bytes.NewReader(nil)
	}

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-Internal-Secret", c.secret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("identity-service %s: status %d", path, resp.StatusCode)
	}
	return resp.StatusCode, json.NewDecoder(resp.Body).Decode(out)
}

type orgLookup struct {
	OrgID string `json:"org_id"`
}

func (c *identityClient) orgBySlug(slug string) (orgLookup, error) {
	var out orgLookup
	_, err := c.do(http.MethodGet, "/internal/organizations/by-slug/"+slug, nil, &out)
	return out, err
}

type loginResult struct {
	UserID string `json:"user_id"`
	OrgID  string `json:"org_id"`
	Role   string `json:"role"`
	Email  string `json:"email"`
}

// login returns (result, statusCode, error). Callers check statusCode to
// tell "wrong password" (401) apart from "identity-service unreachable".
func (c *identityClient) login(email, password string) (loginResult, int, error) {
	var out loginResult
	status, err := c.do(http.MethodPost, "/internal/auth/login", map[string]string{
		"email": email, "password": password,
	}, &out)
	return out, status, err
}
