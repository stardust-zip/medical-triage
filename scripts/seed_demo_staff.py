"""
scripts/seed_demo_staff.py - create the first staff login for the demo org.

Safe to re-run: an existing membership (409 from identity-service) is
treated as already done, not an error.

Required: INTERNAL_SHARED_SECRET (same value the running services use)
Optional: IDENTITY_URL (default http://localhost:8082)
          DEMO_ORG_SLUG (default evergreen-demo)
          DEMO_STAFF_EMAIL (default owner@evergreen-demo.test)
          DEMO_STAFF_PASSWORD (default a random one, printed at the end)
          DEMO_STAFF_ROLE (default OWNER)
"""

from __future__ import annotations

import os
import secrets

import httpx


def env(name: str, default: str | None = None) -> str:
    value = os.getenv(name, default)
    if not value:
        raise SystemExit(f"missing required environment variable: {name}")
    return value


def get_org_id(identity_url: str, internal_secret: str, slug: str) -> str:
    resp = httpx.get(
        f"{identity_url}/internal/organizations/by-slug/{slug}",
        headers={"X-Internal-Secret": internal_secret},
        timeout=10.0,
    )
    resp.raise_for_status()
    return resp.json()["org_id"]


def create_staff_user(
    identity_url: str, internal_secret: str, org_id: str, email: str, password: str, role: str
) -> bool:
    """Returns True if a new account was created, False if it already existed."""
    resp = httpx.post(
        f"{identity_url}/internal/users",
        headers={"X-Internal-Secret": internal_secret},
        json={"org_id": org_id, "email": email, "password": password, "role": role},
        timeout=10.0,
    )
    if resp.status_code == 409:
        return False
    resp.raise_for_status()
    return True


def main() -> None:
    internal_secret = env("INTERNAL_SHARED_SECRET")
    identity_url = env("IDENTITY_URL", "http://localhost:8082")
    org_slug = env("DEMO_ORG_SLUG", "evergreen-demo")
    email = env("DEMO_STAFF_EMAIL", "owner@evergreen-demo.test")
    password = os.getenv("DEMO_STAFF_PASSWORD") or secrets.token_urlsafe(12)
    role = env("DEMO_STAFF_ROLE", "OWNER")

    org_id = get_org_id(identity_url, internal_secret, org_slug)
    created = create_staff_user(identity_url, internal_secret, org_id, email, password, role)

    if not created:
        print(f"{email} already exists — password unchanged, nothing to do.")
        return

    print("Demo staff login ready:")
    print(f"  email:    {email}")
    print(f"  password: {password}")
    print(f"  role:     {role} on org '{org_slug}'")


if __name__ == "__main__":
    main()
