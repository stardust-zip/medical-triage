from __future__ import annotations

import os
from pathlib import Path
from typing import Any

from alembic import command
from alembic.config import Config
import psycopg2
from psycopg2 import sql


ROOT = Path(__file__).resolve().parents[1]


def env(name: str, default: str | None = None) -> str:
    value = os.getenv(name, default)
    if not value:
        raise SystemExit(f"missing required environment variable: {name}")
    return value


def create_app_role(conn: Any) -> tuple[str, str]:
    role = env("APP_DB_USER", "triageos_app")
    password = env("APP_DB_PASSWORD")

    with conn.cursor() as cur:
        cur.execute("SELECT 1 FROM pg_roles WHERE rolname = %s", (role,))
        if cur.fetchone():
            cur.execute(
                sql.SQL(
                    "ALTER ROLE {} WITH PASSWORD %s NOSUPERUSER NOBYPASSRLS"
                ).format(
                    sql.Identifier(role)
                ),
                (password,),
            )
        else:
            cur.execute(
                sql.SQL(
                    "CREATE ROLE {} LOGIN PASSWORD %s NOSUPERUSER NOBYPASSRLS"
                ).format(sql.Identifier(role)),
                (password,),
            )
    return role, password


def grant_app_privileges(conn: Any, role: str) -> None:
    with conn.cursor() as cur:
        cur.execute(
            sql.SQL("GRANT CONNECT ON DATABASE {} TO {}").format(
                sql.Identifier(conn.info.dbname),
                sql.Identifier(role),
            )
        )
        cur.execute(
            sql.SQL("GRANT USAGE ON SCHEMA public TO {}").format(
                sql.Identifier(role)
            )
        )
        cur.execute(
            sql.SQL(
                "GRANT SELECT, INSERT, UPDATE, DELETE "
                "ON ALL TABLES IN SCHEMA public TO {}"
            ).format(sql.Identifier(role))
        )
        cur.execute(
            sql.SQL(
                "GRANT USAGE, SELECT, UPDATE "
                "ON ALL SEQUENCES IN SCHEMA public TO {}"
            ).format(sql.Identifier(role))
        )
        cur.execute(
            sql.SQL(
                "GRANT USAGE ON TYPE triage_resolution, queue_status, "
                "user_role TO {}"
            ).format(sql.Identifier(role))
        )


def apply_migrations() -> None:
    cfg = Config(str(ROOT / "alembic.ini"))
    cfg.set_main_option("script_location", str(ROOT / "migrations"))
    command.upgrade(cfg, "head")


def main() -> None:
    admin_url = env("ADMIN_DATABASE_URL", os.getenv("DATABASE_URL"))
    with psycopg2.connect(admin_url) as conn:
        conn.autocommit = False
        role, _password = create_app_role(conn)
        conn.commit()

    apply_migrations()

    with psycopg2.connect(admin_url) as conn:
        conn.autocommit = False
        grant_app_privileges(conn, role)
        conn.commit()
    print("database is up to date")


if __name__ == "__main__":
    main()
