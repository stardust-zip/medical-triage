package main

// Thin HTTP client for the two lookups the gateway needs from identity-service:
// resolving an org by its public slug (to mint a patient session), and
// resolving a Supabase auth user id to their tenant + role (to authorize a
// staff request). Both calls carry the shared internal secret so
// identity-service can reject anything that isn't the gateway.

import (
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

func (c *identityClient) get(path string, out any) error {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Internal-Secret", c.secret)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("identity-service %s: status %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type orgLookup struct {
	OrgID string `json:"org_id"`
}

func (c *identityClient) orgBySlug(slug string) (orgLookup, error) {
	var out orgLookup
	err := c.get("/internal/organizations/by-slug/"+slug, &out)
	return out, err
}

type userLookup struct {
	UserID string `json:"user_id"`
	OrgID  string `json:"org_id"`
	Role   string `json:"role"`
	Email  string `json:"email"`
}

func (c *identityClient) userByAuthID(authUserID string) (userLookup, error) {
	var out userLookup
	err := c.get("/internal/users/by-auth-id/"+authUserID, &out)
	return out, err
}
