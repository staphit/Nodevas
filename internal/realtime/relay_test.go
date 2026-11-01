package realtime

import (
	"fmt"
	"strings"
	"testing"

	"nodevas/internal/identity"
)

// A cursor or a drag ghost is superseded by the next one, so a connection that
// cannot keep up with the room's stream loses frames, never itself: dropping
// it would release its locks and its live documents, and anybody who may read
// the project could do that to an editor by moving a pointer.
func TestASlowReaderLosesCursorsNotItsConnection(t *testing.T) {
	ghostIDs := make([]string, maxDragIDs)
	for index := range ghostIDs {
		ghostIDs[index] = fmt.Sprintf("card-%03d-%s", index, strings.Repeat("x", 64))
	}
	cases := []struct {
		name    string
		sender  string
		message clientMessage
	}{
		{"a viewer's cursors", "viewer", clientMessage{Type: "awareness", Payload: strings.Repeat("A", maxAwarenessBytes)}},
		{"a writer's drag ghost", "writer", clientMessage{Type: "graph-drag", IDs: ghostIDs, DX: 1, DY: 1, Active: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hub := NewHub()
			check := &writers{ids: map[string]bool{"editor": true, "writer": true}}
			hub.SetWriteAccess(check.check)
			editor := docClient(hub, "editor", identity.RoleMember)
			// A real connection's queue, and nobody draining it: an editor on
			// a slow link.
			editor.outbound = make(chan clientOutbound, 32)
			sender := docClient(hub, tc.sender, identity.RoleMember)
			hub.acquireLock(editor, "n1", false)

			for range 40 {
				hub.handleMessage(sender, tc.message)
			}
			if !connected(hub, editor) {
				t.Fatalf("%s disconnected an editor that reads slowly", tc.name)
			}
			hub.mu.Lock()
			lock := hub.locks["room"]["n1"]
			hub.mu.Unlock()
			if lock == nil || lock.client != editor {
				t.Fatalf("the editor lost its lock to %s: %+v", tc.name, lock)
			}
			// The frames that can be dropped leave room for the ones that cannot.
			hub.BroadcastRoom("room", "graph-changed", "")
			if !connected(hub, editor) {
				t.Fatalf("%s filled the queue an ordinary event needed", tc.name)
			}
		})
	}
}

// What one account streams is sent on to everyone in its room, so the hub's
// uplink pays for it once per reader. Its connections share one allowance:
// opening more of them must not buy a bigger share.
func TestOneAccountsCursorsShareOneBudgetAcrossItsConnections(t *testing.T) {
	hub := NewHub()
	reader := docClient(hub, "reader", identity.RoleMember)
	senders := make([]*wsClient, 4)
	for index := range senders {
		sender := docClient(hub, fmt.Sprintf("socket-%d", index), identity.RoleMember)
		sender.actor = identity.Actor{ID: "flooder", Name: "flooder", Role: identity.RoleMember}
		senders[index] = sender
	}

	payload := strings.Repeat("A", maxAwarenessBytes)
	for range 40 {
		for _, sender := range senders {
			hub.handleMessage(sender, clientMessage{Type: "awareness", Payload: payload})
		}
	}
	relayed := 0
	for _, event := range docEvents(t, reader) {
		if event.Type == "awareness" {
			relayed += len(event.Payload)
		}
	}
	// One connection's stream burst, plus a second of refill for the time the
	// loop above takes.
	if limit := wsPayloadByteBurst + wsPayloadBytesPerSec; relayed > limit {
		t.Fatalf("one account relayed %d bytes of cursors through %d connections, past one connection's %d", relayed, len(senders), limit)
	}
}

// Who is in the room and which documents are held are tables, each one
// replacing the last, and anybody in the room can make them change: a viewer
// by saying which document it looks at, a writer by locking and unlocking. A
// reader that is behind skips a table rather than losing its connection, which
// would release its locks and its live documents, and once it has caught up
// it is sent the current one.
func TestARoomsTablesNeverDropASlowReader(t *testing.T) {
	cases := []struct {
		name  string
		flood func(hub *Hub)
		// What the editor is owed once it has caught up.
		wantPresence bool
	}{
		{"a viewer's presence", func(hub *Hub) {
			for socket := range 2 {
				viewer := docClient(hub, fmt.Sprintf("viewer-%d", socket), identity.RoleMember)
				viewer.actor = identity.Actor{ID: "viewer", Name: "viewer", Role: identity.RoleMember}
				for index := range wsMessageBurst {
					hub.handleMessage(viewer, clientMessage{Type: "presence", NodeID: []string{"x", "y"}[index%2]})
				}
			}
		}, true},
		{"a writer's lock and unlock", func(hub *Hub) {
			writer := docClient(hub, "writer", identity.RoleMember)
			for range wsMessageBurst {
				hub.handleMessage(writer, clientMessage{Type: "lock", NodeID: "x"})
				hub.handleMessage(writer, clientMessage{Type: "unlock", NodeID: "x"})
			}
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hub := NewHub()
			check := &writers{ids: map[string]bool{"editor": true, "writer": true}}
			hub.SetWriteAccess(check.check)
			editor := docClient(hub, "editor", identity.RoleMember)
			editor.outbound = make(chan clientOutbound, 32)
			hub.acquireLock(editor, "n1", false)

			tc.flood(hub)
			if !connected(hub, editor) {
				t.Fatalf("%s disconnected an editor that reads slowly", tc.name)
			}
			hub.mu.Lock()
			lock := hub.locks["room"]["n1"]
			hub.mu.Unlock()
			if lock == nil || lock.client != editor {
				t.Fatalf("the editor lost its lock to %s: %+v", tc.name, lock)
			}
			hub.BroadcastRoom("room", "graph-changed", "")
			if !connected(hub, editor) {
				t.Fatalf("%s filled the queue an ordinary event needed", tc.name)
			}

			var presence, locks *Event
			for _, event := range caughtUpEvents(t, hub, editor) {
				switch event.Type {
				case "presence":
					presence = &event
				case "locks":
					locks = &event
				}
			}
			if locks == nil || len(locks.Locks) != 1 || locks.Locks[0].NodeID != "n1" || !locks.Locks[0].Mine {
				t.Fatalf("after catching up the editor's lock table is %+v, want only its own n1", locks)
			}
			if !tc.wantPresence {
				return
			}
			if presence == nil {
				t.Fatal("the editor was never told who is in the room")
			}
			looking := map[string]string{}
			for _, peer := range presence.Peers {
				looking[peer.ID] = peer.NodeID
			}
			if len(looking) != 3 || looking["viewer-0"] != "y" || looking["viewer-1"] != "y" {
				t.Fatalf("after catching up the editor sees %v, want both viewers on y", looking)
			}
		})
	}
}

// caughtUpEvents is a write loop that has caught up: everything that was
// queued, then whatever the reader was owed once it had room.
func caughtUpEvents(t *testing.T, hub *Hub, client *wsClient) []Event {
	t.Helper()
	events := docEvents(t, client)
	for range 4 {
		hub.catchUp(client)
		more := docEvents(t, client)
		if len(more) == 0 {
			break
		}
		events = append(events, more...)
	}
	return events
}
