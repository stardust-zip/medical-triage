# TriageOS

TriageOS is not affiliated with, endorsed by, or connected to any real hospital or clinic network.

All seed data, demo doctors, demo clinics, and patient examples are synthetic.

Do not use real PHI or real patient data in local development, tests, demos, logs, prompts, or hosted environments.

## Quick Start

Prerequisites:

- Docker Engine/Desktop with Docker Compose
- POSIX shell for `setup.sh` on Linux/macOS/WSL/Git Bash
- `make` is optional convenience

Start everything:

```bash
./setup.sh
```

Equivalent command:

```bash
make bootstrap
```

Open:

- Frontend: http://localhost:3000
- API gateway: http://localhost:8000
- API health: http://localhost:8000/health

The compose stack starts Postgres + pgvector, runs the Alembic migration container, then the Python API, Go identity/queue/scheduling services, Go gateway, and Next.js frontend.

The nurse dashboard (http://localhost:3000/dashboard) needs a staff login, which doesn't exist yet on a fresh database. Create one:

```bash
make seed-staff
```

This prints an email/password — use it to sign in.

## Daily Development

After pulling new code, sync your local database without dropping data:

```bash
make migrate
```

Common commands:

```bash
make up          # start existing containers
make logs        # follow all service logs
make down        # stop containers, keep database volume
make build       # rebuild images
make test        # run Python, Go, and frontend tests locally
```

The local database lives in the `postgres-data` Docker volume. `make down` does not delete it. Use `docker compose down -v` only when you intentionally want to reset local state.

## Configuration

Copy `.env.example` to `.env` for local development.

Important variables:

- `OPENAI_API_KEY`: required for live triage and red-flag embedding seeding.
- `DATABASE_URL`: non-superuser application connection string.
- `ADMIN_DATABASE_URL`: privileged migration connection string.
- `GATEWAY_SHARED_SECRET`, `INTERNAL_SHARED_SECRET`,
  `PATIENT_SESSION_SECRET`, `STAFF_SESSION_SECRET`: replace all demo values
  outside local development.
- `CORS_ORIGINS`, `NEXT_PUBLIC_API_URL`: set these to deployed frontend/API
  URLs in production.

Application services should not connect as a Postgres superuser because that
can bypass row-level security. `scripts/migrate.py` creates/updates a
`NOSUPERUSER NOBYPASSRLS` app role, runs Alembic, then grants the required
privileges.

## Database Migrations

Canonical schema migrations are Alembic revisions in `migrations/versions`. SQLAlchemy metadata lives in `src/db_models.py` for future Alembic autogeneration and schema review.

Run migrations through Docker:

```bash
make migrate
```

Run migrations directly on a host with Python dependencies installed:

```bash
python scripts/migrate.py
```

The initial Alembic revision delegates to `db/init.sql` because it contains Postgres-specific details such as pgvector indexes, RLS policies, seed demo directory data, and enum setup. The migration wrapper is safe to run after every pull.

`goose` is included as Go migration tooling under `tools/goose`, but it is not the canonical runner for the current shared schema. Use it only for future Go-owned service schemas so Alembic and goose do not maintain competing histories for the same tables.

## Tests And CI

Local checks:

```bash
pip install ".[dev]"
pytest
cd services/gateway && go test ./...
cd services/identity && go test ./...
cd frontend && npm ci && npm audit --audit-level=moderate && npm run lint && npm run build
```

GitHub Actions runs:

- Python lint, unit tests, and pgvector Alembic migration smoke test
- Go tests for gateway and identity
- Next.js dependency audit, lint, and build
- Docker Compose validation and image builds

## Deployment

See [docs/deployment.md](docs/deployment.md)
