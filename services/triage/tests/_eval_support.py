"""Shared helpers for the live-LLM eval suite (test_red_flag_eval.py,
test_injection_regression.py). Leading underscore keeps pytest from
collecting this as a test module."""

from __future__ import annotations

import os

import psycopg2
import pytest

from triage.config import settings

_PLACEHOLDER_PREFIXES = ("test-", "ci-")  # fake keys from conftest.py / write_ci_env.sh


def has_real_openai_key() -> bool:
    """Best-effort check that OPENAI_API_KEY is a real key, not a placeholder."""
    key = os.environ.get("OPENAI_API_KEY", "")
    return bool(key) and not key.startswith(_PLACEHOLDER_PREFIXES)


requires_real_openai_key = pytest.mark.skipif(
    not has_real_openai_key(),
    reason="requires a real OPENAI_API_KEY for live model calls",
)


@pytest.fixture(scope="module")
def demo_org_id() -> str:
    """The seeded 'evergreen-demo' organization id (see db/init.sql)."""
    conn = psycopg2.connect(settings.DATABASE_URL)
    try:
        with conn.cursor() as cur:
            cur.execute(
                "SELECT id FROM organizations WHERE slug = %s", ("evergreen-demo",)
            )
            row = cur.fetchone()
        assert row, (
            "seed organization 'evergreen-demo' not found — "
            "did `python scripts/migrate.py` run against this DATABASE_URL?"
        )
        return str(row[0])
    finally:
        conn.close()
