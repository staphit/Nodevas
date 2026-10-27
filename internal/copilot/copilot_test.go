package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/copilot/provider"
	"nodevas/internal/copilot/toolbridge"
	"nodevas/internal/db/dbtest"
	"nodevas/internal/identity"
)

var (
	alice = identity.Actor{ID: "u-alice", Name: "alice", Role: identity.RoleMember}
	bob   = identity.Actor{ID: "u-bob", Name: "bob", Role: identity.RoleMember}
)

// step is one scripted model call. It sees the request it was given.
type step func(t *testing.T, ctx context.Context, req provider.Request, onDelta func(provider.Delta)) (provider.Response, error)

type fakeProvider struct {
	t        *testing.T
	mu       sync.Mutex
	steps    []step
	requests []provider.Request
	// repeat, when set, answers every call once the script runs out.
	repeat step
}

func (p *fakeProvider) Name() string  { return "fake" }
func (p *fakeProvider) Model() string { return "fake-1" }

func (p *fakeProvider) Stream(ctx context.Context, req provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
	p.mu.Lock()
	index := len(p.requests)
	p.requests = append(p.requests, req)
	var next step
	if index < len(p.steps) {
		next = p.steps[index]
	} else {
		next = p.repeat
	}
	p.mu.Unlock()
	if next == nil {
		p.t.Errorf("unexpected model call %d", index+1)
		return provider.Response{}, errors.New("no script")
	}
	return next(p.t, ctx, req, onDelta)
}

func (p *fakeProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

type fakeBridge struct {
	mu      sync.Mutex
	calls   []string
	opened  toolbridge.Options
	closed  bool
	outputs map[string]string
}

var fakeTools = []toolbridge.Tool{
	{Tool: provider.Tool{Name: "get_node", Schema: json.RawMessage(`{"type":"object"}`)}, Kind: toolbridge.KindRead, Title: "Get node"},
	{Tool: provider.Tool{Name: "set_node_status", Schema: json.RawMessage(`{"type":"object"}`)}, Kind: toolbridge.KindWrite, Title: "Set status"},
	{Tool: provider.Tool{Name: "update_node_body", Schema: json.RawMessage(`{"type":"object"}`)}, Kind: toolbridge.KindRisky, Title: "Replace page"},
}

func (b *fakeBridge) Tools() []toolbridge.Tool { return fakeTools }

func (b *fakeBridge) Call(_ context.Context, name string, _ json.RawMessage) (toolbridge.Result, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, name)
	if output, ok := b.outputs[name]; ok {
		return toolbridge.Result{Output: output}, nil
	}
	return toolbridge.Result{Output: name + " ok"}, nil
}

func (b *fakeBridge) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	return nil
}

func (b *fakeBridge) called() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.calls...)
}

type harness struct {
	t        *testing.T
	service  *Service
	model    *fakeProvider
	bridge   *fakeBridge
	settings Defaults
}

func newHarness(t *testing.T, mutate func(*Options)) *harness {
	t.Helper()
	h := &harness{t: t, model: &fakeProvider{t: t}, bridge: &fakeBridge{}}
	opts := Options{
		Database: dbtest.Open(t),
		Defaults: Defaults{Enabled: true, Provider: "anthropic", Model: "claude-test", APIKey: "sk-test", AllowBypass: true},
		NewProvider: func(cfg provider.Config) (provider.Provider, error) {
			if cfg.APIKey != "sk-test" || cfg.Model != "claude-test" {
				t.Errorf("provider config = %+v", cfg)
			}
			return h.model, nil
		},
		OpenBridge: func(_ context.Context, options toolbridge.Options) (toolbridge.Bridge, error) {
			h.bridge.mu.Lock()
			h.bridge.opened = options
			h.bridge.mu.Unlock()
			return h.bridge, nil
		},
		PermissionTimeout: 5 * time.Second,
		ClientToolTimeout: 5 * time.Second,
	}
	if mutate != nil {
		mutate(&opts)
	}
	h.service = New(opts)
	return h
}

func (h *harness) conversation(actor identity.Actor, mode Mode) Conversation {
	h.t.Helper()
	conversation, err := h.service.CreateConversation(context.Background(), actor, "demo", mode)
	if err != nil {
		h.t.Fatal(err)
	}
	return conversation
}

// running is a turn in a goroutine whose events the test reads one by one.
type running struct {
	t      *testing.T
	events chan Event
	done   chan error
	seen   []Event
}

func (h *harness) start(req TurnRequest) *running {
	r := &running{t: h.t, events: make(chan Event, 256), done: make(chan error, 1)}
	go func() {
		err := h.service.Turn(context.Background(), req, func(event Event) { r.events <- event })
		close(r.events)
		r.done <- err
	}()
	return r
}

// until reads events up to and including the first of the given type.
func (r *running) until(eventType string) Event {
	r.t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case event, ok := <-r.events:
			if !ok {
				r.t.Fatalf("stream ended before %s; saw %v", eventType, types(r.seen))
			}
			r.seen = append(r.seen, event)
			if event.Type == eventType {
				return event
			}
		case <-timeout:
			r.t.Fatalf("timed out waiting for %s; saw %v", eventType, types(r.seen))
		}
	}
}

// finish drains the stream and returns every event it carried.
func (r *running) finish() []Event {
	r.t.Helper()
	for event := range r.events {
		r.seen = append(r.seen, event)
	}
	if err := <-r.done; err != nil {
		r.t.Fatalf("Turn: %v", err)
	}
	return r.seen
}

func types(events []Event) []string {
	out := make([]string, 0, len(events))
	for _, event := range events {
		out = append(out, event.Type)
	}
	return out
}

func last(events []Event) Event {
	return events[len(events)-1]
}

func answer(text string) step {
	return func(_ *testing.T, _ context.Context, _ provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
		onDelta(provider.Delta{Text: text})
		return provider.Response{
			Message: provider.Message{Role: provider.RoleAssistant, Blocks: []provider.Block{{Type: provider.BlockText, Text: text}}},
			Stop:    provider.StopEndTurn, InputTokens: 10, OutputTokens: 2,
		}, nil
	}
}

func callTool(id, name string) step {
	return func(_ *testing.T, _ context.Context, _ provider.Request, _ func(provider.Delta)) (provider.Response, error) {
		return provider.Response{
			Message: provider.Message{Role: provider.RoleAssistant, Blocks: []provider.Block{
				{Type: provider.BlockToolCall, ToolCallID: id, ToolName: name, Input: json.RawMessage(`{"id":"n1"}`)},
			}, Raw: json.RawMessage(`{"vendor":"bytes"}`)},
			Stop: provider.StopToolUse,
		}, nil
	}
}

// expectResult checks the last message the model was sent carries the
// result of call id, and answers.
func expectResult(id string, wantError bool, contains string) step {
	return func(t *testing.T, ctx context.Context, req provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
		lastMessage := req.Messages[len(req.Messages)-1]
		if lastMessage.Role != provider.RoleUser || len(lastMessage.Blocks) == 0 {
			t.Errorf("last message = %+v", lastMessage)
		} else {
			block := lastMessage.Blocks[0]
			if block.Type != provider.BlockToolResult || block.ToolCallID != id ||
				block.IsError != wantError || !strings.Contains(block.Output, contains) {
				t.Errorf("tool result = %+v, want id %s error %v containing %q", block, id, wantError, contains)
			}
		}
		return answer("done")(t, ctx, req, onDelta)
	}
}

func (h *harness) turn(conversation Conversation, mode Mode) TurnRequest {
	return TurnRequest{Actor: alice, ConversationID: conversation.ID, Text: "please do it", Mode: mode, Source: "voice"}
}

func toolStatuses(t *testing.T, h *harness, conversation Conversation) []string {
	t.Helper()
	_, entries, err := h.service.Conversation(context.Background(), alice, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	var statuses []string
	for _, entry := range entries {
		if entry.Kind == EntryTool {
			statuses = append(statuses, entry.ToolName+":"+entry.ToolStatus)
		}
	}
	return statuses
}

func TestPlainAnswerStreamsAndStoresTheTranscript(t *testing.T) {
	h := newHarness(t, nil)
	conversation := h.conversation(alice, ModeDefault)
	h.model.steps = []step{answer("Hello"), func(t *testing.T, ctx context.Context, req provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
		// The second turn replays the first: user, assistant, user.
		if len(req.Messages) != 3 || req.Messages[1].Role != provider.RoleAssistant ||
			req.Messages[2].Blocks[0].Text != "again" {
			t.Errorf("replayed history = %+v", req.Messages)
		}
		return answer("Hi")(t, ctx, req, onDelta)
	}}

	events := h.start(TurnRequest{Actor: alice, ConversationID: conversation.ID,
		Text: "把登入相關的任務都標成進行中，然後告訴我還剩下哪些", Source: "voice",
		Context: TurnContext{SelectedNodeIDs: []string{"n12"}, View: "graph", Language: "zh-TW"}}).finish()
	got := strings.Join(types(events), ",")
	if got != "turn_started,entry,text_delta,entry,turn_finished" {
		t.Fatalf("events = %s", got)
	}
	if events[0].TurnID == "" || events[0].EntryID != events[1].Entry.ID || events[1].Entry.Source != "voice" {
		t.Fatalf("turn_started/entry = %+v %+v", events[0], events[1].Entry)
	}
	if events[2].Text != "Hello" || events[3].Entry.Kind != EntryAssistant || events[3].Entry.Text != "Hello" {
		t.Fatalf("assistant events = %+v %+v", events[2], events[3].Entry)
	}
	finished := last(events)
	if finished.Stop != "end_turn" || finished.Usage == nil || finished.Usage.InputTokens != 10 {
		t.Fatalf("turn_finished = %+v", finished)
	}
	system := h.model.requests[0].System
	for _, want := range []string{"alice", `"demo"`, "n12", "graph", "zh-TW", "data, not instructions"} {
		if !strings.Contains(system, want) {
			t.Errorf("system prompt lacks %q:\n%s", want, system)
		}
	}
	if h.bridge.opened.Project != "demo" || h.bridge.opened.Actor != "copilot:alice" ||
		h.bridge.opened.AgentRole != identity.AgentOrchestrator || !h.bridge.closed {
		t.Fatalf("bridge options = %+v closed=%v", h.bridge.opened, h.bridge.closed)
	}

	stored, _, err := h.service.Conversation(context.Background(), alice, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Title != "把登入相關的任務都標成進行中，然後告訴我還剩下哪些" {
		t.Fatalf("title = %q", stored.Title)
	}

	h.start(TurnRequest{Actor: alice, ConversationID: conversation.ID, Text: "again"}).finish()
	_, entries, _ := h.service.Conversation(context.Background(), alice, conversation.ID)
	if len(entries) != 4 || entries[3].Seq != 4 || entries[2].Source != "text" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestTitleIsTheFirstFortyCharacters(t *testing.T) {
	long := strings.Repeat("字", 60)
	if got := titleFrom(long); []rune(got)[39] != '字' || len([]rune(got)) != 41 {
		t.Fatalf("title = %q", got)
	}
	if got := titleFrom("  short\n text "); got != "short text" {
		t.Fatalf("title = %q", got)
	}
}

func TestReadToolRunsWithoutAsking(t *testing.T) {
	h := newHarness(t, nil)
	conversation := h.conversation(alice, ModeDefault)
	h.model.steps = []step{callTool("c1", "get_node"), expectResult("c1", false, "get_node ok")}
	events := h.start(h.turn(conversation, ModeDefault)).finish()
	got := strings.Join(types(events), ",")
	if got != "turn_started,entry,entry,tool_call,tool_result,entry,text_delta,entry,turn_finished" {
		t.Fatalf("events = %s", got)
	}
	if call := events[3]; call.Kind != toolbridge.KindRead || call.Title != "Get node" || string(call.Input) != `{"id":"n1"}` {
		t.Fatalf("tool_call = %+v", call)
	}
	if calls := h.bridge.called(); len(calls) != 1 || calls[0] != "get_node" {
		t.Fatalf("bridge calls = %v", calls)
	}
	if statuses := toolStatuses(t, h, conversation); strings.Join(statuses, ",") != "get_node:ok" {
		t.Fatalf("statuses = %v", statuses)
	}
}

func TestDefaultModeAsksBeforeWriting(t *testing.T) {
	for _, tc := range []struct {
		decision   Decision
		wantCalled bool
		wantStatus string
	}{
		{DecisionAllow, true, "set_node_status:ok"},
		{DecisionDeny, false, "set_node_status:denied"},
	} {
		t.Run(string(tc.decision), func(t *testing.T) {
			h := newHarness(t, nil)
			conversation := h.conversation(alice, ModeDefault)
			contains := "set_node_status ok"
			if !tc.wantCalled {
				contains = "not now"
			}
			h.model.steps = []step{callTool("c1", "set_node_status"), expectResult("c1", !tc.wantCalled, contains)}
			turn := h.start(h.turn(conversation, ModeDefault))
			started := turn.until(EventTurnStarted)
			request := turn.until(EventPermissionRequest)
			if request.CallID != "c1" || request.Kind != toolbridge.KindWrite || !strings.Contains(request.Summary, "Set status") {
				t.Fatalf("permission_request = %+v", request)
			}
			if len(h.bridge.called()) != 0 {
				t.Fatal("tool ran before the decision")
			}
			// Somebody else, or the wrong ids, cannot answer it.
			if err := h.service.Decide(bob, conversation.ID, started.TurnID, request.RequestID, DecisionAllow, ""); !errors.Is(err, ErrNotFound) {
				t.Fatalf("other account decision = %v", err)
			}
			if err := h.service.Decide(alice, conversation.ID, started.TurnID, "nope", DecisionAllow, ""); !errors.Is(err, ErrNotFound) {
				t.Fatalf("unknown request decision = %v", err)
			}
			if err := h.service.Decide(alice, conversation.ID, started.TurnID, request.RequestID, tc.decision, "not now"); err != nil {
				t.Fatal(err)
			}
			resolved := turn.until(EventPermissionResolved)
			if resolved.Decision != tc.decision || resolved.RequestID != request.RequestID {
				t.Fatalf("permission_resolved = %+v", resolved)
			}
			turn.finish()
			if called := len(h.bridge.called()) == 1; called != tc.wantCalled {
				t.Fatalf("bridge called = %v, want %v", called, tc.wantCalled)
			}
			if statuses := toolStatuses(t, h, conversation); strings.Join(statuses, ",") != tc.wantStatus {
				t.Fatalf("statuses = %v", statuses)
			}
		})
	}
}

func TestPermissionTimeoutDenies(t *testing.T) {
	h := newHarness(t, func(opts *Options) { opts.PermissionTimeout = 50 * time.Millisecond })
	conversation := h.conversation(alice, ModeDefault)
	h.model.steps = []step{callTool("c1", "set_node_status"), expectResult("c1", true, "denied")}
	events := h.start(h.turn(conversation, ModeDefault)).finish()
	for _, event := range events {
		if event.Type == EventPermissionResolved && event.Decision != DecisionDeny {
			t.Fatalf("timeout resolved as %q", event.Decision)
		}
	}
	if len(h.bridge.called()) != 0 {
		t.Fatal("a timed-out request ran the tool")
	}
}

func TestAcceptEditsAsksOnlyForRiskyTools(t *testing.T) {
	h := newHarness(t, nil)
	conversation := h.conversation(alice, ModeAcceptEdits)
	h.model.steps = []step{callTool("c1", "set_node_status"), callTool("c2", "update_node_body"), expectResult("c2", false, "update_node_body ok")}
	turn := h.start(h.turn(conversation, ModeAcceptEdits))
	started := turn.until(EventTurnStarted)
	request := turn.until(EventPermissionRequest)
	if request.CallID != "c2" {
		t.Fatalf("asked about %s, want only the risky c2", request.CallID)
	}
	if calls := h.bridge.called(); len(calls) != 1 || calls[0] != "set_node_status" {
		t.Fatalf("calls before the decision = %v", calls)
	}
	if err := h.service.Decide(alice, conversation.ID, started.TurnID, request.RequestID, DecisionAllow, ""); err != nil {
		t.Fatal(err)
	}
	turn.finish()
	if calls := h.bridge.called(); len(calls) != 2 {
		t.Fatalf("calls = %v", calls)
	}
}

func TestPlanModeRefusesWrites(t *testing.T) {
	h := newHarness(t, nil)
	conversation := h.conversation(alice, ModePlan)
	h.model.steps = []step{callTool("c1", "set_node_status"), expectResult("c1", true, "plan mode")}
	events := h.start(h.turn(conversation, ModePlan)).finish()
	for _, event := range events {
		if event.Type == EventPermissionRequest {
			t.Fatal("plan mode asked instead of refusing")
		}
	}
	if len(h.bridge.called()) != 0 {
		t.Fatal("plan mode ran a write")
	}
	if !strings.Contains(h.model.requests[0].System, "plan") {
		t.Fatal("system prompt does not explain plan mode")
	}
	if statuses := toolStatuses(t, h, conversation); strings.Join(statuses, ",") != "set_node_status:denied" {
		t.Fatalf("statuses = %v", statuses)
	}
}

func TestBypassNotAllowedBehavesAsAcceptEdits(t *testing.T) {
	h := newHarness(t, func(opts *Options) { opts.Defaults.AllowBypass = false })
	conversation := h.conversation(alice, ModeBypass)
	h.model.steps = []step{callTool("c1", "update_node_body"), expectResult("c1", false, "ok")}
	turn := h.start(h.turn(conversation, ModeBypass))
	started := turn.until(EventTurnStarted)
	request := turn.until(EventPermissionRequest)
	if err := h.service.Decide(alice, conversation.ID, started.TurnID, request.RequestID, DecisionAllow, ""); err != nil {
		t.Fatal(err)
	}
	turn.finish()

	allowed := newHarness(t, nil)
	conversation = allowed.conversation(alice, ModeBypass)
	allowed.model.steps = []step{callTool("c1", "update_node_body"), expectResult("c1", false, "ok")}
	for _, event := range allowed.start(allowed.turn(conversation, ModeBypass)).finish() {
		if event.Type == EventPermissionRequest {
			t.Fatal("allowed bypass still asked")
		}
	}
}

func TestAllowSessionStopsAskingForThatTool(t *testing.T) {
	h := newHarness(t, nil)
	conversation := h.conversation(alice, ModeDefault)
	h.model.steps = []step{
		callTool("c1", "set_node_status"), callTool("c2", "set_node_status"), callTool("c3", "update_node_body"),
		expectResult("c3", false, "ok"),
	}
	turn := h.start(h.turn(conversation, ModeDefault))
	started := turn.until(EventTurnStarted)
	first := turn.until(EventPermissionRequest)
	if err := h.service.Decide(alice, conversation.ID, started.TurnID, first.RequestID, DecisionAllowSession, ""); err != nil {
		t.Fatal(err)
	}
	// c2 is the same tool and runs without asking; c3 is another tool and asks.
	second := turn.until(EventPermissionRequest)
	if second.CallID != "c3" {
		t.Fatalf("asked again about %s", second.CallID)
	}
	if err := h.service.Decide(alice, conversation.ID, started.TurnID, second.RequestID, DecisionAllow, ""); err != nil {
		t.Fatal(err)
	}
	turn.finish()
	if calls := h.bridge.called(); len(calls) != 3 {
		t.Fatalf("calls = %v", calls)
	}

	// The answer holds for the rest of the conversation, not just the turn.
	h.model.steps = append(h.model.steps, callTool("c4", "set_node_status"), expectResult("c4", false, "ok"))
	for _, event := range h.start(h.turn(conversation, ModeDefault)).finish() {
		if event.Type == EventPermissionRequest {
			t.Fatal("allow_session did not carry to the next turn")
		}
	}
}

func TestClientToolRoundTrip(t *testing.T) {
	h := newHarness(t, nil)
	conversation := h.conversation(alice, ModeDefault)
	h.model.steps = []step{
		func(t *testing.T, ctx context.Context, req provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
			var offered []string
			for _, tool := range req.Tools {
				offered = append(offered, tool.Name)
			}
			joined := strings.Join(offered, ",")
			if !strings.Contains(joined, "ui_open_node") || strings.Contains(joined, "ui_set_view") ||
				strings.Contains(joined, "ui_bogus") {
				t.Errorf("offered tools = %s", joined)
			}
			return callTool("c1", "ui_open_node")(t, ctx, req, onDelta)
		},
		expectResult("c1", false, "opened n1"),
	}
	req := h.turn(conversation, ModePlan)
	req.ClientTools = []string{"ui_open_node", "ui_bogus"}
	turn := h.start(req)
	started := turn.until(EventTurnStarted)
	call := turn.until(EventClientToolCall)
	if call.CallID != "c1" || call.Name != "ui_open_node" {
		t.Fatalf("client_tool_call = %+v", call)
	}
	if err := h.service.ClientResult(bob, conversation.ID, started.TurnID, "c1", "x", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other account result = %v", err)
	}
	if err := h.service.ClientResult(alice, conversation.ID, started.TurnID, "c9", "x", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown call result = %v", err)
	}
	if err := h.service.ClientResult(alice, conversation.ID, started.TurnID, "c1", "opened n1", false); err != nil {
		t.Fatal(err)
	}
	turn.finish()
	if len(h.bridge.called()) != 0 {
		t.Fatal("a client tool went to the bridge")
	}
}

func TestClientToolTimeoutIsAnError(t *testing.T) {
	h := newHarness(t, func(opts *Options) { opts.ClientToolTimeout = 50 * time.Millisecond })
	conversation := h.conversation(alice, ModeDefault)
	h.model.steps = []step{callTool("c1", "ui_set_view"), expectResult("c1", true, "did not run")}
	req := h.turn(conversation, ModeDefault)
	req.ClientTools = []string{"ui_set_view"}
	h.start(req).finish()
}

func TestMaxToolCallsEndsTheTurn(t *testing.T) {
	h := newHarness(t, func(opts *Options) { opts.MaxToolCallsPerTurn = 2 })
	conversation := h.conversation(alice, ModeDefault)
	h.model.repeat = callTool("c", "get_node")
	events := h.start(h.turn(conversation, ModeDefault)).finish()
	if finished := last(events); finished.Type != EventTurnFinished || finished.Stop != StopMaxToolCalls {
		t.Fatalf("last event = %+v", finished)
	}
	if calls := h.bridge.called(); len(calls) != 2 {
		t.Fatalf("bridge calls = %v", calls)
	}
	if h.model.callCount() != 2 {
		t.Fatalf("model calls = %d", h.model.callCount())
	}
}

func TestCancelStopsAWaitingTurnAndKeepsTheHistoryReplayable(t *testing.T) {
	h := newHarness(t, nil)
	conversation := h.conversation(alice, ModeDefault)
	h.model.steps = []step{callTool("c1", "set_node_status")}
	turn := h.start(h.turn(conversation, ModeDefault))
	started := turn.until(EventTurnStarted)
	turn.until(EventPermissionRequest)

	// Only one turn per conversation.
	if err := h.service.Turn(context.Background(), h.turn(conversation, ModeDefault), func(Event) {}); !errors.Is(err, ErrBusy) {
		t.Fatalf("second turn = %v, want ErrBusy", err)
	}
	if err := h.service.Cancel(bob, conversation.ID, started.TurnID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other account cancel = %v", err)
	}
	if err := h.service.Cancel(alice, conversation.ID, started.TurnID); err != nil {
		t.Fatal(err)
	}
	events := turn.finish()
	if finished := last(events); finished.Type != EventTurnFinished || finished.Stop != StopCancelled {
		t.Fatalf("last event = %+v", finished)
	}
	if len(h.bridge.called()) != 0 {
		t.Fatal("a cancelled turn ran the tool")
	}
	// The turn is gone, and the next one sees every call answered.
	if err := h.service.Cancel(alice, conversation.ID, started.TurnID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancel after finish = %v", err)
	}
	h.model.steps = append(h.model.steps, func(t *testing.T, ctx context.Context, req provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
		assistant := req.Messages[1]
		next := req.Messages[2]
		if assistant.Role != provider.RoleAssistant || string(assistant.Raw) != `{"vendor":"bytes"}` ||
			assistant.RawProvider != "fake" || next.Blocks[0].ToolCallID != "c1" ||
			next.Blocks[len(next.Blocks)-1].Text != "please do it" {
			t.Errorf("history after cancel = %+v", req.Messages)
		}
		return answer("ok")(t, ctx, req, onDelta)
	})
	h.start(h.turn(conversation, ModeDefault)).finish()
}

func TestCancelDuringModelCall(t *testing.T) {
	h := newHarness(t, nil)
	conversation := h.conversation(alice, ModeDefault)
	h.model.steps = []step{func(_ *testing.T, ctx context.Context, _ provider.Request, _ func(provider.Delta)) (provider.Response, error) {
		<-ctx.Done()
		return provider.Response{}, ctx.Err()
	}}
	turn := h.start(h.turn(conversation, ModeDefault))
	started := turn.until(EventTurnStarted)
	if err := h.service.Cancel(alice, conversation.ID, started.TurnID); err != nil {
		t.Fatal(err)
	}
	if finished := last(turn.finish()); finished.Stop != StopCancelled {
		t.Fatalf("last event = %+v", finished)
	}
}

func TestProviderErrorBecomesAnErrorEvent(t *testing.T) {
	h := newHarness(t, nil)
	conversation := h.conversation(alice, ModeDefault)
	h.model.steps = []step{func(*testing.T, context.Context, provider.Request, func(provider.Delta)) (provider.Response, error) {
		return provider.Response{}, errors.New("overloaded")
	}}
	events := h.start(h.turn(conversation, ModeDefault)).finish()
	if failed := last(events); failed.Type != EventError || !strings.Contains(failed.Message, "overloaded") {
		t.Fatalf("last event = %+v", failed)
	}
}

func TestTurnRefusals(t *testing.T) {
	h := newHarness(t, nil)
	conversation := h.conversation(alice, ModeDefault)
	noEmit := func(Event) { t.Error("refused turn emitted an event") }
	ctx := context.Background()
	if err := h.service.Turn(ctx, TurnRequest{Actor: bob, ConversationID: conversation.ID, Text: "hi"}, noEmit); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other account turn = %v", err)
	}
	if err := h.service.Turn(ctx, TurnRequest{Actor: alice, ConversationID: conversation.ID, Text: "  "}, noEmit); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty text = %v", err)
	}
	if err := h.service.Turn(ctx, TurnRequest{Actor: alice, ConversationID: conversation.ID, Text: "hi", Mode: "yolo"}, noEmit); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad mode = %v", err)
	}

	off := newHarness(t, func(opts *Options) { opts.Defaults.APIKey = "" })
	conversation = off.conversation(alice, ModeDefault)
	if err := off.service.Turn(ctx, TurnRequest{Actor: alice, ConversationID: conversation.ID, Text: "hi"}, noEmit); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no key = %v", err)
	}
	status, err := off.service.Status(ctx, alice, "")
	if err != nil {
		t.Fatal(err)
	}
	if status.Enabled || !strings.Contains(status.Reason, "API key") {
		t.Fatalf("status without a key = %+v", status)
	}
}

func TestRepairToolResultsAnswersOrphanedCalls(t *testing.T) {
	history := []provider.Message{
		{Role: provider.RoleUser, Blocks: []provider.Block{{Type: provider.BlockText, Text: "a"}}},
		{Role: provider.RoleAssistant, Blocks: []provider.Block{
			{Type: provider.BlockToolCall, ToolCallID: "x", ToolName: "get_node"},
			{Type: provider.BlockToolCall, ToolCallID: "y", ToolName: "get_node"},
		}},
		{Role: provider.RoleUser, Blocks: []provider.Block{{Type: provider.BlockToolResult, ToolCallID: "x"}}},
		{Role: provider.RoleAssistant, Blocks: []provider.Block{{Type: provider.BlockToolCall, ToolCallID: "z"}}},
	}
	repaired := repairToolResults(history)
	if len(repaired) != 5 {
		t.Fatalf("repaired = %+v", repaired)
	}
	if blocks := repaired[2].Blocks; len(blocks) != 2 || blocks[0].ToolCallID != "y" || !blocks[0].IsError {
		t.Fatalf("missing y not answered first: %+v", blocks)
	}
	if blocks := repaired[4].Blocks; repaired[4].Role != provider.RoleUser || blocks[0].ToolCallID != "z" {
		t.Fatalf("trailing call z not answered: %+v", repaired[4])
	}
}

func TestToolOutputIsTruncatedForEventsAndModel(t *testing.T) {
	h := newHarness(t, nil)
	conversation := h.conversation(alice, ModeDefault)
	h.bridge.outputs = map[string]string{"get_node": strings.Repeat("頁", 20000)}
	h.model.steps = []step{callTool("c1", "get_node"), func(t *testing.T, ctx context.Context, req provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
		output := req.Messages[len(req.Messages)-1].Blocks[0].Output
		if len(output) > maxModelOutputBytes || !strings.HasSuffix(output, "…[truncated]") {
			t.Errorf("model output is %d bytes", len(output))
		}
		return answer("ok")(t, ctx, req, onDelta)
	}}
	for _, event := range h.start(h.turn(conversation, ModeDefault)).finish() {
		if event.Type == EventToolResult && (len(*event.Output) > maxEventOutputBytes || !strings.HasSuffix(*event.Output, "…[truncated]")) {
			t.Fatalf("event output is %d bytes", len(*event.Output))
		}
	}
}

func TestMalformedToolInputIsReplaced(t *testing.T) {
	h := newHarness(t, nil)
	conversation := h.conversation(alice, ModeDefault)
	h.model.steps = []step{func(*testing.T, context.Context, provider.Request, func(provider.Delta)) (provider.Response, error) {
		return provider.Response{Message: provider.Message{Blocks: []provider.Block{
			{Type: provider.BlockToolCall, ToolCallID: "c1", ToolName: "get_node", Input: json.RawMessage(`{"id":`)},
		}}, Stop: provider.StopToolUse}, nil
	}, expectResult("c1", false, "ok")}
	for _, event := range h.start(h.turn(conversation, ModeDefault)).finish() {
		if event.Type == EventToolCall && string(event.Input) != `{}` {
			t.Fatalf("tool_call input = %s", event.Input)
		}
	}
}
