package main

// Thin data-access layer over the organizations/users tables identity-service
// owns (see §4 of docs/architecture/implementation-plan.md). Every other
// service treats these as opaque lookups behind the two internal endpoints
// in main.go — nobody else runs SQL against these tables directly.

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errNotFound = errors.New("not found")

type org struct {
	ID   string
	Slug string
	Name string
}

type user struct {
	ID    string
	OrgID string
	Role  string
	Email string
}

func orgBySlug(ctx context.Context, pool *pgxpool.Pool, slug string) (org, error) {
	var o org
	err := pool.QueryRow(ctx,
		`SELECT id, slug, name FROM organizations WHERE slug = $1`, slug,
	).Scan(&o.ID, &o.Slug, &o.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return org{}, errNotFound
	}
	return o, err
}

func userByAuthID(ctx context.Context, pool *pgxpool.Pool, authUserID string) (user, error) {
	var u user
	err := pool.QueryRow(ctx,
		`SELECT id, org_id, role, email FROM users WHERE auth_user_id = $1`, authUserID,
	).Scan(&u.ID, &u.OrgID, &u.Role, &u.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return user{}, errNotFound
	}
	return u, err
}

// createOrganization provisions a brand new tenant plus its first OWNER user
// in one transaction — the minimal "onboard a clinic" path referenced by
// Phase 1 ("stand up identity-service: organizations, users, roles").
// Invite-additional-user flows, self-serve billing, etc. are later phases.
func createOrganization(ctx context.Context, pool *pgxpool.Pool, orgName, slug, ownerAuthUserID, ownerEmail string) (org, user, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return org{}, user{}, err
	}
	defer tx.Rollback(ctx)

	var o org
	err = tx.QueryRow(ctx,
		`INSERT INTO organizations (name, slug) VALUES ($1, $2) RETURNING id, slug, name`,
		orgName, slug,
	).Scan(&o.ID, &o.Slug, &o.Name)
	if err != nil {
		return org{}, user{}, err
	}

	var u user
	err = tx.QueryRow(ctx,
		`INSERT INTO users (org_id, auth_user_id, email, role)
		 VALUES ($1, $2, $3, 'OWNER')
		 RETURNING id, org_id, role, email`,
		o.ID, ownerAuthUserID, ownerEmail,
	).Scan(&u.ID, &u.OrgID, &u.Role, &u.Email)
	if err != nil {
		return org{}, user{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return org{}, user{}, err
	}
	return o, u, nil
}

// inviteUser adds a staff member to an existing org. Real invite-by-email
// delivery is out of scope for Phase 1 — this just creates the membership
// row once the admin has communicated credentials out of band.
func inviteUser(ctx context.Context, pool *pgxpool.Pool, orgID, authUserID, email, role string) (user, error) {
	var u user
	err := pool.QueryRow(ctx,
		`INSERT INTO users (org_id, auth_user_id, email, role)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, org_id, role, email`,
		orgID, authUserID, email, role,
	).Scan(&u.ID, &u.OrgID, &u.Role, &u.Email)
	return u, err
}
