package adapter

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"nodevas/internal/runnerwire"
)

func TestOpenCodeDisablesOtherMCP(t *testing.T) {
	env := RunEnv{Binary: "/usr/bin/opencode", Workdir: "/w",
		MCP:        MCPServer{Command: "/opt/nodevas", Args: []string{"mcp"}},
		DisableMCP: []string{"unityMCP", "repoEvil", "nodevas"}}
	cmd, err := OpenCode{}.Command(runnerwire.Job{RunID: "r", NodeID: "n"}, env)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := envValue(cmd.Env, openCodeConfigEnv)
	var config struct {
		MCP map[string]map[string]any `json:"mcp"`
	}
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatal(err)
	}
	if config.MCP["nodevas"]["enabled"] != true || config.MCP["unityMCP"]["enabled"] != false ||
		config.MCP["repoEvil"]["enabled"] != false || len(config.MCP) != 3 {
		t.Fatalf("mcp = %s", raw)
	}
}

// realOpenCode is the installed OpenCode for the tests against the real CLI,
// which skip when there is none or when it is one runs refuse.
func realOpenCode(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("opencode")
	if err != nil {
		t.Skip("opencode not installed")
	}
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		t.Skipf("opencode --version: %v", err)
	}
	if reason := OpenCodeUnsupported(string(out)); reason != "" {
		t.Skip(reason)
	}
	return bin
}

func TestOpenCodeUnsupported(t *testing.T) {
	for version, refused := range map[string]bool{
		"1.18.23": false, "opencode v1.18.23\n": false, "opencode v2.1.4": false,
		"3.0.0": true, "v10.1": true, "": false, "dev build": false,
	} {
		if got := OpenCodeUnsupported(version) != ""; got != refused {
			t.Errorf("%q: refused = %v", version, got)
		}
	}
}

// `opencode debug config` printed the merged config in 1.x and prints its
// sources in 2.x, each normalized to mcp.servers.
func TestOpenCodeServerNames(t *testing.T) {
	for name, out := range map[string]string{
		"1.x": `{"mcp":{"nodevas":{"enabled":true},"owners":{"type":"local"},"repoEvil":{"type":"local"}}}`,
		"2.x": `[{"type":"directory","path":"/home/a/.config/opencode"},
			{"type":"document","path":"/home/a/.config/opencode/opencode.json","info":{"mcp":{"servers":{"owners":{"type":"local"}}}}},
			{"type":"document","path":"/w/opencode.json","info":{"mcp":{"servers":{"repoEvil":{"type":"local"},"nodevas":{"type":"local"}}}}}]`,
	} {
		names, err := openCodeServerNames([]byte("\ufeff" + out + "\n"))
		if err != nil || !slices.Equal(names, []string{"owners", "repoEvil"}) {
			t.Errorf("%s: %q, %v", name, names, err)
		}
	}
	if _, err := openCodeServerNames([]byte("Error: no service")); err == nil {
		t.Error("not JSON: no error")
	}
}

// Against the real CLI, when it is installed: a server a project config in
// the worktree adds is listed, so the run switches it off.
func TestOpenCodeMCPServersReal(t *testing.T) {
	bin := realOpenCode(t)
	dir := t.TempDir()
	project := `{"mcp":{"repoEvil":{"type":"local","command":["echo","x"],"enabled":true}}}`
	if err := os.WriteFile(filepath.Join(dir, "opencode.json"), []byte(project), 0o644); err != nil {
		t.Fatal(err)
	}
	names, err := OpenCodeMCPServers(context.Background(), bin, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(names, "repoEvil") || slices.Contains(names, "nodevas") {
		t.Fatalf("servers %q", names)
	}
}
