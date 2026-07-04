// lib/supabase.ts – Supabase client for staff auth + Realtime subscriptions
//
// Phase 1: staff (nurse/admin) sign-in uses Supabase Auth so api-gateway can
// verify a real JWT (see services/gateway/auth.go requireStaff) instead of
// trusting a client-generated nurse id. Realtime is still used on the Nurse
// Dashboard to receive live updates when new cases enter the
// human_triage_queue table.

import { createClient, SupabaseClient } from "@supabase/supabase-js";

const supabaseUrl = process.env.NEXT_PUBLIC_SUPABASE_URL ?? "";
const supabaseAnonKey = process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY ?? "";

// ---------------------------------------------------------------------------
// Singleton client
// ---------------------------------------------------------------------------

let _client: SupabaseClient | null = null;

/**
 * Returns the shared Supabase client instance.
 *
 * We use a lazy singleton so the client is only created once, and only when
 * it is actually needed (avoids SSR issues on the server).
 *
 * If the environment variables are not configured the client is still
 * created but Realtime subscriptions will fail gracefully – the dashboard
 * will fall back to polling instead.
 */
export function getSupabaseClient(): SupabaseClient {
  if (_client) return _client;

  if (!supabaseUrl || !supabaseAnonKey) {
    console.warn(
      "[supabase] NEXT_PUBLIC_SUPABASE_URL or NEXT_PUBLIC_SUPABASE_ANON_KEY " +
      "is not set. Realtime subscriptions will be unavailable. " +
      "The dashboard will fall back to polling."
    );
  }

  _client = createClient(supabaseUrl || "https://placeholder.supabase.co", supabaseAnonKey || "placeholder", {
    realtime: {
      params: {
        eventsPerSecond: 10,
      },
    },
    auth: {
      // Staff sign-in (nurse/admin) – keep the session across page loads.
      autoRefreshToken: true,
      persistSession: true,
    },
  });

  return _client;
}

// ---------------------------------------------------------------------------
// Typed Realtime payload shapes
// ---------------------------------------------------------------------------

/**
 * The shape of a row change event emitted by Supabase Realtime for the
 * `human_triage_queue` table.
 */
export interface QueueRealtimePayload {
  schema: string;
  table: string;
  commit_timestamp: string;
  eventType: "INSERT" | "UPDATE" | "DELETE";
  new: {
    id: string;
    patient_id: string;
    clinical_summary: string;
    suggested_dept: string | null;
    status: "PENDING" | "RESOLVED" | "TIMEOUT";
    created_at: string;
  };
  old: {
    id?: string;
    status?: string;
  };
  errors: string[] | null;
}

// ---------------------------------------------------------------------------
// Helper: subscribe to human_triage_queue changes
// ---------------------------------------------------------------------------

/**
 * Subscribe to all INSERT / UPDATE events on `human_triage_queue`.
 *
 * @param onEvent  Callback invoked with the typed payload on each event.
 * @returns        A cleanup function that unsubscribes and removes the channel.
 *
 * @example
 * ```ts
 * const cleanup = subscribeToQueue((payload) => {
 *   if (payload.eventType === "INSERT") refetchQueue();
 *   if (payload.eventType === "UPDATE") refetchQueue();
 * });
 *
 * // In a useEffect cleanup:
 * return () => cleanup();
 * ```
 */
export function subscribeToQueue(
  onEvent: (payload: QueueRealtimePayload) => void
): () => void {
  const client = getSupabaseClient();

  const channel = client
    .channel("human_triage_queue_changes")
    .on(
      "postgres_changes",
      {
        event: "*",           // INSERT | UPDATE | DELETE
        schema: "public",
        table: "human_triage_queue",
      },
      (payload) => {
        onEvent(payload as unknown as QueueRealtimePayload);
      }
    )
    .subscribe((status) => {
      if (status === "SUBSCRIBED") {
        console.info("[supabase] Realtime: subscribed to human_triage_queue");
      } else if (status === "CHANNEL_ERROR" || status === "TIMED_OUT") {
        console.warn("[supabase] Realtime channel error:", status);
      }
    });

  // Return cleanup function
  return () => {
    client.removeChannel(channel).catch(() => {
      // Ignore errors on cleanup
    });
  };
}

/**
 * Check whether the Supabase environment variables are properly configured.
 * Used by the dashboard to decide whether to enable Realtime or fall back
 * to polling.
 */
export function isSupabaseConfigured(): boolean {
  return Boolean(supabaseUrl && supabaseAnonKey &&
    supabaseUrl !== "https://placeholder.supabase.co");
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
