"""
Shared helpers for the live-LLM eval suite (tests/test_red_flag_eval.py,
tests/test_injection_regression.py).

Both of those files exercise the *real* OpenAI API, so they're skipped
unless a real key is configured — see their module docstrings for why a
fake/offline stand-in wouldn't test the thing they exist to catch.

Leading underscore keeps pytest from collecting this file as a test module
in its own right.
"""

from __future__ import annotations

import os

import psycopg2
import pytest

from src.config import settings

# The per-job key CI generates for the regular test suite (write_ci_env.sh)
# and the local-dev default (tests/conftest.py) both use these prefixes.
_PLACEHOLDER_PREFIXES = ("test-", "ci-")


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
