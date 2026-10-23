package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"nodevas/internal/agents"
)

func roster(t *testing.T, url string) map[string]agents.Session {
	t.Helper()
	resp, err := http.Get(url + "/api/agents?all=true")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Sessions []agents.Session `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	out := make(map[string]agents.Session, len(body.Sessions))
	for _, s := range body.Sessions {
		out[s.ID] = s
	}
	return out
}

func writeClaudeRegistry(t *testing.T, dir, pid, sessionID string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"pid":` + pid + `,"sessionId":"` + sessionID + `"}`
	if err := os.WriteFile(filepath.Join(dir, "sessions", pid+".json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

// After Claude Code's /clear the same MCP process serves a new conversation:
// the client moves to the new session ID, keeps its name, and signs the old
// one off, so the roster shows one agent and its mail follows it.
func TestClaudeClearMovesSessionAndKeepsName(t *testing.T) {
	url, _ := liveServer(t)
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	t.Setenv("CLAUDE_PID", "4242")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "conv-1")
	writeClaudeRegistry(t, dir, "4242", "conv-1")

	ctx := context.Background()
	client, err := NewClient(ClientOptions{Server: url, Actor: "mcp:claude", AgentKind: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Heartbeat(ctx, "", "", ""); err != nil {
		t.Fatal(err)
	}
	name := client.SessionName()
	if name == "" {
		t.Fatal("no session name after first heartbeat")
	}

	writeClaudeRegistry(t, dir, "4242", "conv-2")
	if err := client.Heartbeat(ctx, "", "", ""); err != nil {
		t.Fatal(err)
	}
	got := roster(t, url)
	next, ok := got["claude:conv-2"]
	if !ok || !next.Online || next.Name != name {
		t.Fatalf("new session = %+v (ok=%v), want online and named %q", next, ok, name)
	}
	if old := got["claude:conv-1"]; old.Online {
		t.Fatalf("old session still online: %+v", old)
	}
	if client.SessionName() != name {
		t.Fatalf("client name = %q, want %q", client.SessionName(), name)
	}
}

// One Codex MCP process serves every thread; each tool call's _meta names the
// thread, and each thread keeps its own session and name.
func TestCodexThreadsFollowToolCallMeta(t *testing.T) {
	url, _ := liveServer(t)
	t.Setenv("CLAUDE_PID", "")
	ctx := context.Background()
	server, err := NewServer(ctx, Options{Server: url, Actor: "mcp:codex", AgentKind: "codex", AgentRole: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	serverSide, clientSide := sdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverSide, nil); err != nil {
		t.Fatal(err)
	}
	session, err := sdk.NewClient(&sdk.Implementation{Name: "codex", Version: "v1"}, nil).Connect(ctx, clientSide, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	self := func(thread string) string {
		t.Helper()
		res, err := session.CallTool(ctx, &sdk.CallToolParams{
			Meta: sdk.Meta{"sessionId": thread},
			Name: "list_team",
		})
		if err != nil || res.IsError {
			t.Fatalf("list_team as %s: %v %+v", thread, err, res)
		}
		raw, _ := json.Marshal(res.StructuredContent)
		var out listTeamOutput
		_ = json.Unmarshal(raw, &out)
		return out.Self
	}
	first := self("t1")
	second := self("t2")
	if first == "" || second == "" || first == second {
		t.Fatalf("thread names = %q, %q; want two distinct names", first, second)
	}
	if again := self("t1"); again != first {
		t.Fatalf("t1 renamed: %q, want %q", again, first)
	}
	got := roster(t, url)
	if !got["codex:t1"].Online || !got["codex:t2"].Online {
		t.Fatalf("roster = %+v, want both threads online", got)
	}
}
