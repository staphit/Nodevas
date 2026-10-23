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

// Codex runs the OpenAI Codex CLI headless:
//
//	codex exec --json --color never -s <sandbox> -C <worktree> [-m <model>] [--ignore-user-config]
//	           -c approval_policy='never'
//	           -c mcp_servers.nodevas.command='<nodevas>'
//	           -c mcp_servers.nodevas.args=['mcp',…]
//	           -c mcp_servers.nodevas.default_tools_approval_mode='approve'
//	           -c mcp_servers.nodevas.tool_timeout_sec=900
//	           -
//
// with the prompt on stdin ("-"). Checked against codex-cli 0.156.0's
// `codex exec --help`; the config keys and their values were checked against
// the CLI's own error messages for bad values.
//
// The MCP server goes in as -c overrides rather than a file: codex reads
// config files only from CODEX_HOME, where its login lives too, so a private
// CODEX_HOME would cost the owner's sign-in. The overrides are TOML literal
// strings ('…'), which need no double quotes; on Windows, where codex is a
// .cmd shim, wrapBatch starts it through an explicit cmd.exe command line
// (see shim.go).
//
// The owner's config.toml still loads unless IgnoreUserConfig is set: it
// carries their model, provider and sign-in method, which runs should share.
// It also carries their own MCP servers and hooks; IgnoreUserConfig is for
// owners who want runs without those.
type Codex struct{}

// CodexSandboxModes are --sandbox's choices, exactly as the CLI lists them.
var CodexSandboxModes = []string{"read-only", "workspace-write", "danger-full-access"}

// DefaultCodexSandbox lets the agent write in its worktree and nowhere else.
const DefaultCodexSandbox = "workspace-write"

// codexToolTimeoutSec bounds one nodevas MCP tool call. ask_user waits up to
// ten minutes for an answer; codex's own default would cut it off.
const codexToolTimeoutSec = 900

func (Codex) Kind() string { return "codex" }

func (Codex) NewParser() Parser { return &codexParser{} }

func (Codex) Command(job runnerwire.Job, env RunEnv) (*exec.Cmd, error) {
	if env.Binary == "" || env.Workdir == "" {
		return nil, fmt.Errorf("codex: binary and workdir are required")
	}
	sandbox := env.PermissionMode
	if sandbox == "" {
		sandbox = DefaultCodexSandbox
	}
	args := []string{
		"exec",
		"--json",
		// stderr is only read to explain a failure; no escape codes in it.
		"--color", "never",
		"-s", sandbox,
		"-C", env.Workdir,
	}
	if job.Model != "" && job.Model != "default" {
		args = append(args, "-m", job.Model)
	}
	if env.IgnoreUserConfig {
		args = append(args, "--ignore-user-config")
	}
	args = append(args,
		// Headless, nobody can approve anything: a command the sandbox does
		// not allow fails and the model sees that, instead of the run hanging.
		"-c", "approval_policy='never'",
		"-c", "mcp_servers.nodevas.command="+tomlString(env.MCP.Command),
		"-c", "mcp_servers.nodevas.args="+tomlArray(env.MCP.Args),
		// The nodevas tools need no approval: the preamble tells the agent to
		// claim its node, and the hub limits those tools to the agent routes.
		"-c", "mcp_servers.nodevas.default_tools_approval_mode='approve'",
		"-c", "mcp_servers.nodevas.tool_timeout_sec="+strconv.Itoa(codexToolTimeoutSec),
		// The prompt is read from stdin.
		"-",
	)
	cmd := exec.Command(env.Binary, args...)
	cmd.Dir = env.Workdir
	cmd.Stdin = strings.NewReader(Prompt(job, env.Branch))
	cmd.Env = childEnv()
	if err := wrapBatch(cmd); err != nil {
		return nil, fmt.Errorf("codex: %w", err)
	}
	return cmd, nil
}

// codexParser reads `codex exec --json`: one JSON object per line, typed
// thread.started / turn.started / turn.completed / turn.failed / error, and
// item.started / item.updated / item.completed around a typed item.
// The fixtures in testdata are real output from codex-cli 0.156.0.
type codexParser struct {
	outcome Outcome
	// started holds the IDs of tool items whose tool_call went out at
	// item.started, so item.completed adds only the result.
	started map[string]bool
	// lastMessage is the latest agent message: the run's summary.
	lastMessage string
	// lastError is the latest top-level error, the failure's reason when the
	// stream ends without turn.failed.
	lastError string
	usage     runnerwire.Usage
	sawUsage  bool
}

type codexLine struct {
	Type     string          `json:"type"`
	ThreadID string          `json:"thread_id"`
	Message  string          `json:"message"`
	Item     *codexItem      `json:"item"`
	Usage    *codexUsage     `json:"usage"`
	Error    json.RawMessage `json:"error"`
}

type codexUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
}

type codexItem struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	// agent_message, reasoning
	Text string `json:"text"`
	// error
	Message string `json:"message"`
	// command_execution
	Command          string `json:"command"`
	AggregatedOutput string `json:"aggregated_output"`
	ExitCode         *int   `json:"exit_code"`
	Status           string `json:"status"`
	// file_change
	Changes []struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	} `json:"changes"`
	// mcp_tool_call
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Result    *struct {
		Content           json.RawMessage `json:"content"`
		StructuredContent json.RawMessage `json:"structured_content"`
	} `json:"result"`
	CallError *struct {
		Message string `json:"message"`
	} `json:"error"`
	// web_search
	Query string `json:"query"`
	// todo_list
	Items []struct {
		Text      string `json:"text"`
		Completed bool   `json:"completed"`
	} `json:"items"`
}

func (p *codexParser) Outcome() Outcome {
	out := p.outcome
	out.Summary = Truncate(p.lastMessage, runnerwire.MaxSummaryBytes)
	if p.sawUsage {
		usage := p.usage
		out.Usage = &usage
	}
	if !out.Done && p.lastError != "" {
		// The stream ended on an error without saying the turn failed.
		out.IsError = true
		out.Error = Truncate(p.lastError, runnerwire.MaxSummaryBytes)
	}
	return out
}

func (p *codexParser) Parse(line []byte) []runnerwire.RunEvent {
	line = bytes.TrimSpace(bytes.TrimPrefix(line, utf8BOM))
	if len(line) == 0 {
		return nil
	}
	var msg codexLine
	if line[0] != '{' || json.Unmarshal(line, &msg) != nil {
		return []runnerwire.RunEvent{{Type: runnerwire.EventLog, Text: clip(string(line))}}
	}
	switch msg.Type {
	case "thread.started":
		text := "codex session started"
		if msg.ThreadID != "" {
			text += " " + msg.ThreadID
		}
		return []runnerwire.RunEvent{{Type: runnerwire.EventLog, Text: text}}
	case "turn.completed":
		p.outcome.Done = true
		p.outcome.IsError, p.outcome.Error = false, ""
		if msg.Usage == nil {
			return nil
		}
		// Usage is per turn; the event carries the run's total so far. Codex
		// counts cached tokens inside input_tokens; here input is the uncached
		// part, as Claude Code reports it.
		u := msg.Usage
		p.sawUsage = true
		p.usage.InputTokens += max(u.InputTokens-u.CachedInputTokens, 0)
		p.usage.CacheReadTokens += u.CachedInputTokens
		p.usage.CacheWriteTokens += u.CacheWriteInputTokens
		p.usage.OutputTokens += u.OutputTokens
		usage := p.usage
		return []runnerwire.RunEvent{{Type: runnerwire.EventUsage, Usage: &usage}}
	case "turn.failed":
		reason := codexErrorText(msg.Error)
		if reason == "" {
			reason = p.lastError
		}
		if reason == "" {
			reason = "codex reported that the turn failed"
		}
		p.outcome.Done = true
		p.outcome.IsError = true
		p.outcome.Error = Truncate(reason, runnerwire.MaxSummaryBytes)
		if reason == p.lastError {
			return nil // already reported by the error line before it
		}
		return []runnerwire.RunEvent{{Type: runnerwire.EventError, Text: clip(reason)}}
	case "error":
		reason := readableError(msg.Message)
		if reason == "" {
			return nil
		}
		p.lastError = reason
		return []runnerwire.RunEvent{{Type: runnerwire.EventError, Text: clip(reason)}}
	case "item.started":
		if msg.Item == nil {
			return nil
		}
		if call, ok := codexToolCall(msg.Item); ok {
			if p.started == nil {
				p.started = map[string]bool{}
			}
			p.started[msg.Item.ID] = true
			return []runnerwire.RunEvent{call}
		}
	case "item.completed":
		if msg.Item == nil {
			return nil
		}
		return p.completed(msg.Item)
	}
	// turn.started, item.updated (todo progress, output so far): not steps.
	return nil
}

func (p *codexParser) completed(item *codexItem) []runnerwire.RunEvent {
	switch item.Type {
	case "agent_message":
		if strings.TrimSpace(item.Text) == "" {
			return nil
		}
		p.lastMessage = item.Text
		return []runnerwire.RunEvent{{Type: runnerwire.EventText, Text: clip(item.Text)}}
	case "reasoning":
		if strings.TrimSpace(item.Text) == "" {
			return nil
		}
		return []runnerwire.RunEvent{{Type: runnerwire.EventThinking, Text: clip(item.Text)}}
	case "todo_list":
		var b strings.Builder
		b.WriteString("plan:")
		for _, todo := range item.Items {
			mark := "[ ]"
			if todo.Completed {
				mark = "[x]"
			}
			fmt.Fprintf(&b, "\n%s %s", mark, todo.Text)
		}
		return []runnerwire.RunEvent{{Type: runnerwire.EventThinking, Text: clip(b.String())}}
	case "error":
		// A non-fatal error item: codex's warnings (an unknown config key,
		// hooks defined twice). Fatal ones come as top-level error and
		// turn.failed lines, so these are notes, not failures.
		if strings.TrimSpace(item.Message) == "" {
			return nil
		}
		return []runnerwire.RunEvent{{Type: runnerwire.EventLog, Text: clip("codex: " + item.Message)}}
	}
	result, ok := codexToolResult(item)
	if !ok {
		return nil
	}
	var events []runnerwire.RunEvent
	if !p.started[item.ID] {
		// Some items only ever arrive completed.
		if call, ok := codexToolCall(item); ok {
			events = append(events, call)
		}
	}
	delete(p.started, item.ID)
	return append(events, result)
}

// codexToolCall is the tool_call for a tool-like item: a shell command, a
// file edit, an MCP tool call, a web search.
func codexToolCall(item *codexItem) (runnerwire.RunEvent, bool) {
	ev := runnerwire.RunEvent{Type: runnerwire.EventToolCall, ToolID: item.ID}
	switch item.Type {
	case "command_execution":
		ev.Tool = "shell"
		ev.Input = item.Command
	case "file_change":
		ev.Tool = "apply_patch"
		ev.Input = codexChanges(item)
	case "mcp_tool_call":
		// Named the way Claude Code names MCP tools, so the UI and the
		// allowlists read the same for both.
		ev.Tool = "mcp__" + item.Server + "__" + item.Tool
		ev.Input = compactJSON(item.Arguments)
	case "web_search":
		ev.Tool = "web_search"
		ev.Input = item.Query
	default:
		return ev, false
	}
	ev.Input = clip(ev.Input)
	return ev, true
}

func codexToolResult(item *codexItem) (runnerwire.RunEvent, bool) {
	ev := runnerwire.RunEvent{Type: runnerwire.EventToolResult, ToolID: item.ID}
	failed := item.Status == "failed" || item.Status == "declined"
	switch item.Type {
	case "command_execution":
		ev.Text = item.AggregatedOutput
		if item.ExitCode != nil && *item.ExitCode != 0 {
			failed = true
			ev.Text = strings.TrimRight(ev.Text, "\r\n") + fmt.Sprintf("\n(exit code %d)", *item.ExitCode)
		}
		if item.Status == "declined" {
			ev.Text = strings.TrimSpace(ev.Text + "\n(declined by the sandbox policy)")
		}
	case "file_change":
		ev.Text = codexChanges(item)
	case "mcp_tool_call":
		if item.CallError != nil && item.CallError.Message != "" {
			failed = true
			ev.Text = item.CallError.Message
		} else if item.Result != nil {
			ev.Text = toolResultText(item.Result.Content)
			if ev.Text == "" {
				ev.Text = compactJSON(item.Result.StructuredContent)
			}
		}
	case "web_search":
		ev.Text = "searched: " + item.Query
	default:
		return ev, false
	}
	ev.Text = clip(ev.Text)
	ev.IsError = failed
	return ev, true
}

func codexChanges(item *codexItem) string {
	lines := make([]string, 0, len(item.Changes))
	for _, change := range item.Changes {
		lines = append(lines, change.Kind+" "+change.Path)
	}
	return strings.Join(lines, "\n")
}

// codexErrorText reads turn.failed's error: {"message": …}.
func codexErrorText(raw json.RawMessage) string {
	var e struct {
		Message string `json:"message"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &e) != nil {
		return ""
	}
	return readableError(e.Message)
}

// readableError unwraps an API error that codex passes on as a JSON string,
// {"type":"error","status":400,"error":{"message":"…"}}, to its message.
func readableError(message string) string {
	message = strings.TrimSpace(message)
	if !strings.HasPrefix(message, "{") {
		return message
	}
	var api struct {
		Status int `json:"status"`
		Error  struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(message), &api) != nil || api.Error.Message == "" {
		return message
	}
	if api.Status != 0 {
		return fmt.Sprintf("%s (HTTP %d)", api.Error.Message, api.Status)
	}
	return api.Error.Message
}
