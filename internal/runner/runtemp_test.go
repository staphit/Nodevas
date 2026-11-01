package runner

import (
	"path/filepath"
	"testing"

	"nodevas/internal/runnerwire"
)

// A run's temp dir holds what programs outside any sandbox read: the MCP
// config the agent CLI loads, agy's hooks. It is the runner's own, under its
// data dir, never the system's temp dir, which a Codex run's default sandbox
// lets the agent write.
func TestRunTempDirIsUnderTheDataDir(t *testing.T) {
	env := startJobRunner(t, nil)
	env.hub.hello()
	env.hub.offer(testJob("run-tmp", "ok:"))
	env.hub.wait("run-tmp", runnerwire.TypeFinish)
	config := env.recorded(t, "run-tmp", "mcp-path")
	data := env.client.Config.DataDir
	if rel, err := filepath.Rel(data, config); err != nil || !filepath.IsLocal(rel) {
		t.Fatalf("the run's MCP config was at %s, outside the runner's data dir %s", config, data)
	}
}
