"""Offline test for agent.create_queue_item's contract with queue-service
(Phase 3) — mocks httpx.AsyncClient, no real network or running service."""

from __future__ import annotations

import asyncio
from unittest.mock import AsyncMock, MagicMock

import pytest

from triage import agent


def _fake_httpx_client(response):
    client = AsyncMock()
    client.post = AsyncMock(return_value=response)
    client.__aenter__ = AsyncMock(return_value=client)
    client.__aexit__ = AsyncMock(return_value=False)
    return client


def test_create_queue_item_posts_to_internal_endpoint_with_secret(monkeypatch):
    response = MagicMock()
    response.raise_for_status = MagicMock()
    response.json = MagicMock(return_value={"queue_id": "queue-123"})
    fake_client = _fake_httpx_client(response)
    monkeypatch.setattr(agent.httpx, "AsyncClient", MagicMock(return_value=fake_client))

    queue_id = asyncio.run(
        agent.create_queue_item(
            org_id="org-1",
            patient_session_id="patient-1",
            clinical_summary="đau đầu nhẹ",
            suggested_dept="THAN_KINH",
            triage_log_id="log-1",
        )
    )

    assert queue_id == "queue-123"
    args, kwargs = fake_client.post.call_args
    assert args[0].endswith("/internal/queue/items")
    assert kwargs["headers"]["X-Internal-Secret"] == agent.settings.INTERNAL_SHARED_SECRET
    assert kwargs["json"] == {
        "org_id": "org-1",
        "patient_session_id": "patient-1",
        "clinical_summary": "đau đầu nhẹ",
        "suggested_dept": "THAN_KINH",
        "triage_log_id": "log-1",
    }


def test_create_queue_item_propagates_http_errors(monkeypatch):
    response = MagicMock()
    response.raise_for_status = MagicMock(side_effect=RuntimeError("503 from queue-service"))
    fake_client = _fake_httpx_client(response)
    monkeypatch.setattr(agent.httpx, "AsyncClient", MagicMock(return_value=fake_client))

    with pytest.raises(RuntimeError):
        asyncio.run(
            agent.create_queue_item(
                org_id="org-1",
                patient_session_id="patient-1",
                clinical_summary="x",
                suggested_dept=None,
                triage_log_id=None,
            )
        )
