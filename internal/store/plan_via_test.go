package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"nodevas/internal/identity"
)

// A coding agent's plan draft (P6) is committed by the person with via
// "copilot-plan:<entry>": the general cap of 100, not the copilot's 40, and
// its own line in the history.
func TestCopilotPlanViaTakesAHundredNodesAndNamesTheHistory(t *testing.T) {
	nodes := func(n int) []SubgraphNode {
		out := make([]SubgraphNode, n)
		for i := range out {
			out[i] = SubgraphNode{Key: fmt.Sprintf("k%d", i), Title: "T"}
		}
		return out
	}
	st := subgraphStore(t)
	result, err := st.CreateSubgraph(identity.Local, SubgraphSpec{Nodes: nodes(100), Batch: &BatchSpec{Via: CopilotPlanVia + "e1"}})
	if err != nil {
		t.Fatalf("100 plan nodes: %v", err)
	}
	if !result.Undoable || len(result.IDs) != 100 {
		t.Fatalf("result: undoable %v, %d ids", result.Undoable, len(result.IDs))
	}
	var tooLarge *ErrSubgraphTooLarge
	if _, err := st.CreateSubgraph(identity.Local, SubgraphSpec{Nodes: nodes(41), Batch: &BatchSpec{Via: CopilotDraftVia + "d1"}}); !errors.As(err, &tooLarge) {
		t.Fatalf("41 copilot draft nodes: %v", err)
	}
	if _, err := st.CreateSubgraph(identity.Local, SubgraphSpec{Nodes: nodes(101), Batch: &BatchSpec{Via: CopilotPlanVia + "e1"}}); !errors.As(err, &tooLarge) {
		t.Fatalf("101 plan nodes: %v", err)
	}

	git, root := newGitStore(t, time.Hour)
	if _, err := git.CreateSubgraph(identity.Local, SubgraphSpec{
		Nodes: []SubgraphNode{{Key: "a", Title: "Plan node"}},
		Batch: &BatchSpec{Via: "copilot-plan:e7"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := git.FlushHistory(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, message := headMessage(t, root); !strings.Contains(message,
		"Plan node: created from a coding agent's plan (via Copilot) (copilot-plan:e7)") {
		t.Fatalf("message = %q", message)
	}
}
