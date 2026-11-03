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

// The runner's own secrets never reach an agent, headless or interactive: a
// task that talks the agent into running `env` finds nothing to steal.
func TestAgentsGetNoRunnerSecrets(t *testing.T) {
	secrets := []string{"NODEVAS_COPILOT_API_KEY", "NODEVAS_ADVISOR_API_KEY", "NODEVAS_RUNNER_TOKEN",
		"NODEVAS_ACCESS_CLIENT_SECRET", "NODEVAS_SECRET_KEY", "GH_TOKEN", "GITHUB_TOKEN",
		"GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"}
	for _, name := range secrets {
		t.Setenv(name, "sk-secret-"+name)
	}
	t.Setenv("ANTHROPIC_API_KEY", "kept")
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
		for mode, e := range map[string][]string{"headless": cmd.Env, "interactive": icmd.Env} {
			for _, name := range secrets {
				if _, ok := envValue(e, name); ok {
					t.Errorf("%s %s: %s reached the agent", kind, mode, name)
				}
			}
			if v, _ := envValue(e, "ANTHROPIC_API_KEY"); v != "kept" {
				t.Errorf("%s %s: ANTHROPIC_API_KEY = %q", kind, mode, v)
			}
			if id, _ := envValue(e, RunIDEnv); id != "run-7" {
				t.Errorf("%s %s: %s = %q", kind, mode, RunIDEnv, id)
			}
		}
	}
}

// A runner started from inside another agent's terminal (Claude Code, Codex,
// …) must not hand that agent's session on: the run's `nodevas mcp` would
// adopt the outer session id ("claude:<outer id>") and the run's asks and
// heartbeats would land on the wrong session. The outer agent's own settings
// (CLAUDE_CODE_USE_BEDROCK) stay.
func TestAgentsGetNoOuterAgentSession(t *testing.T) {
	outer := []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SESSION_ID", "CODEX_THREAD_ID",
		"CODEX_SANDBOX", "OPENCODE_SESSION_ID", "OPENCODE", "GEMINI_SESSION_ID", "GEMINI_CLI",
		"COPILOT_AGENT_SESSION_ID", "GROK_SESSION_ID", "CURSOR_TRACE_ID", "CURSOR_AGENT", "NODEVAS_SESSION_ID",
		// Claude Code's channel to its session: a child would talk to it.
		"CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_MESSAGING_TOKEN",
		"CLAUDE_CODE_SESSION_ATTENDED", "CLAUDE_CODE_SSE_PORT", "CLAUDE_PID"}
	for _, name := range outer {
		t.Setenv(name, "outer-"+name)
	}
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
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
		for mode, e := range map[string][]string{"headless": cmd.Env, "interactive": icmd.Env} {
			for _, name := range outer {
				if v, ok := envValue(e, name); ok {
					t.Errorf("%s %s: %s=%q reached the agent", kind, mode, name, v)
				}
			}
			if v, _ := envValue(e, "CLAUDE_CODE_USE_BEDROCK"); v != "1" {
				t.Errorf("%s %s: CLAUDE_CODE_USE_BEDROCK = %q", kind, mode, v)
			}
		}
	}
}
