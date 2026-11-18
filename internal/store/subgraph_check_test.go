package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

// treeSnapshot is every file under the project, .vised included: a dry run
// leaves no trace anywhere, not even in the history or a sidecar.
func treeSnapshot(t *testing.T, st *Store) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.Walk(st.Root(), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(st.Root(), path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// The dry run refuses exactly what the commit refuses, with the same error,
// and changes nothing: no file, no history, no change event, same rev.
func TestCheckSubgraphRefusesWhatCreateRefuses(t *testing.T) {
	worker := agentActor(identity.AgentWorker)
	many := make([]SubgraphNode, MaxCopilotDraftNodes+1)
	for i := range many {
		many[i] = SubgraphNode{Key: fmt.Sprintf("k%d", i), Title: "T"}
	}
	cases := []struct {
		name  string
		actor identity.Actor
		spec  SubgraphSpec
	}{
		{"cycle", identity.Local, SubgraphSpec{
			Nodes: []SubgraphNode{{Key: "x", Title: "X", DependsOn: []string{"intro"}}},
			Edges: []SubgraphEdge{{From: "x", To: "intro"}},
		}},
		{"unknown dependsOn", identity.Local, SubgraphSpec{Nodes: []SubgraphNode{{Key: "a", Title: "A", DependsOn: []string{"nope"}}}}},
		{"unknown kind", identity.Local, SubgraphSpec{Nodes: []SubgraphNode{{Key: "a", Title: "A", Kind: "custom-type-9"}}}},
		{"bad priority", identity.Local, SubgraphSpec{Nodes: []SubgraphNode{{Key: "a", Title: "A", Priority: "asap"}}}},
		{"key is an existing id", identity.Local, SubgraphSpec{Nodes: []SubgraphNode{{Key: "intro", Title: "A"}}}},
		{"edge into a human-only node", worker, SubgraphSpec{
			Nodes: []SubgraphNode{{Key: "a", Title: "A"}},
			Edges: []SubgraphEdge{{From: "a", To: "finale"}},
		}},
		{"position of an unknown key", identity.Local, SubgraphSpec{
			Nodes:     []SubgraphNode{{Key: "a", Title: "A"}},
			Positions: map[string]SubgraphPosition{"b": {Col: 1, Row: 1}},
		}},
		{"position off the board", identity.Local, SubgraphSpec{
			Nodes:     []SubgraphNode{{Key: "a", Title: "A"}},
			Positions: map[string]SubgraphPosition{"a": {Col: MaxPositionCol + 1, Row: 0}},
		}},
		{"negative row", identity.Local, SubgraphSpec{
			Nodes:     []SubgraphNode{{Key: "a", Title: "A"}},
			Positions: map[string]SubgraphPosition{"a": {Col: 0, Row: -1}},
		}},
		{"bad via", identity.Local, SubgraphSpec{
			Nodes: []SubgraphNode{{Key: "a", Title: "A"}},
			Batch: &BatchSpec{Via: "copilot draft with spaces"},
		}},
		{"draft over its cap", identity.Local, SubgraphSpec{Nodes: many, Batch: &BatchSpec{Via: "copilot-draft:d1"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := subgraphStore(t)
			_, rev, _ := st.LoadGraph()
			before := treeSnapshot(t, st)
			var events atomic.Int32
			st.SetOnChange(func(string) { events.Add(1) })

			_, checkErr := st.CheckSubgraph(c.actor, c.spec)
			if checkErr == nil {
				t.Fatal("the dry run accepted what the commit must refuse")
			}
			if after := treeSnapshot(t, st); !reflect.DeepEqual(before, after) {
				t.Fatalf("the dry run wrote files:\nbefore %v\nafter %v", keys(before), keys(after))
			}
			if n := events.Load(); n != 0 {
				t.Fatalf("the dry run raised %d change events", n)
			}
			_, createErr := st.CreateSubgraph(c.actor, c.spec)
			if createErr == nil {
				t.Fatal("the commit accepted it")
			}
			if reflect.TypeOf(checkErr) != reflect.TypeOf(createErr) || checkErr.Error() != createErr.Error() {
				t.Fatalf("check said %T %v, create said %T %v", checkErr, checkErr, createErr, createErr)
			}
			if _, after, _ := st.LoadGraph(); after != rev {
				t.Fatal("rev changed")
			}
		})
	}
}

func TestCheckSubgraphWritesNothingOnSuccess(t *testing.T) {
	st := subgraphStore(t)
	_, rev, _ := st.LoadGraph()
	history, _ := st.ListHistory("graph.yaml")
	before := treeSnapshot(t, st)
	var events atomic.Int32
	st.SetOnChange(func(string) { events.Add(1) })
	spec := SubgraphSpec{
		Nodes: []SubgraphNode{
			{Key: "a", Title: "第二場", Kind: "scene", DependsOn: []string{"intro"}},
			{Key: "b", Title: "第三場", Kind: "scene", DependsOn: []string{"a"}},
		},
		Edges:     []SubgraphEdge{{From: "b", To: "finale"}},
		Positions: map[string]SubgraphPosition{"a": {Col: 2, Row: 3}},
		Batch:     &BatchSpec{Via: "copilot-draft:d1"},
	}
	result, err := st.CheckSubgraph(identity.Local, spec)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rev != rev || len(result.IDs) != 2 || result.Dependencies != 3 || result.BatchID != "" || result.Undoable {
		t.Fatalf("result = %+v", result)
	}
	if after := treeSnapshot(t, st); !reflect.DeepEqual(before, after) {
		t.Fatalf("the dry run wrote files:\nbefore %v\nafter %v", keys(before), keys(after))
	}
	if after, _ := st.ListHistory("graph.yaml"); len(after) != len(history) {
		t.Fatal("the dry run added a history version")
	}
	if n := events.Load(); n != 0 {
		t.Fatalf("the dry run raised %d change events", n)
	}
	if _, err := os.Stat(st.batchesPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the dry run recorded a batch: %v", err)
	}
	// The same spec then commits.
	if _, err := st.CreateSubgraph(identity.Local, spec); err != nil {
		t.Fatal(err)
	}
}

// The dry run takes the read lock: two of them run side by side, and neither
// waits for the other.
func TestCheckSubgraphRunsUnderTheReadLock(t *testing.T) {
	st := subgraphStore(t)
	st.mu.RLock()
	done := make(chan error, 1)
	go func() {
		_, err := st.CheckSubgraph(identity.Local, SubgraphSpec{Nodes: []SubgraphNode{{Key: "a", Title: "A"}}})
		done <- err
	}()
	select {
	case err := <-done:
		st.mu.RUnlock()
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		st.mu.RUnlock()
		t.Fatal("the dry run waited for a reader")
	}
}

func TestSubgraphPositionsAreApplied(t *testing.T) {
	st := subgraphStore(t)
	result, err := st.CreateSubgraph(identity.Local, SubgraphSpec{
		Nodes:     []SubgraphNode{{Key: "a", Title: "A"}, {Key: "b", Title: "B"}},
		Positions: map[string]SubgraphPosition{"a": {Col: 7, Row: 4}},
	})
	if err != nil {
		t.Fatal(err)
	}
	g, _, _ := st.LoadGraph()
	if g.UI == nil {
		t.Fatal("no ui")
	}
	if got := g.UI.Positions[result.IDs["a"]]; got != (engine.Position{X: 7, Y: 4}) {
		t.Fatalf("a at %+v", got)
	}
	if _, ok := g.UI.Positions[result.IDs["b"]]; ok {
		t.Fatal("b was given a position nobody asked for")
	}

	// Without positions nothing is written to the layout.
	plain := subgraphStore(t)
	if _, err := plain.CreateSubgraph(identity.Local, SubgraphSpec{Nodes: []SubgraphNode{{Key: "a", Title: "A"}}}); err != nil {
		t.Fatal(err)
	}
	g, _, _ = plain.LoadGraph()
	if g.UI != nil && len(g.UI.Positions) > 0 {
		t.Fatalf("positions written without being asked: %v", g.UI.Positions)
	}
}

func TestSubgraphPositionRefusalsNameTheKey(t *testing.T) {
	st := subgraphStore(t)
	for name, c := range map[string]struct {
		positions map[string]SubgraphPosition
		field     string
	}{
		"unknown key":  {map[string]SubgraphPosition{"zz": {Col: 1, Row: 1}}, "positions.zz"},
		"column 500":   {map[string]SubgraphPosition{"a": {Col: 500, Row: 1}}, "positions.a"},
		"row 201":      {map[string]SubgraphPosition{"a": {Col: 1, Row: 201}}, "positions.a"},
		"negative col": {map[string]SubgraphPosition{"a": {Col: -1, Row: 1}}, "positions.a"},
	} {
		_, err := st.CreateSubgraph(identity.Local, SubgraphSpec{Nodes: []SubgraphNode{{Key: "a", Title: "A"}}, Positions: c.positions})
		var invalid *ErrInvalidSubgraph
		if !errors.As(err, &invalid) || invalid.Field != c.field {
			t.Errorf("%s: err = %v, want field %s", name, err, c.field)
		}
	}
}

func TestACopilotDraftIsCappedAtFortyNodes(t *testing.T) {
	st := subgraphStore(t)
	nodes := func(n int) []SubgraphNode {
		out := make([]SubgraphNode, n)
		for i := range out {
			out[i] = SubgraphNode{Key: fmt.Sprintf("k%d", i), Title: "T"}
		}
		return out
	}
	_, err := st.CreateSubgraph(identity.Local, SubgraphSpec{Nodes: nodes(41), Batch: &BatchSpec{Via: "copilot-draft:d1"}})
	var tooLarge *ErrSubgraphTooLarge
	if !errors.As(err, &tooLarge) {
		t.Fatalf("41 draft nodes: %v", err)
	}
	if _, err := st.CreateSubgraph(identity.Local, SubgraphSpec{Nodes: nodes(40), Batch: &BatchSpec{Via: "copilot-draft:d1"}}); err != nil {
		t.Fatalf("40 draft nodes: %v", err)
	}
	// The general limit is unchanged for other callers.
	if _, err := st.CreateSubgraph(identity.Local, SubgraphSpec{Nodes: nodes(41)}); err != nil {
		t.Fatalf("41 plain nodes: %v", err)
	}
	if _, err := st.CreateSubgraph(identity.Local, SubgraphSpec{Nodes: nodes(41), Batch: &BatchSpec{Via: "import"}}); err != nil {
		t.Fatalf("41 nodes in another kind of batch: %v", err)
	}
}

func TestSubgraphBatchIsRecorded(t *testing.T) {
	st := subgraphStore(t)
	bea := identity.Actor{ID: "u-bea", Name: "bea", Role: identity.RoleMember}
	result, err := st.CreateSubgraph(bea, SubgraphSpec{
		Nodes: []SubgraphNode{
			{Key: "a", Title: "A", Body: "first", DependsOn: []string{"intro"}},
			{Key: "b", Title: "B", DependsOn: []string{"a"}},
		},
		Edges: []SubgraphEdge{{From: "b", To: "finale"}},
		Batch: &BatchSpec{Via: "copilot-draft:d1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Undoable || !batchIDPattern.MatchString(result.BatchID) {
		t.Fatalf("result = %+v", result)
	}
	record := loadBatchForTest(t, st, result.BatchID)
	a, b := result.IDs["a"], result.IDs["b"]
	if record.ID != result.BatchID || record.Actor.ID != "u-bea" || record.Via != "copilot-draft:d1" ||
		record.CreatedAt == "" || record.Project == "" || !reflect.DeepEqual(record.IDs, []string{a, b}) {
		t.Fatalf("record = %+v", record)
	}
	if len(record.Nodes) != 2 || record.Nodes[a].Fingerprint == "" || record.Nodes[a].Fingerprint == record.Nodes[b].Fingerprint {
		t.Fatalf("marks = %+v", record.Nodes)
	}
	// Only the link into a node that was already there is the batch's to
	// put back: the new nodes carry their own.
	if !reflect.DeepEqual(record.Links, []BatchLink{{From: b, To: "finale"}}) {
		t.Fatalf("links = %+v", record.Links)
	}

	// Without a batch, nothing is recorded.
	plain, err := st.CreateSubgraph(bea, SubgraphSpec{Nodes: []SubgraphNode{{Key: "c", Title: "C"}}})
	if err != nil || plain.BatchID != "" || plain.Undoable {
		t.Fatalf("plain = %+v %v", plain, err)
	}
}

// A batch the store cannot record still commits; it is just not undoable.
func TestABatchThatCannotBeRecordedStillCommits(t *testing.T) {
	st := subgraphStore(t)
	// A directory where the sidecar goes makes its write fail and nothing else.
	if err := os.MkdirAll(st.batchesPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := st.CreateSubgraph(identity.Local, SubgraphSpec{
		Nodes: []SubgraphNode{{Key: "a", Title: "A"}},
		Batch: &BatchSpec{Via: "copilot-draft:d1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Undoable || result.BatchID != "" {
		t.Fatalf("result = %+v", result)
	}
	g, _, _ := st.LoadGraph()
	if g.NodeByID(result.IDs["a"]) == nil {
		t.Fatal("the nodes were not committed")
	}
}

func TestACopilotDraftCommitIsNamedInTheHistory(t *testing.T) {
	st, root := newGitStore(t, time.Hour)
	result, err := st.CreateSubgraph(identity.Local, SubgraphSpec{
		Nodes: []SubgraphNode{{Key: "a", Title: "Draft node"}},
		Batch: &BatchSpec{Via: "copilot-draft:d42"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Undoable {
		t.Fatal("not undoable")
	}
	if err := st.FlushHistory(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, message := headMessage(t, root)
	if !strings.Contains(message, "Draft node: created from a Copilot draft (copilot-draft:d42)") {
		t.Fatalf("message = %q", message)
	}
}

func loadBatchForTest(t *testing.T, st *Store, id string) *batchRecord {
	t.Helper()
	file, err := st.loadBatchesLocked()
	if err != nil {
		t.Fatal(err)
	}
	record := file.Batches[id]
	if record == nil {
		t.Fatalf("batch %s not recorded", id)
	}
	return record
}
