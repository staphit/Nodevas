package copilot

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"nodevas/internal/copilot/provider"
	"nodevas/internal/copilot/toolbridge"
)

// proposeCall is the chat model calling propose_nodes, after checking it was
// offered as a read tool.
func proposeCall(id, description string) step {
	return func(t *testing.T, ctx context.Context, req provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
		offered := false
		for _, tool := range req.Tools {
			offered = offered || tool.Name == proposeNodesTool
		}
		if !offered {
			t.Errorf("propose_nodes is not offered in an Ask turn")
		}
		input, _ := json.Marshal(map[string]string{"description": description})
		return callWith(id, proposeNodesTool, string(input))(t, ctx, req, onDelta)
	}
}

// An Ask turn that turns into "make these" hands off to the drafter: the
// draft lands as a draft card in the same conversation, nothing is written
// to the board, and nobody is asked, in bypass and in plan mode alike.
func TestProposeNodesDraftsFromAChatTurn(t *testing.T) {
	for _, mode := range []Mode{ModeBypass, ModeDefault, ModePlan} {
		t.Run(string(mode), func(t *testing.T) {
			h := draftHarness(t)
			conversation := h.conversation(alice, mode)
			h.model.steps = []step{
				proposeCall("c1", "寫文件、測試、發布"),
				answerJSON(goodDraft, provider.StopEndTurn, func(t *testing.T, req provider.Request) {
					if req.ResponseSchema == nil || len(req.Tools) != 0 {
						t.Errorf("the drafter's call: schema %v, tools %d", req.ResponseSchema, len(req.Tools))
					}
					user := req.Messages[0].Blocks[0].Text
					// The turn's selection and language reach the drafter.
					if !strings.Contains(user, "- n-ship | Ship it") || !strings.Contains(user, "zh-TW") ||
						!strings.HasSuffix(user, "寫文件、測試、發布\n") {
						t.Errorf("drafter's message:\n%s", user)
					}
				}),
				expectResult("c1", false, "Drafted 3 nodes as a draft card"),
			}
			request := h.turn(conversation, mode)
			request.Text = "幫我把上線拆成步驟"
			request.Context = TurnContext{SelectedNodeIDs: []string{"n-ship"}, Language: "zh-TW"}
			events := h.start(request).finish()
			var drafts int
			for _, event := range events {
				switch {
				case event.Type == EventPermissionRequest:
					t.Fatalf("propose_nodes asked the person: %+v", event)
				case event.Type == EventEntry && event.Entry.Kind == EntryDraft:
					drafts++
				}
			}
			if drafts != 1 || last(events).Type != EventTurnFinished {
				t.Fatalf("draft entry events = %d; events %v", drafts, types(events))
			}
			entry, data := draftEntry(t, h, conversation)
			if len(data.Draft.Nodes) != 3 || data.State != DraftOpen || data.Model != "claude-test" || entry.Message != "" {
				t.Fatalf("draft = %+v, message %q", data, entry.Message)
			}
			for _, call := range h.bridge.called() {
				if call != "get_graph_outline" && call != "get_node" {
					t.Errorf("propose_nodes reached the board: %s", call)
				}
			}
			if got := toolStatuses(t, h, conversation); len(got) != 1 || got[0] != "propose_nodes:ok" {
				t.Fatalf("tool entries = %v", got)
			}
			// The next turn's history still alternates: the draft card
			// carries no message between the call and its result.
			_, entries, _ := h.service.Conversation(context.Background(), alice, conversation.ID)
			history := historyFrom(entries)
			for i := 1; i < len(history); i++ {
				if history[i].Role == history[i-1].Role {
					t.Fatalf("history roles repeat at %d: %+v", i, history)
				}
			}
			finished := last(events)
			if finished.Usage == nil || finished.Usage.InputTokens < 100 {
				t.Errorf("the drafter's tokens are not counted: %+v", finished.Usage)
			}
		})
	}
}

// A description the drafter cannot draft comes back as its questions, still
// as a card; an empty description is refused without a model call.
func TestProposeNodesQuestionsAndBadInput(t *testing.T) {
	h := draftHarness(t)
	conversation := h.conversation(alice, ModeDefault)
	h.model.steps = []step{
		proposeCall("c1", "弄一下"),
		answerJSON(`{"summary":"","nodes":[],"questions":["要做什麼？"]}`, provider.StopEndTurn, nil),
		expectResult("c1", false, "要做什麼？"),
	}
	h.start(h.turn(conversation, ModeDefault)).finish()
	if _, data := draftEntry(t, h, conversation); len(data.Draft.Questions) != 1 {
		t.Fatalf("questions draft = %+v", data)
	}

	h = draftHarness(t)
	conversation = h.conversation(alice, ModeDefault)
	h.model.steps = []step{
		callWith("c1", proposeNodesTool, `{"description":"   "}`),
		expectResult("c1", true, "description"),
	}
	h.start(h.turn(conversation, ModeDefault)).finish()
	if h.model.callCount() != 2 {
		t.Fatalf("model calls = %d", h.model.callCount())
	}
}

// propose_nodes needs no router: it is offered with the bridge alone, as a
// read tool that never asks.
func TestProposeNodesIsAReadTool(t *testing.T) {
	tools, info := collectTools(nil, nil, nil, true)
	ti, ok := info[proposeNodesTool]
	if !ok || ti.kind != toolbridge.KindRead || ti.alwaysAsk || ti.server || !ti.propose || len(tools) != 1 {
		t.Fatalf("propose_nodes info = %+v, tools %d", ti, len(tools))
	}
	if _, without := collectTools(nil, nil, nil, false); len(without) != 0 {
		t.Fatal("propose_nodes offered in a draft turn")
	}
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(tools[0].Schema, &schema); err != nil || len(schema.Required) != 1 || schema.Required[0] != "description" {
		t.Fatalf("schema = %s", tools[0].Schema)
	}
}

func TestSystemPromptPrefersProposeNodes(t *testing.T) {
	prompt := systemPrompt("Bea", "main", ModeDefault, TurnContext{}, nil, "text")
	for _, want := range []string{"propose_nodes", "only when the person asked"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("authoring guidance lacks %q:\n%s", want, prompt)
		}
	}
	if browser := browserSystemPrompt("Bea", "main", ModeDefault, TurnContext{}, nil, "text"); !strings.Contains(browser, "propose_nodes") {
		t.Errorf("browser prompt lacks propose_nodes:\n%s", browser)
	}
}
