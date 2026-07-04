#!/usr/bin/env sh
set -eu

if ! command -v docker >/dev/null 2>&1; then
  echo "Docker is required. Install Docker Engine/Desktop and rerun setup.sh." >&2
  exit 1
fi

if ! docker compose version >/dev/null 2>&1; then
  echo "Docker Compose v2 is required. Install the Docker Compose plugin." >&2
  exit 1
fi

random_hex() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 24
  else
    od -An -N24 -tx1 /dev/urandom | tr -d " \n"
  fi
}

set_env_var() {
  key="$1"
  value="$2"
  tmp_file=".env.tmp.$$"

  if grep -q "^${key}=" .env; then
    awk -v key="${key}" -v value="${value}" '
      BEGIN { prefix = key "=" }
      index($0, prefix) == 1 { print key "=" value; next }
      { print }
    ' .env >"${tmp_file}"
    mv "${tmp_file}" .env
  else
    printf '%s=%s\n' "${key}" "${value}" >>.env
  fi
}

get_env_var() {
  key="$1"
  awk -v key="${key}" '
    BEGIN { prefix = key "=" }
    index($0, prefix) == 1 {
      sub(prefix, "")
      print
      exit
    }
  ' .env
}

is_placeholder_value() {
  case "$1" in
    "" | "postgres" | "triageos_app_password" | "change-me-"* | "your-supabase-jwt-secret")
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

ensure_secret_var() {
  key="$1"
  current_value="$(get_env_var "${key}")"
  if is_placeholder_value "${current_value}"; then
    set_env_var "${key}" "$(random_hex)"
  fi
}

ensure_env_var() {
  key="$1"
  value="$2"
  if [ -z "$(get_env_var "${key}")" ]; then
    set_env_var "${key}" "${value}"
  fi
}

has_placeholder_database_url() {
  case "$1" in
    "" | *"triageos_app_password"* | *"postgres:postgres"*)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

created_env=0

if [ ! -f .env ]; then
  cp .env.example .env
  created_env=1
  echo "Created .env from .env.example."
fi

if [ "${created_env}" -eq 1 ]; then
  ensure_secret_var "APP_DB_PASSWORD"
  ensure_secret_var "POSTGRES_PASSWORD"
  ensure_secret_var "GATEWAY_SHARED_SECRET"
  ensure_secret_var "INTERNAL_SHARED_SECRET"
  ensure_secret_var "SUPABASE_JWT_SECRET"
  ensure_secret_var "PATIENT_SESSION_SECRET"
else
  ensure_env_var "APP_DB_PASSWORD" "triageos_app_password"
  ensure_env_var "POSTGRES_PASSWORD" "postgres"
  ensure_env_var "GATEWAY_SHARED_SECRET" "$(random_hex)"
  ensure_env_var "INTERNAL_SHARED_SECRET" "$(random_hex)"
  ensure_env_var "SUPABASE_JWT_SECRET" "$(random_hex)"
  ensure_env_var "PATIENT_SESSION_SECRET" "$(random_hex)"
fi

ensure_env_var "APP_DB_USER" "triageos_app"

app_db_user="$(get_env_var "APP_DB_USER")"
app_db_password="$(get_env_var "APP_DB_PASSWORD")"
postgres_password="$(get_env_var "POSTGRES_PASSWORD")"

if has_placeholder_database_url "$(get_env_var "DATABASE_URL")"; then
  set_env_var "DATABASE_URL" "postgresql://${app_db_user}:${app_db_password}@localhost:5432/triageos"
fi

if has_placeholder_database_url "$(get_env_var "ADMIN_DATABASE_URL")"; then
  set_env_var "ADMIN_DATABASE_URL" "postgresql://postgres:${postgres_password}@localhost:5432/triageos"
fi

if has_placeholder_database_url "$(get_env_var "COMPOSE_DATABASE_URL")"; then
  set_env_var "COMPOSE_DATABASE_URL" "postgresql://${app_db_user}:${app_db_password}@db:5432/triageos"
fi

if has_placeholder_database_url "$(get_env_var "COMPOSE_ADMIN_DATABASE_URL")"; then
  set_env_var "COMPOSE_ADMIN_DATABASE_URL" "postgresql://postgres:${postgres_password}@db:5432/triageos"
fi

if [ -z "$(get_env_var "POSTGRES_DB")" ]; then
  set_env_var "POSTGRES_DB" "triageos"
fi

if [ -z "$(get_env_var "POSTGRES_USER")" ]; then
  set_env_var "POSTGRES_USER" "postgres"
fi

docker compose up --build -d

echo "TriageOS is starting."
echo "Frontend: http://localhost:3000"
echo "Gateway:  http://localhost:8000"
echo "Health:   http://localhost:8000/health"
echo "Edit .env to set OPENAI_API_KEY before using live AI triage."
