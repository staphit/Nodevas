package copilot

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

var testBoard = draftBoard{
	known: map[string]string{"n-docs": "寫文件", "n-ship": "Ship it"},
	kinds: []string{"task", "goal", "note", "gate"},
}

func codes(issues []DraftIssue) []string {
	var out []string
	for _, issue := range issues {
		out = append(out, issue.Severity+":"+issue.Code+":"+issue.Key)
	}
	return out
}

func TestParseDraftToleratesFencesButNotTruncation(t *testing.T) {
	draft, err := parseDraft("```json\n{\"summary\":\"s\",\"nodes\":[{\"key\":\"a\",\"title\":\"A\",\"kind\":\"task\"}]}\n```")
	if err != nil || len(draft.Nodes) != 1 || draft.Nodes[0].Title != "A" {
		t.Fatalf("fenced draft = %+v, %v", draft, err)
	}
	for _, raw := range []string{
		`{"summary":"s","nodes":[{"key":"a","title":"A"`,
		`I made three steps for you.`,
		``,
		`{"summary": "s", "nodes": [}`,
	} {
		if _, err := parseDraft(raw); !errors.Is(err, errDraftUnreadable) {
			t.Errorf("parseDraft(%q) err = %v, want unreadable", raw, err)
		}
	}
}

func TestNormalizeDraftClipsFillsAndCaps(t *testing.T) {
	nodes := make([]DraftNode, MaxDraftNodes+5)
	for i := range nodes {
		nodes[i] = DraftNode{Key: fmt.Sprintf("n%d", i), Title: "  step  \n " + fmt.Sprint(i)}
	}
	nodes[0].Title = strings.Repeat("長", 80)
	nodes[1].After = []string{"n-docs", "n44", " n0 ", "n0", "nowhere"}
	draft := Draft{Nodes: nodes}
	if !normalizeDraft(&draft, testBoard, "task") {
		t.Fatal("a 45-node draft was not reported capped")
	}
	if len(draft.Nodes) != MaxDraftNodes {
		t.Fatalf("nodes = %d", len(draft.Nodes))
	}
	if got := []rune(draft.Nodes[0].Title); len(got) != maxDraftTitleRunes {
		t.Errorf("title clipped to %d runes", len(got))
	}
	if draft.Nodes[2].Title != "step 2" || draft.Nodes[2].Kind != "task" {
		t.Errorf("node 2 = %+v", draft.Nodes[2])
	}
	// A bare board id becomes existing:, a key the cap cut is dropped, and
	// so is anything unknown once the draft was capped.
	if want := []string{"existing:n-docs", "n0"}; !slices.Equal(draft.Nodes[1].After, want) {
		t.Errorf("after = %v, want %v", draft.Nodes[1].After, want)
	}
}

func TestValidateDraftFindsEachProblem(t *testing.T) {
	draft := Draft{Nodes: []DraftNode{
		{Key: "a", Title: "Write spec", Kind: "task"},
		{Key: "b", Title: "", Kind: "task", After: []string{"a", "existing:n-ship", "n9"}},
		{Key: "a", Title: "Again", Kind: "task"},
		{Key: "bad key", Title: "X", Kind: "task"},
		{Key: "c", Title: "Wizard", Kind: "dragon", After: []string{"c"}},
		{Key: "d", Title: "寫 文件。", Kind: "note", Refs: []string{"existing:gone"}},
		{Key: "e", Title: "Write  SPEC", Kind: "task"},
		{Key: "n-docs", Title: "Clash", Kind: "task"},
	}}
	got := codes(validateDraft(draft, testBoard))
	for _, want := range []string{
		"error:empty_title:b",
		"error:unknown_after:b",
		"error:duplicate_key:a",
		"error:bad_key:bad key",
		"error:unknown_kind:c",
		"error:self_after:c",
		"warning:unknown_ref:d",
		"warning:duplicate_existing:d",
		"warning:duplicate_in_draft:e",
		"error:duplicate_key:n-docs",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("issues lack %s; got %v", want, got)
		}
	}
	if slices.Contains(got, "error:unknown_after:b") && strings.Contains(strings.Join(got, ","), "existing:n-ship") {
		t.Error("a listed existing node was reported unknown")
	}
}

func TestValidateDraftFindsCycles(t *testing.T) {
	draft := Draft{Nodes: []DraftNode{
		{Key: "a", Title: "A", Kind: "task", After: []string{"c"}},
		{Key: "b", Title: "B", Kind: "task", After: []string{"a"}},
		{Key: "c", Title: "C", Kind: "task", After: []string{"b", "existing:n-ship"}},
		{Key: "d", Title: "D", Kind: "task", After: []string{"c"}},
	}}
	issues := validateDraft(draft, testBoard)
	var cycle *DraftIssue
	for i := range issues {
		if issues[i].Code == IssueCycle {
			cycle = &issues[i]
		}
	}
	if cycle == nil || !strings.Contains(cycle.Message, "a → c → b → a") {
		t.Fatalf("cycle issue = %+v (all %v)", cycle, codes(issues))
	}
}

func TestValidDraftsHaveNoErrors(t *testing.T) {
	ok := Draft{Nodes: []DraftNode{
		{Key: "a", Title: "Docs", Kind: "task", After: []string{"existing:n-ship"}},
		{Key: "b", Title: "Test", Kind: "gate", After: []string{"a"}, Refs: []string{"existing:n-docs"}},
	}}
	if issues := validateDraft(ok, testBoard); hasErrors(issues) {
		t.Fatalf("valid draft: %v", codes(issues))
	}
	questions := Draft{Summary: "?", Questions: []string{"Which release?"}}
	if issues := validateDraft(questions, testBoard); len(issues) != 0 {
		t.Fatalf("questions-only draft: %v", codes(issues))
	}
	if issues := validateDraft(Draft{}, testBoard); !hasErrors(issues) || issues[0].Code != IssueEmptyDraft {
		t.Fatalf("empty draft: %v", codes(issues))
	}
}

func TestDraftSchemaHasProjectKindsAndNoAcceptance(t *testing.T) {
	raw := draftSchema([]string{"task", "custom-type-bug"})
	if strings.Contains(string(raw), "acceptance") || strings.Contains(string(raw), "assignee") {
		t.Fatalf("schema offers fields drafts leave out: %s", raw)
	}
	var schema struct {
		AdditionalProperties bool `json:"additionalProperties"`
		Properties           struct {
			Nodes struct {
				Items struct {
					AdditionalProperties bool `json:"additionalProperties"`
					Properties           struct {
						Kind struct {
							Enum []string `json:"enum"`
						} `json:"kind"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"nodes"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(schema.Properties.Nodes.Items.Properties.Kind.Enum, []string{"task", "custom-type-bug"}) {
		t.Errorf("kind enum = %v", schema.Properties.Nodes.Items.Properties.Kind.Enum)
	}
	if schema.AdditionalProperties || schema.Properties.Nodes.Items.AdditionalProperties {
		t.Error("schema leaves objects open")
	}
}

func TestNormalizeTitle(t *testing.T) {
	for in, want := range map[string]string{
		"寫 文件。":        "寫文件",
		"Write  Docs!": "writedocs",
		"ＡＢＣ　１２３":      "abc123",
		"「發布」":         "發布",
		"   ":          "",
	} {
		if got := normalizeTitle(in); got != want {
			t.Errorf("normalizeTitle(%q) = %q, want %q", in, got, want)
		}
	}
}
