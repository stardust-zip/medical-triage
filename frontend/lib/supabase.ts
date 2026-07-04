// lib/supabase.ts – Supabase client for staff auth
//
// Phase 1: staff (nurse/admin) sign-in uses Supabase Auth so api-gateway can
// verify a real JWT (see services/gateway/auth.go requireStaff) instead of
// trusting a client-generated nurse id. Live nurse-dashboard updates go
// through queue-service's own WebSocket hub now (Phase 3, see
// lib/queueSocket.ts) — Supabase Realtime is no longer used for that.

import { createClient, SupabaseClient } from "@supabase/supabase-js";

const supabaseUrl = process.env.NEXT_PUBLIC_SUPABASE_URL ?? "";
const supabaseAnonKey = process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY ?? "";

// ---------------------------------------------------------------------------
// Singleton client
// ---------------------------------------------------------------------------

let _client: SupabaseClient | null = null;

/**
 * Returns the shared Supabase auth client instance.
 *
 * We use a lazy singleton so the client is only created once, and only when
 * it is actually needed (avoids SSR issues on the server).
 */
export function getSupabaseClient(): SupabaseClient {
  if (_client) return _client;

  if (!supabaseUrl || !supabaseAnonKey) {
    console.warn(
      "[supabase] NEXT_PUBLIC_SUPABASE_URL or NEXT_PUBLIC_SUPABASE_ANON_KEY " +
      "is not set. Staff sign-in will be unavailable."
    );
  }

  _client = createClient(supabaseUrl || "https://placeholder.supabase.co", supabaseAnonKey || "placeholder", {
    auth: {
      // Staff sign-in (nurse/admin) – keep the session across page loads.
      autoRefreshToken: true,
      persistSession: true,
    },
  });

  return _client;
}

// ---------------------------------------------------------------------------
// Staff auth (nurse / admin / owner)
// ---------------------------------------------------------------------------

/** Sign in a staff member; api-gateway verifies the resulting JWT itself. */
export async function signInStaff(email: string, password: string) {
  const { data, error } = await getSupabaseClient().auth.signInWithPassword({
    email,
    password,
  });
  if (error) throw new Error(error.message);
  return data.session;
}

export async function signOutStaff(): Promise<void> {
  await getSupabaseClient().auth.signOut();
}

export interface StaffSession {
  accessToken: string;
  email: string;
}

function toStaffSession(session: { access_token: string; user: { email?: string } } | null): StaffSession | null {
  if (!session) return null;
  return { accessToken: session.access_token, email: session.user.email ?? "" };
}

/** Current staff session (access token + email), or null if signed out. */
export async function getStaffSession(): Promise<StaffSession | null> {
  const { data } = await getSupabaseClient().auth.getSession();
  return toStaffSession(data.session);
}

export function onStaffAuthChange(callback: (session: StaffSession | null) => void): () => void {
  const { data } = getSupabaseClient().auth.onAuthStateChange((_event, session) => {
    callback(toStaffSession(session));
  });
  return () => data.subscription.unsubscribe();
}
