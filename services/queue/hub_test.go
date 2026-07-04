package main

import (
	"errors"
	"testing"
)

type fakeConn struct {
	messages [][]byte
	failing  bool
}

func (f *fakeConn) WriteMessage(_ int, data []byte) error {
	if f.failing {
		return errors.New("write failed")
	}
	f.messages = append(f.messages, data)
	return nil
}

func TestHubBroadcastReachesOnlyTheTargetOrg(t *testing.T) {
	h := newHub()
	orgA := &fakeConn{}
	orgB := &fakeConn{}
	h.register("org-a", orgA)
	h.register("org-b", orgB)

	h.broadcast("org-a", []byte("ping"))

	if len(orgA.messages) != 1 {
		t.Fatalf("org-a connection got %d messages, want 1", len(orgA.messages))
	}
	if len(orgB.messages) != 0 {
		t.Fatalf("org-b connection got %d messages, want 0 (cross-tenant leak)", len(orgB.messages))
	}
}

func TestHubUnregisterStopsFurtherBroadcasts(t *testing.T) {
	h := newHub()
	conn := &fakeConn{}
	h.register("org-a", conn)
	h.unregister("org-a", conn)

	h.broadcast("org-a", []byte("ping"))

	if len(conn.messages) != 0 {
		t.Fatal("unregistered connection still received a broadcast")
	}
	if h.connectionCount("org-a") != 0 {
		t.Fatalf("connectionCount = %d, want 0 after unregister", h.connectionCount("org-a"))
	}
}

func TestHubBroadcastSkipsFailedConnectionsWithoutPanicking(t *testing.T) {
	h := newHub()
	good := &fakeConn{}
	bad := &fakeConn{failing: true}
	h.register("org-a", good)
	h.register("org-a", bad)

	h.broadcast("org-a", []byte("ping")) // must not panic despite bad's write error

	if len(good.messages) != 1 {
		t.Fatalf("good connection got %d messages, want 1", len(good.messages))
	}
}

func TestHubSupportsMultipleConnectionsPerOrg(t *testing.T) {
	h := newHub()
	h.register("org-a", &fakeConn{})
	h.register("org-a", &fakeConn{})

	if got := h.connectionCount("org-a"); got != 2 {
		t.Fatalf("connectionCount = %d, want 2", got)
	}
}
