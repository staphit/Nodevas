package store

import (
	"strings"
	"testing"

	"nodevas/internal/engine"
)

// ValidateGraphForStorage writes the edge for a bent requires-only wire, and
// that parsed the target's requires before its 64 KiB cap was checked: an
// editor's two million parentheses on a bent wire overflowed the parser's
// stack, a fatal error that took the hub down.
func TestValidateGraphForStorageRefusesHugeRequiresBeforeParsing(t *testing.T) {
	g := &engine.Graph{
		Version: 1,
		Nodes: []*engine.Node{
			{ID: "a", Title: "A"},
			{ID: "b", Title: "B", Requires: strings.Repeat("(", 2_000_000) + "a"},
		},
		UI: &engine.UIState{WireVertices: map[string][]engine.Position{"a->b": {{X: 1, Y: 1}}}},
	}
	err := ValidateGraphForStorage(g)
	if err == nil || !strings.Contains(err.Error(), "oversized") {
		t.Fatalf("error = %v, want the oversized requirement refused", err)
	}
}

// The size checks run before any bent wire's edge is written: an oversized
// requires that parses is refused without the work of materialising it.
func TestValidateGraphForStorageChecksSizesBeforeWritingWires(t *testing.T) {
	g := &engine.Graph{
		Version: 1,
		Nodes: []*engine.Node{
			{ID: "a", Title: "A"},
			{ID: "b", Title: "B", Requires: "a" + strings.Repeat(" or a", 70<<10/5)},
		},
		UI: &engine.UIState{WireVertices: map[string][]engine.Position{"a->b": {{X: 1, Y: 1}}}},
	}
	err := ValidateGraphForStorage(g)
	if err == nil || !strings.Contains(err.Error(), "oversized") {
		t.Fatalf("error = %v, want the oversized requirement refused", err)
	}
	if len(g.Edges) != 0 {
		t.Fatalf("edges = %+v, want none written before the size check", g.Edges)
	}
}
