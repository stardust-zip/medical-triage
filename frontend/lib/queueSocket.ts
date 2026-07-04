// lib/queueSocket.ts – nurse-dashboard live-update socket (Phase 3).
//
// Replaces the old Supabase Realtime subscription: instead of listening to
// Postgres row changes directly, this holds one WebSocket connection to
// queue-service (via api-gateway) and gets a small "queue_changed" ping on
// any create/resolve/timeout. The dashboard just refetches the queue on
// that signal — same "any change → refresh" behavior the Realtime handler
// had, so callers don't need the payload's contents.
//
// Browsers can't set a custom Authorization header on a WebSocket handshake,
// so the staff token travels as a query param instead (api-gateway's
// requireStaffWS reads it from there — see services/gateway/auth.go).

const API_BASE = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8000";

function queueSocketURL(staffToken: string): string {
  const url = new URL("/ws/queue", API_BASE);
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  url.searchParams.set("token", staffToken);
  return url.toString();
}

/**
 * Open a live connection to the nurse queue and call `onChange` whenever the
 * server reports a change. Reconnects with a fixed backoff if the socket
 * drops, so a transient network blip doesn't silently stop live updates.
 *
 * @returns A cleanup function that closes the socket and stops reconnecting.
 */
export function subscribeToQueueSocket(
  staffToken: string,
  onChange: () => void,
  onStatusChange?: (connected: boolean) => void
): () => void {
  let socket: WebSocket | null = null;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  let stopped = false;

  const RECONNECT_DELAY_MS = 5_000;

  const connect = () => {
    if (stopped) return;

    socket = new WebSocket(queueSocketURL(staffToken));

    socket.onopen = () => onStatusChange?.(true);
    socket.onmessage = () => onChange();
    socket.onerror = () => socket?.close();
    socket.onclose = () => {
      onStatusChange?.(false);
      if (!stopped) {
        reconnectTimer = setTimeout(connect, RECONNECT_DELAY_MS);
      }
    };
  };

  connect();

  return () => {
    stopped = true;
    if (reconnectTimer) clearTimeout(reconnectTimer);
    socket?.close();
  };
}
