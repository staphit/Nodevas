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
	// Edited is set once the person's own version replaced the drafted one
	// (C2: the draft-state call carried it), so a reload shows what they had.
	Edited bool `json:"edited,omitempty"`
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
	// maxNeighbourReads is how many of the selection's neighbours missing
	// from the outline (closed ones, on a board read by open status) are
	// read one by one.
	maxNeighbourReads = 10
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
	job := draftJob{
		model: r.model, bridge: r.bridge, plan: r.plan,
		text: r.request.Text, language: r.request.Context.Language,
		selected: r.request.Context.SelectedNodeIDs, previous: r.request.Previous, refineOf: r.request.RefineOf,
		step: func(step string) { r.emit(Event{Type: EventDraftStatus, Code: step}) },
	}
	data, usage, err := job.produce(r.ctx)
	if err != nil {
		if r.ctx.Err() != nil {
			return StopCancelled, usage, nil
		}
		return "", usage, err
	}
	entry, err := r.service.store.AppendEntry(r.storeCtx, Entry{
		ConversationID: r.conversationID, Kind: EntryDraft, Text: draftLine(data.Draft),
		Message: mustJSON(textMessage(draftHistoryText(data.Draft))), Data: mustJSON(data),
	})
	if err != nil {
		return "", usage, err
	}
	r.emit(Event{Type: EventEntry, Entry: &entry})
	return string(provider.StopEndTurn), usage, nil
}

// draftJob is one draft to produce: from a draft turn, or from a chat
// turn's propose_nodes (C1). It reads the board through the turn's bridge,
// so the project's read rules apply, and writes nothing.
type draftJob struct {
	model    provider.Provider
	bridge   toolbridge.Bridge
	plan     modelPlan
	text     string
	language string
	selected []string
	previous *Draft
	refineOf string
	// step reports DraftStep* progress; nil reports nothing.
	step func(string)
}

// produce reads the board, drafts, and returns the entry's data. A failure
// is a codedError where the panel has its own words for it.
func (j draftJob) produce(ctx context.Context) (DraftData, Usage, error) {
	step := j.step
	if step == nil {
		step = func(string) {}
	}
	step(DraftStepReading)
	small := j.plan.host == ModelHostBrowser
	context, board, err := readBoard(ctx, j.bridge, j.selected, small)
	if err != nil {
		return DraftData{}, Usage{}, &codedError{code: "draft_board", message: "Could not read the board: " + err.Error()}
	}
	context.Text = j.text
	context.Language = j.language
	context.Previous = j.previous
	context.Small = small
	if small {
		// The browser's model takes a few thousand tokens: the message is
		// budgeted here, where the instruction can be kept whole.
		context.MaxBytes = maxBrowserStructuredMessageBytes
	}
	if context.Previous != nil {
		// A refined draft's own keys are known, and so are the board nodes
		// it already links to: the person may have linked ones the listing
		// left out.
		addPreviousTargets(&board, context.Previous)
	}

	outcome, err := draftWith(ctx, j.model, context, board, step)
	if err != nil {
		return DraftData{}, outcome.usage, err
	}
	if outcome.issues == nil {
		outcome.issues = []DraftIssue{}
	}
	return DraftData{
		Draft: outcome.draft, Issues: outcome.issues, Capped: outcome.capped,
		Provider: string(j.plan.provider), Model: j.plan.model,
		State: DraftOpen, RefineOf: j.refineOf,
	}, outcome.usage, nil
}

// mentionsCode reports whether the person's words name code: a repo path, a
// file or a function, or a code span. The same check as the panel's
// (web/src/copilot/draft/draftStore.ts mentionsCode), so the server marks a
// draft that needs a repository even when the model did not.
func mentionsCode(text string) bool {
	return slices.ContainsFunc(codeMentionPatterns, func(pattern *regexp.Regexp) bool { return pattern.MatchString(text) })
}

var codeMentionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(^|[^\w/])[\w.-]+/[\w./-]+`),
	regexp.MustCompile(`(?i)\b[\w-]+\.(ts|tsx|js|jsx|go|py|rs|java|kt|rb|cs|cpp|c|h|swift|php|vue|svelte|css|scss|sql|md|yaml|yml|json)\b`),
	regexp.MustCompile(`\b[A-Za-z_]\w*\(\)`),
	regexp.MustCompile("`[^`]+`"),
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
	temperature := draftTemperature
	request := provider.Request{
		System:         draftSystemPrompt(context),
		MaxTokens:      draftMaxTokens,
		ResponseSchema: &provider.ResponseSchema{Name: draftSchemaName, Schema: draftSchema(board.allowedKinds())},
		Temperature:    &temperature,
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
	// Words that name code mark the draft whatever the model said: it is
	// what offers the coding agent's deeper plan.
	if mentionsCode(context.Text) {
		outcome.draft.NeedsCodeKnowledge = true
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
// project's read rules apply: the selected nodes in full, then their
// neighbours (what they wait for, then what waits for them), then other
// nodes, open ones first, up to the listing cap minus the selection.
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

	// The selection's neighbours first: what it waits for and what waits
	// for it, which a request about the selection links to most. Then open
	// nodes, then the rest, in the outline's order.
	room := func() bool { return len(c.Listed) < limit-len(c.Selected) }
	list := func(node boardNode) {
		picked[node.ID] = true
		c.Listed = append(c.Listed, boardNode{ID: node.ID, Title: node.Title, Kind: node.Kind, Status: node.Status})
		board.known[node.ID] = node.Title
	}
	inOutline := make(map[string]nodevasmcp.OutlineNode, len(nodes))
	for _, node := range nodes {
		inOutline[node.ID] = node
	}
	fetched := 0
	for _, neighbour := range selectionNeighbours(c.Selected, edges) {
		if !room() {
			break
		}
		if picked[neighbour] {
			continue
		}
		if node, ok := inOutline[neighbour]; ok {
			list(boardNode{ID: node.ID, Title: node.Title, Kind: node.Kind, Status: node.Status})
			continue
		}
		// Not in the listing (a closed node on a board read by open
		// status): read on its own, a bounded few.
		if fetched >= maxNeighbourReads {
			continue
		}
		fetched++
		node, ok, err := callNode(ctx, bridge, neighbour)
		if err != nil {
			return c, board, err
		}
		if ok {
			list(node)
		}
	}
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
		if !room() {
			break
		}
		if picked[node.ID] {
			continue
		}
		list(boardNode{ID: node.ID, Title: node.Title, Kind: node.Kind, Status: node.Status})
	}
	seen := map[[2]string]bool{}
	link := func(waiter, first string) {
		if pair := [2]string{waiter, first}; picked[waiter] && picked[first] && !seen[pair] {
			seen[pair] = true
			c.Edges = append(c.Edges, pair)
		}
	}
	for _, edge := range edges {
		// From must finish first; To waits for it.
		if edge.Relation != "deprecated" {
			link(edge.To, edge.From)
		}
	}
	// The selection's own links, which a paged outline may not carry.
	for _, node := range c.Selected {
		for _, up := range node.After {
			link(node.ID, up)
		}
		for _, down := range node.Before {
			link(down, node.ID)
		}
	}
	if c.Total < len(c.Selected)+len(c.Listed) {
		c.Total = len(c.Selected) + len(c.Listed)
	}
	return c, board, nil
}

// selectionNeighbours lists, per selected node, what it waits for and then
// what waits for it, from the node itself and from the outline's edges;
// each once, the selection left out.
func selectionNeighbours(selected []boardNode, edges []nodevasmcp.OutlineEdge) []string {
	isSelected := make(map[string]bool, len(selected))
	for _, node := range selected {
		isSelected[node.ID] = true
	}
	var out []string
	add := func(id string) {
		if id != "" && !isSelected[id] && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	for _, node := range selected {
		for _, id := range node.After {
			add(id)
		}
		for _, edge := range edges {
			if edge.Relation != "deprecated" && edge.To == node.ID {
				add(edge.From)
			}
		}
		for _, id := range node.Before {
			add(id)
		}
		for _, edge := range edges {
			if edge.Relation != "deprecated" && edge.From == node.ID {
				add(edge.To)
			}
		}
	}
	return out
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
		ID         string   `json:"id"`
		Title      string   `json:"title"`
		Kind       string   `json:"kind"`
		Status     string   `json:"status"`
		Upstream   []string `json:"upstream"`
		Downstream []string `json:"downstream"`
	}
	if err := json.Unmarshal([]byte(result.Output), &node); err != nil || node.ID == "" {
		return boardNode{}, false, nil
	}
	return boardNode{ID: node.ID, Title: node.Title, Kind: node.Kind, Status: node.Status,
		After: node.Upstream, Before: node.Downstream}, true, nil
}

// SetDraftState records what the person did with a draft: created it
// (committed, with the batch and the node ids), discarded it, or undid a
// creation (open again). Only the conversation's owner may, and only on a
// draft entry. draft, when set, is the person's own version of it (C2): it
// replaces the stored one, bounded and normalized as a refine's previous
// draft is, and marks the data edited.
func (s *Service) SetDraftState(ctx context.Context, accountID, conversationID, entryID, state, batchID string,
	ids []string, draft *Draft) (Entry, error) {
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
	limit, titleLimit := MaxDraftNodes, maxDraftTitleRunes
	if data.Origin != nil {
		limit, titleLimit = MaxPlanDraftNodes, maxPlanDraftTitleRunes
	}
	if len(ids) > limit {
		return Entry{}, fmt.Errorf("%w: too many node ids", ErrInvalid)
	}
	if draft != nil {
		mine := *draft
		mine.Nodes = slices.Clone(draft.Nodes)
		if err := boundDraft(&mine, limit, titleLimit); err != nil {
			return Entry{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		if mine.Nodes == nil {
			mine.Nodes = []DraftNode{}
		}
		data.Draft, data.Edited = mine, true
	}
	data.State = state
	// A commit without ids for the batch already stored (another tab's Create
	// met BATCH_VIA_TAKEN and only marks the card) keeps the ids the
	// creating tab recorded.
	if state == DraftCommitted && (ids != nil || batchID != data.BatchID) {
		data.BatchID, data.CreatedIDs = batchID, ids
	}
	return s.store.SetEntryData(ctx, accountID, conversationID, entryID, EntryDraft, mustJSON(data))
}
