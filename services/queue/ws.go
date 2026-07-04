package main

// GET /ws/queue — nurse-dashboard live-update socket (see hub.go). Gateway
// authenticates the handshake (browsers can't set an Authorization header
// on a WebSocket upgrade) and forwards the same trusted X-Org-Id/X-User-Role
// headers as every other staff route, so this reuses requireStaff as-is.

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// Origin is enforced by api-gateway's CORS layer in front of this
	// internal-network-only service; nothing reaches queue-service directly.
	CheckOrigin: func(r *http.Request) bool { return true },
}

func handleQueueWS(h *hub) func(http.ResponseWriter, *http.Request, staffContext) {
	return func(w http.ResponseWriter, r *http.Request, ctx staffContext) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("ws upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		h.register(ctx.OrgID, conn)
		defer h.unregister(ctx.OrgID, conn)

		// Push-only channel: the only thing worth reading is the connection
		// closing, so we can unregister promptly instead of waiting for the
		// next failed broadcast to notice.
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}
}
