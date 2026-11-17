package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"nodevas/internal/copilot/provider"
)

const draftOutline = `{"layout":"workflow","nodeTypes":[{"id":"task","label":"Task"},{"id":"gate","label":"Review"},` +
	`{"id":"custom-type-bug","label":"Bug"}],"nodes":[{"id":"n-spec","title":"寫規格","kind":"task","status":"done"},` +
	`{"id":"n-ship","title":"Ship it","kind":"task","status":"ready"}],"edges":[{"from":"n-spec","to":"n-ship"}],"total":2}`

func draftHarness(t *testing.T) *harness {
	h := newHarness(t, nil)
	h.bridge.outputs = map[string]string{
		"get_graph_outline": draftOutline,
		"get_node":          `{"id":"n-ship","title":"Ship it","kind":"task","status":"ready","upstream":["n-spec"],"body":""}`,
	}
	return h
}

func draftTurn(conversation Conversation) TurnRequest {
	return TurnRequest{Actor: alice, ConversationID: conversation.ID, Text: "規劃產品上線：寫文件、測試、發布",
		Source: "text", Intent: IntentDraft, Context: TurnContext{SelectedNodeIDs: []string{"n-ship"}, Language: "zh-TW"}}
}

// answerJSON answers a model call with raw text, after letting check look at
// the request.
func answerJSON(raw string, stop provider.StopReason, check func(*testing.T, provider.Request)) step {
	return func(t *testing.T, _ context.Context, req provider.Request, _ func(provider.Delta)) (provider.Response, error) {
		if check != nil {
			check(t, req)
		}
		return provider.Response{
			Message: provider.Message{Role: provider.RoleAssistant, Blocks: []provider.Block{{Type: provider.BlockText, Text: raw}}},
			Stop:    stop, InputTokens: 100, OutputTokens: 50,
		}, nil
	}
}

const goodDraft = `{"summary":"上線三步","nodes":[` +
	`{"key":"n1","title":"寫使用文件","kind":"task","after":["existing:n-spec"]},` +
	`{"key":"n2","title":"整合測試","kind":"task","after":["n1"]},` +
	`{"key":"n3","title":"發布","kind":"gate","after":["n2"]}]}`

func draftEntry(t *testing.T, h *harness, conversation Conversation) (Entry, DraftData) {
	t.Helper()
	_, entries, err := h.service.Conversation(context.Background(), alice, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Kind == EntryDraft {
			var data DraftData
			if err := json.Unmarshal([]byte(entry.Data), &data); err != nil {
				t.Fatal(err)
			}
			return entry, data
		}
	}
	t.Fatalf("no draft entry among %+v", entries)
	return Entry{}, DraftData{}
}

func TestDraftValidOnTheFirstCall(t *testing.T) {
	h := draftHarness(t)
	conversation := h.conversation(alice, ModeBypass)
	h.model.steps = []step{answerJSON(goodDraft, provider.StopEndTurn, func(t *testing.T, req provider.Request) {
		if req.ResponseSchema == nil || req.ResponseSchema.Name != draftSchemaName {
			t.Fatalf("no response schema: %+v", req.ResponseSchema)
		}
		if !strings.Contains(string(req.ResponseSchema.Schema), `"custom-type-bug"`) {
			t.Errorf("kind enum lacks the project's type: %s", req.ResponseSchema.Schema)
		}
		if len(req.Tools) != 0 || req.MaxTokens != draftMaxTokens {
			t.Errorf("tools = %d, max tokens = %d", len(req.Tools), req.MaxTokens)
		}
		user := req.Messages[0].Blocks[0].Text
		if !strings.Contains(user, "- n-ship | Ship it | task | ready | n-spec") || !strings.Contains(user, "- n-ship: n-spec") ||
			!strings.Contains(user, "規劃產品上線") {
			t.Errorf("user message:\n%s", user)
		}
	})}
	events := h.start(draftTurn(conversation)).finish()
	var steps []string
	for _, event := range events {
		if event.Type == EventDraftStatus {
			steps = append(steps, event.Code)
		}
	}
	if !slices.Equal(steps, []string{DraftStepReading, DraftStepDrafting}) {
		t.Errorf("steps = %v", steps)
	}
	if finished := last(events); finished.Type != EventTurnFinished || finished.Stop != string(provider.StopEndTurn) {
		t.Fatalf("last event = %+v (all %v)", finished, types(events))
	}
	entry, data := draftEntry(t, h, conversation)
	if len(data.Draft.Nodes) != 3 || hasErrors(data.Issues) || data.State != DraftOpen || data.Model != "claude-test" {
		t.Fatalf("draft data = %+v", data)
	}
	if entry.Text != "上線三步" || !strings.Contains(entry.Message, "I proposed a draft of 3 new nodes") {
		t.Errorf("entry = %+v", entry)
	}
	// Bypass or not, a draft turn only reads.
	for _, call := range h.bridge.called() {
		if call != "get_graph_outline" && call != "get_node" {
			t.Errorf("draft turn called %s", call)
		}
	}
	if h.model.callCount() != 1 {
		t.Errorf("model calls = %d", h.model.callCount())
	}
}

func TestDraftRepairsOnceWithTheExactProblems(t *testing.T) {
	h := draftHarness(t)
	conversation := h.conversation(alice, ModeDefault)
	bad := `{"summary":"s","nodes":[{"key":"n1","title":"A","kind":"task","after":["n9"]}]}`
	h.model.steps = []step{
		answerJSON(bad, provider.StopEndTurn, nil),
		answerJSON(goodDraft, provider.StopEndTurn, func(t *testing.T, req provider.Request) {
			if len(req.Messages) != 3 || req.Messages[1].Role != provider.RoleAssistant {
				t.Fatalf("repair messages = %+v", req.Messages)
			}
			repair := req.Messages[2].Blocks[0].Text
			if !strings.Contains(repair, `nodes[0].after[0]: unknown key or node "n9"`) {
				t.Errorf("repair message:\n%s", repair)
			}
		}),
	}
	events := h.start(draftTurn(conversation)).finish()
	if !slices.ContainsFunc(events, func(e Event) bool { return e.Type == EventDraftStatus && e.Code == DraftStepChecking }) {
		t.Errorf("no checking step: %v", types(events))
	}
	if _, data := draftEntry(t, h, conversation); len(data.Draft.Nodes) != 3 || hasErrors(data.Issues) {
		t.Fatalf("repaired draft = %+v", data)
	}
}

func TestDraftStillInvalidIsStoredWithItsErrors(t *testing.T) {
	h := draftHarness(t)
	conversation := h.conversation(alice, ModeDefault)
	bad := `{"summary":"s","nodes":[{"key":"n1","title":"A","kind":"dragon","after":[]}]}`
	h.model.steps = []step{answerJSON(bad, provider.StopEndTurn, nil), answerJSON(bad, provider.StopEndTurn, nil)}
	h.start(draftTurn(conversation)).finish()
	_, data := draftEntry(t, h, conversation)
	if !slices.ContainsFunc(data.Issues, func(i DraftIssue) bool { return i.Code == IssueUnknownKind && i.Key == "n1" }) {
		t.Fatalf("issues = %+v", data.Issues)
	}
	if h.model.callCount() != 2 {
		t.Errorf("model calls = %d, want one repair only", h.model.callCount())
	}
}

func TestDraftTruncatedIsAnErrorNotAnEmptyDraft(t *testing.T) {
	h := draftHarness(t)
	conversation := h.conversation(alice, ModeDefault)
	cut := `{"summary":"s","nodes":[{"key":"n1","title":"A`
	h.model.steps = []step{
		answerJSON(cut, provider.StopMaxTokens, nil),
		answerJSON(cut, provider.StopMaxTokens, func(t *testing.T, req provider.Request) {
			if repair := req.Messages[2].Blocks[0].Text; !strings.Contains(repair, "cut off") {
				t.Errorf("repair message:\n%s", repair)
			}
		}),
	}
	events := h.start(draftTurn(conversation)).finish()
	if finished := last(events); finished.Type != EventError || finished.Code != "draft_unreadable" {
		t.Fatalf("last event = %+v", finished)
	}
	_, entries, _ := h.service.Conversation(context.Background(), alice, conversation.ID)
	for _, entry := range entries {
		if entry.Kind == EntryDraft {
			t.Fatalf("a draft was stored from an unreadable answer: %+v", entry)
		}
	}
}

func TestDraftQuestionsOnly(t *testing.T) {
	h := draftHarness(t)
	conversation := h.conversation(alice, ModeDefault)
	h.model.steps = []step{answerJSON(`{"summary":"?","questions":["哪個版本？","何時發布？"],"nodes":[]}`, provider.StopEndTurn, nil)}
	h.start(draftTurn(conversation)).finish()
	entry, data := draftEntry(t, h, conversation)
	if len(data.Draft.Questions) != 2 || len(data.Draft.Nodes) != 0 || len(data.Issues) != 0 {
		t.Fatalf("data = %+v", data)
	}
	if !strings.Contains(entry.Message, "I asked before drafting") {
		t.Errorf("message = %s", entry.Message)
	}
}

func TestDraftRefineSendsThePersonsEdits(t *testing.T) {
	h := draftHarness(t)
	conversation := h.conversation(alice, ModeDefault)
	req := draftTurn(conversation)
	req.Text = "發布前加一個審查"
	req.RefineOf = "e1"
	// The person linked a node the listing does not show; it stays valid.
	req.Previous = &Draft{Summary: "s", Nodes: []DraftNode{{Key: "n1", Title: "Docs", Kind: "task", After: []string{"existing:n-far"}}}}
	h.model.steps = []step{answerJSON(`{"summary":"s","nodes":[{"key":"n1","title":"Docs","kind":"task","after":["existing:n-far"]},`+
		`{"key":"n2","title":"Review","kind":"gate","after":["n1"]}]}`, provider.StopEndTurn, func(t *testing.T, req provider.Request) {
		if user := req.Messages[0].Blocks[0].Text; !strings.Contains(user, `"existing:n-far"`) || !strings.Contains(user, "發布前加一個審查") {
			t.Errorf("refine message:\n%s", user)
		}
	})}
	h.start(req).finish()
	if _, data := draftEntry(t, h, conversation); hasErrors(data.Issues) || data.RefineOf != "e1" || len(data.Draft.Nodes) != 2 {
		t.Fatalf("refined = %+v", data)
	}
}

func TestDraftIntentValidation(t *testing.T) {
	h := draftHarness(t)
	conversation := h.conversation(alice, ModeDefault)
	for name, mutate := range map[string]func(*TurnRequest){
		"unknown intent":        func(r *TurnRequest) { r.Intent = "build" },
		"refine on an ask turn": func(r *TurnRequest) { r.Intent = IntentAsk },
		"too large a draft":     func(r *TurnRequest) { r.Previous = &Draft{Nodes: make([]DraftNode, MaxDraftNodes+1)} },
		"bad refineOf":          func(r *TurnRequest) { r.RefineOf = "../x" },
	} {
		req := draftTurn(conversation)
		req.Previous = &Draft{}
		mutate(&req)
		if err := h.service.Turn(context.Background(), req, func(Event) {}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestSetDraftState(t *testing.T) {
	h := draftHarness(t)
	conversation := h.conversation(alice, ModeDefault)
	h.model.steps = []step{answerJSON(goodDraft, provider.StopEndTurn, nil)}
	h.start(draftTurn(conversation)).finish()
	entry, _ := draftEntry(t, h, conversation)
	ctx := context.Background()
	if _, err := h.service.SetDraftState(ctx, bob.ID, conversation.ID, entry.ID, DraftCommitted, "b1", nil, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another account: %v", err)
	}
	if _, err := h.service.SetDraftState(ctx, alice.ID, conversation.ID, entry.ID, "published", "", nil, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad state: %v", err)
	}
	updated, err := h.service.SetDraftState(ctx, alice.ID, conversation.ID, entry.ID, DraftCommitted, "b-1", []string{"n-a", "n-b", "n-c"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var data DraftData
	_ = json.Unmarshal([]byte(updated.Data), &data)
	if data.State != DraftCommitted || data.BatchID != "b-1" || len(data.CreatedIDs) != 3 || len(data.Draft.Nodes) != 3 {
		t.Fatalf("committed data = %+v", data)
	}
	// A non-draft entry is not a draft.
	_, entries, _ := h.service.Conversation(ctx, alice, conversation.ID)
	if _, err := h.service.SetDraftState(ctx, alice.ID, conversation.ID, entries[0].ID, DraftDiscarded, "", nil, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("user entry as draft: %v", err)
	}
}
