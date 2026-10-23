package agenthook

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"nodevas/internal/agents"
	"nodevas/internal/agentsetup"
	agentsapi "nodevas/internal/httpapi/agents"
)

func TestParseInputReadsTheModel(t *testing.T) {
	for raw, want := range map[string]string{
		`{"session_id":"a","model":"claude-opus-4-1"}`:                             "claude-opus-4-1",
		`{"session_id":"a","model":{"id":"claude-sonnet-4-5","display_name":"S"}}`: "claude-sonnet-4-5",
		`{"session_id":"a"}`: "",
	} {
		if got := ParseInput([]byte(raw)).Model; got != want {
			t.Errorf("ParseInput(%s).Model = %q, want %q", raw, got, want)
		}
	}
}

func TestExcludedFolderReportsNothing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hub := agents.NewHub(nil, nil)
	router := gin.New()
	agentsapi.New(nil, hub).Register(router.Group("/api"))
	srv := httptest.NewServer(router)
	defer srv.Close()

	private := t.TempDir()
	config := filepath.Join(t.TempDir(), "agent-network.json")
	if err := agentsetup.SaveNetworkConfig(config, agentsetup.NetworkConfig{ExcludeFolders: []string{private}}); err != nil {
		t.Fatal(err)
	}
	ascii := false
	opts := Options{Server: srv.URL, Actor: "mcp:alice", HTTPClient: srv.Client(), Windows: &ascii, ExclusionFile: config}
	ctx := context.Background()

	for _, event := range []string{EventSessionStart, EventUserPromptSubmit, EventStop, EventSessionEnd} {
		if out := Run(ctx, "claude", event, Input{SessionID: "x", Cwd: filepath.Join(private, "repo")}, opts); out != "" {
			t.Fatalf("%s in an excluded folder printed %s", event, out)
		}
	}
	if _, ok := hub.SessionByID("", "claude:x"); ok {
		t.Fatal("a session in an excluded folder was registered")
	}

	// Outside it the hook works as ever, and reports the model.
	if out := Run(ctx, "claude", EventSessionStart, Input{SessionID: "y", Cwd: t.TempDir(), Model: "claude-opus-4-1"}, opts); out == "" {
		t.Fatal("session-start outside the excluded folder printed nothing")
	}
	s, ok := hub.SessionByID("", "claude:y")
	if !ok || s.Model != "claude-opus-4-1" {
		t.Fatalf("session = %+v, %v", s, ok)
	}
}
