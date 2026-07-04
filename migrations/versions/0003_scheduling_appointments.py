"""scheduling-service: idempotent + double-booking-safe appointments

Revision ID: 0003_scheduling_appointments
Revises: 0002_queue_triage_log_id
Create Date: 2026-07-06 00:00:00
"""

from __future__ import annotations

from collections.abc import Sequence

from alembic import op


revision: str = "0003_scheduling_appointments"
down_revision: str | Sequence[str] | None = "0002_queue_triage_log_id"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    # IF NOT EXISTS: db/init.sql already defines these for a brand new
    # database (revision 0001 executes the current db/init.sql verbatim),
    # so this only does real work against a database provisioned before
    # Phase 4.
    op.execute(
        "ALTER TABLE appointments ADD COLUMN IF NOT EXISTS idempotency_key VARCHAR(255)"
    )
    op.execute(
        """
        CREATE UNIQUE INDEX IF NOT EXISTS idx_appointments_no_double_booking
            ON appointments (org_id, doctor_id, appointment_time)
        """
    )
    op.execute(
        """
        CREATE UNIQUE INDEX IF NOT EXISTS idx_appointments_idempotency_key
            ON appointments (org_id, idempotency_key)
            WHERE idempotency_key IS NOT NULL
        """
    )


def downgrade() -> None:
    op.execute("DROP INDEX IF EXISTS idx_appointments_idempotency_key")
    op.execute("DROP INDEX IF EXISTS idx_appointments_no_double_booking")
    op.execute("ALTER TABLE appointments DROP COLUMN IF EXISTS idempotency_key")
