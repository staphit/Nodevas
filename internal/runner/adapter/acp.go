package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"nodevas/internal/runnerwire"
)

// ACP (Agent Client Protocol, agentclientprotocol.com) is JSON-RPC 2.0 over
// the agent's stdio, one message per line. The runner is the client: it
// opens a session in the worktree, sends the prompt as one turn, answers the
// agent's requests, and closes stdin when the turn is over, which ends the
// agent. Checked against opencode 1.18.23's `opencode acp` (protocol version
// 1; testdata/opencode_acp_*.jsonl).
//
// What ACP adds over a CLI's own output format: one event shape for every
// agent that speaks it, and session/request_permission, which asks the
// runner instead of rejecting the call and ending the run the way a headless
// CLI does. Without permissionRelay the runner answers every such request
// with a rejection (acpDecide); the agent gets the refusal as the tool's
// result and goes on. With it, the request is parked and asked about in
// Nodevas (acpSession.relay), and answered when a person has. The CLI's own
// permission rules stay the boundary: what they deny is never offered to the
// model and never asked about.

// Conversational is an adapter whose CLI is spoken to over stdin while it
// runs, instead of handed its prompt up front.
type Conversational interface {
	// NewConversation returns the run's parser, which also writes to the
	// CLI's stdin. The runner leaves cmd.Stdin unset for it.
	NewConversation(job runnerwire.Job, env RunEnv) Conversation
}

// Conversation is a Parser that talks back.
type Conversation interface {
	Parser
	// Begin is called once, after the process started, with its stdin. The
	// conversation closes stdin when it is over.
	Begin(stdin io.WriteCloser)
}

// acpProtocolVersion is the ACP version the runner speaks.
const acpProtocolVersion = 1

// acpQueue bounds the messages waiting to be written to the agent's stdin.
const acpQueue = 64

// JSON-RPC error codes the runner answers with.
const (
	rpcMethodNotFound = -32601
)

// acpMCPServer is a stdio MCP server as session/new takes it.
type acpMCPServer struct {
	Name    string      `json:"name"`
	Command string      `json:"command"`
	Args    []string    `json:"args"`
	Env     []acpEnvVar `json:"env"`
}

type acpEnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// acpSession is one run's conversation: initialize, session/new, one
// session/prompt, then stdin is closed.
type acpSession struct {
	agent  string
	cwd    string
	prompt string
	mcp    []acpMCPServer

	mu      sync.Mutex
	out     chan []byte
	closed  bool
	nextID  int
	pending map[int]string // request id → method
	session string

	outcome   Outcome
	lastText  string
	lastError string
	usage     runnerwire.Usage
	sawUsage  bool

	// The message or thought being streamed, a chunk at a time; it becomes
	// one event when something else arrives.
	chunkKind string // runnerwire.EventText or EventThinking
	chunkID   string
	chunk     strings.Builder

	tools map[string]*acpTool

	// asker answers permission requests (permissionRelay); nil rejects them.
	asker PermissionAsker
	// ctx ends with the session: a person's answer no longer matters then.
	ctx    context.Context
	cancel context.CancelFunc
	// deferred are events from answers that arrived between lines; the next
	// Parse returns them first.
	deferred []runnerwire.RunEvent
}

// acpTool is what the runner knows of one tool call so far.
type acpTool struct {
	name    string
	input   json.RawMessage
	emitted bool
}

func newACPSession(agent, cwd, prompt string, mcp []acpMCPServer) *acpSession {
	if mcp == nil {
		mcp = []acpMCPServer{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &acpSession{agent: agent, cwd: cwd, prompt: prompt, mcp: mcp,
		pending: map[int]string{}, tools: map[string]*acpTool{}, ctx: ctx, cancel: cancel}
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// text is the error as the run reports it: the agent's error name, when it
// gives one, then the message ("APIError: Internal error: Cannot connect").
func (e *rpcError) text() string {
	var data struct {
		ErrorName string `json:"errorName"`
		Message   string `json:"message"`
	}
	_ = json.Unmarshal(e.Data, &data)
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = strings.TrimSpace(data.Message)
	}
	if data.ErrorName != "" && !strings.Contains(message, data.ErrorName) {
		message = data.ErrorName + ": " + message
	}
	return message
}

func (s *acpSession) Begin(stdin io.WriteCloser) {
	s.mu.Lock()
	s.out = make(chan []byte, acpQueue)
	out := s.out
	s.request("initialize", map[string]any{
		"protocolVersion": acpProtocolVersion,
		// No file system or terminal of the runner's: the agent uses its own
		// tools, under its own permission rules, in the worktree.
		"clientCapabilities": map[string]any{
			"fs":       map[string]bool{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
	})
	s.mu.Unlock()
	go func() {
		defer stdin.Close()
		for msg := range out {
			if _, err := stdin.Write(msg); err != nil {
				// The agent is gone; drain so nothing blocks on the queue.
				for range out {
				}
				return
			}
		}
	}()
}

// send queues one message for stdin; s.mu is held.
func (s *acpSession) send(msg any) {
	if s.closed || s.out == nil {
		return
	}
	line, err := json.Marshal(msg)
	if err != nil {
		return
	}
	s.out <- append(line, '\n')
}

// request sends a request of the runner's; s.mu is held.
func (s *acpSession) request(method string, params any) {
	id := s.nextID
	s.nextID++
	s.pending[id] = method
	s.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
}

// End is the runner's word that the agent has exited: permission requests
// still waiting for a person stop waiting, and stdin is closed.
func (s *acpSession) End() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finish()
}

// finish closes stdin, which ends the agent; s.mu is held.
func (s *acpSession) finish() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.closed || s.out == nil {
		s.closed = true
		return
	}
	s.closed = true
	close(s.out)
}

func (s *acpSession) Parse(line []byte) []runnerwire.RunEvent {
	line = bytes.TrimSpace(bytes.TrimPrefix(line, utf8BOM))
	if len(line) == 0 {
		return nil
	}
	var msg rpcMessage
	if line[0] != '{' || json.Unmarshal(line, &msg) != nil || msg.JSONRPC != "2.0" {
		// Not protocol: something the agent printed to stdout. Worth seeing.
		return []runnerwire.RunEvent{{Type: runnerwire.EventLog, Text: clip(string(line))}}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	events := s.deferred
	s.deferred = nil
	switch {
	case msg.Method != "" && len(msg.ID) > 0:
		return append(events, s.agentRequest(msg)...)
	case msg.Method != "":
		if msg.Method == "session/update" {
			return append(events, s.update(msg.Params)...)
		}
	case len(msg.ID) > 0:
		return append(events, s.response(msg)...)
	}
	return events
}

// response is the answer to one of the runner's requests.
func (s *acpSession) response(msg rpcMessage) []runnerwire.RunEvent {
	var id int
	if json.Unmarshal(msg.ID, &id) != nil {
		return nil
	}
	method, ok := s.pending[id]
	if !ok {
		return nil
	}
	delete(s.pending, id)
	if msg.Error != nil {
		events := s.flushChunk()
		reason := msg.Error.text()
		if reason == "" {
			reason = s.agent + " refused " + method
		}
		s.lastError = reason
		s.outcome.Done = method == "session/prompt"
		s.finish()
		return append(events, runnerwire.RunEvent{Type: runnerwire.EventError, Text: clip(reason)})
	}
	switch method {
	case "initialize":
		var r struct {
			ProtocolVersion int `json:"protocolVersion"`
			AgentInfo       struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"agentInfo"`
		}
		_ = json.Unmarshal(msg.Result, &r)
		if r.ProtocolVersion != acpProtocolVersion {
			s.lastError = s.agent + " speaks ACP version " + strconv.Itoa(r.ProtocolVersion) + "; the runner speaks " + strconv.Itoa(acpProtocolVersion)
			s.finish()
			return []runnerwire.RunEvent{{Type: runnerwire.EventError, Text: s.lastError}}
		}
		s.request("session/new", map[string]any{"cwd": s.cwd, "mcpServers": s.mcp})
		text := "ACP: " + s.agent
		if r.AgentInfo.Version != "" {
			text += " " + r.AgentInfo.Version
		}
		return []runnerwire.RunEvent{{Type: runnerwire.EventLog, Text: text + ", protocol version " + strconv.Itoa(r.ProtocolVersion)}}
	case "session/new":
		var r struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(msg.Result, &r)
		if r.SessionID == "" {
			s.lastError = s.agent + " opened no session"
			s.finish()
			return []runnerwire.RunEvent{{Type: runnerwire.EventError, Text: s.lastError}}
		}
		s.session = r.SessionID
		s.request("session/prompt", map[string]any{"sessionId": s.session,
			"prompt": []map[string]string{{"type": "text", "text": s.prompt}}})
		return []runnerwire.RunEvent{{Type: runnerwire.EventLog, Text: s.agent + " session started " + r.SessionID}}
	case "session/prompt":
		return s.promptDone(msg.Result)
	}
	return nil
}

// promptDone is the turn's end: its stop reason and the tokens it used.
func (s *acpSession) promptDone(result json.RawMessage) []runnerwire.RunEvent {
	events := s.flushChunk()
	var r struct {
		StopReason string `json:"stopReason"`
		Usage      *struct {
			InputTokens       int64 `json:"inputTokens"`
			OutputTokens      int64 `json:"outputTokens"`
			ThoughtTokens     int64 `json:"thoughtTokens"`
			CachedReadTokens  int64 `json:"cachedReadTokens"`
			CachedWriteTokens int64 `json:"cachedWriteTokens"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(result, &r)
	if u := r.Usage; u != nil {
		// inputTokens leaves out the cached part, as Claude Code reports it;
		// thought tokens are output the model was billed for.
		s.sawUsage = true
		s.usage.InputTokens = u.InputTokens
		s.usage.OutputTokens = u.OutputTokens + u.ThoughtTokens
		s.usage.CacheReadTokens = u.CachedReadTokens
		s.usage.CacheWriteTokens = u.CachedWriteTokens
		usage := s.usage
		events = append(events, runnerwire.RunEvent{Type: runnerwire.EventUsage, Usage: &usage})
	}
	s.outcome.Done = true
	switch r.StopReason {
	case "end_turn":
		s.lastError = ""
	case "cancelled":
		s.lastError = "the turn was cancelled"
	case "":
		s.lastError = s.agent + " ended the turn without a stop reason"
	default:
		// max_tokens, max_turn_requests, refusal.
		s.lastError = s.agent + " stopped: " + r.StopReason
	}
	if s.lastError != "" {
		events = append(events, runnerwire.RunEvent{Type: runnerwire.EventError, Text: clip(s.lastError)})
	}
	s.finish()
	return events
}

// agentRequest is a request of the agent's. The runner offered no file
// system and no terminal, so the only one it answers is a permission request.
func (s *acpSession) agentRequest(msg rpcMessage) []runnerwire.RunEvent {
	if msg.Method != "session/request_permission" {
		s.send(map[string]any{"jsonrpc": "2.0", "id": msg.ID,
			"error": map[string]any{"code": rpcMethodNotFound, "message": "the Nodevas runner does not offer " + msg.Method}})
		return nil
	}
	var p struct {
		ToolCall acpToolUpdate `json:"toolCall"`
		Options  []acpOption   `json:"options"`
	}
	_ = json.Unmarshal(msg.Params, &p)
	events := s.flushChunk()
	events = append(events, s.toolSeen(p.ToolCall, true)...)
	title := p.ToolCall.Title
	if title == "" {
		title = p.ToolCall.ToolCallID
	}
	if s.asker != nil && !s.closed {
		tool := p.ToolCall.Kind
		if t := s.tools[p.ToolCall.ToolCallID]; t != nil && t.name != "" {
			tool = t.name
		}
		if tool == "" {
			tool = title
		}
		input := title
		if hasJSON(p.ToolCall.RawInput) {
			input = compactJSON(p.ToolCall.RawInput)
		}
		// Parked: the person answers in their own time, and the agent's
		// output keeps being read meanwhile.
		go s.relay(msg.ID, p.Options, title, PermissionRequest{Tool: tool, Input: input})
		return append(events, runnerwire.RunEvent{Type: runnerwire.EventLog,
			Text: clip(s.agent + " asks to run " + title + "; waiting for an answer in Nodevas")})
	}
	s.answerPermission(msg.ID, p.Options, acpDecide(p.ToolCall))
	return append(events, runnerwire.RunEvent{Type: runnerwire.EventLog,
		Text: clip(s.agent + " asked to run " + title + "; the runner refused (headless: nobody to ask)")})
}

// acpOption is one of a permission request's choices.
type acpOption struct {
	OptionID string `json:"optionId"`
	Kind     string `json:"kind"`
}

// The hub's runScope values (agents.RunScope*) the relay acts on: an
// approve_run of the whole tool, or of this one shell command.
const (
	RunScopeTool    = "tool"
	RunScopeCommand = "command"
)

// relay asks the person about a parked permission request and answers it:
// allow_once, allow_always for a tool allowed for the rest of the run, or
// reject_once, which is also the answer to no answer at all. allow_always
// has OpenCode remember a rule of its own for the tool, so it is sent only
// for the hub's whole-tool scope (RunScopeTool); an approval for one
// command, for this call, or of a scope the hub did not name is allow_once,
// and the hub's own run grant answers the next matching ask. Once the
// session is over nothing is sent.
func (s *acpSession) relay(id json.RawMessage, options []acpOption, title string, req PermissionRequest) {
	decision := s.asker.AskPermission(s.ctx, req)
	kind, text := "reject_once", "denied in Nodevas"
	if decision.Allow {
		kind, text = "allow_once", "allowed in Nodevas"
		switch {
		case decision.ForRun && decision.RunScope == RunScopeTool:
			kind, text = "allow_always", "allowed in Nodevas for the rest of the run"
		case decision.ForRun && decision.RunScope == RunScopeCommand:
			text = "allowed in Nodevas, this command for the rest of the run"
		}
	} else if decision.Reason != "" {
		text += ": " + decision.Reason
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if kind == "allow_always" && !hasOption(options, kind) {
		kind = "allow_once"
	}
	s.answerPermission(id, options, kind)
	s.deferred = append(s.deferred, runnerwire.RunEvent{Type: runnerwire.EventLog,
		Text: clip(s.agent + " asked to run " + title + "; " + text)})
}

// answerPermission answers a permission request with the option of kind;
// s.mu is held. No such option: cancelled, the answer that grants nothing.
func (s *acpSession) answerPermission(id json.RawMessage, options []acpOption, kind string) {
	optionID := ""
	for _, o := range options {
		if o.Kind == kind {
			optionID = o.OptionID
			break
		}
	}
	if optionID == "" {
		s.send(map[string]any{"jsonrpc": "2.0", "id": id,
			"result": map[string]any{"outcome": map[string]string{"outcome": "cancelled"}}})
		return
	}
	s.send(map[string]any{"jsonrpc": "2.0", "id": id,
		"result": map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": optionID}}})
}

func hasOption(options []acpOption, kind string) bool {
	for _, o := range options {
		if o.Kind == kind {
			return true
		}
	}
	return false
}

// acpDecide is the runner's answer to a permission request it does not
// relay: the kind of the option it picks. Always a one-off rejection; the
// agent's own rules already allow what the owner's setting allows without
// asking.
func acpDecide(acpToolUpdate) string { return "reject_once" }

type acpToolUpdate struct {
	ToolCallID string          `json:"toolCallId"`
	Title      string          `json:"title"`
	Kind       string          `json:"kind"`
	Status     string          `json:"status"`
	RawInput   json.RawMessage `json:"rawInput"`
	RawOutput  json.RawMessage `json:"rawOutput"`
	Content    []struct {
		Type    string `json:"type"`
		Content struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"content"`
}

// update is a session/update notification.
func (s *acpSession) update(params json.RawMessage) []runnerwire.RunEvent {
	var p struct {
		Update json.RawMessage `json:"update"`
	}
	if json.Unmarshal(params, &p) != nil {
		return nil
	}
	var head struct {
		SessionUpdate string          `json:"sessionUpdate"`
		MessageID     string          `json:"messageId"`
		Content       json.RawMessage `json:"content"`
		Entries       []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"entries"`
		Cost *struct {
			Amount   float64 `json:"amount"`
			Currency string  `json:"currency"`
		} `json:"cost"`
	}
	if json.Unmarshal(p.Update, &head) != nil {
		return nil
	}
	switch head.SessionUpdate {
	case "agent_message_chunk", "agent_thought_chunk":
		kind := runnerwire.EventText
		if head.SessionUpdate == "agent_thought_chunk" {
			kind = runnerwire.EventThinking
		}
		var events []runnerwire.RunEvent
		if kind != s.chunkKind || head.MessageID != s.chunkID {
			events = s.flushChunk()
		}
		s.chunkKind, s.chunkID = kind, head.MessageID
		var content struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(head.Content, &content) == nil && content.Type == "text" {
			s.chunk.WriteString(content.Text)
		}
		return events
	case "tool_call", "tool_call_update":
		var u acpToolUpdate
		if json.Unmarshal(p.Update, &u) != nil {
			return nil
		}
		return append(s.flushChunk(), s.toolSeen(u, false)...)
	case "plan":
		var b strings.Builder
		b.WriteString("plan:")
		for _, e := range head.Entries {
			fmt.Fprintf(&b, "\n- [%s] %s", e.Status, e.Content)
		}
		return append(s.flushChunk(), runnerwire.RunEvent{Type: runnerwire.EventLog, Text: clip(b.String())})
	case "usage_update":
		// The session's cost so far; the tokens come with the turn's end.
		if head.Cost != nil && head.Cost.Currency == "USD" {
			s.sawUsage = true
			s.usage.CostUSD = head.Cost.Amount
		}
	}
	// available_commands_update, current_mode_update, config_option_update,
	// user_message_chunk: nothing to show.
	return nil
}

// flushChunk turns the streamed message or thought into one event; s.mu is
// held.
func (s *acpSession) flushChunk() []runnerwire.RunEvent {
	text := s.chunk.String()
	kind := s.chunkKind
	s.chunk.Reset()
	s.chunkKind, s.chunkID = "", ""
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if kind == runnerwire.EventText {
		s.lastText = text
	}
	return []runnerwire.RunEvent{{Type: kind, Text: clip(text)}}
}

// toolSeen records a tool call or an update to one: the call is shown once
// it is under way (while pending its input is empty or partial) or the agent
// asks about it, the result once it has finished. ask is a permission
// request's tool call.
func (s *acpSession) toolSeen(u acpToolUpdate, ask bool) []runnerwire.RunEvent {
	if u.ToolCallID == "" {
		return nil
	}
	t := s.tools[u.ToolCallID]
	if t == nil {
		// The first title is the tool's name ("read", "bash"); later ones
		// describe the call ("README.md", "git status").
		t = &acpTool{name: acpToolName(u.Title)}
		s.tools[u.ToolCallID] = t
	}
	if hasJSON(u.RawInput) {
		t.input = u.RawInput
	}
	var events []runnerwire.RunEvent
	final := u.Status == "completed" || u.Status == "failed" || u.Status == "cancelled"
	if !t.emitted && ((u.Status != "" && u.Status != "pending") || ask) {
		t.emitted = true
		input := "{}"
		if hasJSON(t.input) {
			input = compactJSON(t.input)
		}
		events = append(events, runnerwire.RunEvent{Type: runnerwire.EventToolCall, Tool: t.name,
			ToolID: u.ToolCallID, Input: clip(input)})
	}
	if final && !ask {
		result := runnerwire.RunEvent{Type: runnerwire.EventToolResult, ToolID: u.ToolCallID,
			IsError: u.Status != "completed"}
		var parts []string
		for _, c := range u.Content {
			if c.Type == "content" && c.Content.Type == "text" {
				parts = append(parts, c.Content.Text)
			}
		}
		result.Text = strings.Join(parts, "\n")
		if result.Text == "" && hasJSON(u.RawOutput) {
			result.Text = compactJSON(u.RawOutput)
		}
		result.Text = clip(result.Text)
		events = append(events, result)
		delete(s.tools, u.ToolCallID)
	}
	return events
}

// acpToolName names a tool the way the UI and the allowlists read it: an MCP
// tool on the nodevas server as Claude Code names it.
func acpToolName(title string) string {
	if rest, ok := strings.CutPrefix(title, "nodevas_"); ok {
		return "mcp__nodevas__" + rest
	}
	return title
}

// hasJSON is raw holding a value other than null or an empty object.
func hasJSON(raw json.RawMessage) bool {
	v := string(bytes.TrimSpace(raw))
	return v != "" && v != "null" && v != "{}"
}

func (s *acpSession) Outcome() Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.outcome
	text := s.lastText
	if s.chunkKind == runnerwire.EventText && strings.TrimSpace(s.chunk.String()) != "" {
		// The stream ended mid-message.
		text = s.chunk.String()
	}
	out.Summary = Truncate(text, runnerwire.MaxSummaryBytes)
	out.Text = Truncate(text, MaxTextBytes)
	if s.sawUsage {
		usage := s.usage
		out.Usage = &usage
	}
	if s.lastError != "" {
		out.IsError = true
		out.Error = Truncate(s.lastError, runnerwire.MaxSummaryBytes)
	} else if !out.Done {
		out.IsError = true
		out.Error = s.agent + " ended before its turn did"
	}
	return out
}
