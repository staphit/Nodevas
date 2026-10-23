// Package agenthook runs `nodevas hook <agent> <event>`: the command an agent's
// own hook system (Claude Code, Codex, Gemini CLI) calls at session start, on
// each prompt, after tool calls, when a turn stops, and at session end.
//
// It tells the Nodevas server the session is alive and busy or idle, and prints
// whatever makes the agent show mail waiting for it. It never fails loudly: a
// Nodevas server that is down or misconfigured must not break the agent, so
// every error ends in silence and a zero exit.
package agenthook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"

	"nodevas/internal/agents"
	"nodevas/internal/mcp"
)

// Events the hook understands, as named on the command line.
const (
	EventSessionStart     = "session-start"
	EventUserPromptSubmit = "user-prompt-submit"
	EventPostToolUse      = "post-tool-use"
	EventStop             = "stop"
	EventSessionEnd       = "session-end"
)

// StdinLimit is how long the hook waits for the agent's JSON payload. An agent
// that never closes stdin must not hang the hook.
const StdinLimit = 3 * time.Second

// Timeout bounds a whole hook run for event. post-tool-use runs after every
// tool call, so it gets the shortest leash.
func Timeout(event string) time.Duration {
	if event == EventPostToolUse {
		return 3 * time.Second
	}
	return 8 * time.Second
}

// Options configure one hook run.
type Options struct {
	Server  string
	Project string
	Actor   string
	// HTTPClient overrides the transport (tests); nil uses a loopback client.
	HTTPClient *http.Client
	// Windows forces (or, false, suppresses) ASCII-only JSON output. Nil
	// follows runtime.GOOS.
	Windows *bool
}

// Input is the part of an agent's hook payload the hook reads.
type Input struct {
	SessionID      string
	Cwd            string
	StopHookActive bool
}

// ParseInput reads an agent's hook payload leniently: a byte-order mark
// (Windows PowerShell pipes one in) is dropped, and unparseable input yields
// an empty Input rather than an error.
func ParseInput(raw []byte) Input {
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	var v map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &v); err != nil {
		return Input{}
	}
	str := func(k string) string {
		s, _ := v[k].(string)
		return strings.TrimSpace(s)
	}
	in := Input{Cwd: str("cwd")}
	for _, k := range []string{"session_id", "sessionId", "conversation_id"} {
		if s := str(k); s != "" {
			in.SessionID = s
			break
		}
	}
	in.StopHookActive, _ = v["stop_hook_active"].(bool)
	return in
}

// ReadStdin reads r until EOF or limit, whichever comes first.
func ReadStdin(r io.Reader, limit time.Duration) []byte {
	if r == nil {
		return nil
	}
	done := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(io.LimitReader(r, 1<<20))
		done <- b
	}()
	select {
	case b := <-done:
		return b
	case <-time.After(limit):
		return nil
	}
}

// Run handles one hook invocation and returns what to print on stdout (empty
// for nothing). Errors are swallowed by design.
func Run(ctx context.Context, agent, event string, in Input, opts Options) string {
	agent = strings.ToLower(strings.TrimSpace(agent))
	if in.SessionID == "" || agent == "" {
		return ""
	}
	switch event {
	case EventSessionStart, EventUserPromptSubmit, EventPostToolUse, EventStop, EventSessionEnd:
	default:
		return ""
	}

	actor := strings.TrimSpace(opts.Actor)
	if actor == "" {
		actor = mcp.DefaultActor
	}
	clientOpts := mcp.ClientOptions{
		Server:     opts.Server,
		Project:    opts.Project,
		Actor:      actor,
		SessionID:  agent + ":" + in.SessionID,
		AgentKind:  agent,
		HTTPClient: opts.HTTPClient,
		Timeout:    Timeout(event),
	}
	if in.Cwd != "" {
		clientOpts.Repo, clientOpts.Branch = mcp.GitHead(in.Cwd)
	}
	client, err := mcp.NewClient(clientOpts)
	if err != nil {
		return ""
	}

	if event == EventSessionEnd {
		_ = client.Leave(ctx)
		return ""
	}

	state := ""
	switch event {
	case EventUserPromptSubmit:
		state = agents.StateBusy
	case EventSessionStart, EventStop:
		state = agents.StateIdle
	}
	if err := client.Heartbeat(ctx, state, "", ""); err != nil {
		return ""
	}

	// A stop hook that already blocked once is running again because of that
	// block; blocking a second time would keep the agent from ever stopping.
	// Mail left now is picked up on the next prompt.
	if event == EventStop && in.StopHookActive {
		return ""
	}

	text := ""
	if client.UnreadCount() > 0 {
		inbox, err := client.CheckInbox(ctx, 0)
		if err == nil && len(inbox.Messages) > 0 {
			text = fmt.Sprintf("Nodevas agent network: %d new message(s).\n\n%s",
				len(inbox.Messages), inbox.Formatted)
		}
	}
	if text == "" && event == EventSessionStart {
		text = fmt.Sprintf("Nodevas agent network: you are %s/%s. list_team / send_message / check_inbox reach teammates.",
			strings.TrimPrefix(actor, "mcp:"), client.SessionName())
	}
	if text == "" {
		return ""
	}
	windows := runtime.GOOS == "windows"
	if opts.Windows != nil {
		windows = *opts.Windows
	}
	return Format(agent, event, text, windows)
}

// Format renders text as the JSON agent expects from this hook event to put it
// in front of the model. Stop hooks block the stop with text as the reason, so
// the agent takes another turn to read its mail. With asciiOnly, every
// non-ASCII character is escaped: agents on Windows run hooks through
// PowerShell, which can re-encode what passes through it.
func Format(agent, event, text string, asciiOnly bool) string {
	var out any
	if event == EventStop {
		out = map[string]any{"decision": "block", "reason": text}
	} else {
		name := hookEventName(agent, event)
		if name == "" {
			return ""
		}
		out = map[string]any{"hookSpecificOutput": map[string]any{
			"hookEventName":     name,
			"additionalContext": text,
		}}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return ""
	}
	s := strings.TrimSuffix(buf.String(), "\n")
	if asciiOnly {
		s = EscapeNonASCII(s)
	}
	return s
}

func hookEventName(agent, event string) string {
	if agent == "gemini" {
		switch event {
		case EventSessionStart:
			return "SessionStart"
		case EventUserPromptSubmit:
			return "BeforeAgent"
		case EventPostToolUse:
			return "AfterTool"
		}
		return ""
	}
	// Claude Code, Codex, and anything unknown share Claude Code's names.
	switch event {
	case EventSessionStart:
		return "SessionStart"
	case EventUserPromptSubmit:
		return "UserPromptSubmit"
	case EventPostToolUse:
		return "PostToolUse"
	}
	return ""
}

// EscapeNonASCII rewrites every non-ASCII rune of a JSON document as a \uXXXX
// escape (a surrogate pair above the BMP). Valid in any JSON string, and
// immune to a shell re-encoding the bytes.
func EscapeNonASCII(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x80 {
			b.WriteRune(r)
			continue
		}
		for _, u := range utf16.Encode([]rune{r}) {
			fmt.Fprintf(&b, `\u%04x`, u)
		}
	}
	return b.String()
}
