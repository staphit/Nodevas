// Package agentsetup wires Nodevas into the coding agents on this machine:
// the `nodevas mcp` server in each agent's MCP config, plus the hooks that
// report a session busy or idle and put waiting messages in front of the
// model (`nodevas hook <agent> <event>`). Every edit merges into the person's
// own config, replacing only what nodevas wrote before, and backs the file up
// once as `<file>.bak-nodevas`.
package agentsetup

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// The agents setup knows how to wire.
const (
	Claude      = "claude"
	Codex       = "codex"
	Gemini      = "gemini"
	OpenCode    = "opencode"
	Antigravity = "antigravity"
)

// Agents lists them in the order setup runs.
var Agents = []string{Claude, Codex, Gemini, OpenCode, Antigravity}

// binaries are the CLIs' executable names where they differ from the agent's.
var binaries = map[string]string{Antigravity: "agy"}

// Binary is the executable an agent's CLI is installed as.
func Binary(agent string) string {
	if b, ok := binaries[agent]; ok {
		return b
	}
	return agent
}

// ServerName is the name Nodevas's MCP server goes by in every agent config.
const ServerName = "nodevas"

// Hook events, as `nodevas hook <agent> <event>` takes them.
const (
	EventSessionStart     = "session-start"
	EventUserPromptSubmit = "user-prompt-submit"
	EventPostToolUse      = "post-tool-use"
	EventStop             = "stop"
	EventSessionEnd       = "session-end"
)

// Env is everything setup reads from the machine. Tests point it at a
// temporary directory and fake the `claude` CLI.
type Env struct {
	// Home is the user's home directory.
	Home string
	// CodexHome overrides ~/.codex (CODEX_HOME). GeminiHome replaces the home
	// directory Gemini CLI keeps its .gemini folder in (GEMINI_CLI_HOME).
	CodexHome  string
	GeminiHome string
	// XDGConfigHome replaces ~/.config, where OpenCode keeps its opencode
	// folder (XDG_CONFIG_HOME, on every platform).
	XDGConfigHome string
	// AntigravityHome replaces the home directory the Antigravity CLI keeps
	// its .gemini folder in; empty is Home.
	AntigravityHome string
	// Exe is the nodevas binary agents will run.
	Exe string
	// GOOS decides how hook commands are quoted.
	GOOS string
	// LookPath finds an agent's CLI; an error means it is not installed.
	LookPath func(name string) (string, error)
	// Run runs a command and returns its stdout. err is non-nil when it could
	// not run or exited non-zero.
	Run func(name string, args ...string) (stdout string, err error)
}

// SystemEnv is the real machine.
func SystemEnv() (Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Env{}, err
	}
	exe, err := os.Executable()
	if err != nil {
		return Env{}, err
	}
	// Windows reports the full path already; its canonical form (`\\?\C:\…`)
	// is not for other programs' configs.
	if runtime.GOOS != "windows" {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
	}
	return Env{
		Home:          home,
		CodexHome:     os.Getenv("CODEX_HOME"),
		GeminiHome:    os.Getenv("GEMINI_CLI_HOME"),
		XDGConfigHome: os.Getenv("XDG_CONFIG_HOME"),
		Exe:           exe,
		GOOS:          runtime.GOOS,
		LookPath:      exec.LookPath,
		Run: func(name string, args ...string) (string, error) {
			cmd := exec.Command(name, args...)
			var stderr strings.Builder
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil && stderr.Len() > 0 {
				err = fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
			}
			return string(out), err
		},
	}, nil
}

func (e Env) codexHome() string {
	if e.CodexHome != "" {
		return e.CodexHome
	}
	return filepath.Join(e.Home, ".codex")
}

func (e Env) geminiHome() string {
	if e.GeminiHome != "" {
		return filepath.Join(e.GeminiHome, ".gemini")
	}
	return filepath.Join(e.Home, ".gemini")
}

func (e Env) claudeHome() string { return filepath.Join(e.Home, ".claude") }

// openCodeHome is OpenCode's global config directory: $XDG_CONFIG_HOME/opencode,
// else ~/.config/opencode (on Windows too).
func (e Env) openCodeHome() string {
	if e.XDGConfigHome != "" {
		return filepath.Join(e.XDGConfigHome, "opencode")
	}
	return filepath.Join(e.Home, ".config", "opencode")
}

// antigravityGemini is the .gemini folder the Antigravity CLI uses: its
// config (MCP servers, hooks) in config/, its own settings in
// antigravity-cli/.
func (e Env) antigravityGemini() string {
	home := e.AntigravityHome
	if home == "" {
		home = e.Home
	}
	return filepath.Join(home, ".gemini")
}

func (e Env) has(bin string) bool {
	if e.LookPath == nil {
		return false
	}
	_, err := e.LookPath(bin)
	return err == nil
}

// Detect lists the agents installed here: their CLI is on PATH or their
// config directory exists.
func Detect(env Env) []string {
	var found []string
	for _, a := range Agents {
		dir := map[string]string{
			Claude:      env.claudeHome(),
			Codex:       env.codexHome(),
			Gemini:      env.geminiHome(),
			OpenCode:    env.openCodeHome(),
			Antigravity: filepath.Join(env.antigravityGemini(), "antigravity-cli"),
		}[a]
		if a == Gemini && onlyAntigravity(dir) {
			// The Antigravity CLI keeps its files in ~/.gemini too.
			dir = ""
		}
		if env.has(Binary(a)) || (dir != "" && dirExists(dir)) {
			found = append(found, a)
		}
	}
	return found
}

// onlyAntigravity reports whether a .gemini folder holds the Antigravity
// CLI's folders (antigravity-cli, config) and nothing else.
func onlyAntigravity(geminiDir string) bool {
	entries, err := os.ReadDir(geminiDir)
	if err != nil || len(entries) == 0 {
		return false
	}
	for _, e := range entries {
		if name := e.Name(); name != "antigravity-cli" && name != "config" {
			return false
		}
	}
	return true
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// Options are what `nodevas setup-agent` was asked to do.
type Options struct {
	Agents []string
	Remove bool
	DryRun bool
	// RunsOnly wires only what a runner's runs need from the person's own
	// config, and nothing that joins their hand-started sessions to a hub:
	// for Antigravity the mcp(nodevas/*) allow rule (headless agy cannot
	// ask), for the others nothing, since runs bring their own MCP server.
	RunsOnly bool
	// Server, Project and AgentRole go on every MCP command line; Server and
	// Project on every hook line too.
	Server    string
	Project   string
	AgentRole string
	// Role is the agent role (planner, qa, tester, executor, reviewer,
	// researcher or custom-role-*) every MCP command line declares with
	// --role; empty is a legacy session with every tool.
	Role string
	// Actor is the attribution for every agent; empty gives each its own,
	// `mcp:<agent>`. An agent's MCP server and its hooks always share one,
	// because the server binds a session to the actor that registered it.
	Actor string
}

// Report is what setup did to one agent.
type Report struct {
	Agent string
	Lines []string
	Notes []string
	Err   error
}

// Label is the agent's product name.
func Label(agent string) string {
	switch agent {
	case Claude:
		return "Claude Code"
	case Codex:
		return "Codex"
	case Gemini:
		return "Gemini CLI"
	case OpenCode:
		return "OpenCode"
	case Antigravity:
		return "Antigravity CLI"
	}
	return agent
}

// Apply wires (or, with Remove, unwires) each agent in opts.Agents. One
// agent failing does not stop the rest; its Report carries the error.
func Apply(env Env, opts Options) []Report {
	if env.GOOS == "" {
		env.GOOS = runtime.GOOS
	}
	reports := make([]Report, 0, len(opts.Agents))
	for _, agent := range opts.Agents {
		s := &setup{env: env, opts: opts, agent: agent, report: Report{Agent: agent}}
		var err error
		switch {
		case opts.RunsOnly && agent == Antigravity:
			err = s.antigravityAllow(filepath.Join(env.antigravityGemini(), "antigravity-cli", "settings.json"))
		case opts.RunsOnly:
			if !slices.Contains(Agents, agent) {
				err = fmt.Errorf("unknown agent %q (want one of %s)", agent, strings.Join(Agents, ", "))
				break
			}
			s.line("nothing to change: runner runs bring their own MCP server")
		default:
			err = s.wire()
		}
		s.report.Err = err
		for _, b := range s.backups {
			s.note("backed up the original to %s", tilde(env.Home, b))
		}
		reports = append(reports, s.report)
	}
	return reports
}

// wire wires (or unwires) one agent fully: MCP server, hooks, permissions.
func (s *setup) wire() error {
	switch agent := s.agent; agent {
	case Claude:
		return s.claude()
	case Codex:
		return s.codex()
	case Gemini:
		return s.gemini()
	case OpenCode:
		return s.openCode()
	case Antigravity:
		return s.antigravity()
	default:
		return fmt.Errorf("unknown agent %q (want one of %s)", agent, strings.Join(Agents, ", "))
	}
}

// ParseAgents splits a comma list and checks every name.
func ParseAgents(list string) ([]string, error) {
	var out []string
	for _, a := range strings.Split(list, ",") {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			continue
		}
		if !slices.Contains(Agents, a) {
			return nil, fmt.Errorf("unknown agent %q (want one of %s)", a, strings.Join(Agents, ", "))
		}
		if !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no agents named")
	}
	return out, nil
}

// setup is one agent's run.
type setup struct {
	env     Env
	opts    Options
	agent   string
	report  Report
	backups []string
}

func (s *setup) line(format string, args ...any) {
	s.report.Lines = append(s.report.Lines, fmt.Sprintf(format, args...))
}

func (s *setup) note(format string, args ...any) {
	s.report.Notes = append(s.report.Notes, fmt.Sprintf(format, args...))
}

// changed records what happened to path.
func (s *setup) changed(path string, did bool) {
	verb := "unchanged"
	switch {
	case did && s.opts.DryRun:
		verb = "would change"
	case did:
		verb = "changed"
	}
	s.line("%s %s", verb, tilde(s.env.Home, path))
}

func (s *setup) actor() string {
	if a := strings.TrimSpace(s.opts.Actor); a != "" {
		return a
	}
	return "mcp:" + s.agent
}

// mcpArgs is the argument list after the binary for the MCP server.
func (s *setup) mcpArgs() []string {
	args := []string{"mcp", "--server", s.opts.Server}
	if s.opts.Project != "" {
		args = append(args, "--project", s.opts.Project)
	}
	role := s.opts.AgentRole
	if role == "" {
		role = "worker"
	}
	args = append(args, "--actor", s.actor(), "--agent-role", role)
	if r := strings.TrimSpace(s.opts.Role); r != "" {
		args = append(args, "--role", r)
	}
	return args
}

// hookFlags are the flags after `hook <agent> <event>` on every hook line.
func (s *setup) hookFlags() []string {
	args := []string{"--server", s.opts.Server}
	if s.opts.Project != "" {
		args = append(args, "--project", s.opts.Project)
	}
	return append(args, "--actor", s.actor())
}

// hookCmd is the shell command an agent runs for event.
func (s *setup) hookCmd(event string) string {
	args := append([]string{"hook", s.agent, event}, s.hookFlags()...)
	return shellCmd(shellFor(s.agent, s.env.GOOS), s.env.GOOS, s.env.Exe, args)
}

// isOurs recognises a hook command this program wrote for the agent.
func (s *setup) isOurs(command string) bool {
	return ourHook(command, s.agent, s.env.Exe)
}

// commandGroup is a Claude-style handler group running one hook event.
// timeout is in the agent's own unit (seconds for Claude Code and Codex,
// milliseconds for Gemini CLI).
func (s *setup) commandGroup(event, matcher, name string, timeout int) *object {
	handler := obj("type", "command")
	if name != "" {
		handler.set("name", name)
	}
	handler.set("command", s.hookCmd(event))
	handler.set("timeout", timeout)
	g := newObject()
	if matcher != "" {
		g.set("matcher", matcher)
	}
	g.set("hooks", []any{handler})
	return g
}

// mergeHooksInto merges groups under doc's "hooks" key (removing ours when
// opts.Remove), dropping the key if nothing is left in it.
func (s *setup) mergeHooksInto(doc *object, groups []hookGroup) error {
	_, existed := doc.get("hooks")
	hooks, err := doc.child("hooks")
	if err != nil {
		return err
	}
	// An empty "hooks" the person wrote stays; one we created or emptied goes.
	hadAny := len(hooks.keys) > 0
	if s.opts.Remove {
		groups = nil
	}
	mergeHookGroups(hooks, groups, s.isOurs)
	if len(hooks.keys) == 0 && (hadAny || !existed) {
		doc.del("hooks")
	}
	return nil
}
