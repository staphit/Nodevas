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
