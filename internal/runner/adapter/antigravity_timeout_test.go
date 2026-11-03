package adapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"nodevas/internal/runnerwire"
)

// agy cuts an MCP tool call off after three minutes unless the server's
// entry says otherwise ("MCP tool call to server \"nodevas\" timed out after
// 3m0s"), while ask_user waits up to ten for a person. The run's nodevas
// entry carries timeoutSeconds, the key agy 1.4.x reads for it (a 5 s value
// cut a 20 s call at 5 s, a 240 s one let a 200 s call finish), past
// ask_user's wait and its long poll's slack. Headless and interactive alike.
func TestAntigravityMCPToolTimeoutOutlastsAskUser(t *testing.T) {
	read := func(t *testing.T, tmp string) float64 {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(tmp, antigravityRootDir, ".agents", "mcp_config.json"))
		if err != nil {
			t.Fatal(err)
		}
		var config struct {
			MCPServers map[string]map[string]any `json:"mcpServers"`
		}
		if err := json.Unmarshal(raw, &config); err != nil {
			t.Fatal(err)
		}
		timeout, _ := config.MCPServers["nodevas"]["timeoutSeconds"].(float64)
		return timeout
	}
	const askUserWait = 10*60 + 30 // ask_user's ten minutes and the long poll's slack
	job := runnerwire.Job{RunID: "run-1", NodeID: "n-1", Prompt: "do it"}
	env := RunEnv{Binary: "agy", Workdir: t.TempDir(), Branch: "b", TempDir: t.TempDir(),
		MCP: MCPServer{Command: "/opt/nodevas", Args: []string{"mcp"}}}
	if _, err := (Antigravity{}).Command(job, env); err != nil {
		t.Fatal(err)
	}
	if got := read(t, env.TempDir); got <= askUserWait {
		t.Errorf("headless: timeoutSeconds = %v, want more than %d", got, askUserWait)
	}
	env.TempDir = t.TempDir()
	if _, err := (Antigravity{}).InteractiveCommand(job, env); err != nil {
		t.Fatal(err)
	}
	if got := read(t, env.TempDir); got <= askUserWait {
		t.Errorf("interactive: timeoutSeconds = %v, want more than %d", got, askUserWait)
	}
}
