# Go Migrations

Alembic is the canonical migration runner for the current shared TriageOS
schema.

This directory is reserved for future Go-owned service schemas if the database
is split by service ownership. Use `goose` here only when a Go service owns the
schema being changed. Do not create a second goose history for tables already
managed by Alembic.
