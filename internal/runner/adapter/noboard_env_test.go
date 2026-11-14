package adapter

import (
	"os/exec"
	"testing"

	"nodevas/internal/runnerwire"
)

// Every agent of a global run, headless or interactive, carries the run's
// no-board marker in its environment, for a `nodevas mcp` its CLI starts
// from the owner's config to refuse on; a node's run never does, whatever
// the runner's own environment holds.
func TestNoBoardEnv(t *testing.T) {
	t.Setenv(runnerwire.NoBoardEnv, "from-the-runner")
	type command func(runnerwire.Job, RunEnv) (*exec.Cmd, error)
	agents := []struct {
		kind, binary string
		commands     map[string]command
	}{
		{"claude", plainBinary(), map[string]command{"headless": Claude{}.Command, "interactive": Claude{}.InteractiveCommand}},
		{"codex", "/usr/bin/codex", map[string]command{"headless": Codex{}.Command, "interactive": Codex{}.InteractiveCommand}},
		{"gemini", "/usr/bin/gemini", map[string]command{"headless": Gemini{}.Command, "interactive": Gemini{}.InteractiveCommand}},
		{"opencode", "/usr/bin/opencode", map[string]command{"headless": OpenCode{}.Command, "interactive": OpenCode{}.InteractiveCommand,
			"acp": OpenCodeACP{}.Command}},
		{"antigravity", "/usr/bin/agy", map[string]command{"headless": Antigravity{}.Command, "interactive": Antigravity{}.InteractiveCommand}},
	}
	for _, agent := range agents {
		for name, build := range agent.commands {
			mode := ""
			if name == "interactive" {
				mode = runnerwire.ModeInteractive
			}

			job := globalJob()
			job.Agent, job.Mode = agent.kind, mode
			env := globalEnv(t, agent.binary)
			env.Version = "opencode 1.18.23"
			cmd, err := build(job, env)
			if err != nil {
				t.Fatalf("%s %s: %v", agent.kind, name, err)
			}
			if got, ok := envValue(cmd.Env, runnerwire.NoBoardEnv); !ok || got != job.RunID {
				t.Errorf("%s %s, global: %s = %q, %v", agent.kind, name, runnerwire.NoBoardEnv, got, ok)
			}

			node := runnerwire.Job{RunID: "run-n1", Project: "demo", NodeID: "n-1", NodeTitle: "Fix it",
				Prompt: "fix it", Agent: agent.kind, Model: "sonnet", Mode: mode}
			env = globalEnv(t, agent.binary)
			env.Version = "opencode 1.18.23"
			env.NoBoard, env.PermissionRelay = false, false
			env.MCP.Args = []string{"mcp", "--server-file", "/tmp/mcp-server", "--project", "demo"}
			if cmd, err = build(node, env); err != nil {
				t.Fatalf("%s %s, node: %v", agent.kind, name, err)
			}
			if got, ok := envValue(cmd.Env, runnerwire.NoBoardEnv); ok {
				t.Errorf("%s %s, node: %s = %q", agent.kind, name, runnerwire.NoBoardEnv, got)
			}
		}
	}
}
