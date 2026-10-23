package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
	config, err := json.Marshal(map[string]any{
		"mcpServers": map[string]MCPServer{"nodevas": env.MCP},
	})
	if err != nil {
		return nil, err
	}
	mcpPath := filepath.Join(env.TempDir, "mcp.json")
	if err := os.WriteFile(mcpPath, config, 0o600); err != nil {
		return nil, fmt.Errorf("writing the MCP config: %w", err)
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
	args = append(args, "--allowedTools", NodevasMCPTools)
	args = append(args, env.AllowedTools...)
	cmd := exec.Command(env.Binary, args...)
	cmd.Dir = env.Workdir
	// On stdin, not as an argument: no command-line length limit and no
	// cmd.exe quoting for a prompt full of quotes and percent signs.
	cmd.Stdin = strings.NewReader(Prompt(job, env.Branch))
	cmd.Env = childEnv()
	return cmd, nil
}

// childEnv is the runner's environment minus the markers Claude Code sets for
// its own children: a runner started from inside a Claude Code session would
// otherwise hand them on, and the CLI refuses to start "inside" another one.
func childEnv() []string {
	env := os.Environ()
	out := env[:0:0]
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT":
			continue
		}
		out = append(out, kv)
	}
	return out
}

// claudeParser reads `--output-format stream-json --verbose`: one JSON object
// per line, typed system / assistant / user / result (and others, ignored).
type claudeParser struct {
	outcome Outcome
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

	// result
	IsError      bool            `json:"is_error"`
	Result       string          `json:"result"`
	TotalCostUSD float64         `json:"total_cost_usd"`
	Usage        *claudeUsage    `json:"usage"`
	Errors       json.RawMessage `json:"errors"`
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
	var events []runnerwire.RunEvent
	if msg.Usage != nil || msg.TotalCostUSD > 0 {
		usage := &runnerwire.Usage{CostUSD: msg.TotalCostUSD}
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
