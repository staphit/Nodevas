package realtime

import (
	"testing"

	"nodevas/internal/identity"
)

func eventTypes(t *testing.T, client *wsClient) []string {
	t.Helper()
	var out []string
	for _, event := range docEvents(t, client) {
		out = append(out, event.Type)
	}
	return out
}

func got(t *testing.T, client *wsClient, kind string) bool {
	t.Helper()
	for _, k := range eventTypes(t, client) {
		if k == kind {
			return true
		}
	}
	return false
}

// BroadcastReaders reaches a project's readers wherever they are watching,
// and nobody the access check keeps out of it.
func TestBroadcastReaders(t *testing.T) {
	hub := NewHub()
	hub.SetAccess(func(actor identity.Actor, room string) bool {
		return room != "secret" || actor.ID == "insider"
	})
	inRoom := docClientIn(hub, "watcher", "secret")
	insider := docClientIn(hub, "insider", "elsewhere")
	outsider := docClientIn(hub, "outsider", "elsewhere")
	everywhere := docClientIn(hub, "unsubscribed", "")

	hub.BroadcastReaders("secret", "building-changed", "")
	if !got(t, inRoom, "building-changed") {
		t.Fatal("the room's own watcher missed it")
	}
	if !got(t, insider, "building-changed") {
		t.Fatal("a reader watching another project missed it")
	}
	if got(t, outsider, "building-changed") {
		t.Fatal("a client that may not read the project received it")
	}
	if got(t, everywhere, "building-changed") {
		t.Fatal("an unsubscribed client that may not read the project received it")
	}

	// BroadcastRoom, by contrast, stays in the room.
	hub.BroadcastRoom("secret", "runs-changed", "")
	if got(t, insider, "runs-changed") {
		t.Fatal("BroadcastRoom left the room")
	}
}
