"""Offline regression tests for the fail-safe emergency check (see
src/agent.py::check_red_flags). No network/API key needed — that's
tests/test_red_flag_eval.py's job."""

from __future__ import annotations

import asyncio
import json
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest

from src import agent


class _FakeCursor:
    def __init__(self, row):
        self._row = row

    def __enter__(self):
        return self

    def __exit__(self, *exc_info):
        return False

    def execute(self, *args, **kwargs):
        pass

    def fetchone(self):
        return self._row


class _FakeConn:
    """Stands in for a psycopg2 connection returning a fixed top-1 row."""

    def __init__(self, row):
        self._row = row

    def cursor(self, cursor_factory=None):
        return _FakeCursor(self._row)


# ---------------------------------------------------------------------------
# check_red_flags: the three honest outcomes
# ---------------------------------------------------------------------------


def test_check_red_flags_reports_emergency_above_threshold(monkeypatch):
    monkeypatch.setattr(agent, "get_embedding", AsyncMock(return_value=[0.0] * 1536))
    conn = _FakeConn({"keyword": "đau thắt ngực", "similarity": 0.9})

    status, keyword, similarity = asyncio.run(agent.check_red_flags("x", conn))

    assert status == "EMERGENCY"
    assert keyword == "đau thắt ngực"
    assert similarity == 0.9


def test_check_red_flags_reports_safe_below_threshold(monkeypatch):
    monkeypatch.setattr(agent, "get_embedding", AsyncMock(return_value=[0.0] * 1536))
    conn = _FakeConn({"keyword": "đau thắt ngực", "similarity": 0.1})

    status, _keyword, _similarity = asyncio.run(agent.check_red_flags("x", conn))

    assert status == "SAFE"


def test_check_red_flags_fails_safe_on_embedding_error(monkeypatch):
    monkeypatch.setattr(
        agent, "get_embedding", AsyncMock(side_effect=RuntimeError("openai down"))
    )
    conn = _FakeConn({"keyword": "đau thắt ngực", "similarity": 0.9})

    status, keyword, similarity = asyncio.run(agent.check_red_flags("x", conn))

    assert status == "CHECK_FAILED"
    assert (keyword, similarity) == ("", 0.0)


def test_check_red_flags_fails_safe_when_table_is_empty(monkeypatch):
    monkeypatch.setattr(agent, "get_embedding", AsyncMock(return_value=[0.0] * 1536))
    conn = _FakeConn(None)  # no rows – red_flags was never seeded

    status, _keyword, _similarity = asyncio.run(agent.check_red_flags("x", conn))

    assert status == "CHECK_FAILED"


# ---------------------------------------------------------------------------
# run_triage_pipeline: CHECK_FAILED must escalate, never continue silently
# ---------------------------------------------------------------------------


def _fake_openai_client_calling_check_emergency():
    """A fake AsyncOpenAI client whose one response is a check_emergency
    tool call, so the pipeline reaches the code path under test."""
    tool_call = SimpleNamespace(
        id="call_1",
        type="function",
        function=SimpleNamespace(
            name="check_emergency",
            arguments=json.dumps({"symptoms": "đau ngực"}),
        ),
    )
    message = SimpleNamespace(tool_calls=[tool_call], content=None, role="assistant")
    response = SimpleNamespace(choices=[SimpleNamespace(message=message)])

    completions = SimpleNamespace(create=AsyncMock(return_value=response))
    return SimpleNamespace(chat=SimpleNamespace(completions=completions))


def test_pipeline_fails_safe_to_emergency_when_db_is_unavailable(monkeypatch):
    monkeypatch.setattr(
        agent, "_get_openai", lambda: _fake_openai_client_calling_check_emergency()
    )
    monkeypatch.setattr(
        agent,
        "_get_db_connection",
        lambda: (_ for _ in ()).throw(RuntimeError("db down")),
    )

    result = asyncio.run(
        agent.run_triage_pipeline(
            patient_id="patient-1",
            org_id="00000000-0000-4000-8000-000000000000",
            message="tôi bị đau ngực",
        )
    )

    assert result["flow"] == "EMERGENCY"


def test_pipeline_fails_safe_to_emergency_when_check_itself_fails(monkeypatch):
    monkeypatch.setattr(
        agent, "_get_openai", lambda: _fake_openai_client_calling_check_emergency()
    )
    monkeypatch.setattr(agent, "_get_db_connection", lambda: _FakeConn(None))
    monkeypatch.setattr(agent, "_set_org_context", lambda conn, org_id: None)
    monkeypatch.setattr(agent, "get_embedding", AsyncMock(return_value=[0.0] * 1536))

    result = asyncio.run(
        agent.run_triage_pipeline(
            patient_id="patient-1",
            org_id="00000000-0000-4000-8000-000000000000",
            message="tôi bị đau ngực",
        )
    )

    assert result["flow"] == "EMERGENCY"


if __name__ == "__main__":
    raise SystemExit(pytest.main([__file__, "-v"]))
