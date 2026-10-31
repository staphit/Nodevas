package runner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"nodevas/internal/mcp"
	"nodevas/internal/runner/adapter"
	"nodevas/internal/runnerwire"
)

// A run's permission asks (the runner's relay: agy's gate, OpenCode over
// ACP) and its own asks (the sidecar's ask_user) belong to one session. The
// web matches an ask to its session by sessionId, and the hub counts pending
// asks per sessionId, so both carry the id the sidecar heartbeats under.
func TestRunAsksShareTheSidecarSessionID(t *testing.T) {
	var mu sync.Mutex
	ids := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		ids[r.URL.Path[strings.LastIndex(r.URL.Path, "/api/"):]] = body.SessionID
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/asks") {
			_, _ = io.WriteString(w, `{"ask":{"status":"approved"}}`)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(srv.Close)

	for _, tc := range []struct {
		agent string
		ad    adapter.Adapter
	}{
		{"antigravity", adapter.Antigravity{}},
		{"opencode", adapter.OpenCodeACP{}},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			job := runnerwire.Job{RunID: "run-abcdef12", Agent: tc.agent, Model: "m", Project: "demo", NodeID: "n-1", Prompt: "do it"}
			args := mcpArgs(srv.URL+"/r/secret", job, runnerwire.Repo{Name: "web"})
			env := adapter.RunEnv{Binary: tc.agent, Workdir: t.TempDir(), Branch: "b", TempDir: t.TempDir(),
				MCP: adapter.MCPServer{Command: "/opt/nodevas", Args: args}, PermissionRelay: true}
			cmd, err := tc.ad.Command(job, env)
			if err != nil {
				t.Fatal(err)
			}
			// The sidecar's environment: the agent's, plus NODEVAS_AGENT
			// from its MCP entry; nothing of the test's own session.
			for _, name := range []string{"CLAUDE_CODE_SESSION_ID", "CODEX_THREAD_ID", "OPENCODE_SESSION_ID",
				"GEMINI_SESSION_ID", "COPILOT_AGENT_SESSION_ID", "GROK_SESSION_ID", "NODEVAS_SESSION_ID"} {
				value := ""
				for _, kv := range cmd.Env {
					if k, v, _ := strings.Cut(kv, "="); strings.EqualFold(k, name) {
						value = v
					}
				}
				t.Setenv(name, value)
			}
			t.Setenv("NODEVAS_AGENT", tc.agent)

			asker, ok := adapter.HubAskerFromMCPArgs(args)
			if !ok {
				t.Fatal("no asker")
			}
			if got := asker.AskPermission(context.Background(), adapter.PermissionRequest{Tool: "bash", Input: "ls"}); !got.Allow {
				t.Fatalf("ask: %+v", got)
			}
			client, err := mcp.NewClient(mcp.ClientOptions{Server: srv.URL, Project: "demo", Actor: "run:" + tc.agent,
				SessionName: asker.SessionName})
			if err != nil {
				t.Fatal(err)
			}
			if err := client.Heartbeat(context.Background(), "", "", ""); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if ids["/api/agents/asks"] == "" || ids["/api/agents/asks"] != ids["/api/agents/heartbeat"] {
				t.Errorf("permission ask sessionId %q, sidecar sessionId %q", ids["/api/agents/asks"], ids["/api/agents/heartbeat"])
			}
		})
	}
}
