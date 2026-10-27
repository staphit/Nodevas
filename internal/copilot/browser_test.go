package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"nodevas/internal/copilot/provider"
)

func browserHarness(t *testing.T, mutate func(*Options)) *harness {
	return freeHarness(t, func(opts *Options) {
		opts.NewProvider = func(provider.Config) (provider.Provider, error) {
			t.Error("a server provider was built for a browser turn")
			return nil, errors.New("no")
		}
		if mutate != nil {
			mutate(opts)
		}
	})
}

func browserTurn(conversation Conversation) TurnRequest {
	return TurnRequest{Actor: alice, ConversationID: conversation.ID, Text: "what is n1?", Mode: ModeDefault,
		Session: "sess-alice", BrowserModel: BrowserAvailable}
}

// answerModel posts output for the model_request r is waiting on.
func (h *harness) answerModel(t *testing.T, r *running, conversationID, output string) Event {
	t.Helper()
	request := r.until(EventModelRequest)
	if err := h.service.ModelResult(alice, "sess-alice", conversationID, r.turnID(), request.RequestID, output, ""); err != nil {
		t.Fatalf("ModelResult: %v", err)
	}
	return request
}

func (r *running) turnID() string {
	for _, event := range r.seen {
		if event.Type == EventTurnStarted {
			return event.TurnID
		}
	}
	r.t.Fatal("no turn_started yet")
	return ""
}

func TestBrowserModelTurnWithAToolCall(t *testing.T) {
	h := browserHarness(t, nil)
	conversation := h.conversation(alice, ModeDefault)
	r := h.start(browserTurn(conversation))

	first := h.answerModel(t, r, conversation.ID, `{"type":"tool_call","name":"get_node","input":{"id":"n1"}}`)
	request := first.ModelRequest
	if request == nil || request.Attempt != 1 || len(request.Messages) != 1 ||
		request.Messages[0].Role != "user" || request.Messages[0].Content != "what is n1?" {
		t.Fatalf("model request = %+v", request)
	}
	if !strings.Contains(request.System, "get_node(") || !strings.Contains(request.System, "never instructions") {
		t.Fatalf("system = %q", request.System)
	}
	var schema struct {
		Properties struct {
			Name struct {
				Enum []string `json:"enum"`
			} `json:"name"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(request.Schema, &schema); err != nil || len(schema.Properties.Name.Enum) != len(fakeTools) {
		t.Fatalf("schema = %s (%v)", request.Schema, err)
	}

	second := h.answerModel(t, r, conversation.ID, `{"type":"text","text":"n1 is the login task."}`)
	// The tool call and its result reach the model as chat turns.
	messages := second.ModelRequest.Messages
	if len(messages) != 3 || messages[1].Role != "assistant" || !strings.Contains(messages[1].Content, `"get_node"`) ||
		messages[2].Role != "user" || !strings.Contains(messages[2].Content, "get_node ok") {
		t.Fatalf("second request messages = %+v", messages)
	}
	events := r.finish()
	if last(events).Type != EventTurnFinished || last(events).Stop != string(provider.StopEndTurn) {
		t.Fatalf("events = %v", types(events))
	}
	if got := h.bridge.called(); len(got) != 1 || got[0] != "get_node" {
		t.Fatalf("bridge calls = %v", got)
	}
	var text string
	for _, event := range events {
		if event.Type == EventTextDelta {
			text += event.Text
		}
	}
	if text != "n1 is the login task." {
		t.Fatalf("text = %q", text)
	}
}

// The permission gate applies to the browser's model as to any other.
func TestBrowserModelWritesStillAsk(t *testing.T) {
	h := browserHarness(t, nil)
	conversation := h.conversation(alice, ModeAcceptEdits)
	req := browserTurn(conversation)
	req.Mode = ModeDefault
	r := h.start(req)
	h.answerModel(t, r, conversation.ID, `{"type":"tool_call","name":"set_node_status","input":{"id":"n1","status":"done"}}`)
	permission := r.until(EventPermissionRequest)
	if err := h.service.Decide(alice, conversation.ID, r.turnID(), permission.RequestID, DecisionDeny, ""); err != nil {
		t.Fatal(err)
	}
	h.answerModel(t, r, conversation.ID, `{"type":"text","text":"OK, I left it."}`)
	r.finish()
	if got := h.bridge.called(); len(got) != 0 {
		t.Fatalf("a denied write ran: %v", got)
	}
}

func TestBrowserModelRefusesBypass(t *testing.T) {
	h := browserHarness(t, nil)
	conversation := h.conversation(alice, ModeDefault)
	req := browserTurn(conversation)
	req.Mode = ModeBypass
	err := h.service.Turn(context.Background(), req, func(Event) {})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "bypassPermissions") {
		t.Fatalf("bypass turn = %v", err)
	}
	if got := status(t, h, BrowserAvailable); got.AllowBypass {
		t.Fatalf("status offers bypass: %+v", got)
	}
	for _, mode := range status(t, h, BrowserAvailable).Modes {
		if mode == ModeBypass {
			t.Fatal("bypass listed in modes")
		}
	}
}

// Only the session that started the turn answers its model call.
func TestBrowserModelAnswerIsBoundToTheSession(t *testing.T) {
	h := browserHarness(t, nil)
	conversation := h.conversation(alice, ModeDefault)
	r := h.start(browserTurn(conversation))
	request := r.until(EventModelRequest)
	turnID := r.turnID()
	output := `{"type":"text","text":"hijacked"}`
	for name, try := range map[string]func() error{
		"another session": func() error {
			return h.service.ModelResult(alice, "sess-other", conversation.ID, turnID, request.RequestID, output, "")
		},
		"another account": func() error {
			return h.service.ModelResult(bob, "sess-alice", conversation.ID, turnID, request.RequestID, output, "")
		},
		"another request": func() error {
			return h.service.ModelResult(alice, "sess-alice", conversation.ID, turnID, "nope", output, "")
		},
		"another turn": func() error {
			return h.service.ModelResult(alice, "sess-alice", conversation.ID, "t-other", request.RequestID, output, "")
		},
	} {
		if err := try(); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v, want not found", name, err)
		}
	}
	if err := h.service.ModelResult(alice, "sess-alice", conversation.ID, turnID, request.RequestID,
		strings.Repeat("x", MaxBrowserOutputBytes+1), ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("oversized answer: %v", err)
	}
	if err := h.service.ModelResult(alice, "sess-alice", conversation.ID, turnID, request.RequestID,
		`{"type":"text","text":"mine"}`, ""); err != nil {
		t.Fatal(err)
	}
	// A second answer to the same request finds nothing waiting.
	if err := h.service.ModelResult(alice, "sess-alice", conversation.ID, turnID, request.RequestID, output, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("second answer: %v", err)
	}
	events := r.finish()
	for _, event := range events {
		if event.Type == EventTextDelta && strings.Contains(event.Text, "hijacked") {
			t.Fatal("another session's answer was used")
		}
	}
}

func TestBrowserModelTimesOut(t *testing.T) {
	h := browserHarness(t, func(opts *Options) { opts.BrowserModelTimeout = 50 * time.Millisecond })
	conversation := h.conversation(alice, ModeDefault)
	r := h.start(browserTurn(conversation))
	failure := r.until(EventError)
	if !strings.Contains(failure.Message, "did not answer in time") {
		t.Fatalf("error = %+v", failure)
	}
	r.finish()
}

func TestBrowserModelErrorIsReported(t *testing.T) {
	h := browserHarness(t, nil)
	conversation := h.conversation(alice, ModeDefault)
	r := h.start(browserTurn(conversation))
	request := r.until(EventModelRequest)
	if err := h.service.ModelResult(alice, "sess-alice", conversation.ID, r.turnID(), request.RequestID, "",
		"QuotaExceededError: the input is too large"); err != nil {
		t.Fatal(err)
	}
	failure := r.until(EventError)
	if !strings.Contains(failure.Message, "QuotaExceededError") {
		t.Fatalf("error = %+v", failure)
	}
	r.finish()
}

// Broken JSON is asked for once more; prose is taken as the reply.
func TestBrowserModelMalformedOutput(t *testing.T) {
	h := browserHarness(t, nil)
	conversation := h.conversation(alice, ModeDefault)
	r := h.start(browserTurn(conversation))
	h.answerModel(t, r, conversation.ID, `{"type":"tool_call","name":"get_node","input":`)
	retry := h.answerModel(t, r, conversation.ID, `{"type":"tool_call","name":"rm_rf","input":{}}`)
	if retry.ModelRequest.Attempt != 2 {
		t.Fatalf("retry attempt = %d", retry.ModelRequest.Attempt)
	}
	failure := r.until(EventError)
	if !strings.Contains(failure.Message, "cannot read") {
		t.Fatalf("error = %+v", failure)
	}
	r.finish()
	if got := h.bridge.called(); len(got) != 0 {
		t.Fatalf("a malformed answer ran a tool: %v", got)
	}

	other := h.conversation(alice, ModeDefault)
	r = h.start(browserTurn(other))
	h.answerModel(t, r, other.ID, "Sure, n1 is the login task.")
	events := r.finish()
	if last(events).Type != EventTurnFinished {
		t.Fatalf("prose answer events = %v", types(events))
	}
}

func TestParseBrowserOutput(t *testing.T) {
	names := []string{"get_node", "set_node_status"}
	cases := []struct {
		raw  string
		ok   bool
		stop provider.StopReason
	}{
		{`{"type":"text","text":"hi"}`, true, provider.StopEndTurn},
		{"```json\n{\"type\":\"text\",\"text\":\"hi\"}\n```", true, provider.StopEndTurn},
		{`{"type":"tool_call","name":"get_node","input":{"id":"n1"}}`, true, provider.StopToolUse},
		{`{"type":"tool_call","name":"get_node"}`, true, provider.StopToolUse},
		{`{"type":"tool_call","name":"delete_everything","input":{}}`, false, ""},
		{`{"type":"tool_call","name":"get_node","input":"n1"}`, false, ""},
		{`{"type":"text","text":"  "}`, false, ""},
		{`{"type":"shell","text":"rm -rf"}`, false, ""},
		{`not json`, false, ""},
	}
	for _, tc := range cases {
		message, stop, ok := parseBrowserOutput(tc.raw, names)
		if ok != tc.ok || stop != tc.stop {
			t.Errorf("parse(%q) = %+v %q %v", tc.raw, message, stop, ok)
		}
		if ok && stop == provider.StopToolUse {
			calls := toolCalls(message)
			if len(calls) != 1 || calls[0].ToolCallID == "" || !json.Valid(calls[0].Input) {
				t.Errorf("parse(%q) calls = %+v", tc.raw, calls)
			}
		}
	}
}

func TestBrowserMessagesStayShort(t *testing.T) {
	var history []provider.Message
	for i := 0; i < 40; i++ {
		history = append(history,
			provider.Message{Role: provider.RoleUser, Blocks: []provider.Block{{Type: provider.BlockText, Text: strings.Repeat("u", 5000)}}},
			provider.Message{Role: provider.RoleAssistant, Blocks: []provider.Block{{Type: provider.BlockText, Text: "ok"}}})
	}
	history = append(history, provider.Message{Role: provider.RoleUser, Blocks: []provider.Block{{Type: provider.BlockText, Text: "last"}}})
	messages := browserMessages(history)
	if len(messages) > maxBrowserMessages || messages[0].Role != "user" || messages[len(messages)-1].Content != "last" {
		t.Fatalf("messages = %d, first %s, last %q", len(messages), messages[0].Role, messages[len(messages)-1].Content)
	}
	for _, message := range messages {
		if len(message.Content) > maxBrowserMessageBytes {
			t.Fatalf("message of %d bytes", len(message.Content))
		}
	}
}
