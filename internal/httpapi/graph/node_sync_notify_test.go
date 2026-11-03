package graph

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"

	"nodevas/internal/engine"
	projectdomain "nodevas/internal/project"
	"nodevas/internal/realtime"
)

// hubEvents connects one websocket client to hub and returns a channel of
// "type:id" for every event it receives.
func hubEvents(t *testing.T, hub *realtime.Hub) <-chan string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/ws", hub.Handle)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	out := make(chan string, 64)
	go func() {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var event struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			}
			if json.Unmarshal(data, &event) == nil {
				out <- event.Type + ":" + event.ID
			}
		}
	}()
	// The hello proves the connection is registered before anything is sent.
	waitForEvent(t, out, "hello:")
	return out
}

func waitForEvent(t *testing.T, events <-chan string, want string) []string {
	t.Helper()
	var seen []string
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-events:
			seen = append(seen, event)
			if event == want || (strings.HasSuffix(want, ":") && strings.HasPrefix(event, want)) {
				return seen
			}
		case <-deadline:
			t.Fatalf("no %q within 3s; saw %v", want, seen)
		}
	}
}

func graphTestAPIWithHub(t *testing.T) (*API, *realtime.Hub, *projectdomain.ProjectManager) {
	t.Helper()
	pm, err := projectdomain.NewManagerAt(t.TempDir(), realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pm.Close)
	hub := realtime.NewHub()
	t.Cleanup(func() { _ = hub.Close() })
	return New(pm, hub), hub, pm
}

// Adding a dependency rewrites the dependent node's file (its frontmatter
// carries `requires`). The store marks that write as its own, so the file
// watcher stays quiet, and an editor holding the node open kept the old
// revision: its next keystroke was refused as a version conflict against a
// change nobody made to the text. The save has to say which documents it
// rewrote.
func TestGraphSaveAnnouncesTheNodeFilesItRewrote(t *testing.T) {
	api, hub, pm := graphTestAPIWithHub(t)
	rev := seedGraph(t, pm.Store(),
		&engine.Node{ID: "alpha", Title: "Alpha"},
		&engine.Node{ID: "beta", Title: "Beta"},
	)
	events := hubEvents(t, hub)

	body := `{"baseRev":"` + rev + `","graph":{"version":1,"nodes":[
		{"id":"alpha","title":"Alpha"},
		{"id":"beta","title":"Beta","requires":"alpha"}
	]}}`
	request := httptest.NewRequest(http.MethodPut, "/api/graph", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.putGraph(ginContext(response, request))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	seen := waitForEvent(t, events, "node-changed:beta")
	for _, event := range seen {
		if event == "node-changed:alpha" {
			t.Fatalf("alpha's file did not change but was announced: %v", seen)
		}
	}
}

func TestGraphOpsAnnounceTheNodeFilesTheyRewrote(t *testing.T) {
	api, hub, pm := graphTestAPIWithHub(t)
	seedGraph(t, pm.Store(),
		&engine.Node{ID: "alpha", Title: "Alpha"},
		&engine.Node{ID: "beta", Title: "Beta"},
	)
	events := hubEvents(t, hub)

	body := `{"ops":[{"kind":"add-edge","from":"alpha","to":"beta"}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/graph/ops", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.postGraphOps(ginContext(response, request))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	waitForEvent(t, events, "node-changed:beta")
}
