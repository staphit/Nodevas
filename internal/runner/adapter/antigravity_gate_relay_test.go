package adapter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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

// agyArg is the value of cmd's --name=value argument, if it has one.
func agyArg(args []string, name string) (string, bool) {
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			return v, true
		}
	}
	return "", false
}

// Headless agy 1.4.10 refuses a shell command after the gate's "allow"
// ("user denied permission to run command"): a hook cannot grant the command
// permission. With the relay gate in place, the run's session settings grant
// it, so the gate (its own denials, then the person in Nodevas) is the only
// say on shell commands. The owner's other permission rules are carried over,
// as --settings replaces the owner's permissions for the session; nothing
// else grants commands, and skip-permissions stays off.
func TestAntigravityRelayGrantsCommandsToTheGate(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "nodevas.exe")
	if antigravityGateHooks(exe, false, true) == nil {
		t.Skip("no hook path on this machine")
	}
	owner := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(owner, []byte("\xef\xbb\xbf"+`{"model":"m","permissions":{"allow":["mcp(nodevas/*)"],"deny":["command(rm *)"],"defaultMode":"x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old := antigravityOwnerSettingsPath
	antigravityOwnerSettingsPath = func() (string, error) { return owner, nil }
	t.Cleanup(func() { antigravityOwnerSettingsPath = old })

	command := func(mode string, relay, review bool, nodevas string) []string {
		t.Helper()
		job := runnerwire.Job{RunID: "run-1", NodeID: "node-1", Prompt: "do it"}
		if review {
			job.Kind = runnerwire.JobReview
		}
		env := RunEnv{Binary: "agy", Workdir: t.TempDir(), Branch: "b", TempDir: t.TempDir(),
			MCP: MCPServer{Command: nodevas, Args: []string{"mcp"}}, PermissionMode: mode, PermissionRelay: relay}
		cmd, err := (Antigravity{}).Command(job, env)
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(cmd.Args, "--dangerously-skip-permissions") && mode != "full" {
			t.Errorf("%s relay=%v: skip-permissions in %q", mode, relay, cmd.Args)
		}
		return cmd.Args
	}

	for _, mode := range []string{"", "accept-edits"} {
		args := command(mode, true, false, exe)
		path, ok := agyArg(args, "--settings")
		if !ok {
			t.Fatalf("%q relay: no --settings in %q", mode, args)
		}
		if i, j := slices.Index(args, "--settings="+path), slices.Index(args, "-p="); i < 0 || j != len(args)-1 || i > j {
			t.Errorf("%q: --settings must come before -p=: %q", mode, args)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Permissions map[string]any `json:"permissions"`
			Model       *string        `json:"model"`
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		allow, _ := got.Permissions["allow"].([]any)
		if !slices.Contains(allow, any("command(*)")) || !slices.Contains(allow, any("mcp(nodevas/*)")) {
			t.Errorf("%q: allow = %v", mode, allow)
		}
		if deny, _ := got.Permissions["deny"].([]any); !slices.Contains(deny, any("command(rm *)")) ||
			got.Permissions["defaultMode"] != "x" || got.Model != nil {
			t.Errorf("%q: settings = %s", mode, raw)
		}
	}

	// No relay gate, no grant: relay off, plan, auto, full, a review, and a
	// nodevas path no hook can run.
	for _, tc := range []struct {
		name, mode string
		relay      bool
		review     bool
		nodevas    string
	}{
		{"relay off", "accept-edits", false, false, exe},
		{"plan", "plan", true, false, exe},
		{"auto", "auto", true, false, exe},
		{"full", "full", true, false, exe},
		{"review", "accept-edits", true, true, exe},
		{"no hook path", "accept-edits", true, false, ""},
	} {
		if args := command(tc.mode, tc.relay, tc.review, tc.nodevas); slices.ContainsFunc(args, func(a string) bool {
			return strings.HasPrefix(a, "--settings")
		}) {
			t.Errorf("%s: %q", tc.name, args)
		}
	}

	// The owner has no settings file: the grant alone. A file that does not
	// parse is left to agy, without a grant that would drop its rules.
	if err := os.Remove(owner); err != nil {
		t.Fatal(err)
	}
	path, ok := agyArg(command("accept-edits", true, false, exe), "--settings")
	if !ok {
		t.Fatal("no settings without an owner file")
	}
	if raw, _ := os.ReadFile(path); !strings.Contains(string(raw), `"command(*)"`) {
		t.Errorf("settings = %s", raw)
	}
	for _, bad := range []string{`{"permissions":`, `{"permissions":{"allow":"command(*)"}}`, `{"permissions":[]}`} {
		if err := os.WriteFile(owner, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok := agyArg(command("accept-edits", true, false, exe), "--settings"); ok {
			t.Errorf("owner %s: granted", bad)
		}
	}
}
