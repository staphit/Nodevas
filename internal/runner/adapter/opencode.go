package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"nodevas/internal/runnerwire"
)

// OpenCode runs sst's OpenCode CLI headless:
//
//	opencode run --format json --dir <worktree> [-m <provider/model>]
//
// with the prompt on stdin (a non-terminal stdin is read as the message) and
// OPENCODE_CONFIG_CONTENT holding the run's config: the nodevas MCP server and
// the permission rules for the owner's setting. Checked against opencode
// 1.18.23's `opencode run --help` and real runs (testdata/opencode_*.jsonl).
//
// Inline config is merged over the owner's own opencode.json (their providers,
// default model and sign-ins, which runs share), and wins over it and over a
// project config in the worktree, so the permissions are the run's. The
// owner's own MCP servers still load, as Codex's config.toml does.
//
// OpenCode takes its project directory from PWD when it is set, not from the
// working directory it was started in (a runner started from a POSIX shell
// has one), so the worktree goes in --dir and in PWD as well as in cmd.Dir.
//
// Headless, a permission that would ask is rejected and ends the run, so the
// rules deny instead: a denied tool is not offered to the model at all.
type OpenCode struct{}

// OpenCodePermissionModes are the runner's permission settings for OpenCode
// runs: read-only (no edits, no shell), edit (edits, no shell) and full
// (edits and any shell command, without asking).
var OpenCodePermissionModes = []string{"read-only", "edit", "full"}

// DefaultOpenCodePermission lets the agent edit files in its worktree and
// nothing more; the shell is not offered.
const DefaultOpenCodePermission = "edit"

// openCodeConfigEnv is the environment variable OpenCode reads inline config
// from.
const openCodeConfigEnv = "OPENCODE_CONFIG_CONTENT"

// openCodeToolTimeoutMs bounds one nodevas MCP request. ask_user waits up to
// ten minutes for an answer; OpenCode's default is five seconds.
const openCodeToolTimeoutMs = 900_000

func (OpenCode) Kind() string { return "opencode" }

func (OpenCode) NewParser() Parser { return &openCodeParser{} }

func (OpenCode) Command(job runnerwire.Job, env RunEnv) (*exec.Cmd, error) {
	if env.Binary == "" || env.Workdir == "" {
		return nil, fmt.Errorf("opencode: binary and workdir are required")
	}
	config, err := openCodeConfig(env, false)
	if err != nil {
		return nil, err
	}
	args := []string{"run", "--format", "json", "--dir", env.Workdir}
	if job.Model != "" && job.Model != "default" {
		args = append(args, "-m", job.Model)
	}
	cmd := exec.Command(env.Binary, args...)
	cmd.Dir = env.Workdir
	cmd.Stdin = strings.NewReader(Prompt(job, env.Branch))
	cmd.Env = jobEnv(job, "PWD="+env.Workdir, openCodeConfigEnv+"="+config)
	// npm installs opencode on Windows as a .cmd shim; nothing here needs
	// quoting (the config is in the environment, the prompt on stdin).
	if err := wrapBatch(cmd); err != nil {
		return nil, fmt.Errorf("opencode: %w", err)
	}
	return cmd, nil
}

// openCodeConfig is the run's OPENCODE_CONFIG_CONTENT. interactive turns each
// deny into ask: someone is at the terminal to answer.
func openCodeConfig(env RunEnv, interactive bool) (string, error) {
	mode := env.PermissionMode
	if mode == "" {
		mode = DefaultOpenCodePermission
	}
	permission := map[string]string{"edit": "allow", "bash": "deny", "webfetch": "allow"}
	switch mode {
	case "read-only":
		permission["edit"] = "deny"
	case "edit":
	case "full":
		permission["bash"] = "allow"
	default:
		return "", fmt.Errorf("opencode: unknown permission setting %q (want one of %s)",
			mode, strings.Join(OpenCodePermissionModes, ", "))
	}
	if interactive {
		for tool, rule := range permission {
			if rule == "deny" {
				permission[tool] = "ask"
			}
		}
	}
	command := append([]string{env.MCP.Command}, env.MCP.Args...)
	config, err := json.Marshal(map[string]any{
		"mcp": map[string]any{
			"nodevas": map[string]any{
				"type":        "local",
				"command":     command,
				"enabled":     true,
				"timeout":     openCodeToolTimeoutMs,
				"environment": map[string]string{"NODEVAS_AGENT": "opencode"},
			},
		},
		"permission": permission,
	})
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
	State  *struct {
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
	call := runnerwire.RunEvent{Type: runnerwire.EventToolCall, Tool: name, ToolID: part.CallID}
	result := runnerwire.RunEvent{Type: runnerwire.EventToolResult, ToolID: part.CallID}
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
// {"message": …}}, {"message": …} or a string.
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
		Message string `json:"message"`
		Data    struct {
			Message string `json:"message"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return ""
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
