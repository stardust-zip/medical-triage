"""queue-service: add human_triage_queue.triage_log_id

Revision ID: 0002_queue_triage_log_id
Revises: 0001_initial_schema
Create Date: 2026-07-05 00:00:00
"""

from __future__ import annotations

from collections.abc import Sequence

from alembic import op


revision: str = "0002_queue_triage_log_id"
down_revision: str | Sequence[str] | None = "0001_initial_schema"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    # IF NOT EXISTS: db/init.sql already defines this column for a brand new
    # database (revision 0001 executes the current db/init.sql verbatim), so
    # this only does real work against a database provisioned before Phase 3.
    op.execute(
        """
        ALTER TABLE human_triage_queue
            ADD COLUMN IF NOT EXISTS triage_log_id UUID
                REFERENCES triage_logs(id) ON DELETE SET NULL
        """
    )
    op.execute(
        """
        CREATE INDEX IF NOT EXISTS idx_human_triage_queue_triage_log_id
            ON human_triage_queue (triage_log_id)
        """
    )


def downgrade() -> None:
    op.execute("DROP INDEX IF EXISTS idx_human_triage_queue_triage_log_id")
    op.execute("ALTER TABLE human_triage_queue DROP COLUMN IF EXISTS triage_log_id")
