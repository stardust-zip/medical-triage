package main

// Per-tenant WebSocket fan-out for the nurse dashboard (Phase 3: "Real
// WebSocket hub... replaces the Supabase Realtime dependency"). Instead of
// the dashboard subscribing to Postgres row changes directly, it holds one
// WS connection here and gets a small "queue_changed" ping whenever a queue
// item is created, resolved, or timed out — the dashboard just refetches
// GET /queue/pending on that signal, so the payload never needs the row.
//
// wsWriter narrows *websocket.Conn to the one method the hub needs, so tests
// can register a fake instead of opening a real socket.

import (
	"sync"

	"github.com/gorilla/websocket"
)

type wsWriter interface {
	WriteMessage(messageType int, data []byte) error
}

type hub struct {
	mu    sync.Mutex
	conns map[string]map[wsWriter]struct{} // org_id -> connection set
}

func newHub() *hub {
	return &hub{conns: make(map[string]map[wsWriter]struct{})}
}

func (h *hub) register(orgID string, conn wsWriter) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conns[orgID] == nil {
		h.conns[orgID] = make(map[wsWriter]struct{})
	}
	h.conns[orgID][conn] = struct{}{}
}

func (h *hub) unregister(orgID string, conn wsWriter) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.conns[orgID], conn)
	if len(h.conns[orgID]) == 0 {
		delete(h.conns, orgID)
	}
}

func (h *hub) connectionCount(orgID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.conns[orgID])
}

// broadcast sends message to every connection registered for orgID. A write
// failure just drops that connection here; its own read loop (main.go) will
// hit the same closed socket and unregister it.
func (h *hub) broadcast(orgID string, message []byte) {
	h.mu.Lock()
	conns := make([]wsWriter, 0, len(h.conns[orgID]))
	for c := range h.conns[orgID] {
		conns = append(conns, c)
	}
	h.mu.Unlock()

	for _, c := range conns {
		_ = c.WriteMessage(websocket.TextMessage, message)
	}
}
