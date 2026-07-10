import pytest
from fastapi import HTTPException

from triage.context import (
    get_patient_context,
    get_staff_context,
    require_gateway_secret,
    require_roles,
)


ORG_ID = "11111111-1111-4111-8111-111111111111"


def test_gateway_secret_rejects_untrusted_request():
    with pytest.raises(HTTPException) as exc:
        require_gateway_secret(x_gateway_secret="wrong")

    assert exc.value.status_code == 401


def test_patient_context_requires_valid_tenant_and_session():
    ctx = get_patient_context(
        _gateway=None,
        x_org_id=ORG_ID,
        x_patient_session_id="patient-session-id",
    )

    assert ctx.org_id == ORG_ID
    assert ctx.patient_session_id == "patient-session-id"


def test_patient_context_rejects_malformed_tenant():
    with pytest.raises(HTTPException) as exc:
        get_patient_context(
            _gateway=None,
            x_org_id="not-a-uuid",
            x_patient_session_id="patient-session-id",
        )

    assert exc.value.status_code == 401


def test_staff_context_rejects_unknown_role():
    with pytest.raises(HTTPException) as exc:
        get_staff_context(
            _gateway=None,
            x_org_id=ORG_ID,
            x_user_id="user-id",
            x_user_role="PATIENT",
            x_user_email="nurse@example.com",
        )

    assert exc.value.status_code == 401


def test_require_roles_allows_expected_staff_role():
    dependency = require_roles("NURSE")
    staff = get_staff_context(
        _gateway=None,
        x_org_id=ORG_ID,
        x_user_id="user-id",
        x_user_role="NURSE",
        x_user_email="nurse@example.com",
    )

    assert dependency(staff).role == "NURSE"
