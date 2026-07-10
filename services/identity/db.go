package main

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var (
	errNotFound        = errors.New("not found")
	errInvalidPassword = errors.New("invalid password")
)

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

func authenticateUser(ctx context.Context, pool *pgxpool.Pool, email, password string) (user, error) {
	var u user
	var hash string
	err := pool.QueryRow(ctx,
		`SELECT id, org_id, role, email, password_hash FROM users WHERE email = $1`, email,
	).Scan(&u.ID, &u.OrgID, &u.Role, &u.Email, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return user{}, errNotFound
	}
	if err != nil {
		return user{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return user{}, errInvalidPassword
	}
	return u, nil
}

func hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}

// createOrganization provisions a new tenant plus its first OWNER user.
func createOrganization(ctx context.Context, pool *pgxpool.Pool, orgName, slug, ownerPassword, ownerEmail string) (org, user, error) {
	passwordHash, err := hashPassword(ownerPassword)
	if err != nil {
		return org{}, user{}, err
	}

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
		`INSERT INTO users (org_id, email, password_hash, role)
		 VALUES ($1, $2, $3, 'OWNER')
		 RETURNING id, org_id, role, email`,
		o.ID, ownerEmail, passwordHash,
	).Scan(&u.ID, &u.OrgID, &u.Role, &u.Email)
	if err != nil {
		return org{}, user{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return org{}, user{}, err
	}
	return o, u, nil
}

// inviteUser adds a staff member to an existing org with a set password —
// real invite-by-email delivery is a later phase.
func inviteUser(ctx context.Context, pool *pgxpool.Pool, orgID, email, password, role string) (user, error) {
	passwordHash, err := hashPassword(password)
	if err != nil {
		return user{}, err
	}

	var u user
	err = pool.QueryRow(ctx,
		`INSERT INTO users (org_id, email, password_hash, role)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, org_id, role, email`,
		orgID, email, passwordHash, role,
	).Scan(&u.ID, &u.OrgID, &u.Role, &u.Email)
	return u, err
}
