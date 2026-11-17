package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"nodevas/internal/store"
)

// planDraftNote is the hub's note for a recorded draft (graph.PlanDraftNote);
// the sidecar passes on whatever the hub says.
const planDraftNote = "Recorded as a draft for the person to review; nothing was created yet. Do not link to these ids."

// A draft plan run's create_subgraph (P6) is held on the hub: the sidecar
// reports the hub's note and the provisional ids, not "Created 0 nodes".
func TestCreateSubgraphReportsAHeldDraft(t *testing.T) {
	var refuse atomic.Bool
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/api/graph/subgraph" {
			// The session's own start-up calls (auth status, heartbeat).
			_, _ = w.Write([]byte(`{}`))
			return
		}
		if refuse.Load() {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"this plan run is a draft: put every task in one create_subgraph call","code":"PLAN_RUN_DRAFTS"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"draft":true,"ids":{"t1":"draft:t1"},"created":[],"issues":[],` +
			`"note":"` + planDraftNote + `","draftNodes":1}`))
	}))
	t.Cleanup(hub.Close)

	session := mcpSession(t, hub.URL, "")
	var out createSubgraphOutput
	result := call(t, session, "create_subgraph", map[string]any{
		"nodes": []map[string]any{{"key": "t1", "title": "Add the users table"}},
	}, &out)
	if result.IsError {
		t.Fatal(errorText(result))
	}
	if out.Note != planDraftNote || out.IDs["t1"] != "draft:t1" || len(out.Created) != 0 {
		t.Fatalf("output = %+v", out)
	}

	refuse.Store(true)
	client, err := NewClient(ClientOptions{Server: hub.URL, Actor: "mcp:test"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CreateSubgraph(context.Background(), store.SubgraphSpec{Nodes: []store.SubgraphNode{{Key: "t1", Title: "x"}}})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != CodePlanRunDrafts || !strings.Contains(apiErr.Message, "one create_subgraph call") {
		t.Fatalf("err = %#v", err)
	}
	// And through the tool the agent reads the hub's message.
	result = call(t, session, "create_subgraph", map[string]any{
		"nodes": []map[string]any{{"key": "t1", "title": "x"}},
	}, nil)
	if !result.IsError || !strings.Contains(errorText(result), "one create_subgraph call") {
		t.Fatalf("tool result = %+v", errorText(result))
	}
}
