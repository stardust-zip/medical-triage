# Deployment

This app ships as six runtime containers plus Postgres:

- `frontend`: Next.js standalone server
- `gateway`: public Go API gateway
- `api`: private Python/FastAPI triage service
- `identity`: private Go identity service (owns login: email + password_hash)
- `queue`: private Go queue-service
- `scheduling`: private Go scheduling-service
- `db`: Postgres with pgvector for local/VPS deployments

For AWS, GCP, and most PaaS providers, prefer a managed Postgres instance with pgvector enabled and run only the app containers.

## Production Checklist

- Set strong unique values for `GATEWAY_SHARED_SECRET`,
  `INTERNAL_SHARED_SECRET`, `PATIENT_SESSION_SECRET`, and
  `STAFF_SESSION_SECRET`.
- Use a privileged `ADMIN_DATABASE_URL` only for the Alembic migration job.
- Use a separate non-superuser `DATABASE_URL` for app services.
- Keep `api` and `identity` private; expose only `gateway` and `frontend`.
- Set `CORS_ORIGINS` to the deployed frontend origin.
- Set `NEXT_PUBLIC_API_URL` to the public gateway URL.
- Use HTTPS at the load balancer, reverse proxy, or PaaS edge.
- Enable database backups and point-in-time recovery for production data.
- Do not store real PHI in this demo project.

## VPS

On a single server with Docker Compose:

```bash
./setup.sh
```

Edit `.env` before exposing the server publicly: replace all secrets, set
`OPENAI_API_KEY`, `CORS_ORIGINS`, and `NEXT_PUBLIC_API_URL`.

Put Caddy, Nginx, Traefik, or the cloud provider load balancer in front of:

- `frontend:3000`
- `gateway:8080`

Keep `api`, `identity`, `queue`, `scheduling`, and `db` on the private Docker network.

## AWS

One practical layout:

- ECR for container images
- ECS Fargate or App Runner for `frontend`, `gateway`, `api`, and `identity`
- RDS PostgreSQL with pgvector enabled
- Secrets Manager or SSM Parameter Store for environment variables
- Application Load Balancer exposing frontend and gateway

Deployment sequence:

```bash
python scripts/migrate.py
```

Run the Alembic migration job with `ADMIN_DATABASE_URL`, then deploy services
with `DATABASE_URL`.

## GCP

One practical layout:

- Artifact Registry for images
- Cloud Run for `frontend`, `gateway`, `api`, and `identity`
- Cloud SQL for PostgreSQL with pgvector enabled
- Secret Manager for environment variables
- Serverless VPC access or Cloud SQL connector for private DB connectivity

Run `scripts/migrate.py` as a Cloud Run Job or CI job before rolling out
application revisions. It creates/updates the app role, runs Alembic, then
grants privileges.

## PaaS

For Render, Fly.io, Railway, or similar platforms:

- Create one service per Dockerfile.
- Use a Postgres add-on that supports pgvector, or attach an external managed
  Postgres.
- Run `python scripts/migrate.py` as a pre-deploy Alembic job.
- Route public traffic to frontend and gateway only.

## Rollout Order

1. Build images.
2. Run Alembic migrations using `ADMIN_DATABASE_URL`.
3. Deploy private services: `api`, `identity`, `queue`, `scheduling`.
4. Deploy public services: `gateway`, `frontend`.
5. Check `/health` through the gateway.
6. Run `python scripts/seed_demo_staff.py` to create the first OWNER login
   (prints the email/password to use at `/dashboard`).
7. Seed red-flag embeddings through `POST /api/v1/admin/seed-red-flags` with
   that OWNER token.

## Migration Tooling

Alembic is the source of truth for the current shared database schema.
SQLAlchemy models live in `db_models.py`, and revision files live in
`migrations/versions`.

The repo also includes a `goose` Dockerfile under `tools/goose` for future
Go-owned service schemas. Do not run goose against Alembic-owned tables unless
the schema ownership model has been split first.
