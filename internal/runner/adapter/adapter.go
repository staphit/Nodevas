// Package adapter turns a run into one agent CLI invocation, and that CLI's
// output into the normalised runnerwire.RunEvent stream. One Adapter per CLI;
// the runner around it (worktree, acceptance, process tree, streaming to the
// hub) is the same for all of them.
//
// See docs/nodevas-workstation-plan.md, "Agent adapter". The Claude and Codex
// flags were checked against the CLIs' own --help, because these CLIs change
// quickly; the Gemini adapter follows the CLI's docs only (see gemini.go).
package adapter

import (
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf8"

	"nodevas/internal/runnerwire"
)

// Adapter is one agent CLI.
type Adapter interface {
	// Kind is the runnerwire.Agent kind this adapter runs: "claude", ….
	Kind() string
	// Command builds the process for job; it does not start it. It may write
	// files the CLI needs (MCP config) into env.TempDir.
	Command(job runnerwire.Job, env RunEnv) (*exec.Cmd, error)
	// NewParser returns a parser for one run's stdout.
	NewParser() Parser
}

// RunEnv is what the runner decided for a run: where it runs and how freely.
// None of it comes from the hub; the job carries no permission settings.
type RunEnv struct {
	// Binary is the CLI as found on PATH.
	Binary string
	// Workdir is the run's git worktree; the agent runs there.
	Workdir string
	// Branch is the worktree's branch, for the prompt.
	Branch string
	// TempDir is private to this run and removed when it ends.
	TempDir string
	// MCP is the nodevas MCP server the agent should be given.
	MCP MCPServer
	// PermissionMode is the owner's setting for how freely this kind of agent
	// may act: Claude Code's --permission-mode, Codex's --sandbox, Gemini
	// CLI's --approval-mode. "" is the adapter's safe default.
	PermissionMode string
	// AllowedTools is Claude Code's --allowedTools; other adapters ignore it.
	AllowedTools []string
	// IgnoreUserConfig runs Codex with --ignore-user-config: without the
	// owner's config.toml (their own MCP servers, hooks, model), auth only.
	IgnoreUserConfig bool
}

// MCPServer is a stdio MCP server: a command and its arguments.
type MCPServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// Parser reads one run's stdout, a line at a time. Its events carry no Seq or
// At yet; the runner stamps those as it queues them.
type Parser interface {
	Parse(line []byte) []runnerwire.RunEvent
	// Outcome is what the stream said about how the run ended.
	Outcome() Outcome
}

// Outcome is the CLI's own account of the run, from its final result line.
type Outcome struct {
	// Done is true once the result line arrived.
	Done    bool
	IsError bool
	// Error says what went wrong, when IsError.
	Error   string
	Summary string
	Usage   *runnerwire.Usage
}

// For returns the adapter for an agent kind, or nil when this runner cannot
// run that kind yet.
func For(kind string) Adapter {
	switch kind {
	case "claude":
		return Claude{}
	case "codex":
		return Codex{}
	case "gemini":
		return Gemini{}
	}
	return nil
}

// Prompt is what the agent is told: the runner's preamble, then the task as
// the hub assembled it. The preamble is the runner's, not the hub's, because
// only the runner knows the worktree and what it will do with it afterwards.
func Prompt(job runnerwire.Job, branch string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are running headless for Nodevas node %s", job.NodeID)
	if job.NodeTitle != "" {
		fmt.Fprintf(&b, " (%q)", job.NodeTitle)
	}
	fmt.Fprintf(&b, ", in a dedicated git worktree on branch %s. Nobody is watching this terminal; "+
		"there is no one to answer questions here.\n\n", branch)
	b.WriteString("Rules for this run:\n")
	fmt.Fprintf(&b, "1. First call the nodevas MCP tool claim_task with id %q, then read the task with get_node.\n", job.NodeID)
	b.WriteString("2. Work only inside this worktree (the current directory). Do not touch other checkouts.\n")
	b.WriteString("3. Do not push, and do not open pull requests: the runner handles that after you finish.\n")
	b.WriteString("4. When you are done, report the outcome with set_node_status, " +
		"then finish with a short summary of what you changed.\n\n")
	b.WriteString("The task:\n\n")
	b.WriteString(job.Prompt)
	b.WriteString("\n")
	return b.String()
}

// Truncate cuts s to at most n bytes without splitting a UTF-8 sequence.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
