package realtime

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestANilHubSwallowsBroadcasts(t *testing.T) {
	var h *Hub
	h.Broadcast("x", "")
	h.BroadcastActor("actor", "x", "")
	h.BroadcastRoom("r", "x", "")
	h.BroadcastRoom("", "x", "")
	h.BroadcastOps("r", "peer", json.RawMessage(`[{"kind":"move"}]`))
	h.BroadcastOps("r", "peer", nil)
	h.send(Event{Type: "x"}, func(*wsClient) bool { return true })
	h.sendTo(nil, Event{Type: "x"})
}

func TestAWatcherWithoutAHubStillReportsEvents(t *testing.T) {
	root := t.TempDir()
	var got [][2]string
	emitEvent(nil, root, filepath.Join(root, "graph.yaml"), func(eventType, id string) {
		got = append(got, [2]string{eventType, id})
	})
	emitEvent(nil, root, filepath.Join(root, "nodes", "a.md"), func(eventType, id string) {
		got = append(got, [2]string{eventType, id})
	})
	if len(got) != 2 || got[0] != [2]string{"graph-changed", ""} || got[1] != [2]string{"node-changed", "a"} {
		t.Fatalf("events = %v", got)
	}
}
