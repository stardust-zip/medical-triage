"""Offline tests for agent.py's scheduling-service HTTP client functions
(Phase 4) — mocks httpx.AsyncClient, no real network or running service."""

from __future__ import annotations

import asyncio
from unittest.mock import AsyncMock, MagicMock

import pytest

from src import agent


def _fake_httpx_client(response):
    client = AsyncMock()
    client.get = AsyncMock(return_value=response)
    client.post = AsyncMock(return_value=response)
    client.__aenter__ = AsyncMock(return_value=client)
    client.__aexit__ = AsyncMock(return_value=False)
    return client


def test_get_doctors_by_department_returns_parsed_list(monkeypatch):
    response = MagicMock()
    response.raise_for_status = MagicMock()
    response.json = MagicMock(
        return_value={"doctors": [{"id": "d1", "name": "BS. A", "specialty": "X", "department_code": "TIM_MACH"}]}
    )
    fake_client = _fake_httpx_client(response)
    monkeypatch.setattr(agent.httpx, "AsyncClient", MagicMock(return_value=fake_client))

    doctors = asyncio.run(agent.get_doctors_by_department("org-1", "TIM_MACH"))

    assert doctors == [{"id": "d1", "name": "BS. A", "specialty": "X", "department_code": "TIM_MACH"}]
    args, kwargs = fake_client.get.call_args
    assert args[0].endswith("/internal/scheduling/doctors")
    assert kwargs["params"] == {"org_id": "org-1", "department_code": "TIM_MACH"}
    assert kwargs["headers"]["X-Internal-Secret"] == agent.settings.INTERNAL_SHARED_SECRET


def test_get_doctors_by_department_fails_soft_on_error(monkeypatch):
    fake_client = _fake_httpx_client(MagicMock())
    fake_client.get = AsyncMock(side_effect=RuntimeError("scheduling-service down"))
    monkeypatch.setattr(agent.httpx, "AsyncClient", MagicMock(return_value=fake_client))

    doctors = asyncio.run(agent.get_doctors_by_department("org-1", "TIM_MACH"))

    assert doctors == []


def test_get_clinics_by_department_returns_parsed_list(monkeypatch):
    response = MagicMock()
    response.raise_for_status = MagicMock()
    response.json = MagicMock(return_value={"clinics": [{"name": "Clinic A", "address": "123 Demo St"}]})
    fake_client = _fake_httpx_client(response)
    monkeypatch.setattr(agent.httpx, "AsyncClient", MagicMock(return_value=fake_client))

    clinics = asyncio.run(agent.get_clinics_by_department("org-1", "TIM_MACH"))

    assert clinics == [{"name": "Clinic A", "address": "123 Demo St"}]


def test_get_clinics_by_department_fails_soft_on_error(monkeypatch):
    fake_client = _fake_httpx_client(MagicMock())
    fake_client.get = AsyncMock(side_effect=RuntimeError("scheduling-service down"))
    monkeypatch.setattr(agent.httpx, "AsyncClient", MagicMock(return_value=fake_client))

    clinics = asyncio.run(agent.get_clinics_by_department("org-1", "TIM_MACH"))

    assert clinics == []


def test_create_appointment_posts_to_internal_endpoint_with_secret(monkeypatch):
    response = MagicMock()
    response.raise_for_status = MagicMock()
    response.json = MagicMock(return_value={"appointment_id": "appt-1"})
    fake_client = _fake_httpx_client(response)
    monkeypatch.setattr(agent.httpx, "AsyncClient", MagicMock(return_value=fake_client))

    appt_id = asyncio.run(
        agent.create_appointment(
            org_id="org-1",
            patient_session_id="patient-1",
            doctor_id="doctor-1",
            department_code="TIM_MACH",
            appointment_time="2026-04-10T08:00:00+07:00",
        )
    )

    assert appt_id == "appt-1"
    args, kwargs = fake_client.post.call_args
    assert args[0].endswith("/internal/scheduling/appointments")
    assert kwargs["headers"]["X-Internal-Secret"] == agent.settings.INTERNAL_SHARED_SECRET
    assert kwargs["json"] == {
        "org_id": "org-1",
        "patient_session_id": "patient-1",
        "doctor_id": "doctor-1",
        "department_code": "TIM_MACH",
        "appointment_time": "2026-04-10T08:00:00+07:00",
    }


def test_create_appointment_propagates_http_errors(monkeypatch):
    response = MagicMock()
    response.raise_for_status = MagicMock(side_effect=RuntimeError("409 double booked"))
    fake_client = _fake_httpx_client(response)
    monkeypatch.setattr(agent.httpx, "AsyncClient", MagicMock(return_value=fake_client))

    with pytest.raises(RuntimeError):
        asyncio.run(
            agent.create_appointment(
                org_id="org-1",
                patient_session_id="patient-1",
                doctor_id="doctor-1",
                department_code="TIM_MACH",
                appointment_time="2026-04-10T08:00:00+07:00",
            )
        )
