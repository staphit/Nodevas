package mcp

import (
	"os"
	"testing"
)

// TestMain keeps the suite from following the Claude Code session it may be
// run from: with CLAUDE_PID set, clients would read the real session registry.
func TestMain(m *testing.M) {
	for _, key := range []string{"CLAUDE_PID", "CLAUDE_CODE_SESSION_ID", "CODEX_THREAD_ID", "GEMINI_SESSION_ID", "NODEVAS_AGENT"} {
		_ = os.Unsetenv(key)
	}
	os.Exit(m.Run())
}
