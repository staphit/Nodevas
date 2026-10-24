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

// Gemini runs Google's Gemini CLI headless:
//
//	gemini --output-format stream-json --approval-mode <mode>
//	       --allowed-mcp-server-names nodevas [-m <model>]
//
// with the prompt on stdin (stdin that is not a terminal makes the CLI run
// headless) and GEMINI_CLI_SYSTEM_SETTINGS_PATH pointing at a settings file
// in the run's temp dir that defines the nodevas MCP server.
//
// UNVERIFIED: the Gemini CLI is not installed where this was written. The
// flags, the settings override and the stream-json events follow the CLI's
// documentation (docs/cli/headless.md, docs/get-started/configuration.md),
// and the fixtures in testdata are written from it, not recorded. Check them
// against `gemini --help` and a real run before relying on this adapter.
//
// The MCP server goes in the system settings override, not the project's
// .gemini/settings.json, because the worktree must stay clean. System
// settings take precedence over the owner's own, so the server is defined
// exactly as given here; --allowed-mcp-server-names then leaves the owner's
// own MCP servers out of the run. An administrator's system settings file, if
// there is one, is not read for these runs.
type Gemini struct{}

// GeminiApprovalModes are --approval-mode's choices, per the CLI's docs.
var GeminiApprovalModes = []string{"default", "auto_edit", "yolo"}

// DefaultGeminiApprovalMode approves file edits; anything else (shell
// commands) needs an approval nobody can give headless, so it is refused.
const DefaultGeminiApprovalMode = "auto_edit"

// geminiSystemSettingsEnv names the file the CLI reads as its system settings.
const geminiSystemSettingsEnv = "GEMINI_CLI_SYSTEM_SETTINGS_PATH"

// geminiToolTimeoutMs bounds one nodevas MCP tool call; ask_user waits up to
// ten minutes.
const geminiToolTimeoutMs = 900_000

func (Gemini) Kind() string { return "gemini" }

func (Gemini) NewParser() Parser { return &geminiParser{} }

func (Gemini) Command(job runnerwire.Job, env RunEnv) (*exec.Cmd, error) {
	if env.Binary == "" || env.Workdir == "" || env.TempDir == "" {
		return nil, fmt.Errorf("gemini: binary, workdir and temp dir are required")
	}
	settingsPath, err := writeGeminiSettings(env)
	if err != nil {
		return nil, err
	}

	mode := env.PermissionMode
	if mode == "" {
		mode = DefaultGeminiApprovalMode
	}
	args := []string{
		"--output-format", "stream-json",
		"--approval-mode", mode,
		"--allowed-mcp-server-names", "nodevas",
	}
	if job.Model != "" && job.Model != "default" {
		args = append(args, "-m", job.Model)
	}
	cmd := exec.Command(env.Binary, args...)
	cmd.Dir = env.Workdir
	cmd.Stdin = strings.NewReader(Prompt(job, env.Branch))
	cmd.Env = jobEnv(job, geminiSystemSettingsEnv+"="+settingsPath)
	if err := wrapBatch(cmd); err != nil {
		return nil, fmt.Errorf("gemini: %w", err)
	}
	return cmd, nil
}

// writeGeminiSettings writes the system settings override that defines the
// nodevas MCP server into the run's temp dir.
func writeGeminiSettings(env RunEnv) (string, error) {
	settings, err := json.MarshalIndent(map[string]any{
		"mcpServers": map[string]any{
			"nodevas": map[string]any{
				"command": env.MCP.Command,
				"args":    env.MCP.Args,
				// trust skips the CLI's confirmation for this server's tools:
				// the preamble has the agent claim its node, nobody can say
				// yes headless, and the hub limits these tools anyway.
				"trust":   true,
				"timeout": geminiToolTimeoutMs,
			},
		},
	}, "", "  ")
	if err != nil {
		return "", err
	}
	settingsPath := filepath.Join(env.TempDir, "gemini-settings.json")
	if err := os.WriteFile(settingsPath, settings, 0o600); err != nil {
		return "", fmt.Errorf("writing the Gemini settings: %w", err)
	}
	return settingsPath, nil
}

// geminiParser reads `--output-format stream-json`: one JSON object per line,
// typed init / message / tool_use / tool_result / error / result.
//
// Assistant text arrives as deltas; they are gathered and sent as one text
// event when something else happens (a tool call, the result), so a reply is
// one event rather than one per token.
type geminiParser struct {
	outcome Outcome
	// model is the model the init line named.
	model   string
	pending strings.Builder
	// lastText is the latest finished assistant text: the run's summary.
	lastText string
	// lastError is the latest error line, the failure's reason when the
	// result does not give one.
	lastError string
}

type geminiLine struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Model     string `json:"model"`
	// message
	Role    string `json:"role"`
	Content string `json:"content"`
	Delta   bool   `json:"delta"`
	// tool_use
	ToolName   string          `json:"tool_name"`
	ToolID     string          `json:"tool_id"`
	Parameters json.RawMessage `json:"parameters"`
	// tool_result, result: "success" or "error"
	Status string          `json:"status"`
	Output string          `json:"output"`
	Error  json.RawMessage `json:"error"`
	// error
	Severity string `json:"severity"`
	Message  string `json:"message"`
	// result
	Stats *geminiStats `json:"stats"`
}

type geminiStats struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	Cached       int64 `json:"cached"`
}

func (p *geminiParser) Outcome() Outcome {
	out := p.outcome
	summary := p.lastText
	if p.pending.Len() > 0 {
		summary = p.pending.String()
	}
	out.Summary = Truncate(summary, runnerwire.MaxSummaryBytes)
	out.Text = Truncate(summary, MaxTextBytes)
	if !out.Done && p.lastError != "" {
		out.IsError = true
		out.Error = Truncate(p.lastError, runnerwire.MaxSummaryBytes)
	}
	return out
}

// flush turns the gathered assistant text into one text event.
func (p *geminiParser) flush() []runnerwire.RunEvent {
	text := p.pending.String()
	p.pending.Reset()
	if strings.TrimSpace(text) == "" {
		return nil
	}
	p.lastText = text
	return []runnerwire.RunEvent{{Type: runnerwire.EventText, Text: clip(text)}}
}

func (p *geminiParser) Parse(line []byte) []runnerwire.RunEvent {
	line = bytes.TrimSpace(bytes.TrimPrefix(line, utf8BOM))
	if len(line) == 0 {
		return nil
	}
	var msg geminiLine
	if line[0] != '{' || json.Unmarshal(line, &msg) != nil {
		return []runnerwire.RunEvent{{Type: runnerwire.EventLog, Text: clip(string(line))}}
	}
	if msg.Type == "message" && msg.Role == "assistant" {
		if !msg.Delta {
			// A whole message: it ends whatever came before it.
			events := p.flush()
			p.pending.WriteString(msg.Content)
			return append(events, p.flush()...)
		}
		// Not clipped here: flush clips what is sent, and the summary wants
		// the whole reply.
		if p.pending.Len() < 1<<20 {
			p.pending.WriteString(msg.Content)
		}
		return nil
	}
	events := p.flush()
	switch msg.Type {
	case "init":
		p.model = strings.TrimSpace(msg.Model)
		text := "gemini session started"
		if msg.SessionID != "" {
			text += " " + msg.SessionID
		}
		if msg.Model != "" {
			text += " (model " + msg.Model + ")"
		}
		events = append(events, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: text})
	case "tool_use":
		events = append(events, runnerwire.RunEvent{
			Type:   runnerwire.EventToolCall,
			Tool:   msg.ToolName,
			ToolID: msg.ToolID,
			Input:  clip(compactJSON(msg.Parameters)),
		})
	case "tool_result":
		text := msg.Output
		isError := msg.Status == "error"
		if reason := geminiErrorText(msg.Error); reason != "" {
			isError = true
			if text == "" {
				text = reason
			}
		}
		events = append(events, runnerwire.RunEvent{
			Type:    runnerwire.EventToolResult,
			ToolID:  msg.ToolID,
			Text:    clip(text),
			IsError: isError,
		})
	case "error":
		text := strings.TrimSpace(msg.Message)
		if text == "" {
			break
		}
		if msg.Severity == "warning" {
			events = append(events, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: clip("gemini: " + text)})
			break
		}
		p.lastError = text
		events = append(events, runnerwire.RunEvent{Type: runnerwire.EventError, Text: clip(text)})
	case "result":
		events = append(events, p.result(msg)...)
	}
	// message from the user (the prompt echoed back): nothing to show.
	return events
}

func (p *geminiParser) result(msg geminiLine) []runnerwire.RunEvent {
	p.outcome.Done = true
	var events []runnerwire.RunEvent
	if msg.Stats != nil {
		// Gemini's input_tokens include the cached ones; input here is the
		// uncached part, as for the other agents.
		usage := &runnerwire.Usage{
			InputTokens:     max(msg.Stats.InputTokens-msg.Stats.Cached, 0),
			OutputTokens:    msg.Stats.OutputTokens,
			CacheReadTokens: msg.Stats.Cached,
			Model:           p.model,
		}
		p.outcome.Usage = usage
		copied := *usage
		events = append(events, runnerwire.RunEvent{Type: runnerwire.EventUsage, Usage: &copied})
	}
	if msg.Status != "" && msg.Status != "success" {
		reason := geminiErrorText(msg.Error)
		if reason == "" {
			reason = p.lastError
		}
		if reason == "" {
			reason = "gemini reported an error (" + msg.Status + ")"
		}
		p.outcome.IsError = true
		p.outcome.Error = Truncate(reason, runnerwire.MaxSummaryBytes)
		if reason != p.lastError {
			events = append(events, runnerwire.RunEvent{Type: runnerwire.EventError, Text: clip(reason)})
		}
	}
	return events
}

// geminiErrorText reads an error field: {"type": …, "message": …} or a string.
func geminiErrorText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var e struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return ""
	}
	if e.Message == "" {
		return e.Type
	}
	return e.Message
}
