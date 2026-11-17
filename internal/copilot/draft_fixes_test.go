package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"nodevas/internal/copilot/provider"
	"nodevas/internal/runnerwire"
)

// C2: the draft-state call may carry the person's final draft, which then
// replaces the stored one and marks it edited, so a reload shows what they
// had; the same bounds as a refine's previous draft apply.
func TestSetDraftStateKeepsThePersonsDraft(t *testing.T) {
	h := draftHarness(t)
	conversation := h.conversation(alice, ModeDefault)
	h.model.steps = []step{answerJSON(goodDraft, provider.StopEndTurn, nil)}
	h.start(draftTurn(conversation)).finish()
	entry, _ := draftEntry(t, h, conversation)
	ctx := context.Background()

	edited := &Draft{Summary: "  two \n steps ", Nodes: []DraftNode{
		{Key: "n1", Title: "寫使用文件\n（含範例）", Kind: "task", Body: strings.Repeat("長", 900)},
		{Key: "n9", Title: "Ship", Kind: "gate", After: []string{" n1 "}},
	}}
	updated, err := h.service.SetDraftState(ctx, alice.ID, conversation.ID, entry.ID, DraftCommitted, "b-1",
		[]string{"x1", "x2"}, edited)
	if err != nil {
		t.Fatal(err)
	}
	var data DraftData
	_ = json.Unmarshal([]byte(updated.Data), &data)
	nodes := data.Draft.Nodes
	if !data.Edited || data.State != DraftCommitted || len(nodes) != 2 || nodes[1].Key != "n9" ||
		nodes[0].Title != "寫使用文件 （含範例）" || utf8.RuneCountInString(nodes[0].Body) != maxDraftBodyRunes ||
		nodes[1].After[0] != "n1" || data.Draft.Summary != "two steps" {
		t.Fatalf("data = %+v", data)
	}

	// Without a draft the stored one stays.
	again, err := h.service.SetDraftState(ctx, alice.ID, conversation.ID, entry.ID, DraftOpen, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(again.Data), &data)
	if len(data.Draft.Nodes) != 2 || data.Draft.Nodes[1].Key != "n9" || !data.Edited {
		t.Fatalf("data after a state-only call = %+v", data)
	}

	tooMany := &Draft{}
	for i := 0; i <= MaxDraftNodes; i++ {
		tooMany.Nodes = append(tooMany.Nodes, DraftNode{Key: fmt.Sprintf("k%d", i), Title: "t", Kind: "task"})
	}
	if _, err := h.service.SetDraftState(ctx, alice.ID, conversation.ID, entry.ID, DraftOpen, "", nil, tooMany); !errors.Is(err, ErrInvalid) {
		t.Fatalf("41 nodes on a copilot draft: %v", err)
	}
	badKey := &Draft{Nodes: []DraftNode{{Key: strings.Repeat("k", 65), Title: "t", Kind: "task"}}}
	if _, err := h.service.SetDraftState(ctx, alice.ID, conversation.ID, entry.ID, DraftOpen, "", nil, badKey); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an oversized key: %v", err)
	}

	// A coding agent's plan holds up to 100.
	plan := appendPlanDraft(t, h, conversation, "node-0042", DraftOpen, nil)
	hundred := &Draft{}
	for i := 0; i < MaxPlanDraftNodes; i++ {
		hundred.Nodes = append(hundred.Nodes, DraftNode{Key: fmt.Sprintf("t%d", i), Title: "t", Kind: "task"})
	}
	if _, err := h.service.SetDraftState(ctx, alice.ID, conversation.ID, plan.ID, DraftCommitted, "b-2", nil, hundred); err != nil {
		t.Fatalf("100 nodes on a plan draft: %v", err)
	}
	hundred.Nodes = append(hundred.Nodes, DraftNode{Key: "t-extra", Title: "t", Kind: "task"})
	if _, err := h.service.SetDraftState(ctx, alice.ID, conversation.ID, plan.ID, DraftCommitted, "b-2", nil, hundred); !errors.Is(err, ErrInvalid) {
		t.Fatalf("101 nodes on a plan draft: %v", err)
	}
}

// Finding 15: the follow-up's entries are written together or not at all,
// so a retry after a failed write cannot leave a second event note.
func TestAppendEntriesIsAllOrNothing(t *testing.T) {
	h := newHarness(t, nil)
	conversation := h.conversation(alice, ModeDefault)
	ctx := context.Background()
	_, err := h.service.store.AppendEntries(ctx, []Entry{
		{ConversationID: conversation.ID, Kind: EntryEvent, Text: "Plan ready"},
		// Postgres refuses a NUL byte in text: the second insert fails.
		{ConversationID: conversation.ID, Kind: EntryDraft, Text: "bad\x00text"},
	})
	if err == nil {
		t.Fatal("an entry Postgres cannot store was accepted")
	}
	if entries := entriesOf(t, h, conversation); len(entries) != 0 {
		t.Fatalf("a failed batch left entries: %+v", entries)
	}
	stored, err := h.service.store.AppendEntries(ctx, []Entry{
		{ConversationID: conversation.ID, Kind: EntryEvent, Text: "a"},
		{ConversationID: conversation.ID, Kind: EntryDraft, Text: "b"},
	})
	if err != nil || len(stored) != 2 || stored[0].Seq != 1 || stored[1].Seq != 2 || stored[0].ID == stored[1].ID {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
}

// Finding 16: a planner's longer task title is kept (the card is not the
// drafter's 60-character one); only a title past the plan bound is cut, and
// then the card says so.
func TestPlanDraftKeepsLongTitles(t *testing.T) {
	seventy := strings.Repeat("長", 70)
	huge := strings.Repeat("x", maxPlanDraftTitleRunes+50)
	capture := runnerwire.PlanDraft{Nodes: []runnerwire.PlanDraftNode{
		{Key: "a", Title: seventy}, {Key: "b", Title: huge},
	}}
	data := planDraftData(capture, draftPlanRun(runnerwire.RunSucceeded), "node-0042", "Login page")
	if data.Draft.Nodes[0].Title != seventy {
		t.Fatalf("a 70-character title became %q", data.Draft.Nodes[0].Title)
	}
	if n := utf8.RuneCountInString(data.Draft.Nodes[1].Title); n != maxPlanDraftTitleRunes {
		t.Fatalf("an oversized title is %d runes", n)
	}
	if len(data.Issues) != 1 || data.Issues[0].Code != IssueTitleClipped || data.Issues[0].Key != "b" ||
		data.Issues[0].Field != "title" || data.Issues[0].Severity != IssueWarning {
		t.Fatalf("issues = %+v", data.Issues)
	}
}

// Finding 39: the model-facing words describe the draft flow, not the old
// direct write and the autopilot question.
func TestBreakdownWordingMatchesTheDraftFlow(t *testing.T) {
	prompt := systemPrompt("Bea", "main", ModeDefault, TurnContext{}, []string{"start_breakdown", "start_autopilot"}, "text")
	for _, unwanted := range []string{"breaks the goal into tasks on the board", "asked whether to start the autopilot"} {
		if strings.Contains(prompt, unwanted) {
			t.Errorf("system prompt still says %q", unwanted)
		}
	}
	if !strings.Contains(prompt, "drafts the goal's tasks") {
		t.Errorf("system prompt lacks the draft wording:\n%s", prompt)
	}

	api := standardAPI()
	env := toolEnv(api)
	env.draftPlans = true
	env.followup = func(string, string, *Revision) error { return nil }
	result := runStartBreakdown(context.Background(), env, json.RawMessage(`{"requirement":"Add login"}`))
	if result.IsError {
		t.Fatalf("start_breakdown: %s", result.Output)
	}
	var out struct {
		Note string `json:"note"`
	}
	_ = json.Unmarshal([]byte(result.Output), &out)
	if strings.Contains(out.Note, "whether to start the autopilot") || !strings.Contains(out.Note, "draft card") {
		t.Fatalf("draft note = %q", out.Note)
	}
	// Without drafts the old follow-up wording stays.
	plain := toolEnv(standardAPI())
	plain.followup = func(string, string, *Revision) error { return nil }
	result = runStartBreakdown(context.Background(), plain, json.RawMessage(`{"requirement":"Add login"}`))
	_ = json.Unmarshal([]byte(result.Output), &out)
	if !strings.Contains(out.Note, "whether to start the autopilot") {
		t.Fatalf("plain note = %q", out.Note)
	}
}
