package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"nodevas/internal/realtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// wsPeer is one browser for the purposes of these tests. Frames are drained by
// a goroutine: coder/websocket tears the connection down when a read is
// cancelled, so a test cannot poll with per-read deadlines and keep the socket.
type wsPeer struct {
	t      *testing.T
	conn   *websocket.Conn
	events chan realtime.Event
}

func dialHub(t *testing.T, server *httptest.Server) *wsPeer {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	// Match browser behavior for the largest legal collaboration frame. The
	// coder/websocket test client otherwise defaults below the protocol's 48 KiB
	// snapshot chunk even though the server correctly stays under 64 KiB.
	conn.SetReadLimit(64 << 10)
	peer := &wsPeer{t: t, conn: conn, events: make(chan realtime.Event, 64)}
	go func() {
		defer close(peer.events)
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var event realtime.Event
			if err := json.Unmarshal(data, &event); err != nil {
				continue
			}
			peer.events <- event
		}
	}()
	t.Cleanup(func() { _ = conn.CloseNow() })
	return peer
}

func (p *wsPeer) send(message map[string]any) {
	p.t.Helper()
	data, err := json.Marshal(message)
	if err != nil {
		p.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.conn.Write(ctx, websocket.MessageText, data); err != nil {
		p.t.Fatalf("write %v: %v", message, err)
	}
}

// await waits for the first event of a type that also satisfies match, so
// unrelated traffic cannot make a test flaky.
func (p *wsPeer) await(kind string, match func(realtime.Event) bool) realtime.Event {
	p.t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case event, ok := <-p.events:
			if !ok {
				p.t.Fatalf("connection closed while waiting for %q", kind)
			}
			if event.Type == kind && (match == nil || match(event)) {
				return event
			}
		case <-timeout:
			p.t.Fatalf("timed out waiting for a %q event", kind)
		}
	}
}

// awaitNone fails if an event of the given type arrives within the window.
func (p *wsPeer) awaitNone(kind string, window time.Duration) {
	p.t.Helper()
	timeout := time.After(window)
	for {
		select {
		case event, ok := <-p.events:
			if !ok {
				return
			}
			if event.Type == kind {
				p.t.Fatalf("received an unwanted %q event: %+v", kind, event)
			}
		case <-timeout:
			return
		}
	}
}

func hubTestServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	server, _ := twoProjectServer(t)
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	return httpServer, server
}

func joinRoom(t *testing.T, server *httptest.Server, project string) *wsPeer {
	t.Helper()
	peer := dialHub(t, server)
	peer.await("hello", nil)
	peer.send(map[string]any{"type": "subscribe", "project": project})
	peer.await("presence", nil)
	return peer
}

func TestPresenceListsEveryoneInTheProject(t *testing.T) {
	httpServer, _ := hubTestServer(t)

	first := joinRoom(t, httpServer, "main")
	second := joinRoom(t, httpServer, "main")
	first.await("presence", func(event realtime.Event) bool { return len(event.Peers) == 2 })

	second.send(map[string]any{"type": "presence", "nodeId": "alpha", "editing": true})
	first.await("presence", func(event realtime.Event) bool {
		for _, peer := range event.Peers {
			if peer.NodeID == "alpha" && peer.Editing {
				return true
			}
		}
		return false
	})
}

// A client watching one project must not be woken by another project's edits.
func TestRoomsKeepProjectEventsApart(t *testing.T) {
	httpServer, server := hubTestServer(t)

	watcher := joinRoom(t, httpServer, "other")

	// An edit in the active project ("main") is not this client's business.
	server.hub.BroadcastRoom(server.pm.Store().Root(), "graph-changed", "")
	watcher.awaitNone("graph-changed", 300*time.Millisecond)

	other, err := server.pm.StoreFor("other")
	if err != nil {
		t.Fatal(err)
	}
	server.hub.BroadcastRoom(other.Root(), "graph-changed", "")
	watcher.await("graph-changed", nil)
}

// A client that never subscribed still sees everything: older clients and the
// popout editor do not send a subscription.
func TestUnsubscribedClientStillReceivesEvents(t *testing.T) {
	httpServer, server := hubTestServer(t)

	peer := dialHub(t, httpServer)
	peer.await("hello", nil)

	server.hub.BroadcastRoom(server.pm.Store().Root(), "graph-changed", "")
	peer.await("graph-changed", nil)
}

func TestNodeLockIsHeldUntilReleasedAndCanBeStolen(t *testing.T) {
	httpServer, _ := hubTestServer(t)

	holder := joinRoom(t, httpServer, "main")
	rival := joinRoom(t, httpServer, "main")

	holder.send(map[string]any{"type": "lock", "nodeId": "alpha"})
	locks := rival.await("locks", func(event realtime.Event) bool { return len(event.Locks) == 1 })
	if locks.Locks[0].NodeID != "alpha" || locks.Locks[0].Mine {
		t.Fatalf("locks = %+v, want alpha held by the other client", locks.Locks)
	}

	rival.send(map[string]any{"type": "lock", "nodeId": "alpha"})
	denied := rival.await("lock-denied", nil)
	if denied.ID != "alpha" {
		t.Fatalf("lock-denied = %+v", denied)
	}

	// "Force edit" is a deliberate act, so stealing is allowed.
	rival.send(map[string]any{"type": "lock", "nodeId": "alpha", "steal": true})
	rival.await("locks", func(event realtime.Event) bool {
		return len(event.Locks) == 1 && event.Locks[0].Mine
	})

	rival.send(map[string]any{"type": "unlock", "nodeId": "alpha"})
	holder.await("locks", func(event realtime.Event) bool { return len(event.Locks) == 0 })
}

// A browser that closes mid-edit must not leave the document locked.
func TestClosingAConnectionReleasesItsLocks(t *testing.T) {
	httpServer, _ := hubTestServer(t)

	holder := joinRoom(t, httpServer, "main")
	observer := joinRoom(t, httpServer, "main")

	holder.send(map[string]any{"type": "lock", "nodeId": "alpha"})
	observer.await("locks", func(event realtime.Event) bool { return len(event.Locks) == 1 })

	_ = holder.conn.CloseNow()
	observer.await("locks", func(event realtime.Event) bool { return len(event.Locks) == 0 })
}
