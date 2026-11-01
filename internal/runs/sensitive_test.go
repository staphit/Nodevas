package runs

import (
	"testing"

	"nodevas/internal/runner/adapter"
)

// Every CLI's repository-local config can run a program on the next runner
// that loads it, so a change to any of it waits for a person, not only
// Claude Code's; so does vite.config, which vitest reads for which tests run.
func TestSensitivePathCoversAgentRepoConfig(t *testing.T) {
	for _, p := range []string{"opencode.json", "opencode.jsonc", "tui.json", "tui.jsonc", ".opencode/plugin/x.ts",
		".gemini/settings.json", ".agents/hooks.json", ".agents/mcp_config.json", "_agents/x.md", ".agent/x.md",
		".env", ".env.local", ".envrc", ".codex/config.toml", "GEMINI.md", "web/GEMINI.md", "vite.config.ts",
		"web/vite.config.mjs", ".claude/settings.json", ".mcp.json"} {
		if !SensitivePath(p) {
			t.Errorf("%s is not sensitive", p)
		}
	}
	// The runner's own list, each path as a file (or a symlink in a
	// folder's place).
	for _, p := range adapter.RepoConfigPaths() {
		if !SensitivePath(p) {
			t.Errorf("%s is not sensitive", p)
		}
	}
	for _, p := range []string{"src/environment.ts", "docs/agents.txt", "web/src/opencode.ts"} {
		if SensitivePath(p) {
			t.Errorf("%s is sensitive", p)
		}
	}
}
