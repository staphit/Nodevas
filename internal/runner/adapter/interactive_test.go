package adapter

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

func interactiveEnv(t *testing.T, binary string) RunEnv {
	t.Helper()
	return RunEnv{
		Binary:         binary,
		Workdir:        t.TempDir(),
		Branch:         "nodevas/n-1-abc",
		TempDir:        t.TempDir(),
		MCP:            MCPServer{Command: "/usr/bin/nodevas", Args: []string{"mcp", "--server", "http://127.0.0.1:1/x"}},
		PermissionMode: "",
		AllowedTools:   []string{"Bash(go test *)"},
	}
}

func interactiveJob() runnerwire.Job {
	return runnerwire.Job{RunID: "run-1", NodeID: "n-1", NodeTitle: "Fix it", Prompt: "do the thing",
		Agent: "claude", Model: "sonnet", Mode: runnerwire.ModeInteractive}
}

// Off Windows a .cmd path is just a name; on Windows wrapBatch turns the
// command into an explicit cmd.exe line, so the arguments are checked there.
func plainBinary() string {
	if runtime.GOOS == "windows" {
		return `C:\tools\claude.exe`
	}
	return "/usr/local/bin/claude"
}

func envValue(env []string, name string) (string, bool) {
	value, found := "", false
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, name) {
			value, found = v, true
		}
	}
	return value, found
}

func TestClaudeInteractiveCommand(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	env := interactiveEnv(t, plainBinary())
	cmd, err := Claude{}.InteractiveCommand(interactiveJob(), env)
	if err != nil {
		t.Fatal(err)
	}
	args := cmd.Args[1:]
	prompt := args[0]
	if !strings.Contains(prompt, "running interactively for Nodevas node n-1") || !strings.HasSuffix(prompt, "do the thing\n") {
		t.Fatalf("prompt = %q", prompt)
	}
	mcpPath := filepath.Join(env.TempDir, "mcp.json")
	want := []string{"--permission-mode", "acceptEdits", "--mcp-config", mcpPath, "--strict-mcp-config",
		"--model", "sonnet", "--allowedTools", NodevasMCPTools, "Bash(go test *)"}
	if !slices.Equal(args[1:], want) {
		t.Fatalf("args = %q\nwant %q", args[1:], want)
	}
	if slices.Contains(args, "-p") || slices.Contains(args, "--output-format") {
		t.Fatalf("headless flags in %q", args)
	}
	if raw, err := os.ReadFile(mcpPath); err != nil || !strings.Contains(string(raw), `"nodevas"`) {
		t.Fatalf("mcp config: %s %v", raw, err)
	}
	if cmd.Dir != env.Workdir || cmd.Stdin != nil {
		t.Fatalf("dir %q stdin %v", cmd.Dir, cmd.Stdin)
	}
	if _, ok := envValue(cmd.Env, "CLAUDECODE"); ok {
		t.Fatal("CLAUDECODE passed on")
	}
	if term, _ := envValue(cmd.Env, "TERM"); term != "xterm-256color" {
		t.Fatalf("TERM = %q", term)
	}
}

func TestClaudeInteractivePromptInFile(t *testing.T) {
	cases := map[string]RunEnv{
		// A .cmd shim cannot take the prompt's newlines and quotes.
		"batch shim": interactiveEnv(t, filepath.Join(t.TempDir(), "claude.cmd")),
	}
	long := interactiveEnv(t, plainBinary())
	cases["too long"] = long
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			job := interactiveJob()
			if name == "too long" {
				job.Prompt = strings.Repeat("x", maxPromptArg)
			} else {
				job.Prompt = `say "hi" & echo %PATH% | more`
			}
			cmd, err := Claude{}.InteractiveCommand(job, env)
			if err != nil {
				t.Fatal(err)
			}
			taskPath := filepath.Join(env.TempDir, TaskFileName)
			raw, err := os.ReadFile(taskPath)
			if err != nil || !strings.Contains(string(raw), job.Prompt) {
				t.Fatalf("task file: %v", err)
			}
			// On Windows a .cmd shim's whole command line is cmd.exe's last argument.
			line := strings.Join(cmd.Args, " ")
			if !strings.Contains(line, "Read your task from the file") || !strings.Contains(line, taskPath) {
				t.Fatalf("command line %q does not point at the task file", line)
			}
			if !strings.Contains(line, "--add-dir") {
				t.Fatalf("no --add-dir for the task file: %q", line)
			}
			if strings.Contains(line, "do the thing") || strings.Contains(line, "%PATH%") {
				t.Fatalf("prompt on the command line: %q", line)
			}
		})
	}
}

func TestCodexInteractiveCommand(t *testing.T) {
	env := interactiveEnv(t, "/usr/local/bin/codex")
	env.PermissionMode = "read-only"
	env.IgnoreUserConfig = true
	job := interactiveJob()
	job.Agent, job.Model = "codex", "gpt-5.5"
	cmd, err := Codex{}.InteractiveCommand(job, env)
	if err != nil {
		t.Fatal(err)
	}
	args := cmd.Args[1:]
	want := []string{"-s", "read-only", "-C", env.Workdir, "-m", "gpt-5.5",
		"-c", "mcp_servers.nodevas.command='/usr/bin/nodevas'",
		"-c", "mcp_servers.nodevas.args=['mcp','--server','http://127.0.0.1:1/x']",
		"-c", "mcp_servers.nodevas.default_tools_approval_mode='approve'",
		"-c", "mcp_servers.nodevas.tool_timeout_sec=900"}
	if !slices.Equal(args[:len(want)], want) || len(args) != len(want)+1 {
		t.Fatalf("args = %q\nwant %q + prompt", args, want)
	}
	if prompt := args[len(args)-1]; !strings.Contains(prompt, "do the thing") {
		t.Fatalf("prompt = %q", prompt)
	}
	for _, headless := range []string{"exec", "--json", "-", "--ignore-user-config", "approval_policy='never'"} {
		if slices.Contains(args, headless) {
			t.Fatalf("headless argument %q in %q", headless, args)
		}
	}
}

func TestGeminiInteractiveCommand(t *testing.T) {
	env := interactiveEnv(t, "/usr/local/bin/gemini")
	job := interactiveJob()
	job.Agent, job.Model = "gemini", "default"
	cmd, err := Gemini{}.InteractiveCommand(job, env)
	if err != nil {
		t.Fatal(err)
	}
	args := cmd.Args[1:]
	want := []string{"--approval-mode", "auto_edit", "--allowed-mcp-server-names", "nodevas", "-i"}
	if !slices.Equal(args[:len(want)], want) || len(args) != len(want)+1 {
		t.Fatalf("args = %q", args)
	}
	if !strings.Contains(args[len(args)-1], "do the thing") {
		t.Fatalf("prompt = %q", args[len(args)-1])
	}
	settings, ok := envValue(cmd.Env, geminiSystemSettingsEnv)
	if !ok || settings != filepath.Join(env.TempDir, "gemini-settings.json") {
		t.Fatalf("settings env = %q", settings)
	}
	if slices.Contains(args, "--output-format") {
		t.Fatal("headless flag in interactive command")
	}
}
