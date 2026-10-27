package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"nodevas/internal/runnerwire"
)

// OpenCode runs sst's OpenCode CLI headless:
//
//	opencode run --format json --dir <worktree> [-m <provider/model>]   (1.x)
//	opencode run --standalone --format json [-m <provider/model>]       (2.x)
//
// with the prompt on stdin (a non-terminal stdin is read as the message) and
// OPENCODE_CONFIG_CONTENT holding the run's config: the nodevas MCP server and
// the permission rules for the owner's setting. Checked against opencode
// 1.18.23's `opencode run --help` and real runs (testdata/opencode_*.jsonl).
//
// Inline config is merged over the owner's own opencode.json (their providers,
// default model and sign-ins, which runs share), and wins over it and over a
// project config in the worktree, so the permissions are the run's. Every
// other MCP server, the owner's or one a project config in the worktree
// adds, is switched off (RunEnv.DisableMCP, from OpenCodeMCPServers).
//
// OpenCode takes its project directory from PWD when it is set, not from the
// working directory it was started in (a runner started from a POSIX shell
// has one), so the worktree goes in --dir and in PWD as well as in cmd.Dir.
//
// Headless, a permission that would ask is rejected and ends the run, so the
// rules deny instead: a denied tool is not offered to the model at all.
//
// A read-only run (ReadOnly) also gets OPENCODE_DISABLE_PROJECT_CONFIG=1 and
// no LSP servers or formatters: the worktree's opencode.json and .opencode/
// are not the owner's. The runner removes those from the worktree as well,
// because opencode 1.18.23's newer config loader ignores the variable (see
// repoconfig.go).
//
// OpenCode 2 (checked against 2.1.4) differs in four ways that matter here:
//   - `run` has no --dir and, without --standalone, hands the run to the
//     owner's shared background service, which keeps its own environment and
//     so ignores this run's config and OPENCODE_DISABLE_PROJECT_CONFIG.
//     --standalone gives the run a private server that reads both.
//   - Its `execute` tool runs JavaScript that may call fetch, past a webfetch
//     deny, so it is denied short of full.
//   - MCP tools are reached only through `execute` unless their server says
//     codemode: false, which nodevas's does; its tools are then offered
//     directly, as nodevas_<tool>, and allowed by name.
//   - MCP servers are configured under mcp.servers, and an entry that lacks
//     its type is dropped rather than merged, so a server is switched off
//     with a whole entry.
//
// OpenCodeUnsupported says why the OpenCode that printed version cannot take
// runs, or "" when it can. The adapter is verified against 1.x and 2.x; a
// later major version changed enough before (2.x dropped `run --dir`) that
// it is refused until checked. A version it cannot read is left to fail, or
// not, on its own.
func OpenCodeUnsupported(version string) string {
	if major := openCodeMajor(version); major >= 3 {
		return fmt.Sprintf("opencode: version %d is not supported yet; runs need OpenCode 1.x or 2.x", major)
	}
	return ""
}

type OpenCode struct{}

// openCodeMajor is the major version in an `opencode --version` line, or 0.
func openCodeMajor(version string) int {
	for _, field := range strings.Fields(version) {
		major, _, found := strings.Cut(strings.TrimPrefix(field, "v"), ".")
		if n, err := strconv.Atoi(major); found && err == nil {
			return n
		}
	}
	return 0
}

// OpenCodeV2 says whether the OpenCode that printed version is 2.x. An
// unknown version is taken as 1.x, what the adapter was first written for.
func OpenCodeV2(version string) bool { return openCodeMajor(version) == 2 }

// openCode2 says whether the run's OpenCode is 2.x.
func openCode2(env RunEnv) bool { return OpenCodeV2(env.Version) }

// OpenCodePermissionModes are the runner's permission settings for OpenCode
// runs: read-only (no edits, no shell, no web), edit (edits, no shell, no
// web) and full (edits, any shell command and the web, without asking).
// OpenCode 2's execute tool, which can reach the web, is offered in full only.
var OpenCodePermissionModes = []string{"read-only", "edit", "full"}

// DefaultOpenCodePermission lets the agent edit files in its worktree and
// nothing more; the shell is not offered.
const DefaultOpenCodePermission = "edit"

// openCodeConfigEnv is the environment variable OpenCode reads inline config
// from.
const openCodeConfigEnv = "OPENCODE_CONFIG_CONTENT"

// openCodeNoProjectConfig makes OpenCode skip the project's opencode.json and
// .opencode/ directories (opencode 1.18.23's config loader and its plugin
// dependency install).
const openCodeNoProjectConfig = "OPENCODE_DISABLE_PROJECT_CONFIG=1"

// openCodeToolTimeoutMs bounds one nodevas MCP request. ask_user waits up to
// ten minutes for an answer; OpenCode's default is five seconds.
const openCodeToolTimeoutMs = 900_000

func (OpenCode) Kind() string { return "opencode" }

func (OpenCode) NewParser() Parser { return &openCodeParser{} }

func (OpenCode) Command(job runnerwire.Job, env RunEnv) (*exec.Cmd, error) {
	if env.Binary == "" || env.Workdir == "" {
		return nil, fmt.Errorf("opencode: binary and workdir are required")
	}
	config, err := openCodeConfig(env, false, ReadOnly(job))
	if err != nil {
		return nil, err
	}
	args := []string{"run", "--format", "json", "--dir", env.Workdir}
	if openCode2(env) {
		args = []string{"run", "--standalone", "--format", "json"}
	}
	if job.Model != "" && job.Model != "default" {
		args = append(args, "-m", job.Model)
	}
	cmd := exec.Command(env.Binary, args...)
	cmd.Dir = env.Workdir
	cmd.Stdin = strings.NewReader(Prompt(job, env.Branch))
	cmd.Env = jobEnv(job, openCodeEnv(job, env, config)...)
	// npm installs opencode on Windows as a .cmd shim; nothing here needs
	// quoting (the config is in the environment, the prompt on stdin).
	if err := wrapBatch(cmd); err != nil {
		return nil, fmt.Errorf("opencode: %w", err)
	}
	return cmd, nil
}

// openCodeEnv is what an OpenCode process gets on top of jobEnv.
func openCodeEnv(job runnerwire.Job, env RunEnv, config string) []string {
	extra := []string{"PWD=" + env.Workdir, openCodeConfigEnv + "=" + config}
	if ReadOnly(job) {
		extra = append(extra, openCodeNoProjectConfig)
	}
	return extra
}

// openCodeConfig is the run's OPENCODE_CONFIG_CONTENT. interactive turns each
// deny into ask: someone is at the terminal to answer. readOnly also turns off
// LSP servers and formatters, programs OpenCode starts on its own.
func openCodeConfig(env RunEnv, interactive, readOnly bool) (string, error) {
	mode := env.PermissionMode
	if mode == "" {
		mode = DefaultOpenCodePermission
	}
	// external_directory (a path outside the worktree) defaults to ask, and a
	// headless ask is a rejection that ends the session there and then (a
	// review stops before its verdict); deny instead hands the model an error
	// it works around (checked against opencode 1.18.23).
	//
	// webfetch and websearch are denied short of full: a prompt-injected
	// agent could otherwise send what it read in the worktree to any URL.
	permission := map[string]string{"edit": "allow", "bash": "deny", "webfetch": "deny", "websearch": "deny",
		"external_directory": "deny"}
	switch mode {
	case "read-only":
		permission["edit"] = "deny"
	case "edit":
	case "full":
		permission["bash"] = "allow"
		permission["external_directory"] = "allow"
		permission["webfetch"] = "allow"
		permission["websearch"] = "allow"
	default:
		return "", fmt.Errorf("opencode: unknown permission setting %q (want one of %s)",
			mode, strings.Join(OpenCodePermissionModes, ", "))
	}
	if openCode2(env) {
		permission["execute"] = "deny"
		if mode == "full" {
			permission["execute"] = "allow"
		}
		permission["nodevas_*"] = "allow"
	}
	if interactive {
		for tool, rule := range permission {
			if rule == "deny" {
				permission[tool] = "ask"
			}
		}
	}
	command := append([]string{env.MCP.Command}, env.MCP.Args...)
	servers := map[string]any{
		"nodevas": map[string]any{
			"type":        "local",
			"command":     command,
			"enabled":     true,
			"timeout":     openCodeToolTimeoutMs,
			"environment": map[string]string{"NODEVAS_AGENT": "opencode"},
		},
	}
	for _, name := range env.DisableMCP {
		if name != "nodevas" {
			servers[name] = map[string]any{"enabled": false}
		}
	}
	var mcp any = servers
	if openCode2(env) {
		nodevas := servers["nodevas"].(map[string]any)
		delete(nodevas, "enabled")
		nodevas["codemode"] = false
		for name := range servers {
			if name != "nodevas" {
				// Nothing listens on the discard port, and a disabled server
				// is not connected to anyway.
				servers[name] = map[string]any{"type": "remote", "url": "http://127.0.0.1:9", "disabled": true}
			}
		}
		mcp = map[string]any{"servers": servers}
	}
	inline := map[string]any{
		"mcp":        mcp,
		"permission": permission,
	}
	if readOnly {
		inline["lsp"] = false
		inline["formatter"] = false
	}
	config, err := json.Marshal(inline)
	if err != nil {
		return "", err
	}
	return string(config), nil
}

// openCodeParser reads `opencode run --format json`: one JSON object per line,
// typed step_start / text / reasoning / tool_use / step_finish / error, each
// carrying a message part. A tool_use line arrives once the call has finished,
// with its input and output together. There is no closing result line: a
// step_finish whose reason is "stop" ends the turn.
type openCodeParser struct {
	outcome Outcome
	started bool
	// lastText is the latest assistant text: the run's summary.
	lastText string
	// lastError is the latest error line; a later finished turn clears it.
	lastError string
	usage     runnerwire.Usage
	sawUsage  bool
}

type openCodeLine struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionID"`
	Part      *openCodePart   `json:"part"`
	Error     json.RawMessage `json:"error"`
}

type openCodePart struct {
	Type string `json:"type"`
	// text, reasoning
	Text string `json:"text"`
	// tool
	Tool   string `json:"tool"`
	CallID string `json:"callID"`
	// ID is the part's own ID in 1.x and the tool call's in 2.x, which has no
	// callID.
	ID    string `json:"id"`
	State *struct {
		Status string          `json:"status"`
		Input  json.RawMessage `json:"input"`
		Output string          `json:"output"`
		Error  string          `json:"error"`
	} `json:"state"`
	// step-finish
	Reason string `json:"reason"`
	Tokens *struct {
		Input     int64 `json:"input"`
		Output    int64 `json:"output"`
		Reasoning int64 `json:"reasoning"`
		Cache     struct {
			Read  int64 `json:"read"`
			Write int64 `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
	Cost float64 `json:"cost"`
}

func (p *openCodeParser) Outcome() Outcome {
	out := p.outcome
	out.Summary = Truncate(p.lastText, runnerwire.MaxSummaryBytes)
	out.Text = Truncate(p.lastText, MaxTextBytes)
	if p.sawUsage {
		usage := p.usage
		out.Usage = &usage
	}
	if p.lastError != "" {
		out.IsError = true
		out.Error = Truncate(p.lastError, runnerwire.MaxSummaryBytes)
	}
	return out
}

func (p *openCodeParser) Parse(line []byte) []runnerwire.RunEvent {
	line = bytes.TrimSpace(bytes.TrimPrefix(line, utf8BOM))
	if len(line) == 0 {
		return nil
	}
	var msg openCodeLine
	if line[0] != '{' || json.Unmarshal(line, &msg) != nil {
		return []runnerwire.RunEvent{{Type: runnerwire.EventLog, Text: clip(string(line))}}
	}
	var events []runnerwire.RunEvent
	if !p.started && msg.SessionID != "" {
		p.started = true
		events = append(events, runnerwire.RunEvent{Type: runnerwire.EventLog,
			Text: "opencode session started " + msg.SessionID})
	}
	if msg.Type == "error" {
		reason := openCodeErrorText(msg.Error)
		if reason == "" {
			reason = "opencode reported an error"
		}
		p.lastError = reason
		return append(events, runnerwire.RunEvent{Type: runnerwire.EventError, Text: clip(reason)})
	}
	part := msg.Part
	if part == nil {
		return events
	}
	switch msg.Type {
	case "text":
		if strings.TrimSpace(part.Text) == "" {
			break
		}
		p.lastText = part.Text
		events = append(events, runnerwire.RunEvent{Type: runnerwire.EventText, Text: clip(part.Text)})
	case "reasoning":
		if strings.TrimSpace(part.Text) != "" {
			events = append(events, runnerwire.RunEvent{Type: runnerwire.EventThinking, Text: clip(part.Text)})
		}
	case "tool_use":
		events = append(events, openCodeTool(part)...)
	case "step_finish":
		if part.Tokens != nil {
			t := part.Tokens
			p.sawUsage = true
			// input is the uncached part already, as Claude Code reports it;
			// reasoning tokens are output the model was billed for.
			p.usage.InputTokens += t.Input
			p.usage.OutputTokens += t.Output + t.Reasoning
			p.usage.CacheReadTokens += t.Cache.Read
			p.usage.CacheWriteTokens += t.Cache.Write
			p.usage.CostUSD += part.Cost
			usage := p.usage
			events = append(events, runnerwire.RunEvent{Type: runnerwire.EventUsage, Usage: &usage})
		}
		if part.Reason == "stop" {
			p.outcome.Done = true
			p.lastError = ""
		}
	}
	// step_start: not a step worth showing.
	return events
}

// openCodeTool is a finished tool call: its call and its result.
func openCodeTool(part *openCodePart) []runnerwire.RunEvent {
	name := part.Tool
	// MCP tools are named <server>_<tool>; nodevas's are named the way Claude
	// Code names MCP tools, so the UI and the allowlists read the same.
	if rest, ok := strings.CutPrefix(name, "nodevas_"); ok {
		name = "mcp__nodevas__" + rest
	}
	id := part.CallID
	if id == "" {
		id = part.ID
	}
	call := runnerwire.RunEvent{Type: runnerwire.EventToolCall, Tool: name, ToolID: id}
	result := runnerwire.RunEvent{Type: runnerwire.EventToolResult, ToolID: id}
	if s := part.State; s != nil {
		call.Input = clip(compactJSON(s.Input))
		result.Text = s.Output
		if s.Status == "error" {
			result.IsError = true
			if result.Text == "" {
				result.Text = s.Error
			}
		}
	}
	result.Text = clip(result.Text)
	return []runnerwire.RunEvent{call, result}
}

// openCodeErrorText reads an error line's error: {"name": …, "data":
// {"message": …}}, {"message": …}, 2.x's {"type": …, "message": …} or a
// string.
func openCodeErrorText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var e struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		Message string `json:"message"`
		Data    struct {
			Message string `json:"message"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return ""
	}
	if e.Name == "" {
		e.Name = e.Type
	}
	message := strings.TrimSpace(e.Data.Message)
	if message == "" {
		message = strings.TrimSpace(e.Message)
	}
	switch {
	case message == "":
		return e.Name
	case e.Name != "":
		return e.Name + ": " + message
	}
	return message
}
