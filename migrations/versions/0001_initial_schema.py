"""initial schema

Revision ID: 0001_initial_schema
Revises:
Create Date: 2026-07-04 00:00:00
"""

from __future__ import annotations

from collections.abc import Sequence
from pathlib import Path

from alembic import op


revision: str = "0001_initial_schema"
down_revision: str | Sequence[str] | None = None
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    sql_path = Path(__file__).resolve().parents[2] / "db" / "init.sql"
    op.execute(sql_path.read_text(encoding="utf-8"))


def downgrade() -> None:
    raise NotImplementedError(
        "Initial TriageOS schema downgrade is intentionally unsupported. "
        "Restore from backup or recreate the database for destructive resets."
    )
