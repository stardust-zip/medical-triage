// lib/patientSession.ts – anonymous, token-bound patient sessions.
//
// Phase 1 replaces the old free-text `patient_id` (a client-generated string
// stashed in localStorage and sent as a body field) with a signed session
// token minted by api-gateway: POST /api/v1/session/anonymous. The token is
// still cached in localStorage (same "survives a page refresh" property the
// old code had), but the server can now verify it instead of trusting
// whatever the client claims.

const API_BASE = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8000";
const ORG_SLUG = process.env.NEXT_PUBLIC_ORG_SLUG ?? "evergreen-demo";
const STORAGE_KEY = "triageos_patient_session";

interface StoredSession {
  token: string;
  expiresAt: string;
}

async function mintSession(): Promise<StoredSession> {
  const res = await fetch(`${API_BASE}/api/v1/session/anonymous`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ org_slug: ORG_SLUG }),
  });
  if (!res.ok) {
    throw new Error(`Could not start a patient session (HTTP ${res.status}).`);
  }
  const data = await res.json();
  return { token: data.token, expiresAt: data.expires_at };
}

function readCached(): StoredSession | null {
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

/** Returns a valid patient-session bearer token, minting a fresh one if needed. */
export async function getPatientToken(): Promise<string> {
  const cached = readCached();
  if (cached) return cached.token;

  const session = await mintSession();
  if (typeof window !== "undefined") {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(session));
  }
  return session.token;
}
