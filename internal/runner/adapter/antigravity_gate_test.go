package adapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

func TestAntigravityGate(t *testing.T) {
	ws, other := t.TempDir(), t.TempDir()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	for _, tc := range []struct {
		name, payload string
		deny          bool
	}{
		{"shell", `{"toolCall":{"name":"run_command","args":{"CommandLine":"git status"}},"stepIdx":2}`, true},
		{"command input", `{"toolCall":{"name":"send_command_input","args":{}}}`, true},
		{"nodevas mcp", `{"toolCall":{"name":"call_mcp_tool","args":{"ServerName":"nodevas","ToolName":"claim_task"}}}`, false},
		{"other mcp", `{"toolCall":{"name":"call_mcp_tool","args":{"ServerName":"unityMCP","ToolName":"x"}}}`, true},
		{"browser", `{"toolCall":{"name":"browser_click_element","args":{}}}`, true},
		{"edit", `{"toolCall":{"name":"write_to_file","args":{"TargetFile":"a.txt"}}}`, false},
		{"bom", "\xef\xbb\xbf" + `{"toolCall":{"name":"run_command"}}`, true},
		{"garbage", `not json`, false},
		{"file inside", `{"toolCall":{"name":"view_file","args":{"AbsolutePath":` + jsonPath(ws, "src", "a.go") +
			`}},"workspacePaths":[` + jsonPath(ws) + `]}`, false},
		{"file outside", `{"toolCall":{"name":"read_file","args":{"Path":` + jsonPath(other, "package.json") +
			`}},"workspacePaths":[` + jsonPath(ws) + `]}`, true},
		{"relative file", `{"toolCall":{"name":"read_file","args":{"Path":"src/a.go"}},"workspacePaths":[` +
			jsonPath(ws) + `]}`, false},
		{"agy's own dir", `{"toolCall":{"name":"read_file","args":{"Path":` +
			jsonPath(home, ".gemini", "antigravity-cli", "mcp", "nodevas", "claim_task.json") +
			`}},"workspacePaths":[` + jsonPath(ws) + `]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := AntigravityGate([]byte(tc.payload))
			var got map[string]string
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("output %q is not a JSON object: %v", out, err)
			}
			// No opinion is "ask": an empty decision is a denial to agy.
			if !tc.deny && got["decision"] != "ask" {
				t.Fatalf("decision = %q, want ask", got["decision"])
			}
			if deny := got["decision"] == "deny"; deny != tc.deny {
				t.Fatalf("decision = %q, want deny=%v", got["decision"], tc.deny)
			}
			if tc.deny && got["reason"] == "" {
				t.Fatal("a denial carries no reason")
			}
		})
	}
}

// The gate goes in the root of a headless run in the modes that cannot run
// commands, not in auto or full, and never in an interactive run.
func TestAntigravityGateHooksFile(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "nodevas.exe")
	job := runnerwire.Job{RunID: "run-1", NodeID: "node-1", Prompt: "do it"}
	for _, tc := range []struct {
		mode        string
		interactive bool
		want        bool
	}{
		{"", false, true},
		{"accept-edits", false, true},
		{"plan", false, true},
		{"auto", false, false},
		{"full", false, false},
		{"accept-edits", true, false},
	} {
		tmp := t.TempDir()
		env := RunEnv{Binary: "agy", Workdir: t.TempDir(), Branch: "b", TempDir: tmp,
			MCP: MCPServer{Command: exe, Args: []string{"mcp"}}, PermissionMode: tc.mode}
		var err error
		if tc.interactive {
			_, err = Antigravity{}.InteractiveCommand(job, env)
		} else {
			_, err = Antigravity{}.Command(job, env)
		}
		if err != nil {
			t.Fatalf("%s: %v", tc.mode, err)
		}
		data, err := os.ReadFile(filepath.Join(tmp, antigravityRootDir, ".agents", "hooks.json"))
		if got := err == nil; got != tc.want {
			t.Fatalf("mode %q interactive=%v: hooks.json written = %v, want %v", tc.mode, tc.interactive, got, tc.want)
		}
		if tc.want && !strings.Contains(string(data), AntigravityGateArgs) {
			t.Fatalf("hooks.json does not run the gate: %s", data)
		}
	}
}

func jsonPath(parts ...string) string {
	b, _ := json.Marshal(filepath.Join(parts...))
	return string(b)
}
