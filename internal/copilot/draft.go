package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"nodevas/internal/copilot/provider"
	"nodevas/internal/copilot/toolbridge"
	nodevasmcp "nodevas/internal/mcp"
)

// Turn intents. A draft turn does not run the chat tool loop: it reads the
// board, makes one structured model call (plus at most one repair) and
// stores what came back as a draft the person previews, edits and creates
// themselves. The model never writes to the board on this path.
const (
	IntentAsk   = "ask"
	IntentDraft = "draft"
)

// EventDraftStatus says which step a draft turn is on: Code is one of the
// DraftStep* values.
const EventDraftStatus = "draft_status"

// Draft turn steps, for the panel's progress line.
const (
	DraftStepReading  = "reading"
	DraftStepDrafting = "drafting"
	DraftStepChecking = "checking"
)

// Draft states, as kept in DraftData.State.
const (
	DraftOpen      = "open"
	DraftCommitted = "committed"
	DraftDiscarded = "discarded"
)

// DraftData is an EntryDraft's data: the draft, what is wrong with it, and
// what the person did with it.
type DraftData struct {
	Draft  Draft        `json:"draft"`
	Issues []DraftIssue `json:"issues"`
	// Capped is set when the model proposed more than MaxDraftNodes.
	Capped bool `json:"capped,omitempty"`
	// Provider and Model name what drafted it, for the card's header.
	Provider string `json:"provider"`
	Model    string `json:"model"`
	State    string `json:"state"`
	RefineOf string `json:"refineOf,omitempty"`
	// BatchID and CreatedIDs are set once the person created it.
	BatchID    string   `json:"batchId,omitempty"`
	CreatedIDs []string `json:"createdIds,omitempty"`
	// Origin and Extras are set on a coding agent's plan (P6): the run it
	// came from, and by key what the planner wrote that the card does not
	// edit, which the web sends with the Create.
	Origin *DraftOrigin           `json:"origin,omitempty"`
	Extras map[string]DraftExtras `json:"extras,omitempty"`
}

// DraftOrigin names the plan run a draft came from (a coding agent's plan,
// P6); nil for the copilot's own drafts.
type DraftOrigin struct {
	RunID     string `json:"runId"`
	GoalID    string `json:"goalId"`
	GoalTitle string `json:"goalTitle,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Model     string `json:"model,omitempty"`
}

// DraftExtras is what the planner wrote for a node that the card does not
// edit; the web sends it with the Create, merged by key.
type DraftExtras struct {
	Tags       []string `json:"tags,omitempty"`
	Acceptance []string `json:"acceptance,omitempty"`
	Priority   string   `json:"priority,omitempty"`
}

// Draft-specific bounds.
const (
	// maxDraftSelection is the most selected nodes read in full.
	maxDraftSelection = 20
	// smallOutlineNodes is the listing for an on-device model.
	smallOutlineNodes = 30
	// maxDraftRepairs is how many times a broken answer is sent back.
	maxDraftRepairs = 1
)

// draftRun is one draft turn's state.
type draftRun struct {
	service  *Service
	ctx      context.Context
	storeCtx context.Context
	emit     func(Event)
	model    provider.Provider
	bridge   toolbridge.Bridge
	plan     modelPlan
	request  TurnRequest
	// conversationID is the conversation the draft lands in.
	conversationID string
}

// run drafts and stores the result. Like the chat loop, a failure after the
// stream began is reported through an event, not returned.
func (r *draftRun) run() (string, Usage, error) {
	var usage Usage
	r.emit(Event{Type: EventDraftStatus, Code: DraftStepReading})
	small := r.plan.host == ModelHostBrowser
	context, board, err := readBoard(r.ctx, r.bridge, r.request.Context.SelectedNodeIDs, small)
	if err != nil {
		if r.ctx.Err() != nil {
			return StopCancelled, usage, nil
		}
		return "", usage, &codedError{code: "draft_board", message: "Could not read the board: " + err.Error()}
	}
	context.Text = r.request.Text
	context.Language = r.request.Context.Language
	context.Previous = r.request.Previous
	context.Small = small
	if context.Previous != nil {
		// A refined draft's own keys are known, and so are the board nodes
		// it already links to: the person may have linked ones the listing
		// left out.
		addPreviousTargets(&board, context.Previous)
	}

	outcome, err := draftWith(r.ctx, r.model, context, board, func(step string) {
		r.emit(Event{Type: EventDraftStatus, Code: step})
	})
	usage = outcome.usage
	if err != nil {
		if r.ctx.Err() != nil {
			return StopCancelled, usage, nil
		}
		return "", usage, err
	}
	draft, issues, capped := outcome.draft, outcome.issues, outcome.capped
	if issues == nil {
		issues = []DraftIssue{}
	}
	data := DraftData{
		Draft: draft, Issues: issues, Capped: capped,
		Provider: string(r.plan.provider), Model: r.plan.model,
		State: DraftOpen, RefineOf: r.request.RefineOf,
	}
	entry, err := r.service.store.AppendEntry(r.storeCtx, Entry{
		ConversationID: r.conversationID, Kind: EntryDraft, Text: draftLine(draft),
		Message: mustJSON(textMessage(draftHistoryText(draft))), Data: mustJSON(data),
	})
	if err != nil {
		return "", usage, err
	}
	r.emit(Event{Type: EventEntry, Entry: &entry})
	return string(provider.StopEndTurn), usage, nil
}

// draftOutcome is what one drafting attempt produced.
type draftOutcome struct {
	draft  Draft
	issues []DraftIssue
	capped bool
	usage  Usage
	// calls is how many model calls it took.
	calls int
}

// draftWith is the drafter's core, without the board reading or the storing
// around it: the prompt, one structured call, at most one repair with the
// exact problems, then the normalized draft and its issues. step reports
// DraftStep* progress. An answer that never parses is a codedError.
func draftWith(ctx context.Context, model provider.Provider, context draftContext, board draftBoard, step func(string)) (draftOutcome, error) {
	var outcome draftOutcome
	usage := &outcome.usage
	messages := []provider.Message{{Role: provider.RoleUser, Blocks: []provider.Block{{
		Type: provider.BlockText, Text: draftUserMessage(context),
	}}}}
	request := provider.Request{
		System:         draftSystemPrompt(context),
		MaxTokens:      draftMaxTokens,
		ResponseSchema: &provider.ResponseSchema{Name: draftSchemaName, Schema: draftSchema(board.allowedKinds())},
	}

	step(DraftStepDrafting)
	var parsed bool
	for attempt := 0; attempt <= maxDraftRepairs; attempt++ {
		if attempt > 0 {
			step(DraftStepChecking)
		}
		request.Messages = messages
		response, err := model.Stream(ctx, request, nil)
		outcome.calls++
		if err != nil {
			return outcome, err
		}
		usage.InputTokens += response.InputTokens
		usage.OutputTokens += response.OutputTokens
		raw := messageText(response.Message)

		var problems []string
		candidate, err := parseDraft(raw)
		switch {
		case err != nil && response.Stop == provider.StopMaxTokens:
			problems = []string{"the answer was cut off at the length limit; propose fewer nodes with shorter bodies"}
		case err != nil:
			problems = []string{"the answer is not one complete JSON object matching the schema"}
		default:
			outcome.draft, parsed = candidate, true
			outcome.capped = normalizeDraft(&outcome.draft, board, defaultKind(context))
			outcome.issues = validateDraft(outcome.draft, board)
			for _, issue := range outcome.issues {
				if issue.Severity == IssueError {
					problems = append(problems, issue.Message)
				}
			}
		}
		if len(problems) == 0 || attempt == maxDraftRepairs {
			break
		}
		// The repair sees its own answer and exactly what was wrong.
		answer := strings.TrimSpace(raw)
		if answer == "" {
			answer = "{}"
		}
		messages = append(messages,
			provider.Message{Role: provider.RoleAssistant, Blocks: []provider.Block{{Type: provider.BlockText, Text: answer}}},
			provider.Message{Role: provider.RoleUser, Blocks: []provider.Block{{Type: provider.BlockText, Text: draftRepairMessage(problems)}}},
		)
	}
	if !parsed {
		return outcome, &codedError{code: "draft_unreadable",
			message: "The model's answer could not be read as a draft. Try again, or describe fewer steps."}
	}
	if outcome.issues == nil {
		outcome.issues = []DraftIssue{}
	}
	return outcome, nil
}

// codedError is a turn failure with its own error code for the panel.
type codedError struct {
	code, message string
}

func (e *codedError) Error() string { return e.message }

func defaultKind(c draftContext) string {
	if c.Layout == "story" {
		return "scene"
	}
	return "task"
}

// addPreviousTargets makes the board nodes a refined draft already links to
// known, so the person's own links survive the check.
func addPreviousTargets(board *draftBoard, previous *Draft) {
	for _, node := range previous.Nodes {
		for _, target := range slices.Concat(node.After, node.Refs) {
			if id, ok := strings.CutPrefix(target, existingPrefix); ok && id != "" {
				if _, known := board.known[id]; !known {
					board.known[id] = ""
				}
			}
		}
	}
}

// draftLine is the entry's one-line text: the summary, or a count.
func draftLine(draft Draft) string {
	if draft.Summary != "" {
		return draft.Summary
	}
	if len(draft.Nodes) == 0 {
		return "Questions before drafting"
	}
	return fmt.Sprintf("Draft: %d nodes", len(draft.Nodes))
}

// draftHistoryText is what later chat turns see of a draft: what was
// proposed, not that it exists on the board (the person may not create it).
func draftHistoryText(draft Draft) string {
	var b strings.Builder
	if len(draft.Nodes) == 0 {
		b.WriteString("I asked before drafting: ")
		b.WriteString(strings.Join(draft.Questions, " "))
		return b.String()
	}
	fmt.Fprintf(&b, "I proposed a draft of %d new nodes for the person to review and create", len(draft.Nodes))
	if draft.Summary != "" {
		b.WriteString(" (" + draft.Summary + ")")
	}
	b.WriteString(": ")
	titles := make([]string, 0, len(draft.Nodes))
	for _, node := range draft.Nodes {
		titles = append(titles, node.Title)
	}
	b.WriteString(truncateUTF8(strings.Join(titles, "; "), 1500))
	return b.String()
}

// readBoard lists the board for the prompt, through the bridge, so the
// project's read rules apply: the selected nodes in full, then other nodes,
// open ones first, up to the listing cap.
func readBoard(ctx context.Context, bridge toolbridge.Bridge, selected []string, small bool) (draftContext, draftBoard, error) {
	board := draftBoard{known: map[string]string{}}
	var c draftContext
	limit := maxOutlineNodes
	if small {
		limit = smallOutlineNodes
	}

	outline, err := callOutline(ctx, bridge, nodevasmcp.OutlineFilter{Limit: 200})
	if err != nil {
		return c, board, err
	}
	c.Layout, c.Total = outline.Layout, outline.Total
	c.KindLabels = map[string]string{}
	for _, nodeType := range outline.NodeTypes {
		if nodeType.ID != "" {
			c.Kinds = append(c.Kinds, nodeType.ID)
			c.KindLabels[nodeType.ID] = nodeType.Label
		}
	}
	board.kinds = c.Kinds
	nodes, edges := outline.Nodes, outline.Edges
	if len(nodes) == 0 && outline.Total > 0 {
		// Too large to list whole: what is still open, by status.
		for _, status := range []string{"ready", "in_progress", "started", "failed"} {
			page, err := callOutline(ctx, bridge, nodevasmcp.OutlineFilter{Status: status, Limit: 200})
			if err != nil {
				return c, board, err
			}
			nodes, edges = append(nodes, page.Nodes...), append(edges, page.Edges...)
			if len(nodes) >= limit {
				break
			}
		}
	}

	picked := map[string]bool{}
	for _, id := range selected {
		if len(c.Selected) >= maxDraftSelection {
			break
		}
		id = strings.TrimSpace(id)
		if id == "" || picked[id] {
			continue
		}
		node, ok, err := callNode(ctx, bridge, id)
		if err != nil {
			return c, board, err
		}
		if !ok {
			continue
		}
		picked[id] = true
		c.Selected = append(c.Selected, node)
		board.known[node.ID] = node.Title
	}

	// Open nodes first, then the rest, in the outline's order.
	open := func(status string) bool { return status != "done" && status != "skipped" && status != "deprecated" }
	ordered := slices.Clone(nodes)
	slices.SortStableFunc(ordered, func(a, b nodevasmcp.OutlineNode) int {
		switch oa, ob := open(a.Status), open(b.Status); {
		case oa && !ob:
			return -1
		case ob && !oa:
			return 1
		}
		return 0
	})
	for _, node := range ordered {
		if len(c.Listed) >= limit-len(c.Selected) {
			break
		}
		if picked[node.ID] {
			continue
		}
		picked[node.ID] = true
		c.Listed = append(c.Listed, boardNode{ID: node.ID, Title: node.Title, Kind: node.Kind, Status: node.Status})
		board.known[node.ID] = node.Title
	}
	for _, edge := range edges {
		// From must finish first; To waits for it.
		if edge.Relation != "deprecated" && picked[edge.From] && picked[edge.To] {
			c.Edges = append(c.Edges, [2]string{edge.To, edge.From})
		}
	}
	if c.Total < len(c.Selected)+len(c.Listed) {
		c.Total = len(c.Selected) + len(c.Listed)
	}
	return c, board, nil
}

func callOutline(ctx context.Context, bridge toolbridge.Bridge, filter nodevasmcp.OutlineFilter) (nodevasmcp.Outline, error) {
	input, _ := json.Marshal(map[string]any{"status": filter.Status, "limit": filter.Limit})
	result, err := bridge.Call(ctx, "get_graph_outline", input)
	if err != nil {
		return nodevasmcp.Outline{}, err
	}
	if result.IsError {
		return nodevasmcp.Outline{}, errors.New(truncateUTF8(result.Output, maxSummaryBytes))
	}
	var outline nodevasmcp.Outline
	if err := json.Unmarshal([]byte(result.Output), &outline); err != nil {
		return nodevasmcp.Outline{}, fmt.Errorf("unreadable board outline: %w", err)
	}
	return outline, nil
}

// nodeIDPattern bounds a selected id before it goes into a tool call.
var nodeIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

// callNode reads one selected node. A node that is gone or not readable is
// skipped (ok false), not an error: the selection may be stale.
func callNode(ctx context.Context, bridge toolbridge.Bridge, id string) (boardNode, bool, error) {
	if !nodeIDPattern.MatchString(id) {
		return boardNode{}, false, nil
	}
	input, _ := json.Marshal(map[string]any{"id": id, "maxBodyChars": 1})
	result, err := bridge.Call(ctx, "get_node", input)
	if err != nil {
		return boardNode{}, false, err
	}
	if result.IsError {
		return boardNode{}, false, nil
	}
	var node struct {
		ID       string   `json:"id"`
		Title    string   `json:"title"`
		Kind     string   `json:"kind"`
		Status   string   `json:"status"`
		Upstream []string `json:"upstream"`
	}
	if err := json.Unmarshal([]byte(result.Output), &node); err != nil || node.ID == "" {
		return boardNode{}, false, nil
	}
	return boardNode{ID: node.ID, Title: node.Title, Kind: node.Kind, Status: node.Status, After: node.Upstream}, true, nil
}

// SetDraftState records what the person did with a draft: created it
// (committed, with the batch and the node ids), discarded it, or undid a
// creation (open again). Only the conversation's owner may, and only on a
// draft entry.
func (s *Service) SetDraftState(ctx context.Context, accountID, conversationID, entryID, state, batchID string, ids []string) (Entry, error) {
	switch state {
	case DraftOpen, DraftCommitted, DraftDiscarded:
	default:
		return Entry{}, fmt.Errorf("%w: state must be open, committed or discarded", ErrInvalid)
	}
	if batchID != "" && !nodeIDPattern.MatchString(batchID) {
		return Entry{}, fmt.Errorf("%w: bad batch id", ErrInvalid)
	}
	if len(ids) > MaxPlanDraftNodes {
		return Entry{}, fmt.Errorf("%w: too many node ids", ErrInvalid)
	}
	for _, id := range ids {
		if !nodeIDPattern.MatchString(id) {
			return Entry{}, fmt.Errorf("%w: bad node id", ErrInvalid)
		}
	}
	if _, err := s.store.Conversation(ctx, accountID, conversationID); err != nil {
		return Entry{}, err
	}
	entries, err := s.store.Entries(ctx, conversationID)
	if err != nil {
		return Entry{}, err
	}
	index := slices.IndexFunc(entries, func(entry Entry) bool { return entry.ID == entryID && entry.Kind == EntryDraft })
	if index < 0 {
		return Entry{}, ErrNotFound
	}
	var data DraftData
	if err := json.Unmarshal([]byte(entries[index].Data), &data); err != nil {
		return Entry{}, fmt.Errorf("copilot: draft %s: %w", entryID, err)
	}
	// A coding agent's plan holds up to 100 nodes; the copilot's own, 40.
	limit := MaxDraftNodes
	if data.Origin != nil {
		limit = MaxPlanDraftNodes
	}
	if len(ids) > limit {
		return Entry{}, fmt.Errorf("%w: too many node ids", ErrInvalid)
	}
	data.State = state
	if state == DraftCommitted {
		data.BatchID, data.CreatedIDs = batchID, ids
	}
	return s.store.SetEntryData(ctx, accountID, conversationID, entryID, EntryDraft, mustJSON(data))
}
