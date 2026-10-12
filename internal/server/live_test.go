package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"nodevas/internal/engine"
	"nodevas/internal/httpapi/graph"
	"nodevas/internal/identity"
	"nodevas/internal/realtime"
	"strings"
	"testing"
	"time"
)

func TestAwarenessReachesTheRoomButNotItsSender(t *testing.T) {
	httpServer, _ := hubTestServer(t)
	first := joinRoom(t, httpServer, "main")
	second := joinRoom(t, httpServer, "main")
	elsewhere := joinRoom(t, httpServer, "other")

	first.send(map[string]any{"type": "awareness", "payload": "AQIDBA=="})

	event := second.await("awareness", nil)
	if event.Payload != "AQIDBA==" {
		t.Fatalf("payload = %q", event.Payload)
	}
	if event.From == "" {
		t.Fatal("relayed awareness carries no sender")
	}
	// The sender already has what it sent, and a round trip is exactly what
	// would make its own cursor lag behind its pointer.
	first.awaitNone("awareness", 300*time.Millisecond)
	elsewhere.awaitNone("awareness", 300*time.Millisecond)
}

func TestADragGhostTravelsWithItsOffset(t *testing.T) {
	httpServer, _ := hubTestServer(t)
	first := joinRoom(t, httpServer, "main")
	second := joinRoom(t, httpServer, "main")

	first.send(map[string]any{
		"type": "graph-drag", "ids": []string{"alpha", "beta"},
		"dx": 1.5, "dy": -2, "active": true,
	})
	event := second.await("graph-drag", nil)
	if event.Drag == nil {
		t.Fatal("no drag payload")
	}
	if len(event.Drag.IDs) != 2 || event.Drag.DX != 1.5 || event.Drag.DY != -2 || !event.Drag.Active {
		t.Fatalf("drag = %+v", event.Drag)
	}
}

func TestAnOversizedAwarenessUpdateClosesTheConnection(t *testing.T) {
	httpServer, _ := hubTestServer(t)
	peer := joinRoom(t, httpServer, "main")

	peer.send(map[string]any{
		"type":    "awareness",
		"payload": strings.Repeat("A", 32<<10),
	})
	// The read loop refuses it and hangs up; the drain goroutine closes the
	// channel on its way out.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-peer.events:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("an oversized awareness update was accepted")
		}
	}
}

// liveOpsServer is a hub server whose main project has a node to move.
func liveOpsServer(t *testing.T) *httptest.Server {
	t.Helper()
	server, pm := twoProjectServer(t)
	st := pm.Store()
	_, rev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveGraph(identity.Local, &engine.Graph{
		Version: 1,
		Nodes:   []*engine.Node{{ID: "alpha", Title: "Alpha"}},
	}, rev); err != nil {
		t.Fatalf("seed graph: %v", err)
	}
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	return httpServer
}

func TestGraphOpsAreSentToTheRoomAsOps(t *testing.T) {
	httpServer := liveOpsServer(t)
	peer := joinRoom(t, httpServer, "main")

	body := `{"ops":[{"kind":"move","nodeId":"alpha","x":3,"y":4}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/graph/ops", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(graph.PeerHeader, "abc123")
	response := httptest.NewRecorder()
	httpServer.Config.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}

	event := peer.await("graph-ops", nil)
	if event.From != "abc123" {
		t.Fatalf("from = %q, want the peer that made the change", event.From)
	}
	var ops []map[string]any
	if err := json.Unmarshal(event.Ops, &ops); err != nil {
		t.Fatalf("ops payload: %v", err)
	}
	if len(ops) != 1 || ops[0]["kind"] != "move" || ops[0]["nodeId"] != "alpha" {
		t.Fatalf("ops = %+v", ops)
	}
}

func TestAForgedPeerHeaderIsNotEchoed(t *testing.T) {
	httpServer := liveOpsServer(t)
	peer := joinRoom(t, httpServer, "main")

	body := `{"ops":[{"kind":"move","nodeId":"alpha","x":1,"y":1}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/graph/ops", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	// Not the alphabet the hub mints ids from; it goes to every client in the
	// room, so it is dropped rather than passed along.
	request.Header.Set(graph.PeerHeader, "<script>alert(1)</script>")
	response := httptest.NewRecorder()
	httpServer.Config.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}

	event := peer.await("graph-ops", func(realtime.Event) bool { return true })
	if event.From != "" {
		t.Fatalf("from = %q, want it dropped", event.From)
	}
}
