package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readyGraph exercises every reason a node is or is not actionable: a plain
// prerequisite, an optional wire that must not block, a deprecated parent that
// counts as finished, a flag condition, and a gate.
const readyGraph = `
version: 1
type: workflow
nodes:
  - id: design
    title: Design
    priority: high
  - id: build
    title: Build
    requires: design
  - id: nice-to-have
    title: Nice to have
    priority: low
  - id: ship
    title: Ship
    priority: urgent
    deadline: "2026-01-01"
    requires: flag(approved)
edges:
  - from: design
    to: build
  - from: nice-to-have
    to: build
    relation: optional
flags:
  approved: false
`

func parseReady(t *testing.T) *Graph {
	t.Helper()
	g, err := ParseGraph([]byte(readyGraph))
	if err != nil {
		t.Fatalf("ParseGraph: %v", err)
	}
	return g
}

// stateWith builds a RunState in which the named nodes carry the given status.
func stateWith(entries map[string]Status) *RunState {
	rs := &RunState{Nodes: map[string]*NodeState{}}
	for id, status := range entries {
		rs.Nodes[id] = &NodeState{Status: status}
	}
	return rs
}

func ids(tasks []ReadyNode) []string {
	out := make([]string, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task.ID)
	}
	return out
}

func TestAnUnfinishedPrerequisiteKeepsANodeOutOfTheQueue(t *testing.T) {
	g := parseReady(t)
	readiness := ComputeReadiness(g, stateWith(nil))

	if got := strings.Join(ids(readiness.Ready), ","); got != "design,nice-to-have" {
		t.Fatalf("ready = %q, want design and nice-to-have", got)
	}
	// build waits on design; ship waits on its flag.
	blockedFor := map[string]ReadyNode{}
	for _, task := range readiness.Blocked {
		blockedFor[task.ID] = task
	}
	build, ok := blockedFor["build"]
	if !ok {
		t.Fatalf("build should be blocked, blocked = %v", ids(readiness.Blocked))
	}
	if build.Reason != "prerequisites" || strings.Join(build.BlockedBy, ",") != "design" {
		t.Fatalf("build blocked by %v (%s), want design via prerequisites", build.BlockedBy, build.Reason)
	}
	// The optional wire from nice-to-have must not appear: an optional edge is
	// drawn, not enforced.
	for _, id := range build.BlockedBy {
		if id == "nice-to-have" {
			t.Fatal("an optional edge blocked its target")
		}
	}
}

func TestFinishingThePrerequisiteReleasesTheNode(t *testing.T) {
	g := parseReady(t)
	readiness := ComputeReadiness(g, stateWith(map[string]Status{"design": StatusDone}))

	// nice-to-have leads because it states a priority at all; build states none.
	if got := strings.Join(ids(readiness.Ready), ","); got != "nice-to-have,build" {
		t.Fatalf("ready = %q, want nice-to-have and build", got)
	}
	if readiness.Busy != 1 {
		t.Fatalf("busy = %d, want 1 (design)", readiness.Busy)
	}
}

// Dropped work is not owed work: a deprecated node must satisfy what depends on
// it, or abandoning a branch would wedge everything downstream of it forever.
func TestADeprecatedPrerequisiteDoesNotHoldUpItsTarget(t *testing.T) {
	g := parseReady(t)
	readiness := ComputeReadiness(g, stateWith(map[string]Status{"design": StatusDeprecated}))

	if got := strings.Join(ids(readiness.Ready), ","); !strings.Contains(got, "build") {
		t.Fatalf("ready = %q, want build released", got)
	}
}

func TestACustomStateOnlyCountsAsFinishedWhenItSaysSo(t *testing.T) {
	g := parseReady(t)
	g.UI = &UIState{CustomStatuses: []StatusDefinition{
		{ID: "custom-status-review", Label: "In review"},
	}}
	inReview := stateWith(map[string]Status{"design": "custom-status-review"})

	if got := strings.Join(ids(ComputeReadiness(g, inReview).Blocked), ","); !strings.Contains(got, "build") {
		t.Fatalf("an unsettled custom state released its dependant: blocked = %q", got)
	}

	g.UI.CustomStatuses[0].Settled = true
	readiness := ComputeReadiness(g, inReview)
	if got := strings.Join(ids(readiness.Ready), ","); !strings.Contains(got, "build") {
		t.Fatalf("ready = %q, want build released by the settled custom state", got)
	}
}

func TestAFalseRequiresExpressionHoldsANodeBack(t *testing.T) {
	g := parseReady(t)
	readiness := ComputeReadiness(g, stateWith(nil))

	for _, task := range readiness.Blocked {
		if task.ID == "ship" {
			if task.Reason != "requires" {
				t.Fatalf("ship blocked for %q, want requires", task.Reason)
			}
			return
		}
	}
	t.Fatalf("ship should wait on its flag, blocked = %v", ids(readiness.Blocked))
}

func TestSettingTheFlagReleasesTheNode(t *testing.T) {
	g := parseReady(t)
	rs := stateWith(nil)
	rs.Flags = map[string]any{"approved": true}

	readiness := ComputeReadiness(g, rs)
	if got := strings.Join(ids(readiness.Ready), ","); !strings.HasPrefix(got, "ship") {
		t.Fatalf("ready = %q, want ship first once approved", got)
	}
}

// A typo in a requires expression must not be read as "no condition". The
// queue is the one place where failing open would hand an agent work it was
// explicitly told to wait on.
func TestAnUnparseableRequiresExpressionDoesNotGrantPermission(t *testing.T) {
	g := parseReady(t)
	g.NodeByID("nice-to-have").Requires = "design and and"

	readiness := ComputeReadiness(g, stateWith(nil))
	for _, task := range readiness.Ready {
		if task.ID == "nice-to-have" {
			t.Fatal("a node with a broken requires expression was offered as ready")
		}
	}
}

func TestRequiresTruthTableMatchesTheWebAnalyzer(t *testing.T) {
	var contract struct {
		Cases []struct {
			Name         string         `json:"name"`
			Requires     string         `json:"requires"`
			Done         []string       `json:"done"`
			GraphFlags   map[string]any `json:"graphFlags"`
			RuntimeFlags map[string]any `json:"runtimeFlags"`
			Valid        *bool          `json:"valid"`
			Satisfied    bool           `json:"satisfied"`
			BlockedBy    []string       `json:"blockedBy"`
		} `json:"cases"`
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "requires_truth_table.json"))
	if err != nil {
		t.Fatalf("read requires truth table: %v", err)
	}
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatalf("parse requires truth table: %v", err)
	}
	for _, tc := range contract.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			g := &Graph{
				Nodes: []*Node{
					{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "chapter-2"},
					{ID: "target", Requires: tc.Requires},
				},
				Flags: tc.GraphFlags,
			}
			statuses := map[string]Status{}
			for _, id := range tc.Done {
				statuses[id] = StatusDone
			}
			rs := stateWith(statuses)
			rs.Flags = tc.RuntimeFlags
			readiness := ComputeReadiness(g, rs)
			var target *ReadyNode
			for index := range readiness.Blocked {
				if readiness.Blocked[index].ID == "target" {
					target = &readiness.Blocked[index]
					break
				}
			}
			if tc.Satisfied {
				if target != nil {
					t.Fatalf("target blocked by %v, want satisfied", target.BlockedBy)
				}
				return
			}
			if target == nil {
				t.Fatal("false requires expression released target")
			}
			if strings.Join(target.BlockedBy, ",") != strings.Join(tc.BlockedBy, ",") {
				t.Fatalf("blocked by %v, want %v", target.BlockedBy, tc.BlockedBy)
			}
			if tc.Valid != nil && !*tc.Valid && target.Reason != "requires" {
				t.Fatalf("parse error reason = %q, want requires", target.Reason)
			}
		})
	}
}

func TestRequiresIsAuthoritativeOverLegacyEdgeDrift(t *testing.T) {
	t.Run("edge only does not block", func(t *testing.T) {
		g := &Graph{
			Nodes: []*Node{{ID: "a"}, {ID: "target"}},
			Edges: []*Edge{{From: "a", To: "target"}},
		}
		for _, task := range ComputeReadiness(g, stateWith(nil)).Blocked {
			if task.ID == "target" {
				t.Fatalf("visual-only edge blocked target: %+v", task)
			}
		}
	})

	t.Run("requires only blocks", func(t *testing.T) {
		g := &Graph{Nodes: []*Node{{ID: "a"}, {ID: "target", Requires: "a"}}}
		blocked := Blocked(g, nil)
		if got, ok := blocked["target"]; !ok || strings.Join(got, ",") != "a" {
			t.Fatalf("target blocked by %v (%v), want requires-only a", got, ok)
		}
	})
}

func materializedGateRequires(operator string, inputs []string) string {
	if len(inputs) == 0 {
		return ""
	}
	switch operator {
	case "must":
		return inputs[0]
	case "and", "or", "xor":
		return strings.Join(inputs, " "+operator+" ")
	case "nand":
		return "not (" + strings.Join(inputs, " and ") + ")"
	case "nor":
		return "not (" + strings.Join(inputs, " or ") + ")"
	}
	return ""
}

func TestAGateDecidesWhichParentsMustFinish(t *testing.T) {
	g := parseReady(t)
	// Either parent will do, so finishing one releases the target even though
	// the other is untouched.
	g.Edges = append(g.Edges, &Edge{From: "nice-to-have", To: "build"})
	g.Edges[1].Relation = RelationRequired
	g.NodeByID("build").Requires = "design or nice-to-have"
	g.UI = &UIState{LogicGates: []LogicGate{{
		ID:       "gate-1",
		Operator: "or",
		Inputs:   []string{"design", "nice-to-have"},
		Output:   "build",
	}}}

	readiness := ComputeReadiness(g, stateWith(map[string]Status{"design": StatusDone}))
	// The first-class gate's OR is materialized into requires and evaluated by
	// the same DSL path as every other executable condition.
	if got := strings.Join(ids(readiness.Ready), ","); !strings.Contains(got, "build") {
		t.Fatalf("ready = %q, want the or-gate to release build", got)
	}

	readiness = ComputeReadiness(g, stateWith(nil))
	for _, task := range readiness.Ready {
		if task.ID == "build" {
			t.Fatal("an or-gate released its target with no parent finished")
		}
	}
}

// A half-wired gate is kept on the canvas as a draft. Reading it as satisfied
// would mean drawing a gate and walking away silently unblocked the target.
func TestAHalfWiredGateSatisfiesNothing(t *testing.T) {
	draft := LogicGate{Operator: "and", Inputs: []string{"design"}, Output: "build"}
	if gateSatisfied(draft, map[string]bool{"design": true}) {
		t.Fatal("an and-gate with one input was treated as satisfied")
	}
}

func TestLogicGatesFollowTheSharedTruthTable(t *testing.T) {
	var contract struct {
		Cases []struct {
			Name         string   `json:"name"`
			Operator     string   `json:"operator"`
			Inputs       []string `json:"inputs"`
			Done         []string `json:"done"`
			Satisfied    bool     `json:"satisfied"`
			BlockedBy    []string `json:"blockedBy"`
			EdgeRelation string   `json:"edgeRelation"`
		} `json:"cases"`
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "logic_gate_truth_table.json"))
	if err != nil {
		t.Fatalf("read gate truth table: %v", err)
	}
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatalf("parse gate truth table: %v", err)
	}
	if len(contract.Cases) == 0 {
		t.Fatal("gate truth table has no cases")
	}

	for _, tc := range contract.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			nodes := make([]*Node, 0, len(tc.Inputs)+1)
			edges := make([]*Edge, 0, len(tc.Inputs))
			for _, id := range tc.Inputs {
				nodes = append(nodes, &Node{ID: id})
				edges = append(edges, &Edge{
					From: id, To: "target", Relation: tc.EdgeRelation,
				})
			}
			nodes = append(nodes, &Node{
				ID: "target", Requires: materializedGateRequires(tc.Operator, tc.Inputs),
			})
			g := &Graph{
				Nodes: nodes,
				Edges: edges,
				UI: &UIState{LogicGates: []LogicGate{{
					ID:       "gate-contract",
					Operator: tc.Operator,
					Inputs:   tc.Inputs,
					Output:   "target",
				}}},
			}
			statuses := map[string]Status{}
			for _, id := range tc.Done {
				statuses[id] = StatusDone
			}

			got, blocked := Blocked(g, statuses)["target"]
			if tc.Satisfied {
				if blocked {
					t.Fatalf("target blocked by %v, want satisfied", got)
				}
				return
			}
			if !blocked {
				t.Fatal("false gate released its target")
			}
			if strings.Join(got, ",") != strings.Join(tc.BlockedBy, ",") {
				t.Fatalf("blocked by %v, want %v", got, tc.BlockedBy)
			}
			readiness := ComputeReadiness(g, stateWith(statuses))
			for _, task := range readiness.Blocked {
				if task.ID != "target" {
					continue
				}
				if task.Reason != "gate_condition" || task.GateOperator != tc.Operator {
					t.Fatalf("reason = %q (%q), want gate_condition (%q)",
						task.Reason, task.GateOperator, tc.Operator)
				}
				return
			}
			t.Fatal("false gate target missing from readiness.Blocked")
		})
	}
}

func TestTheQueueIsOrderedByPriorityThenDeadline(t *testing.T) {
	g := &Graph{Nodes: []*Node{
		{ID: "c", Priority: "low"},
		{ID: "a", Priority: "urgent", Deadline: "2026-03-01"},
		{ID: "b", Priority: "urgent", Deadline: "2026-01-01"},
		{ID: "e"},
		{ID: "d", Priority: "medium"},
	}}

	readiness := ComputeReadiness(g, stateWith(nil))
	if got := strings.Join(ids(readiness.Ready), ","); got != "b,a,d,c,e" {
		t.Fatalf("order = %q, want b,a,d,c,e", got)
	}
}

// Two nodes alike in every sorted field must still come back in one order, or
// paging through the queue would show one twice and skip another.
func TestTheOrderIsTotal(t *testing.T) {
	g := &Graph{Nodes: []*Node{{ID: "second"}, {ID: "first"}}}

	if got := strings.Join(ids(ComputeReadiness(g, stateWith(nil)).Ready), ","); got != "first,second" {
		t.Fatalf("order = %q, want first,second", got)
	}
}

// A canonical dependency loop is present in both requires and its wire
// projection. The executable condition is what stops the queue; the matching
// wires still let validation point at the broken drawing too.
func TestALoopInTheWiresIsReportedAndStopsEverythingInIt(t *testing.T) {
	g := parseReady(t)
	g.Edges = append(g.Edges, &Edge{From: "build", To: "design"})
	g.NodeByID("design").Requires = "build"

	found := false
	for _, issue := range Validate(g) {
		if issue.Field == "edges" && strings.Contains(issue.Msg, "cycle") {
			found = true
			if !strings.Contains(issue.Msg, "projection") {
				t.Fatalf("message = %q, want it to identify projection drift", issue.Msg)
			}
		}
	}
	if !found {
		t.Fatal("a cycle through the wires was not reported")
	}

	readiness := ComputeReadiness(g, stateWith(nil))
	for _, task := range readiness.Ready {
		if task.ID == "design" || task.ID == "build" {
			t.Fatalf("%s was offered despite sitting in a dependency loop", task.ID)
		}
	}
}

func TestASettledNodeIsNotReportedAsBlocked(t *testing.T) {
	g := parseReady(t)
	readiness := ComputeReadiness(g, stateWith(map[string]Status{"build": StatusDone}))

	for _, task := range readiness.Blocked {
		if task.ID == "build" {
			t.Fatal("a finished node was listed as waiting on its prerequisites")
		}
	}
}
