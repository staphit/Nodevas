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

// Against the real CLI, when it is installed: a server a project config in
// the worktree adds is listed, so the run switches it off.
func TestOpenCodeMCPServersReal(t *testing.T) {
	bin, err := exec.LookPath("opencode")
	if err != nil {
		t.Skip("opencode not installed")
	}
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
