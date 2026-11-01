package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"nodevas/internal/engine"
)

// validateWithin runs ValidateGraphForStorage on another goroutine and fails
// the test when it takes longer than limit.
func validateWithin(t *testing.T, limit time.Duration, g *engine.Graph) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- ValidateGraphForStorage(g) }()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		t.Fatalf("validating the graph took longer than %v", limit)
		return nil
	}
}

// Writing the edge of a bent requires-only wire looked for a gate owning it by
// scanning every input of every gate, once per bent wire, before any gate's
// inputs were counted: sixty thousand bends and one gate with a million inputs
// kept an editor's save busy for about a minute.
func TestValidateGraphForStorageFindsWireGatesWithoutRescanning(t *testing.T) {
	const nodes, refs = 1000, 60
	g := &engine.Graph{Version: 1, UI: &engine.UIState{WireVertices: map[string][]engine.Position{}}}
	for i := range nodes {
		names := make([]string, refs)
		for k := range refs {
			from := fmt.Sprintf("n%d", (i+k+1)%nodes)
			names[k] = from
			g.UI.WireVertices[from+"->"+fmt.Sprintf("n%d", i)] = []engine.Position{{X: 1, Y: 1}}
		}
		g.Nodes = append(g.Nodes, &engine.Node{ID: fmt.Sprintf("n%d", i), Title: "N", Requires: strings.Join(names, " or ")})
	}
	inputs := make([]string, 1_000_000)
	for i := range inputs {
		inputs[i] = "z"
	}
	g.UI.LogicGates = []engine.LogicGate{{ID: "g", Operator: "optional", Inputs: inputs, Outputs: []string{"z"}}}
	if err := validateWithin(t, 2*time.Second, g); err == nil {
		t.Fatal("a graph with sixty thousand dependencies and a million gate inputs was accepted")
	}
}

// A bent wire a gate draws is not written as a direct edge; any other bent
// requires-only wire is.
func TestValidateGraphForStorageWritesOnlyUngatedRequiresWires(t *testing.T) {
	bend := []engine.Position{{X: 1, Y: 1}}
	g := &engine.Graph{
		Version: 1,
		Nodes: []*engine.Node{
			{ID: "a", Title: "A"},
			{ID: "c", Title: "C"},
			{ID: "b", Title: "B", Requires: "a and c"},
			{ID: "d", Title: "D", Requires: "a and c"},
			{ID: "e", Title: "E", Requires: "a or c"},
		},
		UI: &engine.UIState{
			WireVertices: map[string][]engine.Position{
				"a->b": bend, "c->b": bend, // b's condition is drawn by an AND gate
				"a->d": bend, "c->d": bend, // the optional gate draws only c->d
				"a->e": bend,
			},
			LogicGates: []engine.LogicGate{
				{ID: "and", Operator: "and", Inputs: []string{"a", "c"}, Output: "b"},
				{ID: "opt", Operator: "optional", Inputs: []string{"c"}, Outputs: []string{"d"}},
			},
		},
	}
	if err := ValidateGraphForStorage(g); err != nil {
		t.Fatalf("validate: %v", err)
	}
	var got []string
	for _, edge := range g.Edges {
		got = append(got, edge.From+"->"+edge.To)
	}
	written := strings.Join(got, " ")
	for _, want := range []string{"a->d", "a->e"} {
		if !strings.Contains(written, want) {
			t.Errorf("edges = %q, want %s written", written, want)
		}
	}
	for _, gated := range []string{"a->b", "c->b", "c->d"} {
		if strings.Contains(written, gated) {
			t.Errorf("edges = %q, want gated %s not written", written, gated)
		}
	}
}
