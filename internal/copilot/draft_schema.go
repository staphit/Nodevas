package copilot

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
)

// Draft caps. Fixed in v1 (the owner's round-2 decision): a draft is read and
// fixed by a person in a panel, so a few dozen steps is already a lot, and a
// board context much larger than this crowds a local model's window.
const (
	// MaxDraftNodes is the most nodes one draft holds; the rest of a longer
	// answer is cut and the draft says so (Capped).
	MaxDraftNodes = 40
	// MaxPlanDraftNodes is the most nodes a coding agent's plan draft holds
	// (P6): what one plan run may record on the hub.
	MaxPlanDraftNodes = runnerwire.MaxPlanDraftNodes
	// draftMaxTokens bounds the drafting call's answer.
	draftMaxTokens = 12000
	// maxOutlineNodes is the most board nodes the prompt lists.
	maxOutlineNodes = 150
	// maxDraftTitleRunes and maxDraftBodyRunes clip a node's text.
	maxDraftTitleRunes = 60
	maxDraftBodyRunes  = 600
	maxDraftQuestions  = 5
	// maxPlanDraftTitleRunes clips a coding agent's task title (P6): the
	// planner is not held to the drafter's 60 characters.
	maxPlanDraftTitleRunes = 200
	// maxStoredDraftKeyBytes and maxStoredDraftRefBytes bound what a draft
	// the browser sends back (a refine's previous, the draft-state's final)
	// may hold per key and per link; maxStoredDraftKindBytes per kind.
	maxStoredDraftKeyBytes  = 64
	maxStoredDraftKindBytes = 128
	maxStoredDraftRefBytes  = 200
	// draftTemperature is the drafter's sampling temperature where a model
	// takes one (C5): a draft should follow the description, not vary.
	draftTemperature = 0.2
	// existingPrefix marks a reference to a node already on the board.
	existingPrefix = "existing:"
)

// builtinKinds are the node types every board has, for a board whose outline
// did not list its types.
var builtinKinds = []string{"task", "goal", "scene", "event", "gate", "choice", "start", "end", "note"}

// Draft is the drafter's answer: proposed new nodes, or questions when the
// description cannot be drafted. It is data shown to the person; only their
// Create writes anything.
type Draft struct {
	Summary            string      `json:"summary"`
	NeedsCodeKnowledge bool        `json:"needsCodeKnowledge,omitempty"`
	Questions          []string    `json:"questions,omitempty"`
	Nodes              []DraftNode `json:"nodes"`
}

// DraftNode is one proposed node. After lists what must finish first: a key
// of this draft, or existing:<id> for a node on the board. Refs are notes it
// uses as context, in the same form.
type DraftNode struct {
	Key   string   `json:"key"`
	Title string   `json:"title"`
	Kind  string   `json:"kind"`
	Body  string   `json:"body,omitempty"`
	After []string `json:"after,omitempty"`
	Refs  []string `json:"refs,omitempty"`
}

// DraftIssue is one problem with a draft. Errors keep it from being created
// as is; warnings are worth a look. Message is English, for the model's
// repair call; the panel words it from Code.
type DraftIssue struct {
	Key        string `json:"key,omitempty"`
	Field      string `json:"field,omitempty"`
	Severity   string `json:"severity"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	ExistingID string `json:"existingId,omitempty"`
	// Title names the existing node a duplicate warning points at.
	Title string `json:"title,omitempty"`
}

// Issue severities and codes.
const (
	IssueError   = "error"
	IssueWarning = "warning"

	IssueEmptyDraft        = "empty_draft"
	IssueBadKey            = "bad_key"
	IssueDuplicateKey      = "duplicate_key"
	IssueEmptyTitle        = "empty_title"
	IssueUnknownKind       = "unknown_kind"
	IssueUnknownAfter      = "unknown_after"
	IssueSelfAfter         = "self_after"
	IssueCycle             = "cycle"
	IssueUnknownRef        = "unknown_ref"
	IssueDuplicateExisting = "duplicate_existing"
	IssueDuplicateInDraft  = "duplicate_in_draft"
	// IssueBodyClipped (warning): a plan draft node's body was longer than
	// the card keeps (maxDraftBodyRunes).
	IssueBodyClipped = "body_clipped"
	// IssueTitleClipped (warning): a plan draft node's title was longer
	// than maxPlanDraftTitleRunes.
	IssueTitleClipped = "title_clipped"
)

var draftKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// draftBoard is what a draft is checked against: the board nodes the
// drafter was shown, and the kinds this project allows.
type draftBoard struct {
	// known maps a listed node id to its title.
	known map[string]string
	kinds []string
}

func (b draftBoard) allowedKinds() []string {
	if len(b.kinds) > 0 {
		return b.kinds
	}
	return builtinKinds
}

// errDraftUnreadable is an answer that is not one JSON draft object: broken,
// cut off, or prose.
var errDraftUnreadable = errors.New("the answer is not one complete JSON object")

// parseDraft reads the model's answer. A code fence or text around the object
// is tolerated; a truncated or malformed object is an error, never an empty
// draft.
func parseDraft(raw string) (Draft, error) {
	text := strings.TrimSpace(raw)
	text = strings.TrimPrefix(strings.TrimPrefix(text, "```json"), "```")
	text = strings.TrimSpace(strings.TrimSuffix(text, "```"))
	if start, end := strings.IndexByte(text, '{'), strings.LastIndexByte(text, '}'); start >= 0 && end > start {
		text = text[start : end+1]
	} else {
		return Draft{}, errDraftUnreadable
	}
	var draft Draft
	decoder := json.NewDecoder(strings.NewReader(text))
	if err := decoder.Decode(&draft); err != nil {
		return Draft{}, fmt.Errorf("%w: %v", errDraftUnreadable, err)
	}
	return draft, nil
}

// normalizeDraft trims and clips the text the model wrote, fills an empty
// kind, rewrites a bare existing id in After or Refs to its existing: form,
// and cuts the draft to MaxDraftNodes. It reports whether nodes were cut.
func normalizeDraft(draft *Draft, board draftBoard, defaultKind string) (capped bool) {
	normalizeDraftText(draft)
	if len(draft.Nodes) > MaxDraftNodes {
		draft.Nodes, capped = draft.Nodes[:MaxDraftNodes], true
	}
	keys := map[string]bool{}
	for i := range draft.Nodes {
		node := &draft.Nodes[i]
		node.Key = strings.TrimSpace(node.Key)
		keys[node.Key] = true
	}
	for i := range draft.Nodes {
		node := &draft.Nodes[i]
		node.Title = clipRunes(flatLine(node.Title), maxDraftTitleRunes)
		node.Body = clipRunes(planDraftBody(node.Body), maxDraftBodyRunes)
		node.Kind = strings.TrimSpace(node.Kind)
		if node.Kind == "" {
			node.Kind = defaultKind
		}
		node.After = normalizeTargets(node.After, keys, board, capped)
		node.Refs = normalizeTargets(node.Refs, keys, board, capped)
	}
	return capped
}

// normalizeDraftText trims and clips the summary and the questions.
func normalizeDraftText(draft *Draft) {
	draft.Summary = clipRunes(flatLine(draft.Summary), 200)
	questions := draft.Questions[:0]
	for _, question := range draft.Questions {
		if question = clipRunes(flatLine(question), 200); question != "" && len(questions) < maxDraftQuestions {
			questions = append(questions, question)
		}
	}
	draft.Questions = questions
}

// boundDraft checks and normalizes a draft the browser sends back: a
// refine's previous draft, or the person's final one on the draft-state call
// (C2). At most limit nodes; a key, kind or link longer than any the card
// makes, or more links than a plan holds nodes, is refused; text is trimmed
// and clipped as a drafted node's is, the title to titleLimit. Links are
// kept as written, trimmed and deduplicated: the board checks them at Create.
func boundDraft(draft *Draft, limit, titleLimit int) error {
	if len(draft.Nodes) > limit {
		return fmt.Errorf("the draft has more than %d nodes", limit)
	}
	normalizeDraftText(draft)
	for i := range draft.Nodes {
		node := &draft.Nodes[i]
		node.Key = strings.TrimSpace(node.Key)
		node.Kind = strings.TrimSpace(node.Kind)
		if len(node.Key) > maxStoredDraftKeyBytes || len(node.Kind) > maxStoredDraftKindBytes {
			return fmt.Errorf("nodes[%d] has a key or kind longer than a draft makes", i)
		}
		node.Title = clipRunes(flatLine(node.Title), titleLimit)
		node.Body = clipRunes(planDraftBody(node.Body), maxDraftBodyRunes)
		for _, list := range []*[]string{&node.After, &node.Refs} {
			if len(*list) > MaxPlanDraftNodes {
				return fmt.Errorf("nodes[%d] has more than %d links", i, MaxPlanDraftNodes)
			}
			var out []string
			for _, target := range *list {
				target = strings.TrimSpace(target)
				if len(target) > maxStoredDraftRefBytes {
					return fmt.Errorf("nodes[%d] links to an overlong id", i)
				}
				if target != "" && !slices.Contains(out, target) {
					out = append(out, target)
				}
			}
			*list = out
		}
	}
	return nil
}

// normalizeTargets trims, deduplicates and rewrites references. With capped,
// a reference to a key that is not in the draft is dropped: it most likely
// named a node the cap cut.
func normalizeTargets(targets []string, keys map[string]bool, board draftBoard, capped bool) []string {
	var out []string
	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}
		if !keys[target] && !strings.HasPrefix(target, existingPrefix) {
			if _, onBoard := board.known[target]; onBoard {
				target = existingPrefix + target
			} else if capped {
				continue
			}
		}
		if !slices.Contains(out, target) {
			out = append(out, target)
		}
	}
	return out
}

// validateDraft lists what is wrong with a normalized draft. A draft with
// questions and no nodes is valid: it is asking, not proposing.
func validateDraft(draft Draft, board draftBoard) []DraftIssue {
	var issues []DraftIssue
	add := func(issue DraftIssue) { issues = append(issues, issue) }
	if len(draft.Nodes) == 0 {
		if len(draft.Questions) == 0 {
			add(DraftIssue{Severity: IssueError, Code: IssueEmptyDraft,
				Message: "the draft has no nodes and no questions; propose nodes, or ask what is unclear"})
		}
		return issues
	}
	kinds := board.allowedKinds()
	keys := map[string]bool{}
	for i, node := range draft.Nodes {
		where := fmt.Sprintf("nodes[%d]", i)
		switch {
		case !draftKeyPattern.MatchString(node.Key):
			add(DraftIssue{Key: node.Key, Field: "key", Severity: IssueError, Code: IssueBadKey,
				Message: fmt.Sprintf("%s.key %q must be 1-64 of A-Z a-z 0-9 _ . -", where, node.Key)})
		case keys[node.Key]:
			add(DraftIssue{Key: node.Key, Field: "key", Severity: IssueError, Code: IssueDuplicateKey,
				Message: fmt.Sprintf("%s.key %q is used twice", where, node.Key)})
		default:
			if _, onBoard := board.known[node.Key]; onBoard {
				add(DraftIssue{Key: node.Key, Field: "key", Severity: IssueError, Code: IssueDuplicateKey,
					Message: fmt.Sprintf("%s.key %q is an existing node's id; use another key", where, node.Key)})
			}
		}
		keys[node.Key] = true
		if node.Title == "" {
			add(DraftIssue{Key: node.Key, Field: "title", Severity: IssueError, Code: IssueEmptyTitle,
				Message: where + ".title is empty"})
		}
		if !slices.Contains(kinds, node.Kind) {
			add(DraftIssue{Key: node.Key, Field: "kind", Severity: IssueError, Code: IssueUnknownKind,
				Message: fmt.Sprintf("%s.kind %q is not one of %s", where, node.Kind, strings.Join(kinds, ", "))})
		}
	}
	for i, node := range draft.Nodes {
		where := fmt.Sprintf("nodes[%d]", i)
		for j, target := range node.After {
			switch {
			case target == node.Key:
				add(DraftIssue{Key: node.Key, Field: "after", Severity: IssueError, Code: IssueSelfAfter,
					Message: fmt.Sprintf("%s.after[%d]: a node cannot wait for itself", where, j)})
			case !targetExists(target, keys, board):
				add(DraftIssue{Key: node.Key, Field: "after", Severity: IssueError, Code: IssueUnknownAfter,
					Message: fmt.Sprintf("%s.after[%d]: unknown key or node %q", where, j, target)})
			}
		}
		for j, target := range node.Refs {
			if !targetExists(target, keys, board) {
				add(DraftIssue{Key: node.Key, Field: "refs", Severity: IssueWarning, Code: IssueUnknownRef,
					Message: fmt.Sprintf("%s.refs[%d]: unknown key or node %q", where, j, target)})
			}
		}
	}
	if cycle := draftCycle(draft); len(cycle) > 0 {
		add(DraftIssue{Key: cycle[0], Field: "after", Severity: IssueError, Code: IssueCycle,
			Message: "these steps wait for each other: " + strings.Join(cycle, " → ")})
	}
	add2 := duplicateTitles(draft, board)
	issues = append(issues, add2...)
	return issues
}

func targetExists(target string, keys map[string]bool, board draftBoard) bool {
	if id, ok := strings.CutPrefix(target, existingPrefix); ok {
		_, known := board.known[id]
		return known
	}
	return keys[target]
}

// draftCycle returns one cycle among the draft's own keys, closed (its first
// key repeated at the end), or nil. Links to existing nodes cannot close a
// cycle here: existing nodes never wait for new ones in a draft.
func draftCycle(draft Draft) []string {
	after := map[string][]string{}
	for _, node := range draft.Nodes {
		for _, target := range node.After {
			if !strings.HasPrefix(target, existingPrefix) {
				after[node.Key] = append(after[node.Key], target)
			}
		}
	}
	const (
		unvisited = iota
		active
		done
	)
	state := map[string]int{}
	var stack []string
	var found []string
	var visit func(key string) bool
	visit = func(key string) bool {
		state[key] = active
		stack = append(stack, key)
		for _, next := range after[key] {
			switch state[next] {
			case active:
				start := slices.Index(stack, next)
				found = append(slices.Clone(stack[start:]), next)
				return true
			case unvisited:
				if visit(next) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[key] = done
		return false
	}
	for _, node := range draft.Nodes {
		if state[node.Key] == unvisited && visit(node.Key) {
			return found
		}
	}
	return nil
}

// duplicateTitles warns about a node whose title matches a listed board
// node's (offering to link to it instead) or an earlier node of the draft.
func duplicateTitles(draft Draft, board draftBoard) []DraftIssue {
	existing := map[string]string{}
	for id, title := range board.known {
		if normalized := normalizeTitle(title); normalized != "" {
			if current, taken := existing[normalized]; !taken || id < current {
				existing[normalized] = id
			}
		}
	}
	var issues []DraftIssue
	seen := map[string]string{}
	for _, node := range draft.Nodes {
		normalized := normalizeTitle(node.Title)
		if normalized == "" {
			continue
		}
		if id, ok := existing[normalized]; ok {
			issues = append(issues, DraftIssue{Key: node.Key, Field: "title", Severity: IssueWarning,
				Code: IssueDuplicateExisting, ExistingID: id, Title: board.known[id],
				Message: fmt.Sprintf("%q may duplicate the existing node %s; link to it instead", node.Title, id)})
			continue
		}
		if first, ok := seen[normalized]; ok {
			issues = append(issues, DraftIssue{Key: node.Key, Field: "title", Severity: IssueWarning,
				Code: IssueDuplicateInDraft, Message: fmt.Sprintf("%q repeats %s", node.Title, first)})
			continue
		}
		seen[normalized] = node.Key
	}
	return issues
}

// normalizeTitle folds case, full-width forms, punctuation and spaces, so
// 「寫文件」, "寫 文件。" and "Write Docs" match "write docs".
func normalizeTitle(title string) string {
	var b strings.Builder
	for _, r := range title {
		if r >= 0xFF01 && r <= 0xFF5E {
			r -= 0xFEE0
		}
		if r == 0x3000 || unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// hasErrors reports whether any issue is an error.
func hasErrors(issues []DraftIssue) bool {
	return slices.ContainsFunc(issues, func(issue DraftIssue) bool { return issue.Severity == IssueError })
}

// draftSchema is the response schema for a draft, its kind an enum of the
// project's types. Every object closes its properties (structured output
// requires it for some vendors); lengths and counts are checked afterwards,
// not in the schema, since not every vendor takes those keywords.
func draftSchema(kinds []string) json.RawMessage {
	if len(kinds) == 0 {
		kinds = builtinKinds
	}
	stringList := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	node := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"key":   map[string]any{"type": "string"},
			"title": map[string]any{"type": "string"},
			"kind":  map[string]any{"type": "string", "enum": kinds},
			"body":  map[string]any{"type": "string"},
			"after": stringList,
			"refs":  stringList,
		},
		"required":             []string{"key", "title", "kind", "after"},
		"additionalProperties": false,
	}
	schema, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"summary":            map[string]any{"type": "string"},
			"needsCodeKnowledge": map[string]any{"type": "boolean"},
			"questions":          stringList,
			"nodes":              map[string]any{"type": "array", "items": node},
		},
		"required":             []string{"summary", "nodes"},
		"additionalProperties": false,
	})
	return schema
}

// draftSchemaName is the schema's name where a vendor asks for one.
const draftSchemaName = "nodevas_draft"

// planDraftBody is a draft node's body with no front matter: a body that
// opens with a delimiter would set fields at Create that the card never
// shows (assignee, requires, effects, priority, tags). Trimmed; a block is
// replaced by what follows it, an unclosed or unreadable one loses its first
// line, at most three rounds, after which a body still opening with a
// delimiter is dropped. Done before the clip, so the clip cannot leave an
// unclosed block behind.
func planDraftBody(raw string) string {
	body := strings.TrimSpace(raw)
	for round := 0; round < 3 && engine.OpensFrontMatter(body); round++ {
		if parsed, err := engine.ParseNodeFile([]byte(body)); err == nil && parsed.Body != body {
			body = strings.TrimSpace(parsed.Body)
			continue
		}
		_, rest, _ := strings.Cut(body, "\n")
		body = strings.TrimSpace(rest)
	}
	if engine.OpensFrontMatter(body) {
		return ""
	}
	return body
}

func flatLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func clipRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return strings.TrimSpace(string([]rune(text)[:limit]))
}
