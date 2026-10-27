package store

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

func typedBool(v bool) *bool { return &v }

var foreshadowType = engine.NodeTypeDef{
	ID: "custom-type-1", Label: "伏筆", Shape: "diamond",
	AgentMode: engine.AgentNone, Completion: engine.CompletionNone, CountsInProgress: typedBool(false),
}

// typedStore is a project with one node of each type the tests need.
func typedStore(t *testing.T) *Store {
	t.Helper()
	root := t.TempDir()
	st := NewStore(root)
	g := &engine.Graph{
		Version:   1,
		NodeTypes: []engine.NodeTypeDef{foreshadowType},
		Nodes: []*engine.Node{
			{ID: "work", Title: "Work"},
			{ID: "review", Title: "Review", Kind: "gate"},
			{ID: "pick", Title: "Pick", Kind: "choice"},
			{ID: "open", Title: "Open", Kind: "start"},
			{ID: "lore", Title: "Lore", Kind: "note"},
			{ID: "hint", Title: "Hint", Kind: "custom-type-1"},
		},
	}
	if _, err := st.SaveGraph(identity.Local, g, ""); err != nil {
		t.Fatal(err)
	}
	return st
}

func wantTypeCode(t *testing.T, err error, code string) {
	t.Helper()
	var denied *ErrNodeTypeDenied
	if !errors.As(err, &denied) || denied.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
}

func saveWith(t *testing.T, st *Store, actor identity.Actor, mutate func(*engine.Graph)) error {
	t.Helper()
	g, rev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	mutate(g)
	_, err = st.SaveGraph(actor, g, rev)
	return err
}

func TestValidateGraphForStorageChecksNodeTypes(t *testing.T) {
	bad := &engine.Graph{Version: 1, NodeTypes: []engine.NodeTypeDef{{ID: "gate", AgentMode: engine.AgentDispatch}}}
	if err := ValidateGraphForStorage(bad); err == nil || !strings.Contains(err.Error(), "only shape and color") {
		t.Fatalf("err = %v", err)
	}
	legacy := &engine.Graph{Version: 1, Nodes: []*engine.Node{{ID: "a", Kind: "bug"}}}
	if err := ValidateGraphForStorage(legacy); err != nil {
		t.Fatalf("a legacy kind must stay storable: %v", err)
	}
	// Free-text kinds from before node types (spaces, CJK) still save.
	freeText := &engine.Graph{Version: 1, Nodes: []*engine.Node{{ID: "a", Kind: "伏筆 note"}}}
	if err := ValidateGraphForStorage(freeText); err != nil {
		t.Fatalf("a legacy free-text kind must stay storable: %v", err)
	}
	odd := &engine.Graph{Version: 1, Nodes: []*engine.Node{{ID: "a", Kind: "bad\x00kind"}}}
	if err := ValidateGraphForStorage(odd); err == nil {
		t.Fatal("a kind with a control character was stored")
	}
}

func TestAnAgentCannotRedefineNodeTypes(t *testing.T) {
	st := typedStore(t)
	agent := agentActor(identity.AgentOrchestrator)
	err := saveWith(t, st, agent, func(g *engine.Graph) {
		g.NodeTypes = append(g.NodeTypes, engine.NodeTypeDef{ID: "gate", Shape: "round"})
	})
	wantTypeCode(t, err, CodeNodeTypesAgentEdit)
	if err := saveWith(t, st, agent, func(g *engine.Graph) { g.NodeByID("work").Title = "Renamed" }); err != nil {
		t.Fatalf("an agent saving without touching types was refused: %v", err)
	}
	if err := saveWith(t, st, identity.Local, func(g *engine.Graph) {
		g.NodeTypes = append(g.NodeTypes, engine.NodeTypeDef{ID: "gate", Shape: "round"})
	}); err != nil {
		t.Fatalf("a person was refused: %v", err)
	}
}

func TestAnAgentCannotRetypeAPeopleOnlyNode(t *testing.T) {
	st := typedStore(t)
	agent := agentActor(identity.AgentOrchestrator)
	err := saveWith(t, st, agent, func(g *engine.Graph) { g.NodeByID("review").Kind = "task" })
	wantTypeCode(t, err, CodeNodeTypePeopleOnly)
	_, _, err = st.ApplyGraphOps(agent, []GraphOp{{Kind: "node-metadata", NodeID: "review", Kind_: stringPtr("task")}})
	wantTypeCode(t, err, CodeNodeTypePeopleOnly)
	_, _, err = st.ApplyGraphOps(agent, []GraphOp{{Kind: "node-metadata", NodeID: "work", Kind_: stringPtr("gate")}})
	wantTypeCode(t, err, CodeNodeTypePeopleOnly)
	if _, _, err := st.ApplyGraphOps(agent, []GraphOp{{Kind: "node-metadata", NodeID: "work", Kind_: stringPtr("scene")}}); err != nil {
		t.Fatalf("task → scene by an agent was refused: %v", err)
	}
	if err := saveWith(t, st, identity.Local, func(g *engine.Graph) { g.NodeByID("review").Kind = "task" }); err != nil {
		t.Fatalf("a person was refused: %v", err)
	}
}

func TestGraphOpsNormalizeAndRefuseUnknownKinds(t *testing.T) {
	st := typedStore(t)
	g, _, err := st.ApplyGraphOps(identity.Local, []GraphOp{{Kind: "node-metadata", NodeID: "work", Kind_: stringPtr("Decision")}})
	if err != nil {
		t.Fatal(err)
	}
	if kind := g.NodeByID("work").Kind; kind != "choice" {
		t.Fatalf("kind = %q, want choice", kind)
	}
	_, _, err = st.ApplyGraphOps(identity.Local, []GraphOp{{Kind: "node-metadata", NodeID: "work", Kind_: stringPtr("custom-type-9")}})
	wantTypeCode(t, err, CodeUnknownNodeType)
}

func TestStatusWritesFollowTheNodeType(t *testing.T) {
	st := typedStore(t)
	agent := agentActor(identity.AgentWorker)
	_, err := st.ReportStatus(agent, "review", engine.StatusDone, "agent", "", "", "")
	wantTypeCode(t, err, CodeNodeTypePeopleOnly)
	if _, err := st.ReportStatus(agent, "review", engine.StatusInProgress, "agent", "", "", ""); err != nil {
		t.Fatalf("an agent moving a review to in_progress was refused: %v", err)
	}
	if _, err := st.ReportStatus(identity.Local, "review", engine.StatusDone, "me", "", "", ""); err != nil {
		t.Fatalf("a person finishing a review was refused: %v", err)
	}
	_, err = st.ReportStatus(identity.Local, "lore", engine.StatusDone, "me", "", "", "")
	wantTypeCode(t, err, CodeNodeTypeNoStatus)
	_, err = st.ReportStatus(identity.Local, "hint", engine.StatusDone, "me", "", "", "")
	wantTypeCode(t, err, CodeNodeTypeNoStatus)
	_, err = st.ReportStatus(agent, "open", engine.StatusSkipped, "agent", "", "", "")
	wantTypeCode(t, err, CodeNodeTypePeopleOnly)
	if _, err := st.SetStatus("lore", engine.StatusDone, "autopilot", ""); err == nil {
		t.Fatal("the actor-less writer set a status on a note")
	}
}

func TestAnAgentCannotClaimANonAgentType(t *testing.T) {
	st := typedStore(t)
	_, err := st.ClaimNode(agentActor(identity.AgentWorker), "pick", "agent-1", 0, "")
	wantTypeCode(t, err, CodeNodeTypeForbidsAgent)
	if _, err := st.ClaimNode(agentActor(identity.AgentWorker), "work", "agent-1", 0, ""); err != nil {
		t.Fatalf("claiming a task was refused: %v", err)
	}
}

func TestCreateNodeNormalizesTheKind(t *testing.T) {
	st := typedStore(t)
	id, err := st.CreateNode(identity.Local, &engine.Node{Title: "Choose", Kind: "decision"}, "")
	if err != nil {
		t.Fatal(err)
	}
	g, _, _ := st.LoadGraph()
	if kind := g.NodeByID(id).Kind; kind != "choice" {
		t.Fatalf("kind = %q, want choice", kind)
	}
	_, err = st.CreateNode(identity.Local, &engine.Node{Title: "Ghost", Kind: "custom-type-9"}, "")
	wantTypeCode(t, err, CodeUnknownNodeType)
	id, err = st.CreateNode(identity.Local, &engine.Node{Title: "Plain"}, "")
	if err != nil {
		t.Fatal(err)
	}
	g, _, _ = st.LoadGraph()
	if kind := g.NodeByID(id).Kind; kind != "task" {
		t.Fatalf("kind = %q, want task", kind)
	}
}

func TestTransferCarriesTheCustomTypesItUses(t *testing.T) {
	workspace := t.TempDir()
	source := transferTestProject(t, workspace, "Source")
	if err := saveWith(t, source, identity.Local, func(g *engine.Graph) {
		g.NodeTypes = []engine.NodeTypeDef{foreshadowType, {
			ID: "custom-type-2", Label: "Unused", AgentMode: engine.AgentNone,
			Completion: engine.CompletionAnyone, CountsInProgress: typedBool(true),
		}}
		g.Nodes = append(g.Nodes, &engine.Node{ID: "hint", Title: "Hint", Kind: "custom-type-1"})
	}); err != nil {
		t.Fatal(err)
	}
	clash := engine.NodeTypeDef{ID: "custom-type-1", Label: "Something else", AgentMode: engine.AgentDispatch,
		Completion: engine.CompletionAnyone, CountsInProgress: typedBool(true)}

	sameLabel := engine.NodeTypeDef{ID: "custom-type-7", Label: "伏筆", Shape: "diamond", AgentMode: engine.AgentNone,
		Completion: engine.CompletionNone, CountsInProgress: typedBool(false)}

	for _, c := range []struct {
		name     string
		existing []engine.NodeTypeDef
		// wantTypes is the target's list after the import; wantKind the
		// imported node's kind.
		wantTypes []engine.NodeTypeDef
		wantKind  string
		warns     bool
	}{
		{"fresh", nil, []engine.NodeTypeDef{foreshadowType}, "custom-type-1", false},
		// custom ids are numbered per project, so an id clash is a different
		// type: it is added under a fresh id rather than adopting the target's.
		{"id clash", []engine.NodeTypeDef{clash},
			[]engine.NodeTypeDef{clash, renamedAs(foreshadowType, engine.NodeTypeDef{ID: "custom-type-2", Label: "伏筆"})},
			"custom-type-2", false},
		{"same label", []engine.NodeTypeDef{sameLabel}, []engine.NodeTypeDef{sameLabel}, "custom-type-7", false},
		// An agent may not add types, so its copy falls back to Task.
		{"agent", nil, nil, "task", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			target := transferTestProject(t, workspace, "Target-"+c.name)
			if c.existing != nil {
				if err := saveWith(t, target, identity.Local, func(g *engine.Graph) { g.NodeTypes = c.existing }); err != nil {
					t.Fatal(err)
				}
			}
			payload, err := source.ExportNodes([]string{"hint"}, false)
			if err != nil {
				t.Fatal(err)
			}
			actor := identity.Local
			if c.name == "agent" {
				actor = agentActor(identity.AgentWorker)
			}
			result, err := target.ImportNodes(actor, payload)
			if err != nil {
				t.Fatal(err)
			}
			g, _, _ := target.LoadGraph()
			if !reflect.DeepEqual(g.NodeTypes, c.wantTypes) {
				t.Fatalf("target types = %+v, want %+v", g.NodeTypes, c.wantTypes)
			}
			if kind := g.NodeByID(result.IDs["hint"]).Kind; kind != c.wantKind {
				t.Fatalf("kind = %q, want %q", kind, c.wantKind)
			}
			if (len(result.Warnings) > 0) != c.warns {
				t.Fatalf("warnings = %v", result.Warnings)
			}
		})
	}
}
