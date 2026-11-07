package agenthook

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/agents"
	"nodevas/internal/engine"
	agentsapi "nodevas/internal/httpapi/agents"
	"nodevas/internal/identity"
	"nodevas/internal/project"
	"nodevas/internal/realtime"
)

// post-tool-use tells the server which tool the agent just called, so its
// desk can show it; every other heartbeat, and a tool call the agent did not
// name, leaves "tool" out of the body (a server from before it refuses an
// unknown field).
func TestPostToolUseReportsTheTool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hub := agents.NewHub(nil, nil)
	router := gin.New()
	var mu sync.Mutex
	var bodies []map[string]any
	router.Use(func(c *gin.Context) {
		if c.Request.URL.Path == "/api/agents/heartbeat" {
			raw, _ := io.ReadAll(c.Request.Body)
			c.Request.Body = io.NopCloser(bytes.NewReader(raw))
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			mu.Lock()
			bodies = append(bodies, body)
			mu.Unlock()
		}
		c.Next()
	})
	agentsapi.New(nil, hub).Register(router.Group("/api"))
	srv := httptest.NewServer(router)
	defer srv.Close()

	ascii := false
	opts := Options{Server: srv.URL, Actor: "mcp:alice", HTTPClient: srv.Client(), Windows: &ascii}
	// heartbeats runs one hook and returns the heartbeat bodies it sent.
	heartbeats := func(event string, in Input) []map[string]any {
		t.Helper()
		mu.Lock()
		bodies = nil
		mu.Unlock()
		in.SessionID = "s1"
		Run(context.Background(), "claude", event, in, opts)
		mu.Lock()
		defer mu.Unlock()
		if len(bodies) == 0 {
			t.Fatalf("%s sent no heartbeat", event)
		}
		return bodies
	}
	session := func() agents.Session {
		t.Helper()
		s, ok := hub.SessionByID("", "claude:s1")
		if !ok {
			t.Fatal("session claude:s1 not registered")
		}
		return s
	}
	noTool := func(event string, sent []map[string]any) {
		t.Helper()
		for _, body := range sent {
			if _, ok := body["tool"]; ok {
				t.Fatalf("%s sent a tool: %v", event, body)
			}
		}
	}

	noTool(EventSessionStart, heartbeats(EventSessionStart, Input{}))
	noTool(EventUserPromptSubmit, heartbeats(EventUserPromptSubmit, Input{ToolName: "Edit"}))

	sent := heartbeats(EventPostToolUse, Input{ToolName: "Edit"})
	if len(sent) != 1 || sent[0]["tool"] != "Edit" {
		t.Fatalf("post-tool-use sent %v, want one heartbeat with tool Edit", sent)
	}
	if s := session(); s.LastTool != "Edit" || s.LastToolAt == 0 || s.State != agents.StateBusy {
		t.Fatalf("after Edit: %+v", s)
	}

	noTool(EventPostToolUse, heartbeats(EventPostToolUse, Input{}))
	if s := session(); s.LastTool != "Edit" {
		t.Fatalf("an unnamed tool call changed the tool: %q", s.LastTool)
	}

	// The turn ending is the agent going idle: no tool any more.
	noTool(EventStop, heartbeats(EventStop, Input{}))
	if s := session(); s.LastTool != "" || s.LastToolAt != 0 {
		t.Fatalf("after stop: %q at %d", s.LastTool, s.LastToolAt)
	}
}

// A hub from before the tool was reported refuses the field (its decoder
// disallows unknown fields). The hook must still heartbeat and still report
// the files an edit wrote: an upgraded runner against a hub not yet
// redeployed loses only the desk's activity, never change attribution.
func TestPostToolUseAgainstAnOldHubStillReportsChanges(t *testing.T) {
	saved := gitTimeout
	gitTimeout = time.Minute
	t.Cleanup(func() { gitTimeout = saved })
	repo := gitRepo(t)
	pm, err := project.NewManagerAt(t.TempDir(), realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pm.StopWatcher)
	if _, err := pm.Store().CreateNode(identity.Local, &engine.Node{ID: "n1", Title: "Fix it"}, ""); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	hub := agents.NewHub(nil, nil)
	router := gin.New()
	var mu sync.Mutex
	refused := 0
	router.Use(func(c *gin.Context) {
		if c.Request.URL.Path != "/api/agents/heartbeat" {
			c.Next()
			return
		}
		raw, _ := io.ReadAll(c.Request.Body)
		c.Request.Body = io.NopCloser(bytes.NewReader(raw))
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		if _, ok := body["tool"]; ok {
			mu.Lock()
			refused++
			mu.Unlock()
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": `invalid JSON body: json: unknown field "tool"`})
			return
		}
		c.Next()
	})
	agentsapi.New(pm, hub).Register(router.Group("/api"))
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	ctx := context.Background()
	ascii := false
	opts := Options{Server: srv.URL, Actor: "mcp:alice", HTTPClient: srv.Client(), Windows: &ascii}
	Run(ctx, "claude", EventSessionStart, Input{SessionID: "s1", Cwd: repo}, opts)
	if _, err := pm.Store().ClaimNode(identity.Local, "n1", "mcp:alice", time.Hour, ""); err != nil {
		t.Fatal(err)
	}
	Run(ctx, "claude", EventPostToolUse, Input{SessionID: "s1", Cwd: repo, ToolName: "Edit",
		Files: []string{filepath.Join(repo, "a.go")}}, opts)

	mu.Lock()
	n := refused
	mu.Unlock()
	if n == 0 {
		t.Fatal("the hook never sent the tool, so the old-hub path was not exercised")
	}
	name, err := filepath.Rel(pm.Workspace(), pm.Store().Root())
	if err != nil {
		t.Fatal(err)
	}
	got, err := hub.NodeSessionChanges(ctx, filepath.ToSlash(name), "n1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0].Files, []string{"a.go"}) {
		t.Fatalf("changes against an old hub: %+v", got)
	}
}
