package engine

import (
	"strings"
	"testing"
	"time"
)

const sampleGraph = `
version: 1
type: story
nodes:
  - id: intro
    title: Intro
    kind: start
  - id: chapter-1
    title: Chapter 1
    kind: scene
    requires: intro
  - id: side-quest
    title: Side Quest
    kind: scene
    requires: chapter-1 and flag(karma >= 3)
  - id: hidden-ending
    title: Hidden Ending
    kind: end
    requires: (side-quest nor chapter-2) and chapter-1
  - id: chapter-2
    title: Chapter 2
    kind: scene
    requires: chapter-1
    effects:
      - set: karma = 5
edges:
  - from: intro
    to: chapter-1
  - from: chapter-1
    to: side-quest
    relation: optional
    line: dashed
flags:
  karma: 0
`

func parseSample(t *testing.T) *Graph {
	t.Helper()
	g, err := ParseGraph([]byte(sampleGraph))
	if err != nil {
		t.Fatalf("ParseGraph: %v", err)
	}
	return g
}

func TestParseGraph(t *testing.T) {
	g := parseSample(t)
	if len(g.Nodes) != 5 {
		t.Fatalf("nodes = %d, want 5", len(g.Nodes))
	}
	n := g.NodeByID("side-quest")
	if n == nil || n.Requires != "chapter-1 and flag(karma >= 3)" {
		t.Fatalf("side-quest requires wrong: %+v", n)
	}
	if len(g.Edges) != 2 || g.Edges[1].Relation != RelationOptional ||
		g.Edges[1].Line != "dashed" {
		t.Fatalf("edges wrong: %+v", g.Edges[1])
	}
	if v, ok := g.Flags["karma"]; !ok || v != 0 {
		t.Fatalf("flags wrong: %v", g.Flags)
	}
}

func TestGraphRoundTrip(t *testing.T) {
	g := parseSample(t)
	out, err := MarshalGraph(g)
	if err != nil {
		t.Fatalf("MarshalGraph: %v", err)
	}
	g2, err := ParseGraph(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(g2.Nodes) != len(g.Nodes) || len(g2.Edges) != len(g.Edges) {
		t.Fatal("round trip lost data")
	}
}

func TestValidateClean(t *testing.T) {
	g := parseSample(t)
	for _, iss := range Validate(g) {
		if iss.Severity == "error" {
			t.Errorf("unexpected error: %+v", iss)
		}
	}
}

func TestValidateCatchesProblems(t *testing.T) {
	bad := `
nodes:
  - id: a
    requires: b and (c or
  - id: b
    requires: missing-node
  - id: c
    requires: d
  - id: d
    requires: c
  - id: island
  - id: a
`
	g, err := ParseGraph([]byte(bad))
	if err != nil {
		t.Fatalf("ParseGraph: %v", err)
	}
	issues := Validate(g)
	find := func(substr string) bool {
		for _, iss := range issues {
			if strings.Contains(iss.Msg, substr) {
				return true
			}
		}
		return false
	}
	if !find("syntax error") {
		t.Error("missing syntax error issue")
	}
	if !find("unknown node") {
		t.Error("missing unknown-node issue")
	}
	if !find("cycle") {
		t.Error("missing cycle issue")
	}
	if !find("duplicate") {
		t.Error("missing duplicate-id issue")
	}
	if !find("not connected") {
		t.Error("missing island warning")
	}
}

func TestSelfCycle(t *testing.T) {
	g, _ := ParseGraph([]byte("nodes:\n  - id: a\n    requires: a\n"))
	issues := Validate(g)
	found := false
	for _, iss := range issues {
		if strings.Contains(iss.Msg, "itself") {
			found = true
		}
	}
	if !found {
		t.Error("self-dependency not reported")
	}
}

func TestComputeStatuses(t *testing.T) {
	g := parseSample(t)
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	rs := NewRunState(now)

	st := ComputeStatuses(g, rs)
	if st["intro"] != StatusReady {
		t.Errorf("intro = %s, want ready (no requires)", st["intro"])
	}
	if st["chapter-1"] != StatusReady {
		t.Errorf("chapter-1 = %s, want ready", st["chapter-1"])
	}
	// hidden-ending: (side-quest nor chapter-2) and chapter-1 — chapter-1 not
	// done yet, so locked.
	if st["hidden-ending"] != StatusReady {
		t.Errorf("hidden-ending = %s, want ready", st["hidden-ending"])
	}

	// Start and complete intro, then chapter-1.
	if err := rs.SetStatus("intro", StatusStarted, "tester", now); err != nil {
		t.Fatal(err)
	}
	if err := rs.SetStatus("intro", StatusInProgress, "tester", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := rs.SetStatus("intro", StatusDone, "tester", now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := rs.SetStatus("chapter-1", StatusStarted, "tester", now.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := rs.SetStatus("chapter-1", StatusInProgress, "tester", now.Add(4*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := rs.SetStatus("chapter-1", StatusDone, "tester", now.Add(5*time.Hour)); err != nil {
		t.Fatal(err)
	}
	st = ComputeStatuses(g, rs)
	if st["chapter-2"] != StatusReady {
		t.Errorf("chapter-2 = %s, want ready", st["chapter-2"])
	}
	// nor: neither side-quest nor chapter-2 done → hidden ending unlocks.
	if st["hidden-ending"] != StatusReady {
		t.Errorf("hidden-ending = %s, want ready", st["hidden-ending"])
	}
	// karma still 0 → side-quest locked.
	if st["side-quest"] != StatusReady {
		t.Errorf("side-quest = %s, want ready", st["side-quest"])
	}

	// Raise karma via runtime flags → side-quest unlocks.
	rs.Flags = map[string]any{"karma": 3}
	st = ComputeStatuses(g, rs)
	if st["side-quest"] != StatusReady {
		t.Errorf("side-quest = %s, want ready after karma=3", st["side-quest"])
	}

	// Complete chapter-2 → nor breaks → hidden ending locks again.
	if err := rs.SetStatus("chapter-2", StatusStarted, "tester", now.Add(6*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := rs.SetStatus("chapter-2", StatusInProgress, "tester", now.Add(7*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := rs.SetStatus("chapter-2", StatusDone, "tester", now.Add(8*time.Hour)); err != nil {
		t.Fatal(err)
	}
	st = ComputeStatuses(g, rs)
	if st["hidden-ending"] != StatusReady {
		t.Errorf("hidden-ending = %s, want ready after chapter-2 done", st["hidden-ending"])
	}
}

func TestSetStatusHistory(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	rs := NewRunState(now)
	if err := rs.SetStatus("a", StatusStarted, "agent-1", now); err != nil {
		t.Fatal(err)
	}
	if err := rs.SetStatus("a", StatusInProgress, "agent-1", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := rs.SetStatus("a", StatusDone, "agent-1", now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(rs.History) != 3 {
		t.Fatalf("history = %d events, want 3", len(rs.History))
	}
	if rs.History[2].From != StatusInProgress || rs.History[2].To != StatusDone {
		t.Errorf("history[2] wrong: %+v", rs.History[2])
	}
	if err := rs.SetStatus("a", "locked", "x", now); err == nil {
		t.Error("setting locked directly should fail")
	}
	if err := rs.SetStatus("a", StatusStarted, "x", now.Add(3*time.Hour)); err != nil {
		t.Errorf("editable status rewind failed: %v", err)
	}

	// Round trip through JSON.
	data, err := MarshalRunState(rs)
	if err != nil {
		t.Fatal(err)
	}
	rs2, err := ParseRunState(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs2.History) != 4 {
		t.Error("history lost in round trip")
	}
}

func TestCanTransition(t *testing.T) {
	tests := []struct {
		from Status
		to   Status
		want bool
	}{
		{StatusReady, StatusStarted, true},
		{StatusReady, StatusInProgress, true},
		{StatusStarted, StatusInProgress, true},
		{StatusInProgress, StatusDone, true},
		{StatusInProgress, StatusStarted, true},
		{StatusDone, StatusStarted, true},
		{StatusFailed, StatusStarted, true},
		{StatusReady, Status("custom-status-review"), true},
		{StatusReady, Status("custom-status-"), false},
		{StatusReady, Status("unknown"), false},
	}
	for _, tt := range tests {
		if got := CanTransition(tt.from, tt.to); got != tt.want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestPlanMilestonesRoundTrip(t *testing.T) {
	g := &Graph{
		Version: 1,
		Nodes:   []*Node{{ID: "build-api", Title: "Build API"}},
		UI: &UIState{Plans: map[string][]PlanMilestone{
			"build-api": {
				{Date: "2026-08-01", Status: StatusStarted, Note: "等待設計確認"},
				{Date: "2026-08-02", Status: StatusInProgress},
				{Date: "2026-08-03", Status: StatusDone},
			},
		}},
	}
	data, err := MarshalGraph(g)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseGraph(data)
	if err != nil {
		t.Fatal(err)
	}
	plans := parsed.UI.Plans["build-api"]
	if len(plans) != 3 || plans[0].Date != "2026-08-01" || plans[0].Note != "等待設計確認" || plans[2].Status != StatusDone {
		t.Fatalf("plan milestones lost in round trip: %+v", plans)
	}
}

func TestCustomStatusesRoundTrip(t *testing.T) {
	g := &Graph{
		Version: 1,
		Nodes:   []*Node{{ID: "review", Title: "Review"}},
		UI: &UIState{CustomStatuses: []StatusDefinition{{
			ID:      "custom-status-review",
			Label:   "審核中",
			Color:   "#8b7cf6",
			Shape:   "diamond",
			Settled: true,
		}}},
	}
	data, err := MarshalGraph(g)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseGraph(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.UI.CustomStatuses; len(got) != 1 ||
		got[0].ID != "custom-status-review" ||
		got[0].Label != "審核中" ||
		got[0].Color != "#8b7cf6" ||
		got[0].Shape != "diamond" ||
		!got[0].Settled {
		t.Fatalf("custom statuses lost in round trip: %+v", got)
	}

	// Settled is omitted when off, so a state that blocks stays the default
	// on disk instead of writing "settled: false" into every project.
	plain, err := MarshalGraph(&Graph{
		Version: 1,
		Nodes:   []*Node{{ID: "review", Title: "Review"}},
		UI: &UIState{CustomStatuses: []StatusDefinition{{
			ID: "custom-status-idea", Label: "構想", Color: "#888", Shape: "circle",
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "settled") {
		t.Errorf("a blocking state should not write settled:\n%s", plain)
	}
}

func TestStateFromJournalReplaysLifecycle(t *testing.T) {
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	events := []HistoryEvent{
		{ID: "ev-start", T: at, Event: "status", Node: "a", To: StatusReady},
		{ID: "ev-done", T: at, Event: "status", Node: "a", To: StatusDone},
		{ID: "ev-rewind", T: at, Event: "status", Node: "a", To: StatusInProgress},
	}
	var journal []byte
	for _, event := range events {
		line, err := AppendJournalLine(event)
		if err != nil {
			t.Fatal(err)
		}
		journal = append(journal, line...)
	}

	rs := StateFromJournal(journal)
	if got := rs.Nodes["a"].Status; got != StatusInProgress {
		t.Fatalf("final status = %s, want in_progress", got)
	}
	if len(rs.History) != 3 {
		t.Fatalf("replayed history = %d events, want 3", len(rs.History))
	}
	want := []Status{StatusReady, StatusDone, StatusInProgress}
	for i, status := range want {
		if rs.History[i].To != status {
			t.Errorf("history[%d] = %s, want %s", i, rs.History[i].To, status)
		}
	}
}

func TestNodeFileRoundTrip(t *testing.T) {
	src := "---\nid: build-api\ntitle: Build API\nkind: task\n---\n\n## Goal\nDo the thing.\n"
	nf, err := ParseNodeFile([]byte(src))
	if err != nil {
		t.Fatalf("ParseNodeFile: %v", err)
	}
	if nf.Meta["id"] != "build-api" {
		t.Errorf("meta id = %v", nf.Meta["id"])
	}
	if !strings.Contains(nf.Body, "## Goal") {
		t.Errorf("body wrong: %q", nf.Body)
	}

	n := &Node{ID: "build-api", Title: "Build API v2", Kind: "task", Requires: "setup-db"}
	SyncFrontmatter(nf, n)
	out, err := ComposeNodeFile(nf)
	if err != nil {
		t.Fatalf("ComposeNodeFile: %v", err)
	}
	nf2, err := ParseNodeFile(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if nf2.Meta["title"] != "Build API v2" || nf2.Meta["requires"] != "setup-db" {
		t.Errorf("sync lost: %v", nf2.Meta)
	}
	if !strings.Contains(nf2.Body, "## Goal") {
		t.Errorf("body lost: %q", nf2.Body)
	}
}

func TestNodeFileRoundTripStable(t *testing.T) {
	src := "---\nid: x\n---\n\n## Body\ntext\n"
	cur := []byte(src)
	for i := range 3 {
		nf, err := ParseNodeFile(cur)
		if err != nil {
			t.Fatalf("iter %d parse: %v", i, err)
		}
		out, err := ComposeNodeFile(nf)
		if err != nil {
			t.Fatalf("iter %d compose: %v", i, err)
		}
		if i > 0 && string(out) != string(cur) {
			t.Fatalf("iter %d not stable:\n%q\nvs\n%q", i, cur, out)
		}
		cur = out
	}
	if strings.Contains(string(cur), "\n\n\n") {
		t.Fatalf("blank lines accumulated: %q", cur)
	}
}

func TestNodeFileNoFrontmatter(t *testing.T) {
	nf, err := ParseNodeFile([]byte("plain markdown\n"))
	if err != nil {
		t.Fatal(err)
	}
	if nf.Body != "plain markdown\n" || len(nf.Meta) != 0 {
		t.Errorf("wrong parse: %+v", nf)
	}
}
