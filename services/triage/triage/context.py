"""
context.py - Trust-boundary extraction for TriageOS (Phase 1: tenancy).

api-gateway verifies identity — a self-issued staff session JWT, or a signed
anonymous session token for patients — and forwards the result as headers, stripping
whatever the client originally sent. This module is the single place that
reads those headers into typed context, so route handlers in api.py never
touch raw headers or fall back to trusting request-body fields for identity
(org_id, nurse_id, etc. must never come from the client).
"""

from __future__ import annotations

import uuid
from dataclasses import dataclass

from fastapi import Depends, Header, HTTPException, status

from .config import settings


def require_gateway_secret(
    x_gateway_secret: str | None = Header(default=None),
) -> None:
    """
    Reject any request that didn't pass through api-gateway.

    The gateway re-signs this shared secret on every proxied request after
    stripping client-supplied identity headers (see
    services/gateway/main.go's trustedDirector). Without a match, nothing
    else in this module can be trusted.
    """
    if not x_gateway_secret or x_gateway_secret != settings.GATEWAY_SHARED_SECRET:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="Request did not originate from api-gateway.",
        )


def require_internal_secret(
    x_internal_secret: str | None = Header(default=None),
) -> None:
    """Auth for server-to-server calls (e.g. queue-service), not gateway-proxied."""
    if not x_internal_secret or x_internal_secret != settings.INTERNAL_SHARED_SECRET:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="Missing or invalid internal secret.",
        )


def _parse_org_id(x_org_id: str | None) -> str:
    if not x_org_id:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="Missing tenant context.",
        )
    try:
        return str(uuid.UUID(x_org_id))
    except ValueError as exc:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="Malformed tenant context.",
        ) from exc


@dataclass(frozen=True)
class PatientContext:
    """Identity of an anonymous, token-bound patient session."""

    org_id: str
    patient_session_id: str


@dataclass(frozen=True)
class StaffContext:
    """Identity of an authenticated staff member (nurse/admin/owner/doctor)."""

    org_id: str
    user_id: str
    role: str
    email: str


def get_patient_context(
    _gateway: None = Depends(require_gateway_secret),
    x_org_id: str | None = Header(default=None),
    x_patient_session_id: str | None = Header(default=None),
) -> PatientContext:
    if not x_patient_session_id:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="Missing patient session.",
        )
    return PatientContext(
        org_id=_parse_org_id(x_org_id),
        patient_session_id=x_patient_session_id,
    )


_STAFF_ROLES = {"OWNER", "ADMIN", "NURSE", "DOCTOR"}


def get_staff_context(
    _gateway: None = Depends(require_gateway_secret),
    x_org_id: str | None = Header(default=None),
    x_user_id: str | None = Header(default=None),
    x_user_role: str | None = Header(default=None),
    x_user_email: str | None = Header(default=None),
) -> StaffContext:
    if not x_user_id or x_user_role not in _STAFF_ROLES:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="Missing or invalid staff identity.",
        )
    return StaffContext(
        org_id=_parse_org_id(x_org_id),
        user_id=x_user_id,
        role=x_user_role,  # narrowed to _STAFF_ROLES above
        email=x_user_email or "",
    )


def require_roles(*roles: str):
    """
    Dependency factory: 403s unless the caller's role is one of *roles*.

    api-gateway already gates routes by role (see requireStaff in
    services/gateway/auth.go); this is defense-in-depth so the backend
    doesn't rely solely on the gateway getting routing right.
    """

    def _check(ctx: StaffContext = Depends(get_staff_context)) -> StaffContext:
        if ctx.role not in roles:
            raise HTTPException(
                status_code=status.HTTP_403_FORBIDDEN,
                detail="Your role does not permit this action.",
            )
        return ctx

    return _check
