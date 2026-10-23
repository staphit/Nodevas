package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"nodevas/internal/copilot/provider"
	"nodevas/internal/copilot/toolbridge"
	"nodevas/internal/identity"
)

// Event types on the turn stream (COPILOT.md, "Turn stream").
const (
	EventTurnStarted        = "turn_started"
	EventTextDelta          = "text_delta"
	EventToolCall           = "tool_call"
	EventPermissionRequest  = "permission_request"
	EventPermissionResolved = "permission_resolved"
	EventClientToolCall     = "client_tool_call"
	EventToolResult         = "tool_result"
	EventEntry              = "entry"
	EventError              = "error"
	EventTurnFinished       = "turn_finished"
)

// Stop reasons the loop adds to the provider's own.
const (
	StopCancelled    = "cancelled"
	StopMaxToolCalls = "max_tool_calls"
)

// Output bounds. The event copy is for a tool card in the panel; the model
// copy is what the model reasons over, capped so one huge page cannot crowd
// the rest of the conversation out of its context.
const (
	maxEventOutputBytes = 2 << 10
	maxModelOutputBytes = 32 << 10
	maxSummaryBytes     = 300
	turnMaxTokens       = 4096
)

// Event is one line of the turn stream. Only the fields its Type names are
// set.
type Event struct {
	Type      string          `json:"type"`
	TurnID    string          `json:"turnId,omitempty"`
	EntryID   string          `json:"entryId,omitempty"`
	Text      string          `json:"text,omitempty"`
	CallID    string          `json:"callId,omitempty"`
	Name      string          `json:"name,omitempty"`
	Title     string          `json:"title,omitempty"`
	Kind      toolbridge.Kind `json:"kind,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	RequestID string          `json:"requestId,omitempty"`
	Summary   string          `json:"summary,omitempty"`
	Decision  Decision        `json:"decision,omitempty"`
	Output    *string         `json:"output,omitempty"`
	IsError   *bool           `json:"isError,omitempty"`
	Entry     *Entry          `json:"entry,omitempty"`
	Message   string          `json:"message,omitempty"`
	Code      string          `json:"code,omitempty"`
	Stop      string          `json:"stop,omitempty"`
	Usage     *Usage          `json:"usage,omitempty"`
}

// Usage is the token count of a whole turn, summed over its model calls.
type Usage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}

// TurnRequest is one thing the person said.
type TurnRequest struct {
	Actor          identity.Actor
	ConversationID string
	Text           string
	// Source is "voice" or "text".
	Source string
	// Mode is the mode chosen in the panel; empty keeps the conversation's.
	Mode        Mode
	Context     TurnContext
	ClientTools []string
	// Host and Cookie come from the person's request, so the bridge's
	// in-process calls authenticate as them and pass the Host allow-list.
	Host   string
	Cookie string
	// RemoteAddr is the person's client address, so the abuse limits and the
	// audit trail see the bridge's calls as coming from them.
	RemoteAddr string
}

// Turn runs one turn: it stores what the person said, lets the model answer
// and use tools under the conversation's permission mode, and reports every
// step through emit, in order, from the calling goroutine.
//
// An error is returned only when the turn could not start, before anything
// was emitted, so the caller can still answer with an HTTP status. Once the
// stream has begun, failures arrive as an EventError and Turn returns nil.
func (s *Service) Turn(ctx context.Context, req TurnRequest, emit func(Event)) error {
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return fmt.Errorf("%w: text is required", ErrInvalid)
	}
	if len(text) > maxTurnTextBytes || !utf8.ValidString(text) {
		return fmt.Errorf("%w: text is too long or not UTF-8", ErrInvalid)
	}
	switch req.Source {
	case "":
		req.Source = "text"
	case "text", "voice":
	default:
		return fmt.Errorf("%w: source must be voice or text", ErrInvalid)
	}
	if req.Mode != "" && !req.Mode.Valid() {
		return fmt.Errorf("%w: unknown mode %q", ErrInvalid, req.Mode)
	}

	conversation, err := s.store.Conversation(ctx, req.Actor.ID, req.ConversationID)
	if err != nil {
		return err
	}
	settings, key, _, err := s.settings.resolve(ctx)
	if err != nil {
		return err
	}
	if reason := s.unavailableReason(settings, key); reason != "" {
		return fmt.Errorf("%w: %s", ErrUnavailable, reason)
	}
	model, err := s.opts.NewProvider(provider.Config{
		Kind: settings.Provider, Model: settings.Model, BaseURL: settings.BaseURL,
		APIKey: key, Effort: settings.Effort,
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}

	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	turn := &activeTurn{
		id: newID(), conversationID: conversation.ID, accountID: req.Actor.ID, cancel: cancel,
		permissions: map[string]chan permissionAnswer{},
		clientCalls: map[string]chan toolbridge.Result{},
	}
	if err := s.register(turn); err != nil {
		return err
	}
	defer s.unregister(turn)

	bridgeOptions := toolbridge.Options{
		Host: req.Host, Cookie: req.Cookie, RemoteAddr: req.RemoteAddr,
		Project: conversation.Project, Actor: "copilot:" + req.Actor.Name,
		AgentRole: s.opts.AgentRole,
	}
	if s.opts.Handler != nil {
		bridgeOptions.Handler = s.opts.Handler()
	}
	bridge, err := s.opts.OpenBridge(turnCtx, bridgeOptions)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBridge, err)
	}
	defer bridge.Close()

	// Rows are written with a context the turn's cancellation does not reach:
	// a cancelled turn should still leave a transcript that says what it did.
	storeCtx := context.WithoutCancel(ctx)

	requested := req.Mode
	if requested == "" {
		requested = conversation.Mode
	}
	if requested != conversation.Mode {
		// The mode is per conversation; the panel sends it with each turn so
		// the choice sticks when the conversation is reopened.
		if _, err := s.store.UpdateConversation(storeCtx, req.Actor.ID, conversation.ID,
			ConversationPatch{Mode: &requested}); err != nil {
			return err
		}
	}
	mode := effectiveMode(requested, settings.AllowBypass)

	stored, err := s.store.Entries(storeCtx, conversation.ID)
	if err != nil {
		return err
	}
	history := historyFrom(stored)

	userMessage := provider.Message{Role: provider.RoleUser, Blocks: []provider.Block{{
		Type: provider.BlockText, Text: text,
	}}}
	userEntry, err := s.store.AppendEntry(storeCtx, Entry{
		ConversationID: conversation.ID, Kind: EntryUser, Source: req.Source, Text: text,
		Message: mustJSON(userMessage),
	})
	if err != nil {
		return err
	}
	if conversation.Title == "" {
		_ = s.store.setTitleIfEmpty(storeCtx, conversation.ID, titleFrom(text))
	}

	// From here on the stream has started: failures become events.
	emit(Event{Type: EventTurnStarted, TurnID: turn.id, EntryID: userEntry.ID})
	emit(Event{Type: EventEntry, Entry: &userEntry})

	tools, info := collectTools(bridge.Tools(), req.ClientTools)
	run := &turnRun{
		service: s, turn: turn, ctx: turnCtx, storeCtx: storeCtx, emit: emit,
		conversation: conversation, mode: mode, model: model, bridge: bridge,
		tools: tools, info: info,
		system:  systemPrompt(req.Actor.Name, conversation.Project, mode, req.Context),
		history: appendMessage(history, userMessage),
	}
	stop, usage, err := run.loop()
	if err != nil {
		emit(Event{Type: EventError, Message: err.Error(), Code: "provider_error"})
		return nil
	}
	emit(Event{Type: EventTurnFinished, Stop: stop, Usage: &usage})
	return nil
}

// turnRun is the state of one turn's loop.
type turnRun struct {
	service      *Service
	turn         *activeTurn
	ctx          context.Context
	storeCtx     context.Context
	emit         func(Event)
	conversation Conversation
	mode         Mode
	model        provider.Provider
	bridge       toolbridge.Bridge
	tools        []provider.Tool
	info         map[string]toolInfo
	system       string
	history      []provider.Message
}

// loop alternates model calls and tool calls until the model stops asking for
// tools, the turn is cancelled, or the per-turn tool budget runs out.
func (r *turnRun) loop() (string, Usage, error) {
	var usage Usage
	executed := 0
	limit := r.service.opts.MaxToolCallsPerTurn
	for {
		if r.ctx.Err() != nil {
			return StopCancelled, usage, nil
		}
		response, err := r.model.Stream(r.ctx, provider.Request{
			System: r.system, Messages: r.history, Tools: r.tools, MaxTokens: turnMaxTokens,
		}, func(delta provider.Delta) {
			if delta.Text != "" {
				r.emit(Event{Type: EventTextDelta, Text: delta.Text})
			}
		})
		if err != nil {
			if r.ctx.Err() != nil {
				return StopCancelled, usage, nil
			}
			return "", usage, err
		}
		usage.InputTokens += response.InputTokens
		usage.OutputTokens += response.OutputTokens

		message := normalizeCalls(response.Message)
		message.Role = provider.RoleAssistant
		if len(message.Raw) > 0 && message.RawProvider == "" {
			message.RawProvider, message.RawModel = r.model.Name(), r.model.Model()
		}
		r.history = append(r.history, message)
		entry, err := r.service.store.AppendEntry(r.storeCtx, Entry{
			ConversationID: r.conversation.ID, Kind: EntryAssistant,
			Text: messageText(message), Message: mustJSON(message),
		})
		if err != nil {
			return "", usage, err
		}
		r.emit(Event{Type: EventEntry, Entry: &entry})

		calls := toolCalls(message)
		if response.Stop != provider.StopToolUse || len(calls) == 0 {
			return string(response.Stop), usage, nil
		}

		results := provider.Message{Role: provider.RoleUser}
		exhausted := false
		for _, call := range calls {
			var block provider.Block
			switch {
			case executed >= limit:
				// Every call still gets a result: a history with an
				// unanswered tool call is one the next turn cannot replay.
				exhausted = true
				block = r.refuse(call, ToolError, fmt.Sprintf(
					"Not run: this turn reached its limit of %d tool calls. Tell the person what is left to do.", limit))
			case r.ctx.Err() != nil:
				block = r.refuse(call, ToolError, "Not run: the person stopped this turn.")
			default:
				executed++
				block = r.runCall(call)
			}
			results.Blocks = append(results.Blocks, block)
		}
		r.history = append(r.history, results)
		if r.ctx.Err() != nil {
			return StopCancelled, usage, nil
		}
		if exhausted || executed >= limit {
			return StopMaxToolCalls, usage, nil
		}
	}
}

// runCall takes one tool call through the permission gate and runs it.
func (r *turnRun) runCall(call provider.Block) provider.Block {
	info, known := r.info[call.ToolName]
	if !known {
		info = toolInfo{kind: toolbridge.KindRisky, title: call.ToolName}
	}
	input := call.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	r.emit(Event{
		Type: EventToolCall, CallID: call.ToolCallID, Name: call.ToolName,
		Title: info.title, Kind: info.kind, Input: input,
	})
	if !known {
		return r.finish(call, input, ToolError, fmt.Sprintf("Unknown tool %q.", call.ToolName), true)
	}

	switch gate(r.mode, info.kind) {
	case verdictRefuse:
		return r.finish(call, input, ToolDenied,
			"Not run: plan mode does not change anything. Propose this change in your reply instead; "+
				"the person can switch modes to apply it.", true)
	case verdictAsk:
		if !r.service.allowedForSession(r.conversation.ID, call.ToolName) {
			answer, answered := r.ask(call, info, input)
			if !answered {
				return r.finish(call, input, ToolError, "Not run: the person stopped this turn.", true)
			}
			switch answer.decision {
			case DecisionDeny:
				output := "The person denied this tool call. Do not retry it; ask what they want instead."
				if note := strings.TrimSpace(answer.message); note != "" {
					output += " They said: " + truncateUTF8(note, maxSummaryBytes)
				}
				return r.finish(call, input, ToolDenied, output, true)
			case DecisionAllowSession:
				r.service.allowForSession(r.conversation.ID, call.ToolName)
			}
		}
	}

	var result toolbridge.Result
	if info.client {
		result = r.callClient(call, input)
	} else {
		var err error
		result, err = r.bridge.Call(r.ctx, call.ToolName, input)
		if err != nil {
			result = toolbridge.Result{Output: "The tool could not be run: " + err.Error(), IsError: true}
		}
	}
	status := ToolOK
	if result.IsError {
		status = ToolError
	}
	return r.finish(call, input, status, result.Output, result.IsError)
}

// refuse records a call that is not run at all, with its tool_call event so
// the panel still shows a card for it.
func (r *turnRun) refuse(call provider.Block, status, output string) provider.Block {
	info := r.info[call.ToolName]
	input := call.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	r.emit(Event{
		Type: EventToolCall, CallID: call.ToolCallID, Name: call.ToolName,
		Title: info.title, Kind: info.kind, Input: input,
	})
	return r.finish(call, input, status, output, true)
}

// ask parks the turn on a permission request. answered is false when the
// turn was cancelled while waiting; a timeout is a denial.
func (r *turnRun) ask(call provider.Block, info toolInfo, input json.RawMessage) (permissionAnswer, bool) {
	requestID := newID()
	answers := make(chan permissionAnswer, 1)
	r.turn.mu.Lock()
	r.turn.permissions[requestID] = answers
	r.turn.mu.Unlock()
	defer func() {
		r.turn.mu.Lock()
		delete(r.turn.permissions, requestID)
		r.turn.mu.Unlock()
	}()

	r.emit(Event{
		Type: EventPermissionRequest, RequestID: requestID, CallID: call.ToolCallID,
		Name: call.ToolName, Kind: info.kind, Input: input,
		Summary: summarize(info.title, input),
	})
	timer := time.NewTimer(r.service.opts.PermissionTimeout)
	defer timer.Stop()
	var answer permissionAnswer
	select {
	case answer = <-answers:
	case <-timer.C:
		answer = permissionAnswer{decision: DecisionDeny, message: "No answer came in time."}
	case <-r.ctx.Done():
		return permissionAnswer{}, false
	}
	r.emit(Event{Type: EventPermissionResolved, RequestID: requestID, Decision: answer.decision})
	return answer, true
}

// callClient hands a UI tool to the browser and waits for its answer.
func (r *turnRun) callClient(call provider.Block, input json.RawMessage) toolbridge.Result {
	answers := make(chan toolbridge.Result, 1)
	r.turn.mu.Lock()
	r.turn.clientCalls[call.ToolCallID] = answers
	r.turn.mu.Unlock()
	defer func() {
		r.turn.mu.Lock()
		delete(r.turn.clientCalls, call.ToolCallID)
		r.turn.mu.Unlock()
	}()

	r.emit(Event{Type: EventClientToolCall, CallID: call.ToolCallID, Name: call.ToolName, Input: input})
	timer := time.NewTimer(r.service.opts.ClientToolTimeout)
	defer timer.Stop()
	select {
	case result := <-answers:
		return result
	case <-timer.C:
		return toolbridge.Result{Output: "The browser did not run this tool in time.", IsError: true}
	case <-r.ctx.Done():
		return toolbridge.Result{Output: "Not run: the person stopped this turn.", IsError: true}
	}
}

// finish reports a tool's outcome, stores its transcript entry and returns
// the result block for the model.
func (r *turnRun) finish(call provider.Block, input json.RawMessage, status, output string, isError bool) provider.Block {
	forModel := truncateUTF8(output, maxModelOutputBytes)
	forEvent := truncateUTF8(output, maxEventOutputBytes)
	r.emit(Event{Type: EventToolResult, CallID: call.ToolCallID, Output: &forEvent, IsError: &isError})

	block := provider.Block{
		Type: provider.BlockToolResult, ToolCallID: call.ToolCallID, Output: forModel, IsError: isError,
	}
	entry, err := r.service.store.AppendEntry(r.storeCtx, Entry{
		ConversationID: r.conversation.ID, Kind: EntryTool, ToolName: call.ToolName,
		ToolInput: string(input), ToolOutput: forModel, ToolStatus: status,
		// Each tool entry carries its own result block; historyFrom merges
		// consecutive ones back into the single user message they were.
		Message: mustJSON(provider.Message{Role: provider.RoleUser, Blocks: []provider.Block{block}}),
	})
	if err == nil {
		r.emit(Event{Type: EventEntry, Entry: &entry})
	}
	return block
}

// historyFrom rebuilds the model history from a stored transcript.
func historyFrom(entries []Entry) []provider.Message {
	var history []provider.Message
	for _, entry := range entries {
		if entry.Message == "" {
			continue
		}
		var message provider.Message
		if err := json.Unmarshal([]byte(entry.Message), &message); err != nil {
			continue
		}
		if len(message.Blocks) == 0 && len(message.Raw) == 0 {
			continue
		}
		history = appendMessage(history, message)
	}
	return repairToolResults(history)
}

// appendMessage adds a message, folding consecutive user messages into one:
// tool results are stored one per entry, and a turn that failed before the
// model answered leaves two user messages in a row. Vendors want the roles
// to alternate.
func appendMessage(history []provider.Message, message provider.Message) []provider.Message {
	if n := len(history); n > 0 && message.Role == provider.RoleUser && history[n-1].Role == provider.RoleUser {
		merged := history[n-1]
		merged.Blocks = append(append([]provider.Block(nil), merged.Blocks...), message.Blocks...)
		history[n-1] = merged
		return history
	}
	return append(history, message)
}

// repairToolResults gives every tool call in the history a result. A turn
// cancelled or broken between a tool call and its result would otherwise
// leave a history the vendor refuses outright, and the conversation could
// never continue.
func repairToolResults(history []provider.Message) []provider.Message {
	for i := 0; i < len(history); i++ {
		if history[i].Role != provider.RoleAssistant {
			continue
		}
		calls := toolCalls(history[i])
		if len(calls) == 0 {
			continue
		}
		answered := map[string]bool{}
		if i+1 < len(history) && history[i+1].Role == provider.RoleUser {
			for _, block := range history[i+1].Blocks {
				if block.Type == provider.BlockToolResult {
					answered[block.ToolCallID] = true
				}
			}
		}
		var missing []provider.Block
		for _, call := range calls {
			if !answered[call.ToolCallID] {
				missing = append(missing, provider.Block{
					Type: provider.BlockToolResult, ToolCallID: call.ToolCallID,
					Output: "Not run: the turn ended before this tool finished.", IsError: true,
				})
			}
		}
		if len(missing) == 0 {
			continue
		}
		if i+1 < len(history) && history[i+1].Role == provider.RoleUser {
			// Results lead the message: they answer the call before anything
			// the person said next.
			next := history[i+1]
			next.Blocks = append(missing, next.Blocks...)
			history[i+1] = next
		} else {
			repaired := provider.Message{Role: provider.RoleUser, Blocks: missing}
			history = append(history[:i+1], append([]provider.Message{repaired}, history[i+1:]...)...)
		}
	}
	return history
}

// normalizeCalls makes every tool call's input a JSON object. A model can
// emit a truncated or malformed argument string; carried as is, it would
// poison the stored history and every stream event that echoes it.
func normalizeCalls(message provider.Message) provider.Message {
	blocks := make([]provider.Block, len(message.Blocks))
	copy(blocks, message.Blocks)
	for i, block := range blocks {
		if block.Type != provider.BlockToolCall {
			continue
		}
		trimmed := strings.TrimSpace(string(block.Input))
		if trimmed == "" || !json.Valid([]byte(trimmed)) || !strings.HasPrefix(trimmed, "{") {
			blocks[i].Input = json.RawMessage(`{}`)
		}
	}
	message.Blocks = blocks
	return message
}

func toolCalls(message provider.Message) []provider.Block {
	var calls []provider.Block
	for _, block := range message.Blocks {
		if block.Type == provider.BlockToolCall {
			calls = append(calls, block)
		}
	}
	return calls
}

func messageText(message provider.Message) string {
	var parts []string
	for _, block := range message.Blocks {
		if block.Type == provider.BlockText && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// summarize is the one line a permission card leads with.
func summarize(title string, input json.RawMessage) string {
	compact := strings.Join(strings.Fields(string(input)), " ")
	return truncateUTF8(title+": "+compact, maxSummaryBytes)
}

// titleFrom names a conversation after the start of its first utterance.
func titleFrom(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= maxTitleRunes {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:maxTitleRunes])) + "…"
}

// truncateUTF8 cuts s to at most limit bytes without splitting a character,
// and says that it did.
func truncateUTF8(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	const marker = "…[truncated]"
	cut := limit - len(marker)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + marker
}

func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		// Every value passed here is built from strings and RawMessages that
		// were already valid JSON; a failure is a bug, not an input problem.
		panic(fmt.Sprintf("copilot: encode: %v", err))
	}
	return string(encoded)
}
