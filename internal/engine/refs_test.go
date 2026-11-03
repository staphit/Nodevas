package engine

import (
	"reflect"
	"strings"
	"testing"
)

const refsBase = `
version: 1
type: story
nodes:
  - id: note-01
    title: Character sheet
    kind: note
  - id: s-01
    title: Scene one
    kind: scene
  - id: s-02
    title: Scene two
    kind: scene
    requires: s-01
  - id: s-03
    title: Scene three
    kind: scene
edges:
  - from: s-01
    to: s-02
`

// withRefs adds references that would form a "cycle" if they were
// dependencies (s-01 -> s-02 -> s-01) and a reference to a note.
func withRefs(t *testing.T) *Graph {
	t.Helper()
	g, err := ParseGraph([]byte(refsBase))
	if err != nil {
		t.Fatal(err)
	}
	g.NodeByID("s-01").Refs = []string{"note-01", "s-02"}
	g.NodeByID("s-02").Refs = []string{"s-01", "note-01"}
	g.NodeByID("s-03").Refs = []string{"note-01", "missing"}
	return g
}

func TestReferencesNeverChangeReadinessOrValidation(t *testing.T) {
	plain, err := ParseGraph([]byte(refsBase))
	if err != nil {
		t.Fatal(err)
	}
	refd := withRefs(t)
	for _, rs := range []*RunState{nil, stateWith(map[string]Status{"s-01": StatusDone})} {
		if a, b := ComputeReadiness(plain, rs), ComputeReadiness(refd, rs); !reflect.DeepEqual(a, b) {
			t.Fatalf("readiness differs:\n%+v\n%+v", a, b)
		}
		if a, b := ComputeStatuses(plain, rs), ComputeStatuses(refd, rs); !reflect.DeepEqual(a, b) {
			t.Fatalf("statuses differ:\n%v\n%v", a, b)
		}
		statuses := ComputeStatuses(plain, rs)
		if a, b := Blocked(plain, statuses), Blocked(refd, statuses); !reflect.DeepEqual(a, b) {
			t.Fatalf("blocked differs:\n%v\n%v", a, b)
		}
	}
	if a, b := Validate(plain), Validate(refd); !reflect.DeepEqual(a, b) {
		t.Fatalf("validation differs:\n%+v\n%+v", a, b)
	}
	// s-03 has only references: still reported as not connected.
	found := false
	for _, issue := range Validate(refd) {
		if issue.NodeID == "s-03" && strings.Contains(issue.Msg, "not connected") {
			found = true
		}
	}
	if !found {
		t.Fatal("a reference connected a node to the flow")
	}
}

func TestReferencedByListsUsersInGraphOrder(t *testing.T) {
	g := withRefs(t)
	if got := g.ReferencedBy("note-01"); !reflect.DeepEqual(got, []string{"s-01", "s-02", "s-03"}) {
		t.Fatalf("ReferencedBy = %v", got)
	}
	if !g.NodeByID("s-01").HasRef("s-02") || g.NodeByID("s-01").HasRef("s-03") {
		t.Fatal("HasRef wrong")
	}
}

func TestLegacyKindsAreOneHintPerProject(t *testing.T) {
	g, err := ParseGraph([]byte(`
version: 1
nodes:
  - id: a
    kind: bug
  - id: b
    kind: bug
    requires: a
  - id: c
    kind: 伏筆
    requires: b
  - id: d
    kind: bug
    requires: c
  - id: e
    kind: 伏筆
    requires: d
`))
	if err != nil {
		t.Fatal(err)
	}
	var hints []Issue
	for _, issue := range Validate(g) {
		if issue.Field == "kind" {
			hints = append(hints, issue)
		}
	}
	if len(hints) != 1 {
		t.Fatalf("kind issues = %+v, want exactly one", hints)
	}
	hint := hints[0]
	if hint.Severity != "info" || hint.NodeID != "a" ||
		!strings.Contains(hint.Msg, `"bug" ×3`) || !strings.Contains(hint.Msg, `"伏筆" ×2`) ||
		!strings.Contains(hint.Msg, "5 node(s)") {
		t.Fatalf("hint = %+v", hint)
	}
}

func TestEffectsOnAnAutomaticNodeAreWarnedAndNotApplied(t *testing.T) {
	g, err := ParseGraph([]byte(`
version: 1
flags:
  finished: false
nodes:
  - id: a
  - id: fin
    kind: end
    requires: a
    effects:
      - set: finished = true
`))
	if err != nil {
		t.Fatal(err)
	}
	var warnings []Issue
	for _, issue := range Validate(g) {
		if issue.Field == "effects" {
			warnings = append(warnings, issue)
		}
	}
	if len(warnings) != 1 || warnings[0].Severity != "warning" || warnings[0].NodeID != "fin" {
		t.Fatalf("effects issues = %+v", warnings)
	}
	rs := stateWith(map[string]Status{"a": StatusDone})
	if got := ComputeStatuses(g, rs)["fin"]; got != StatusDone {
		t.Fatalf("end status = %q, want done", got)
	}
	if got := EffectiveFlags(g, rs)["finished"]; got != false {
		t.Fatalf("flag = %v, want unchanged false", got)
	}
}
