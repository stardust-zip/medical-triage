// lib/staffSession.ts – staff (nurse/admin/owner) login session.
//
// Self-hosted: POST /api/v1/auth/staff/login (gateway verifies email +
// password against identity-service and mints a session JWT), cached in
// localStorage the same way lib/patientSession.ts caches its token.

const API_BASE = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8000";
const STORAGE_KEY = "triageos_staff_session";

export interface StaffSession {
  accessToken: string;
  email: string;
  role: string;
}

interface StoredSession extends StaffSession {
  expiresAt: string;
}

function readCached(): StaffSession | null {
  if (typeof window === "undefined") return null;
  const raw = localStorage.getItem(STORAGE_KEY);
  if (!raw) return null;
  try {
    const parsed: StoredSession = JSON.parse(raw);
    if (new Date(parsed.expiresAt).getTime() > Date.now()) return parsed;
  } catch {
    // ignore malformed cache entry
  }
  return null;
}

export async function signInStaff(email: string, password: string): Promise<StaffSession> {
  const res = await fetch(`${API_BASE}/api/v1/auth/staff/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email, password }),
  });

  if (!res.ok) {
    let detail = "Đăng nhập thất bại.";
    try {
      const body = await res.json();
      detail = body?.message ?? detail;
    } catch {
      // ignore parse error
    }
    throw new Error(detail);
  }

  const data = await res.json();
  const session: StoredSession = {
    accessToken: data.token,
    email: data.email,
    role: data.role,
    expiresAt: data.expires_at,
  };
  if (typeof window !== "undefined") {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(session));
  }
  return session;
}

export function signOutStaff(): void {
  if (typeof window !== "undefined") {
    localStorage.removeItem(STORAGE_KEY);
  }
}

export function getStaffSession(): StaffSession | null {
  return readCached();
}
