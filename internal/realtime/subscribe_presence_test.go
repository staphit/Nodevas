package realtime

import (
	"encoding/json"
	"testing"

	"nodevas/internal/identity"
)

// presenceFrames drains client's queue and counts the presence tables in it.
func presenceFrames(client *wsClient) int {
	n := 0
	for {
		select {
		case out := <-client.outbound:
			var event Event
			if json.Unmarshal(out.payload, &event) == nil && event.Type == "presence" {
				n++
			}
		default:
			return n
		}
	}
}

// A subscribe publishes a room's presence once, and only to a room whose
// table it changed: subscribing again to the room a socket is already in
// tells nobody else anything (the socket itself gets the table), and moving
// rooms tells each of the two rooms once.
func TestSubscribePublishesPresenceOncePerChangedRoom(t *testing.T) {
	hub := NewHub()
	hub.SetProjectResolver(func(name string) (string, error) { return name, nil })
	observer := docClient(hub, "observer", identity.RoleMember)
	mover := docClient(hub, "mover", identity.RoleMember)
	hub.subscribe(observer, "a")
	hub.subscribe(mover, "a")
	presenceFrames(observer)
	presenceFrames(mover)

	for range 5 {
		hub.subscribe(mover, "a")
	}
	if n := presenceFrames(observer); n != 0 {
		t.Errorf("subscribing again to the same room sent the room %d presence tables, want 0", n)
	}
	if n := presenceFrames(mover); n != 5 {
		t.Errorf("the re-subscribing socket got %d tables, want one per subscribe", n)
	}

	for range 3 {
		hub.subscribe(mover, "b")
		hub.subscribe(mover, "a")
	}
	if n := presenceFrames(observer); n != 6 {
		t.Errorf("room a got %d presence tables for 6 moves in or out, want 6", n)
	}
}
