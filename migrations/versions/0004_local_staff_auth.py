"""users: drop auth_user_id, add password_hash (local auth, no Supabase)

Revision ID: 0004_local_staff_auth
Revises: 0003_scheduling_appointments
Create Date: 2026-07-06 12:00:00
"""

from __future__ import annotations

from collections.abc import Sequence

from alembic import op


revision: str = "0004_local_staff_auth"
down_revision: str | Sequence[str] | None = "0003_scheduling_appointments"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.execute("ALTER TABLE users DROP COLUMN IF EXISTS auth_user_id")
    op.execute("ALTER TABLE users ADD COLUMN IF NOT EXISTS password_hash VARCHAR(255) NOT NULL DEFAULT ''")
    op.execute("ALTER TABLE users ALTER COLUMN password_hash DROP DEFAULT")
    op.execute(
        """
        DO $$
        BEGIN
            IF NOT EXISTS (
                SELECT 1 FROM pg_constraint WHERE conname = 'users_email_key'
            ) THEN
                ALTER TABLE users ADD CONSTRAINT users_email_key UNIQUE (email);
            END IF;
        END $$;
        """
    )


def downgrade() -> None:
    raise NotImplementedError("downgrade unsupported, restore from backup")
