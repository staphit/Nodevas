package adapter

import (
	"testing"

	"nodevas/internal/runnerwire"
)

// Every agent process, headless or interactive, says which run it is, so the
// owner's own hooks in it stay quiet.
func TestEveryAdapterSetsRunID(t *testing.T) {
	t.Setenv(RunIDEnv, "from-the-runner")
	for _, kind := range []string{"claude", "codex", "gemini", "opencode", "antigravity"} {
		env := RunEnv{Binary: "/usr/bin/" + Binary(kind), Workdir: t.TempDir(), Branch: "b", TempDir: t.TempDir(),
			MCP: MCPServer{Command: "/usr/bin/nodevas", Args: []string{"mcp"}}}
		job := runnerwire.Job{RunID: "run-7", NodeID: "n-1", Prompt: "p", Agent: kind}
		cmd, err := For(kind).Command(job, env)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		icmd, err := For(kind).(Interactive).InteractiveCommand(job, env)
		if err != nil {
			t.Fatalf("%s interactive: %v", kind, err)
		}
		for name, e := range map[string][]string{"headless": cmd.Env, "interactive": icmd.Env} {
			if id, _ := envValue(e, RunIDEnv); id != "run-7" || countEnv(e, RunIDEnv) != 1 {
				t.Errorf("%s %s: %s = %q (%d entries)", kind, name, RunIDEnv, id, countEnv(e, RunIDEnv))
			}
		}
	}
}
