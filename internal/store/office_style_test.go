package store

import (
	"net/http"
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

// The office style is the people's to choose, like the node type list: an
// agent saving the graph may carry it along unchanged but not change it.
func TestAnAgentCannotChangeTheOfficeStyle(t *testing.T) {
	st := typedStore(t)
	agent := agentActor(identity.AgentOrchestrator)

	err := saveWith(t, st, agent, func(g *engine.Graph) { g.OfficeStyle = engine.OfficeStyleCafe })
	wantTypeCode(t, err, CodeOfficeStyleAgentEdit)
	if denied := err.(*ErrNodeTypeDenied); denied.Status != http.StatusForbidden {
		t.Fatalf("status = %d", denied.Status)
	}

	if err := saveWith(t, st, identity.Local, func(g *engine.Graph) { g.OfficeStyle = engine.OfficeStyleStudy }); err != nil {
		t.Fatalf("a person setting the style: %v", err)
	}
	// Unchanged style, other edits: allowed.
	if err := saveWith(t, st, agent, func(g *engine.Graph) { g.NodeByID("work").Title = "Work more" }); err != nil {
		t.Fatalf("an agent saving with the style unchanged: %v", err)
	}
	// Clearing it is a change too.
	err = saveWith(t, st, agent, func(g *engine.Graph) { g.OfficeStyle = "" })
	wantTypeCode(t, err, CodeOfficeStyleAgentEdit)
	g, _, err := st.LoadGraph()
	if err != nil || g.OfficeStyle != engine.OfficeStyleStudy {
		t.Fatalf("stored style = %q, %v", g.OfficeStyle, err)
	}
	if err := saveWith(t, st, identity.Local, func(g *engine.Graph) { g.OfficeStyle = "" }); err != nil {
		t.Fatalf("a person clearing the style: %v", err)
	}
}

func TestAnUnknownOfficeStyleIsNotStored(t *testing.T) {
	for _, bad := range []string{"Default", "space", " study"} {
		err := ValidateGraphForStorage(&engine.Graph{Version: 1, OfficeStyle: bad})
		if err == nil || !strings.Contains(err.Error(), "office_style") {
			t.Fatalf("%q: err = %v", bad, err)
		}
	}
	for _, ok := range append([]string{""}, engine.OfficeStyles...) {
		if err := ValidateGraphForStorage(&engine.Graph{Version: 1, OfficeStyle: ok}); err != nil {
			t.Fatalf("%q: %v", ok, err)
		}
	}
}
