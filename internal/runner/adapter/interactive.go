package adapter

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"nodevas/internal/runnerwire"
)

// Interactive runs (W2.5): the agent's own TUI in a pseudo-terminal on the
// runner, which the dispatcher watches and types into through the hub. Same
// worktree, MCP server and permission settings as a headless run; the prompt
// goes in as the CLI's initial prompt instead of on stdin, and nothing parses
// stdout (it is a screen, not a stream).

// Interactive is an adapter that can also start its CLI's interactive TUI.
type Interactive interface {
	// InteractiveCommand builds the TUI's process; it does not start it. The
	// runner starts it attached to a pseudo-terminal, so Stdin, Stdout and
	// Stderr are left unset. It may write files into env.TempDir.
	InteractiveCommand(job runnerwire.Job, env RunEnv) (*exec.Cmd, error)
}

// maxPromptArg is the longest initial prompt passed as an argument; a longer
// one goes in a file. Windows caps a whole command line at 32767 characters.
const maxPromptArg = 8 << 10

// TaskFileName is the file in the run's temp dir that holds the task when it
// cannot go on the command line.
const TaskFileName = "task.md"

// InteractivePrompt is what an interactive agent is told first: the runner's
// preamble, then the task. Unlike the headless preamble, someone may be
// watching and answering.
func InteractivePrompt(job runnerwire.Job, branch string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are running interactively for Nodevas node %s", job.NodeID)
	if job.NodeTitle != "" {
		fmt.Fprintf(&b, " (%q)", job.NodeTitle)
	}
	fmt.Fprintf(&b, ", in a dedicated git worktree on branch %s. The person who dispatched this run "+
		"may be watching this terminal and may type to you.\n\n", branch)
	b.WriteString("Rules for this run:\n")
	fmt.Fprintf(&b, "1. First call the nodevas MCP tool claim_task with id %q, then read the task with get_node.\n", job.NodeID)
	b.WriteString("2. Work only inside this worktree (the current directory). Do not touch other checkouts.\n")
	b.WriteString("3. Do not push, and do not open pull requests: the runner handles that after the session ends.\n")
	b.WriteString("4. When you are done, report the outcome with set_node_status and say so; " +
		"the session ends when the person quits it.\n\n")
	b.WriteString("The task:\n\n")
	b.WriteString(job.Prompt)
	b.WriteString("\n")
	return b.String()
}

// initialPrompt is the prompt argument for binary: the prompt itself, or,
// when it cannot travel on the command line (a .cmd shim would refuse its
// characters, or it is too long), a short instruction to read it from a file
// in the run's temp dir. inFile reports the second case.
func initialPrompt(binary, prompt, tempDir string) (arg string, inFile bool, err error) {
	unsafe := isBatch(binary) && strings.ContainsAny(prompt, batchUnsafe)
	if !unsafe && len(prompt) <= maxPromptArg && !strings.ContainsRune(prompt, 0) {
		return prompt, false, nil
	}
	if tempDir == "" {
		return "", false, fmt.Errorf("the prompt cannot go on the command line and there is no temp dir for it")
	}
	path := filepath.Join(tempDir, TaskFileName)
	if err := os.WriteFile(path, []byte(prompt), 0o600); err != nil {
		return "", false, fmt.Errorf("writing the task file: %w", err)
	}
	return "Read your task from the file " + path + " and follow it.", true, nil
}

// InteractiveCommand starts Claude Code's TUI:
//
//	claude <prompt> --permission-mode <mode> --mcp-config <file> --strict-mcp-config
//	       [--model <id>] [--add-dir <temp dir>] --allowedTools <tool>...
//
// `claude [prompt]` starts an interactive session with an initial prompt
// (Claude Code 2.1.299's --help: "starts an interactive session by default").
// The prompt goes first because --allowedTools is variadic.
func (Claude) InteractiveCommand(job runnerwire.Job, env RunEnv) (*exec.Cmd, error) {
	if env.Binary == "" || env.Workdir == "" || env.TempDir == "" {
		return nil, fmt.Errorf("claude: binary, workdir and temp dir are required")
	}
	mcpPath, err := writeMCPConfig(env)
	if err != nil {
		return nil, err
	}
	prompt, inFile, err := initialPrompt(env.Binary, InteractivePrompt(job, env.Branch), env.TempDir)
	if err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	mode := env.PermissionMode
	if mode == "" {
		mode = DefaultClaudePermissionMode
	}
	args := []string{
		prompt,
		"--permission-mode", mode,
		"--mcp-config", mcpPath,
		"--strict-mcp-config",
	}
	if job.Model != "" && job.Model != "default" {
		args = append(args, "--model", job.Model)
	}
	if inFile {
		// The task file is outside the worktree; without this, reading it
		// would need a permission prompt.
		args = append(args, "--add-dir", env.TempDir)
	}
	args = append(args, "--allowedTools", NodevasMCPTools)
	args = append(args, env.AllowedTools...)
	cmd := exec.Command(env.Binary, args...)
	cmd.Dir = env.Workdir
	cmd.Env = append(childEnv(), terminalEnv...)
	// Unlike headless claude, the prompt is on the command line: through a
	// .cmd shim it must go through the explicit cmd.exe command line.
	if err := wrapBatch(cmd); err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	return cmd, nil
}

// writeMCPConfig writes Claude Code's --mcp-config file into the temp dir.
func writeMCPConfig(env RunEnv) (string, error) {
	config, err := json.Marshal(map[string]any{
		"mcpServers": map[string]MCPServer{"nodevas": env.MCP},
	})
	if err != nil {
		return "", err
	}
	path := filepath.Join(env.TempDir, "mcp.json")
	if err := os.WriteFile(path, config, 0o600); err != nil {
		return "", fmt.Errorf("writing the MCP config: %w", err)
	}
	return path, nil
}

// InteractiveCommand starts the Codex TUI:
//
//	codex -s <sandbox> -C <worktree> [-m <model>]
//	      -c mcp_servers.nodevas.command='<nodevas>' -c mcp_servers.nodevas.args=[…]
//	      -c mcp_servers.nodevas.default_tools_approval_mode='approve'
//	      -c mcp_servers.nodevas.tool_timeout_sec=900
//	      <prompt>
//
// Checked against codex-cli 0.156.0's `codex --help` ("codex [OPTIONS]
// [PROMPT]": options are forwarded to the interactive CLI). Unlike exec, no
// approval_policy override: someone is at the terminal to approve. The
// top-level command has no --ignore-user-config (exec only), so the owner's
// config.toml always loads here.
func (Codex) InteractiveCommand(job runnerwire.Job, env RunEnv) (*exec.Cmd, error) {
	if env.Binary == "" || env.Workdir == "" {
		return nil, fmt.Errorf("codex: binary and workdir are required")
	}
	prompt, _, err := initialPrompt(env.Binary, InteractivePrompt(job, env.Branch), env.TempDir)
	if err != nil {
		return nil, fmt.Errorf("codex: %w", err)
	}
	sandbox := env.PermissionMode
	if sandbox == "" {
		sandbox = DefaultCodexSandbox
	}
	args := []string{"-s", sandbox, "-C", env.Workdir}
	if job.Model != "" && job.Model != "default" {
		args = append(args, "-m", job.Model)
	}
	args = append(args,
		"-c", "mcp_servers.nodevas.command="+tomlString(env.MCP.Command),
		"-c", "mcp_servers.nodevas.args="+tomlArray(env.MCP.Args),
		"-c", "mcp_servers.nodevas.default_tools_approval_mode='approve'",
		"-c", "mcp_servers.nodevas.tool_timeout_sec="+strconv.Itoa(codexToolTimeoutSec),
		prompt,
	)
	cmd := exec.Command(env.Binary, args...)
	cmd.Dir = env.Workdir
	cmd.Env = append(childEnv(), terminalEnv...)
	if err := wrapBatch(cmd); err != nil {
		return nil, fmt.Errorf("codex: %w", err)
	}
	return cmd, nil
}

// InteractiveCommand starts the Gemini CLI's TUI:
//
//	gemini --approval-mode <mode> --allowed-mcp-server-names nodevas [-m <model>]
//	       [--include-directories <temp dir>] -i <prompt>
//
// UNVERIFIED, like the headless Gemini adapter: -i/--prompt-interactive
// ("execute the prompt and continue in interactive mode") and
// --include-directories follow the CLI's documentation, not its --help.
func (Gemini) InteractiveCommand(job runnerwire.Job, env RunEnv) (*exec.Cmd, error) {
	if env.Binary == "" || env.Workdir == "" || env.TempDir == "" {
		return nil, fmt.Errorf("gemini: binary, workdir and temp dir are required")
	}
	settingsPath, err := writeGeminiSettings(env)
	if err != nil {
		return nil, err
	}
	prompt, inFile, err := initialPrompt(env.Binary, InteractivePrompt(job, env.Branch), env.TempDir)
	if err != nil {
		return nil, fmt.Errorf("gemini: %w", err)
	}
	mode := env.PermissionMode
	if mode == "" {
		mode = DefaultGeminiApprovalMode
	}
	args := []string{
		"--approval-mode", mode,
		"--allowed-mcp-server-names", "nodevas",
	}
	if job.Model != "" && job.Model != "default" {
		args = append(args, "-m", job.Model)
	}
	if inFile {
		args = append(args, "--include-directories", env.TempDir)
	}
	args = append(args, "-i", prompt)
	cmd := exec.Command(env.Binary, args...)
	cmd.Dir = env.Workdir
	cmd.Env = append(append(childEnv(), terminalEnv...), geminiSystemSettingsEnv+"="+settingsPath)
	if err := wrapBatch(cmd); err != nil {
		return nil, fmt.Errorf("gemini: %w", err)
	}
	return cmd, nil
}

// terminalEnv tells a TUI what it is drawing on: the hub's browser terminal
// is xterm.js, which speaks xterm-256color and truecolor.
var terminalEnv = []string{"TERM=xterm-256color", "COLORTERM=truecolor"}
