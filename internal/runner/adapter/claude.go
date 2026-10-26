package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"nodevas/internal/runnerwire"
)

// Claude runs Claude Code headless:
//
//	claude -p --output-format stream-json --verbose --permission-mode <mode>
//	       --mcp-config <file> --strict-mcp-config [--model <id>] [--allowedTools <tool>...]
//
// with the prompt on stdin. Checked against Claude Code 2.1.299's --help.
type Claude struct{}

// ClaudePermissionModes are --permission-mode's choices, exactly as the CLI
// lists them.
var ClaudePermissionModes = []string{"acceptEdits", "auto", "bypassPermissions", "manual", "dontAsk", "plan"}

// DefaultClaudePermissionMode lets the agent edit files in its worktree but
// not run arbitrary commands unless the owner allowed them (AllowedTools).
const DefaultClaudePermissionMode = "acceptEdits"

// ClaudeReadOnlyDeny is the --disallowedTools list of a review or plan run:
// every built-in tool that writes a file or runs a command (PowerShell is
// Claude Code's shell tool on Windows). Subagents inherit the deny.
const ClaudeReadOnlyDeny = "Edit,Write,MultiEdit,NotebookEdit,Bash,PowerShell"

// NodevasMCPTools is the --allowedTools rule for every tool of the run's
// nodevas MCP server.
const NodevasMCPTools = "mcp__nodevas"

func (Claude) Kind() string { return "claude" }

func (Claude) NewParser() Parser { return &claudeParser{} }

func (Claude) Command(job runnerwire.Job, env RunEnv) (*exec.Cmd, error) {
	if env.Binary == "" || env.Workdir == "" || env.TempDir == "" {
		return nil, fmt.Errorf("claude: binary, workdir and temp dir are required")
	}
	// The MCP config goes in a file rather than inline JSON: on Windows the
	// CLI is a .cmd shim, and cmd.exe's quoting rules mangle JSON arguments.
	mcpPath, err := writeMCPConfig(env)
	if err != nil {
		return nil, err
	}

	mode := env.PermissionMode
	if mode == "" {
		mode = DefaultClaudePermissionMode
	}
	args := []string{
		"-p",
		"--output-format", "stream-json",
		// stream-json with -p requires --verbose.
		"--verbose",
		"--permission-mode", mode,
		"--mcp-config", mcpPath,
		// Only the nodevas server: the owner's own MCP servers (with their own
		// credentials) are not this run's business.
		"--strict-mcp-config",
	}
	if job.Model != "" && job.Model != "default" {
		args = append(args, "--model", job.Model)
	}
	// Last, because the flag is variadic: every argument after it is a tool.
	// Nothing positional follows; the prompt goes on stdin. The nodevas
	// server's tools are always allowed: the preamble tells the agent to claim
	// its node, headless nobody can approve that, and the hub limits what
	// those tools reach to the agent routes anyway.
	if ReadOnly(job) {
		// Read only, whatever the owner's permission mode and allowedTools:
		// a deny wins over every allow, bypassPermissions included. The
		// nodevas tools stay allowed; a plan writes through them.
		// The worktree is a PR head or a base it only reads: its
		// .claude/settings.json (hooks, permissions) is not trusted here.
		args = append(args, "--setting-sources", "user")
		args = append(args, "--disallowedTools", ClaudeReadOnlyDeny)
	}
	args = append(args, "--allowedTools", NodevasMCPTools)
	args = append(args, env.AllowedTools...)
	cmd := exec.Command(env.Binary, args...)
	cmd.Dir = env.Workdir
	// On stdin, not as an argument: no command-line length limit and no
	// cmd.exe quoting for a prompt full of quotes and percent signs.
	cmd.Stdin = strings.NewReader(Prompt(job, env.Branch))
	cmd.Env = jobEnv(job)
	return cmd, nil
}

// childEnv is the runner's environment minus what an agent must not have:
//   - the markers Claude Code sets for its own children (a runner started from
//     inside a Claude Code session would otherwise hand them on, and the CLI
//     refuses to start "inside" another one);
//   - every NODEVAS_* variable: the runner's token, its Cloudflare Access
//     secret, the copilot and advisor keys. jobEnv adds back the few a run
//     needs (NODEVAS_RUN_ID, NODEVAS_RUN_MCP);
//   - GitHub tokens: the runner pushes and opens pull requests itself, an
//     agent never needs them.
//
// An agent that a task or a repository file talks into running `env` must find
// nothing worth stealing. ANTHROPIC_API_KEY and OPENAI_API_KEY stay: agents may
// sign in with them.
func childEnv() []string {
	env := os.Environ()
	out := env[:0:0]
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if strippedEnv(name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// strippedEnv reports whether childEnv drops the variable called name.
func strippedEnv(name string) bool {
	upper := strings.ToUpper(name)
	switch upper {
	case "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT",
		"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN":
		return true
	}
	return strings.HasPrefix(upper, "NODEVAS_")
}

// RunIDEnv names the run an agent process belongs to. `nodevas hook` does
// nothing while it is set, so the owner's own agent hooks (from `nodevas
// setup-agent`) do not report a run as one of the owner's sessions.
const RunIDEnv = "NODEVAS_RUN_ID"

// jobEnv is childEnv for job's agent process: RunIDEnv set, and each of extra
// ("NAME=value") in place of whatever the runner's environment had under that
// name (names compared case-insensitively, as Windows does).
func jobEnv(job runnerwire.Job, extra ...string) []string {
	if job.RunID != "" {
		extra = append([]string{RunIDEnv + "=" + job.RunID}, extra...)
	}
	names := make([]string, 0, len(extra))
	for _, kv := range extra {
		name, _, _ := strings.Cut(kv, "=")
		names = append(names, name)
	}
	env := childEnv()
	out := env[:0:0]
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, name) }) {
			out = append(out, kv)
		}
	}
	return append(out, extra...)
}

// claudeMainModel is the model a run mostly ran on: of the result's models
// (a run's subagents may use others), the one that wrote the most, else the
// model the init line named.
func claudeMainModel(msg claudeLine, initModel string) string {
	best, most := "", int64(-1)
	for id, u := range msg.ModelUsage {
		if id = strings.TrimSpace(id); id == "" {
			continue
		}
		if u.OutputTokens > most || (u.OutputTokens == most && id < best) {
			best, most = id, u.OutputTokens
		}
	}
	if best != "" {
		return best
	}
	return initModel
}

// claudeParser reads `--output-format stream-json --verbose`: one JSON object
// per line, typed system / assistant / user / result (and others, ignored).
type claudeParser struct {
	outcome Outcome
	// model is the model the init line named, for the usage when the result
	// does not name one.
	model string
}

func (p *claudeParser) Outcome() Outcome { return p.outcome }

type claudeLine struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`

	// system/init
	SessionID      string `json:"session_id"`
	Model          string `json:"model"`
	PermissionMode string `json:"permissionMode"`

	// assistant, user: an object with content. Raw because other line types
	// reuse the name for a plain string (system/permission_denied), and one
	// strict field must not cost the whole line.
	Message json.RawMessage `json:"message"`

	// system/permission_denied
	ToolName string `json:"tool_name"`

	// rate_limit_event: the subscription's window, and whether this request
	// was let through.
	RateLimit *struct {
		Status   string `json:"status"`
		ResetsAt int64  `json:"resetsAt"`
	} `json:"rate_limit_info"`

	// result
	IsError      bool            `json:"is_error"`
	Result       string          `json:"result"`
	TotalCostUSD float64         `json:"total_cost_usd"`
	Usage        *claudeUsage    `json:"usage"`
	Errors       json.RawMessage `json:"errors"`
	// ModelUsage is the result's usage per model ID.
	ModelUsage map[string]struct {
		OutputTokens int64 `json:"outputTokens"`
	} `json:"modelUsage"`
}

type claudeUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

type claudeBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"`
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input"`
	// tool_result
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

func (p *claudeParser) Parse(line []byte) []runnerwire.RunEvent {
	line = bytes.TrimSpace(bytes.TrimPrefix(line, utf8BOM))
	if len(line) == 0 {
		return nil
	}
	var msg claudeLine
	if line[0] != '{' || json.Unmarshal(line, &msg) != nil {
		// Not stream-json: a warning the CLI printed to stdout. Worth seeing.
		return []runnerwire.RunEvent{{Type: runnerwire.EventLog, Text: clip(string(line))}}
	}
	switch msg.Type {
	case "system":
		if msg.Subtype == "permission_denied" {
			// Headless, nobody can say yes: the owner widens allowedTools or
			// the permission mode on the runner if this keeps happening.
			text := "permission denied: " + msg.ToolName
			var reason string
			if json.Unmarshal(msg.Message, &reason) == nil && reason != "" {
				text += " (" + reason + ")"
			}
			return []runnerwire.RunEvent{{Type: runnerwire.EventError, Text: clip(text)}}
		}
		if msg.Subtype != "init" {
			return nil // hook chatter, retries: not progress
		}
		p.model = strings.TrimSpace(msg.Model)
		text := "claude session started"
		if msg.SessionID != "" {
			text += " " + msg.SessionID
		}
		var details []string
		if msg.Model != "" {
			details = append(details, "model "+msg.Model)
		}
		if msg.PermissionMode != "" {
			details = append(details, "permission mode "+msg.PermissionMode)
		}
		if len(details) > 0 {
			text += " (" + strings.Join(details, ", ") + ")"
		}
		return []runnerwire.RunEvent{{Type: runnerwire.EventLog, Text: text}}
	case "assistant":
		return p.assistant(msg)
	case "user":
		return p.toolResults(msg)
	case "result":
		return p.result(msg)
	case "rate_limit_event":
		// Only a refusal says when the agent may go on; an "allowed" event's
		// reset time is just the window's end.
		if rl := msg.RateLimit; rl != nil && rl.ResetsAt > 0 && rl.Status != "" && !strings.HasPrefix(rl.Status, "allowed") {
			p.outcome.ResetAt = time.Unix(rl.ResetsAt, 0)
		}
		return nil
	}
	return nil
}

func blocksOf(msg claudeLine) []claudeBlock {
	var message struct {
		// A user message's content is a string (an echoed prompt) or blocks.
		Content json.RawMessage `json:"content"`
	}
	if len(msg.Message) == 0 || msg.Message[0] != '{' || json.Unmarshal(msg.Message, &message) != nil {
		return nil
	}
	if len(message.Content) == 0 || message.Content[0] != '[' {
		return nil
	}
	var blocks []claudeBlock
	if json.Unmarshal(message.Content, &blocks) != nil {
		return nil
	}
	return blocks
}

func (p *claudeParser) assistant(msg claudeLine) []runnerwire.RunEvent {
	var events []runnerwire.RunEvent
	for _, block := range blocksOf(msg) {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				events = append(events, runnerwire.RunEvent{Type: runnerwire.EventText, Text: clip(block.Text)})
			}
		case "thinking":
			if strings.TrimSpace(block.Thinking) != "" {
				events = append(events, runnerwire.RunEvent{Type: runnerwire.EventThinking, Text: clip(block.Thinking)})
			}
		case "tool_use":
			events = append(events, runnerwire.RunEvent{
				Type:   runnerwire.EventToolCall,
				Tool:   block.Name,
				ToolID: block.ID,
				Input:  clip(compactJSON(block.Input)),
			})
		}
	}
	return events
}

func (p *claudeParser) toolResults(msg claudeLine) []runnerwire.RunEvent {
	var events []runnerwire.RunEvent
	for _, block := range blocksOf(msg) {
		if block.Type != "tool_result" {
			continue
		}
		events = append(events, runnerwire.RunEvent{
			Type:    runnerwire.EventToolResult,
			ToolID:  block.ToolUseID,
			Text:    clip(toolResultText(block.Content)),
			IsError: block.IsError,
		})
	}
	return events
}

// toolResultText flattens a tool_result's content: a plain string, or blocks
// of which only the text ones mean anything in a timeline.
func toolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var texts []string
	for _, part := range parts {
		switch part.Type {
		case "text":
			texts = append(texts, part.Text)
		case "image":
			texts = append(texts, "[image]")
		}
	}
	return strings.Join(texts, "\n")
}

func (p *claudeParser) result(msg claudeLine) []runnerwire.RunEvent {
	p.outcome.Done = true
	p.outcome.IsError = msg.IsError
	p.outcome.Summary = Truncate(msg.Result, runnerwire.MaxSummaryBytes)
	p.outcome.Text = Truncate(msg.Result, MaxTextBytes)
	var events []runnerwire.RunEvent
	if msg.Usage != nil || msg.TotalCostUSD > 0 {
		usage := &runnerwire.Usage{CostUSD: msg.TotalCostUSD, Model: claudeMainModel(msg, p.model)}
		if msg.Usage != nil {
			usage.InputTokens = msg.Usage.InputTokens
			usage.OutputTokens = msg.Usage.OutputTokens
			usage.CacheReadTokens = msg.Usage.CacheReadInputTokens
			usage.CacheWriteTokens = msg.Usage.CacheCreationInputTokens
		}
		p.outcome.Usage = usage
		copied := *usage
		events = append(events, runnerwire.RunEvent{Type: runnerwire.EventUsage, Usage: &copied})
	}
	if msg.IsError {
		reason := strings.TrimSpace(msg.Result)
		if reason == "" {
			reason = strings.TrimSpace(errorsText(msg.Errors))
		}
		if reason == "" {
			reason = "claude reported an error"
			if msg.Subtype != "" {
				reason += " (" + msg.Subtype + ")"
			}
		}
		p.outcome.Error = Truncate(reason, runnerwire.MaxSummaryBytes)
		events = append(events, runnerwire.RunEvent{Type: runnerwire.EventError, Text: clip(reason)})
	}
	return events
}

// errorsText reads the result's "errors" field, a list of strings on the
// error_* subtypes.
func errorsText(raw json.RawMessage) string {
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return strings.Join(list, "; ")
	}
	return ""
}

func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil {
		return string(raw)
	}
	return buf.String()
}

func clip(s string) string { return Truncate(s, runnerwire.MaxEventText) }
