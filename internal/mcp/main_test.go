package mcp

import (
	"os"
	"testing"
)

// TestMain keeps the suite from following the Claude Code session it may be
// run from: with CLAUDE_PID set, clients would read the real session registry.
// It also points the user config dir at an empty folder, so the folder
// exclusions of whoever runs the tests cannot take a test session off the
// network.
func TestMain(m *testing.M) {
	for _, key := range []string{"CLAUDE_PID", "CLAUDE_CODE_SESSION_ID", "CODEX_THREAD_ID", "GEMINI_SESSION_ID", "NODEVAS_AGENT", "NODEVAS_MODEL"} {
		_ = os.Unsetenv(key)
	}
	config, err := os.MkdirTemp("", "nodevas-mcp-config-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("APPDATA", config)
	_ = os.Setenv("XDG_CONFIG_HOME", config)
	code := m.Run()
	_ = os.RemoveAll(config)
	os.Exit(code)
}
