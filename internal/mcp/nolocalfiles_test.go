package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A client with NoLocalFiles (the copilot's bridge, inside the hub) has no
// folder: it attaches and saves nothing, and tells the roster nothing about
// the folder the process happens to run in.
func TestANoLocalFilesClientUsesNoFolderOfTheProcess(t *testing.T) {
	folder := t.TempDir()
	if err := os.MkdirAll(filepath.Join(folder, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, ".git", "HEAD"), []byte("ref: refs/heads/hub-branch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "secret.md"), []byte("hub secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(folder)
	t.Setenv("CLAUDE_PID", "4242")

	var heartbeat map[string]any
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/agents/heartbeat" {
			_ = json.NewDecoder(r.Body).Decode(&heartbeat)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer stub.Close()

	client, err := NewClient(ClientOptions{
		Server: stub.URL, Actor: "copilot:ann", AgentKind: "claude",
		HTTPClient: stub.Client(), NoLocalFiles: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.WorkDir() != "" || client.follow != "" {
		t.Fatalf("workDir %q, follow %q: want no folder and no session registry", client.WorkDir(), client.follow)
	}
	ctx := context.Background()
	if err := client.Heartbeat(ctx, "", "", ""); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"repo", "branch", "cwd"} {
		if heartbeat[field] != "" {
			t.Errorf("heartbeat %s = %v, want nothing read from the process's folder", field, heartbeat[field])
		}
	}
	if _, err := client.SendMessage(ctx, sendMessageInput{To: "x", Message: "x", Files: []string{"secret.md"}}); err == nil ||
		!strings.Contains(err.Error(), "no working directory") {
		t.Fatalf("send_message with a file = %v, want refused", err)
	}
	if _, err := client.SaveAttachment(ctx, "abc", "note.md"); err == nil || !strings.Contains(err.Error(), "no working directory") {
		t.Fatalf("save_attachment = %v, want refused", err)
	}

	// The same client without the flag is the agent's own, in its folder.
	local, err := NewClient(ClientOptions{Server: stub.URL, Actor: "mcp:ann", AgentKind: "claude", HTTPClient: stub.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if local.WorkDir() == "" || local.branch != "hub-branch" || local.follow != "claude" {
		t.Fatalf("local client: workDir %q, branch %q, follow %q", local.WorkDir(), local.branch, local.follow)
	}
}
