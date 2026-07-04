#!/usr/bin/env sh
set -eu

if [ -z "${GITHUB_ENV:-}" ]; then
  echo "GITHUB_ENV is required; this script is intended for GitHub Actions." >&2
  exit 1
fi

random_hex() {
  openssl rand -hex 24
}

app_db_user="triageos_app"
app_db_password="$(random_hex)"
postgres_password="$(random_hex)"

{
  echo "ADMIN_DATABASE_URL=postgresql://postgres@localhost:5432/triageos"
  echo "APP_DB_USER=${app_db_user}"
  echo "APP_DB_PASSWORD=${app_db_password}"
  echo "DATABASE_URL=postgresql://${app_db_user}:${app_db_password}@localhost:5432/triageos"
  echo "POSTGRES_PASSWORD=${postgres_password}"
  echo "COMPOSE_ADMIN_DATABASE_URL=postgresql://postgres:${postgres_password}@db:5432/triageos"
  echo "COMPOSE_DATABASE_URL=postgresql://${app_db_user}:${app_db_password}@db:5432/triageos"
  echo "OPENAI_API_KEY=ci-$(random_hex)"
  echo "GATEWAY_SHARED_SECRET=$(random_hex)"
  echo "INTERNAL_SHARED_SECRET=$(random_hex)"
  echo "SUPABASE_JWT_SECRET=$(random_hex)"
  echo "PATIENT_SESSION_SECRET=$(random_hex)"
} >>"${GITHUB_ENV}"
