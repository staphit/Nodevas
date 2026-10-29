package adapter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"nodevas/internal/runnerwire"
)

// fakeAsker answers permission asks in a test and records them.
type fakeAsker struct {
	mu       sync.Mutex
	asked    []PermissionRequest
	decision PermissionDecision
	wait     chan struct{} // when set, an ask waits for it (or its context)
}

func (f *fakeAsker) AskPermission(ctx context.Context, req PermissionRequest) PermissionDecision {
	f.mu.Lock()
	f.asked = append(f.asked, req)
	wait := f.wait
	f.mu.Unlock()
	if wait != nil {
		select {
		case <-wait:
		case <-ctx.Done():
			return PermissionDecision{Reason: "timed out"}
		}
	}
	return f.decision
}

func (f *fakeAsker) requests() []PermissionRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]PermissionRequest(nil), f.asked...)
}

// With permissionRelay the gate asks the hub where it would have refused a
// shell command only because nobody could approve it; its other denials
// come first and are never asked about, and a read-only run never asks.
func TestAntigravityGateRelay(t *testing.T) {
	ws, other := t.TempDir(), t.TempDir()
	shell := `{"toolCall":{"name":"run_command","args":{"CommandLine":"go test ./..."}},"workspacePaths":[` + jsonPath(ws) + `]}`
	decide := func(asker PermissionAsker, payload string, readOnly bool) map[string]string {
		t.Helper()
		var got map[string]string
		out := AntigravityGateRelay(context.Background(), []byte(payload), readOnly, asker)
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("output %q: %v", out, err)
		}
		return got
	}

	allow := &fakeAsker{decision: PermissionDecision{Allow: true}}
	if got := decide(allow, shell, false); got["decision"] != "allow" {
		t.Errorf("approved shell: %v", got)
	}
	if asked := allow.requests(); len(asked) != 1 || asked[0].Tool != "run_command" || !strings.Contains(asked[0].Input, "go test ./...") {
		t.Errorf("asked %+v", asked)
	}

	deny := &fakeAsker{decision: PermissionDecision{Reason: "patrick denied it: not now"}}
	if got := decide(deny, shell, false); got["decision"] != "deny" || !strings.Contains(got["reason"], "not now") {
		t.Errorf("denied shell: %v", got)
	}

	never := &fakeAsker{decision: PermissionDecision{Allow: true}}
	for name, payload := range map[string]string{
		"other mcp":    `{"toolCall":{"name":"call_mcp_tool","args":{"ServerName":"unityMCP","ToolName":"x"}}}`,
		"browser":      `{"toolCall":{"name":"browser_click_element","args":{}}}`,
		"web":          `{"toolCall":{"name":"read_url_content","args":{}}}`,
		"file outside": `{"toolCall":{"name":"read_file","args":{"Path":` + jsonPath(other, "a") + `}},"workspacePaths":[` + jsonPath(ws) + `]}`,
	} {
		if got := decide(never, payload, false); got["decision"] != "deny" {
			t.Errorf("%s: %v", name, got)
		}
	}
	if got := decide(never, shell, true); got["decision"] != "deny" {
		t.Errorf("read-only shell: %v", got)
	}
	for name, payload := range map[string]string{
		"nodevas mcp": `{"toolCall":{"name":"call_mcp_tool","args":{"ServerName":"nodevas","ToolName":"ask_user"}}}`,
		"file inside": `{"toolCall":{"name":"write_to_file","args":{"TargetFile":"a.txt"}},"workspacePaths":[` + jsonPath(ws) + `]}`,
		"garbage":     `not json`,
	} {
		if got := decide(never, payload, false); got["decision"] != "ask" {
			t.Errorf("%s: %v", name, got)
		}
	}
	if asked := never.requests(); len(asked) != 0 {
		t.Errorf("asked about %+v", asked)
	}
}

// The hook binary: --relay reaches the hub through the run's MCP arguments
// in RunMCPEnv; without them it falls back to the plain gate.
func TestAntigravityGateMainRelay(t *testing.T) {
	hub, srv := newFakeHub(t, `{"id":"a1","status":"approved"}`)
	args, _ := json.Marshal(testMCPArgs(srv.URL))
	t.Setenv(RunMCPEnv, string(args))
	shell := []byte(`{"toolCall":{"name":"run_command","args":{"CommandLine":"go vet ./..."}}}`)
	if out := AntigravityGateMain([]string{AntigravityGateRelayFlag}, shell); !strings.Contains(out, `"allow"`) {
		t.Errorf("relay: %s", out)
	}
	if ask := hub.last(); ask["kind"] != "permission" || ask["tool"] != "run_command" {
		t.Errorf("ask = %v", ask)
	}
	if out := AntigravityGateMain(nil, shell); !strings.Contains(out, `"deny"`) || hub.count() != 1 {
		t.Errorf("no relay flag: %s (asks %d)", out, hub.count())
	}
	if out := AntigravityGateMain([]string{AntigravityGateReadOnly, AntigravityGateRelayFlag}, shell); !strings.Contains(out, `"deny"`) || hub.count() != 1 {
		t.Errorf("read-only: %s (asks %d)", out, hub.count())
	}
	t.Setenv(RunMCPEnv, "")
	if out := AntigravityGateMain([]string{AntigravityGateRelayFlag}, shell); !strings.Contains(out, `"deny"`) {
		t.Errorf("relay without the run's MCP: %s", out)
	}
}

// The relay's hook outlasts the hub's wait for a person; without it the
// hook keeps its short timeout. Plan, auto, full and read-only runs never
// relay.
func TestAntigravityGateHooksRelay(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "nodevas.exe")
	if antigravityGateHooks(exe, false, false) == nil {
		t.Skip("no hook path on this machine")
	}
	for _, tc := range []struct {
		mode      string
		relay     bool
		review    bool
		wantRelay bool
	}{
		{"", true, false, true},
		{"accept-edits", true, false, true},
		{"accept-edits", false, false, false},
		{"plan", true, false, false},
		{"accept-edits", true, true, false},
	} {
		tmp := t.TempDir()
		job := runnerwire.Job{RunID: "run-1", NodeID: "node-1", Prompt: "do it"}
		if tc.review {
			job.Kind = runnerwire.JobReview
		}
		env := RunEnv{Binary: "agy", Workdir: t.TempDir(), Branch: "b", TempDir: tmp,
			MCP: MCPServer{Command: exe, Args: []string{"mcp"}}, PermissionMode: tc.mode, PermissionRelay: tc.relay}
		if _, err := (Antigravity{}).Command(job, env); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(tmp, antigravityRootDir, ".agents", "hooks.json"))
		if err != nil {
			t.Fatalf("%+v: %v", tc, err)
		}
		var hooks map[string]map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(data, &hooks); err != nil {
			t.Fatal(err)
		}
		hook := hooks["nodevas-run"]["PreToolUse"][0].Hooks[0]
		if got := strings.HasSuffix(hook.Command, " "+AntigravityGateRelayFlag); got != tc.wantRelay {
			t.Errorf("%+v: command %q", tc, hook.Command)
		}
		if (tc.wantRelay && hook.Timeout < 330) || (!tc.wantRelay && hook.Timeout != 10) {
			t.Errorf("%+v: timeout %d", tc, hook.Timeout)
		}
	}
}
