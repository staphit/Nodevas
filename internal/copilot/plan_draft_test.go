package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"nodevas/internal/copilot/provider"
	"nodevas/internal/runnerwire"
)

// draftPlanRun is alice's draft plan run on the goal (P6).
func draftPlanRun(status string) RunInfo {
	run := planRun(status)
	run.Draft, run.Agent, run.Model = true, "claude", "sonnet"
	return run
}

// threeTaskCapture is what the hub recorded: one task waits on an existing
// node, one on the goal (dropped: the goal is linked at Create) and on the
// first, one has a body longer than the card keeps.
func threeTaskCapture() runnerwire.PlanDraft {
	return runnerwire.PlanDraft{Calls: 1, Nodes: []runnerwire.PlanDraftNode{
		{Key: "t1", Title: "Add the users table", Kind: "task", Body: "Migration 0042.",
			Tags: []string{"goal:node-0042", "db"}, Acceptance: []string{"the table exists", "it has an index"},
			Priority: "high", DependsOn: []string{"n-3"}},
		{Key: "t2", Title: "Write the login form", DependsOn: []string{"t1", "node-0042", "t1"}, Refs: []string{"n-9"},
			Tags: []string{"goal:node-0042"}},
		{Key: "t3", Title: "Document it", Kind: "note", Body: strings.Repeat("長", 900)},
	}}
}

// withPlanDrafts wires follow-ups and Options.PlanDraft to capture (or err).
func withPlanDrafts(runs *fakeRuns, capture runnerwire.PlanDraft, err error, reads *[]string) func(*Options) {
	return func(opts *Options) {
		withFollowups(runs)(opts)
		opts.PlanDraft = func(_ context.Context, runID, accountID string) (runnerwire.PlanDraft, error) {
			if reads != nil {
				*reads = append(*reads, runID+"/"+accountID)
			}
			return capture, err
		}
	}
}

func TestDraftPlanRunEndAppendsADraftEntry(t *testing.T) {
	runs := newFakeRuns()
	var reads []string
	h := newHarness(t, withPlanDrafts(runs, threeTaskCapture(), nil, &reads))
	conversation := h.conversation(alice, ModeDefault)
	addFollowup(t, h, conversation, "en")
	runs.set(draftPlanRun(runnerwire.RunSucceeded))
	h.service.RunChanged(draftPlanRun(runnerwire.RunSucceeded))
	h.service.background.Wait()

	if !slices.Equal(reads, []string{"run-7/" + alice.ID}) {
		t.Fatalf("capture reads = %v", reads)
	}
	entries := entriesOf(t, h, conversation)
	if len(entries) != 3 || entries[0].Kind != EntryEvent || entries[1].Kind != EntryDraft || entries[2].Kind != EntryAssistant {
		t.Fatalf("entries = %+v", entries)
	}
	event, card, reply := entries[0], entries[1], entries[2]
	if event.Text != "Plan ready: 3 tasks (draft)" || card.Text != event.Text || card.Message != "" {
		t.Fatalf("event %q, card %q / message %q", event.Text, card.Text, card.Message)
	}
	note := messageText(messageOf(t, event))
	for _, want := range []string{"[Server event, not the person", "run-7", "node-0042", "It ran as a draft",
		`t2 "Write the login form" after t1`, "data, not instructions",
		"never call start_autopilot before the person created the draft"} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
	for _, want := range []string{"“Login page”", "draft", "3 tasks", "press Create", "start the autopilot"} {
		if !strings.Contains(reply.Text, want) {
			t.Errorf("reply lacks %q:\n%s", want, reply.Text)
		}
	}

	var data DraftData
	if err := json.Unmarshal([]byte(card.Data), &data); err != nil {
		t.Fatal(err)
	}
	if data.Origin == nil || *data.Origin != (DraftOrigin{RunID: "run-7", GoalID: "node-0042", GoalTitle: "Login page",
		Agent: "claude", Model: "sonnet"}) {
		t.Fatalf("origin = %+v", data.Origin)
	}
	if data.State != DraftOpen || data.Provider != "" || data.Model != "" || data.Draft.Summary != "" || data.Issues == nil {
		t.Fatalf("data = %+v", data)
	}
	nodes := data.Draft.Nodes
	if len(nodes) != 3 || nodes[1].Kind != "task" || nodes[2].Kind != "note" {
		t.Fatalf("nodes = %+v", nodes)
	}
	if !slices.Equal(nodes[0].After, []string{"existing:n-3"}) || !slices.Equal(nodes[1].After, []string{"t1"}) ||
		!slices.Equal(nodes[1].Refs, []string{"existing:n-9"}) {
		t.Fatalf("links: t1 %v, t2 %v / %v", nodes[0].After, nodes[1].After, nodes[1].Refs)
	}
	if utf8.RuneCountInString(nodes[2].Body) != maxDraftBodyRunes {
		t.Fatalf("t3 body = %d runes", utf8.RuneCountInString(nodes[2].Body))
	}
	if len(data.Issues) != 1 || data.Issues[0] != (DraftIssue{Key: "t3", Field: "body", Severity: IssueWarning,
		Code: IssueBodyClipped, Message: "the body was cut to 600 characters"}) {
		t.Fatalf("issues = %+v", data.Issues)
	}
	t1 := data.Extras["t1"]
	if !slices.Equal(t1.Tags, []string{"goal:node-0042", "db"}) || len(t1.Acceptance) != 2 || t1.Priority != "high" {
		t.Fatalf("extras = %+v", data.Extras)
	}
	if _, ok := data.Extras["t3"]; ok || len(data.Extras) != 2 {
		t.Fatalf("extras for a node with none: %+v", data.Extras)
	}
	if left := pendingFollowups(t, h); len(left) != 0 {
		t.Fatalf("still pending: %+v", left)
	}
	if got := runs.notifications(); len(got) != 1 {
		t.Fatalf("notifications = %v", got)
	}

	// In Taiwan Mandarin too.
	h2 := newHarness(t, withPlanDrafts(runs, threeTaskCapture(), nil, nil))
	zh := h2.conversation(alice, ModeDefault)
	addFollowup(t, h2, zh, "zh-TW")
	h2.service.RunChanged(draftPlanRun(runnerwire.RunSucceeded))
	h2.service.background.Wait()
	if entries := entriesOf(t, h2, zh); len(entries) != 3 || entries[0].Text != "計畫完成：3 個任務（草稿）" ||
		!strings.Contains(entries[2].Text, "「Login page」的 coding agent 計畫已經是草稿：共 3 個任務") {
		t.Fatalf("zh entries = %+v", entries)
	}
}

func TestDraftPlanRunWithAnEmptyCaptureSaysNoTasks(t *testing.T) {
	runs := newFakeRuns() // the board's goal has tasks: they are not this run's
	h := newHarness(t, withPlanDrafts(runs, runnerwire.PlanDraft{}, nil, nil))
	conversation := h.conversation(alice, ModeDefault)
	addFollowup(t, h, conversation, "en")
	runs.set(draftPlanRun(runnerwire.RunSucceeded))
	h.service.RunChanged(draftPlanRun(runnerwire.RunSucceeded))
	h.service.background.Wait()
	entries := entriesOf(t, h, conversation)
	if len(entries) != 2 || entries[0].Kind != EntryEvent || entries[1].Kind != EntryAssistant ||
		!strings.Contains(entries[1].Text, "added no tasks under the goal") {
		t.Fatalf("entries = %+v", entries)
	}
}

// A draft run that recorded its plan and then failed or was cancelled still
// hands the plan over, as a draft marked as possibly incomplete; one that
// recorded nothing keeps the failure reply.
func TestDraftPlanRunEndedEarlyStillDeliversItsCapture(t *testing.T) {
	for _, tc := range []struct{ status, lang, shown, reply string }{
		{runnerwire.RunFailed, "en", "Plan ended early: 3 tasks (draft, may be incomplete)", "ended early, but it recorded a draft of 3 tasks"},
		{runnerwire.RunCancelled, "en", "Plan ended early: 3 tasks (draft, may be incomplete)", "discard it and I can break the goal down again"},
		{runnerwire.RunFailed, "zh-TW", "計畫提前結束：3 個任務（草稿，可能不完整）", "已經記下 3 個任務的草稿"},
	} {
		runs := newFakeRuns()
		h := newHarness(t, withPlanDrafts(runs, threeTaskCapture(), nil, nil))
		conversation := h.conversation(alice, ModeDefault)
		addFollowup(t, h, conversation, tc.lang)
		run := draftPlanRun(tc.status)
		run.Error = "the agent timed out"
		runs.set(run)
		h.service.RunChanged(run)
		h.service.background.Wait()

		entries := entriesOf(t, h, conversation)
		if len(entries) != 3 || entries[0].Kind != EntryEvent || entries[1].Kind != EntryDraft || entries[2].Kind != EntryAssistant {
			t.Fatalf("%s: entries = %+v", tc.status, entries)
		}
		if entries[0].Text != tc.shown || !strings.Contains(entries[2].Text, tc.reply) {
			t.Errorf("%s/%s: shown %q, reply %q", tc.status, tc.lang, entries[0].Text, entries[2].Text)
		}
		note := messageText(messageOf(t, entries[0]))
		for _, want := range []string{"Error: the agent timed out", "ended early", "may be incomplete",
			"never call start_autopilot before the person created the draft"} {
			if !strings.Contains(note, want) {
				t.Errorf("%s: note lacks %q:\n%s", tc.status, want, note)
			}
		}
		var data DraftData
		if err := json.Unmarshal([]byte(entries[1].Data), &data); err != nil || data.State != DraftOpen ||
			len(data.Draft.Nodes) != 3 || data.Origin == nil {
			t.Fatalf("%s: card = %+v (%v)", tc.status, data, err)
		}
		if left := pendingFollowups(t, h); len(left) != 0 {
			t.Fatalf("%s: still pending: %+v", tc.status, left)
		}
	}

	// Nothing recorded: the failure reply, as today.
	runs := newFakeRuns()
	h := newHarness(t, withPlanDrafts(runs, runnerwire.PlanDraft{}, nil, nil))
	conversation := h.conversation(alice, ModeDefault)
	addFollowup(t, h, conversation, "en")
	runs.set(draftPlanRun(runnerwire.RunFailed))
	h.service.RunChanged(draftPlanRun(runnerwire.RunFailed))
	h.service.background.Wait()
	if entries := entriesOf(t, h, conversation); len(entries) != 2 || entries[1].Kind != EntryAssistant ||
		!strings.Contains(entries[1].Text, "failed") {
		t.Fatalf("empty failed draft: %+v", entries)
	}
}

func TestDraftPlanCaptureUnreadableStaysPending(t *testing.T) {
	runs := newFakeRuns()
	h := newHarness(t, withPlanDrafts(runs, runnerwire.PlanDraft{}, errors.New("database is down"), nil))
	conversation := h.conversation(alice, ModeDefault)
	addFollowup(t, h, conversation, "en")
	runs.set(draftPlanRun(runnerwire.RunSucceeded))
	h.service.RunChanged(draftPlanRun(runnerwire.RunSucceeded))
	h.service.background.Wait()
	if entries := entriesOf(t, h, conversation); len(entries) != 0 {
		t.Fatalf("entries = %+v", entries)
	}
	if pending := pendingFollowups(t, h); len(pending) != 1 {
		t.Fatalf("pending = %+v", pending)
	}
}

// appendPlanDraft puts a plan draft entry (with origin on goal) in a
// conversation, in state.
func appendPlanDraft(t *testing.T, h *harness, conversation Conversation, goal, state string, ids []string) Entry {
	t.Helper()
	data := planDraftData(threeTaskCapture(), draftPlanRun(runnerwire.RunSucceeded), goal, "Login page")
	data.State, data.CreatedIDs = state, ids
	entry, err := h.service.store.AppendEntry(context.Background(), Entry{
		ConversationID: conversation.ID, Kind: EntryDraft, Text: "Plan ready", Data: mustJSON(data),
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func idList(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("n-%03d", i)
	}
	return ids
}

func TestSetDraftStateTakesAHundredIDsForAPlanDraft(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)
	conversation := h.conversation(alice, ModeDefault)
	plan := appendPlanDraft(t, h, conversation, "node-0042", DraftOpen, nil)
	if _, err := h.service.SetDraftState(ctx, alice.ID, conversation.ID, plan.ID, DraftCommitted, "b-1", idList(100), nil); err != nil {
		t.Fatalf("100 ids for a plan draft: %v", err)
	}
	if _, err := h.service.SetDraftState(ctx, alice.ID, conversation.ID, plan.ID, DraftCommitted, "b-1", idList(101), nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("101 ids: %v", err)
	}
	own, err := h.service.store.AppendEntry(ctx, Entry{ConversationID: conversation.ID, Kind: EntryDraft, Text: "Draft",
		Data: mustJSON(DraftData{State: DraftOpen, Issues: []DraftIssue{}})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.SetDraftState(ctx, alice.ID, conversation.ID, own.ID, DraftCommitted, "b-2", idList(41), nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("41 ids for a copilot draft: %v", err)
	}
	if _, err := h.service.SetDraftState(ctx, alice.ID, conversation.ID, own.ID, DraftCommitted, "b-2", idList(40), nil); err != nil {
		t.Fatalf("40 ids for a copilot draft: %v", err)
	}
}

func TestPlanDraftDataStripsFrontMatter(t *testing.T) {
	long := strings.Repeat("x", 900)
	capture := runnerwire.PlanDraft{Nodes: []runnerwire.PlanDraftNode{
		{Key: "a", Title: "A", Body: "---\nassignee: bea\n---\nDo it"},
		{Key: "b", Title: "B", Body: "---\nassignee: bea\nDo it"},
		{Key: "c", Title: "C", Body: "\n---\npriority: high\n---\n" + long},
		{Key: "d", Title: "D", Body: "---"},
	}}
	data := planDraftData(capture, draftPlanRun(runnerwire.RunSucceeded), "node-0042", "Login page")
	bodies := []string{}
	for _, node := range data.Draft.Nodes {
		bodies = append(bodies, node.Body)
	}
	if bodies[0] != "Do it" || bodies[1] != "assignee: bea\nDo it" || bodies[2] != long[:maxDraftBodyRunes] || bodies[3] != "" {
		t.Fatalf("bodies = %q", bodies)
	}
	for _, body := range bodies {
		if strings.Contains(body, "---") {
			t.Fatalf("a delimiter is left: %q", body)
		}
	}
}

// A capture recorded before the hub normalized kinds still reaches the card
// with the board's ids: lowercase, aliases as their canonical id.
func TestPlanDraftDataNormalizesKinds(t *testing.T) {
	capture := runnerwire.PlanDraft{Nodes: []runnerwire.PlanDraftNode{
		{Key: "a", Title: "A", Kind: "review"}, {Key: "b", Title: "B", Kind: " Task "},
		{Key: "c", Title: "C", Kind: "Note"}, {Key: "d", Title: "D", Kind: "Decision"}, {Key: "e", Title: "E"},
	}}
	data := planDraftData(capture, draftPlanRun(runnerwire.RunSucceeded), "node-0042", "Login page")
	var kinds []string
	for _, node := range data.Draft.Nodes {
		kinds = append(kinds, node.Kind)
	}
	if !slices.Equal(kinds, []string{"gate", "task", "note", "choice", "task"}) {
		t.Fatalf("kinds = %q", kinds)
	}
}

func TestHistoryShowsThePlanDraftState(t *testing.T) {
	event := Entry{Kind: EntryEvent, Message: mustJSON(provider.Message{Role: provider.RoleUser,
		Blocks: []provider.Block{{Type: provider.BlockText, Text: "[Server event, not the person: plan]"}}})}
	reply := Entry{Kind: EntryAssistant, Message: mustJSON(textMessage("It is a draft."))}
	card := func(state string, ids []string) Entry {
		data := planDraftData(threeTaskCapture(), draftPlanRun(runnerwire.RunSucceeded), "node-0042", "Login page")
		data.State, data.CreatedIDs = state, ids
		return Entry{Kind: EntryDraft, Data: mustJSON(data)}
	}
	for _, tc := range []struct {
		card Entry
		want string
	}{
		{card(DraftOpen, nil), "is still open now"},
		{card(DraftCommitted, []string{"n-1", "n-2", "n-3"}), "its 3 tasks are on the board now (ids n-1, n-2, n-3)"},
		{card(DraftCommitted, idList(25)), "(ids n-000, n-001"},
		{card(DraftDiscarded, nil), "the person discarded this plan draft"},
	} {
		history := historyFrom([]Entry{event, tc.card, reply})
		if len(history) != 2 || history[0].Role != provider.RoleUser || history[1].Role != provider.RoleAssistant {
			t.Fatalf("history = %+v", history)
		}
		text := messageText(history[0])
		if !strings.Contains(text, "[Server event, not the person: plan]") || !strings.Contains(text, tc.want) ||
			strings.Index(text, "[Server event") > strings.Index(text, "[Server note") {
			t.Errorf("user message %q lacks %q", text, tc.want)
		}
	}
	if text := messageText(historyFrom([]Entry{event, card(DraftCommitted, idList(25)), reply})[0]); !strings.Contains(text, "and 5 more") {
		t.Errorf("25 ids: %q", text)
	}
	// A copilot draft (no origin) still yields its stored message only.
	own := Entry{Kind: EntryDraft, Message: mustJSON(textMessage("I proposed a draft of 2 new nodes")),
		Data: mustJSON(DraftData{State: DraftOpen})}
	history := historyFrom([]Entry{own})
	if len(history) != 1 || history[0].Role != provider.RoleAssistant || messageText(history[0]) != "I proposed a draft of 2 new nodes" {
		t.Fatalf("copilot draft history = %+v", history)
	}
	if history := historyFrom([]Entry{{Kind: EntryDraft, Data: mustJSON(DraftData{State: DraftOpen})}}); len(history) != 0 {
		t.Fatalf("a draft with neither message nor origin = %+v", history)
	}
}

func TestPlanDraftBusy(t *testing.T) {
	ctx := context.Background()
	runs := newFakeRuns()
	h := newHarness(t, withPlanDrafts(runs, runnerwire.PlanDraft{}, nil, nil))
	busy := func(account, project, goal string) string {
		t.Helper()
		reason, err := h.service.planDraftBusy(ctx, account, project, goal)
		if err != nil {
			t.Fatal(err)
		}
		return reason
	}
	other := h.conversation(alice, ModeDefault)
	if got := busy(alice.ID, "demo", "node-0042"); got != "" {
		t.Fatalf("nothing yet: %q", got)
	}
	card := appendPlanDraft(t, h, other, "node-0042", DraftOpen, nil)
	if got := busy(alice.ID, "demo", "node-0042"); !strings.Contains(got, "still a draft") {
		t.Fatalf("open draft in another conversation: %q", got)
	}
	if busy(alice.ID, "demo", "node-0001") != "" || busy(bob.ID, "demo", "node-0042") != "" ||
		busy(alice.ID, "elsewhere", "node-0042") != "" {
		t.Fatal("another goal's, account's or project's view is busy")
	}
	for _, state := range []string{DraftCommitted, DraftDiscarded} {
		if _, err := h.service.SetDraftState(ctx, alice.ID, other.ID, card.ID, state, "", nil, nil); err != nil {
			t.Fatal(err)
		}
		if got := busy(alice.ID, "demo", "node-0042"); got != "" {
			t.Fatalf("%s draft: %q", state, got)
		}
	}
	// Another project's open draft of the same goal id, or another
	// account's, does not count.
	elsewhere, err := h.service.CreateConversation(ctx, alice, "elsewhere", ModeDefault)
	if err != nil {
		t.Fatal(err)
	}
	appendPlanDraft(t, h, elsewhere, "node-0042", DraftOpen, nil)
	appendPlanDraft(t, h, h.conversation(bob, ModeDefault), "node-0042", DraftOpen, nil)
	if got := busy(alice.ID, "demo", "node-0042"); got != "" {
		t.Fatalf("another project's or account's draft: %q", got)
	}

	// A pending breakdown whose run is a draft.
	addFollowup(t, h, other, "en")
	runs.set(draftPlanRun(runnerwire.RunRunning))
	if got := busy(alice.ID, "demo", "node-0042"); !strings.Contains(got, "still drafting") || !strings.Contains(got, "run-7") {
		t.Fatalf("running draft: %q", got)
	}
	runs.set(planRun(runnerwire.RunRunning))
	if got := busy(alice.ID, "demo", "node-0042"); got != "" {
		t.Fatalf("a plain plan run: %q", got)
	}
	runs.mu.Lock()
	delete(runs.runs, "run-7")
	runs.mu.Unlock()
	if _, err := h.service.planDraftBusy(ctx, alice.ID, "demo", "node-0042"); err == nil {
		t.Fatal("a run that cannot be read was guessed")
	}
}
