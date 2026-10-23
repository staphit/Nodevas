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
	"os"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"

	"nodevas/internal/agents"
	"nodevas/internal/agentsetup"
	"nodevas/internal/mcp"
	"nodevas/internal/runnerwire"
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
	// ExclusionFile overrides where the folder exclusion list is read from
	// (tests); empty is agentsetup.NetworkConfigPath().
	ExclusionFile string
}

// Input is the part of an agent's hook payload the hook reads.
type Input struct {
	SessionID      string
	Cwd            string
	StopHookActive bool
	// Model is the model the agent says it runs on, when its payload says.
	Model string
	// ToolName and Files (W6): on post-tool-use, the tool that ran and the
	// files it wrote, as its input names them (see EditedFiles).
	ToolName string
	Files    []string
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
	// Claude Code names the model as a string on some events and as
	// {"id": …, "display_name": …} on others.
	switch model := v["model"].(type) {
	case string:
		in.Model = strings.TrimSpace(model)
	case map[string]any:
		if id, ok := model["id"].(string); ok {
			in.Model = strings.TrimSpace(id)
		}
	}
	if len(in.Model) > agents.MaxModelLen {
		in.Model = ""
	}
	for _, k := range []string{"session_id", "sessionId", "conversation_id"} {
		if s := str(k); s != "" {
			in.SessionID = s
			break
		}
	}
	in.StopHookActive, _ = v["stop_hook_active"].(bool)
	in.ToolName = str("tool_name")
	if in.ToolName != "" {
		in.Files = EditedFiles(in.ToolName, toolInputMap(v["tool_input"]))
	}
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

	// A session in an excluded folder is not on the network: nothing is
	// reported, nothing printed, not even that it is excluded. The agent's
	// own payload says where it runs; the hook's cwd is the fallback.
	cwd := in.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if cwd != "" {
		if folder, _ := agentsetup.ExcludedFolder(opts.ExclusionFile, cwd); folder != "" {
			return ""
		}
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
		Model:      in.Model,
		WorkDir:    cwd,
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
	case EventSessionStart:
		state = agents.StateIdle
	}
	if err := client.Heartbeat(ctx, state, "", ""); err != nil {
		return ""
	}
	reportChanges(ctx, client, event, in)
	if event == EventStop {
		// Idle now, still holding the node it holds: a turn ending is not the
		// claim ending, and the next turn's edits belong to the same node.
		if err := client.Heartbeat(ctx, agents.StateIdle, client.ClaimedNode(), ""); err != nil {
			return ""
		}
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

// reportChanges sends the hub what this event changed, if the session holds a
// node (as the heartbeat just answered): the files an edit tool wrote, or at
// Stop a snapshot of the working tree. Failures are dropped.
func reportChanges(ctx context.Context, client *mcp.Client, event string, in Input) {
	if client.ClaimedNode() == "" || in.Cwd == "" || excluded(in.Cwd) {
		return
	}
	switch event {
	case EventPostToolUse:
		if len(in.Files) == 0 {
			return
		}
		files := RepoRelative(ctx, in.Cwd, in.Files)
		if len(files) > runnerwire.MaxSessionFiles {
			files = files[:runnerwire.MaxSessionFiles]
		}
		_, _ = client.RecordSessionChanges(ctx, files, nil)
	case EventStop:
		// Leave time in the hook's budget for the idle heartbeat after it.
		snapCtx, cancel := context.WithTimeout(ctx, gitTimeout)
		snapshot, ok := Snapshot(snapCtx, in.Cwd)
		cancel()
		if ok {
			_, _ = client.RecordSessionChanges(ctx, nil, &snapshot)
		}
	}
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
