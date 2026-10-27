package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

// refStore has two notes, a scene anyone may write and a human-only scene.
func refStore(t *testing.T) *Store {
	t.Helper()
	st := NewStore(t.TempDir())
	g := &engine.Graph{Version: 1, Nodes: []*engine.Node{
		{ID: "note-01", Title: "角色卡：林曉", Kind: "note"},
		{ID: "note-02", Title: "Setting", Kind: "note"},
		{ID: "s-01", Title: "第一場", Kind: "scene"},
		{ID: "s-locked", Title: "Locked", Kind: "scene", WriteAccess: engine.WriteAccessHumanOnly},
	}}
	if _, err := st.SaveGraph(identity.Local, g, ""); err != nil {
		t.Fatal(err)
	}
	return st
}

func refsOf(t *testing.T, st *Store, id string) []string {
	t.Helper()
	g, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	return g.NodeByID(id).Refs
}

func addRef(st *Store, actor identity.Actor, node, ref string) error {
	_, _, err := st.ApplyGraphOps(actor, []GraphOp{{Kind: "add-ref", NodeID: node, Ref: ref}})
	return err
}

func TestReferenceOps(t *testing.T) {
	st := refStore(t)
	for i := 0; i < 2; i++ { // idempotent
		if err := addRef(st, identity.Local, "s-01", "note-01"); err != nil {
			t.Fatal(err)
		}
	}
	if got := refsOf(t, st, "s-01"); !reflect.DeepEqual(got, []string{"note-01"}) {
		t.Fatalf("refs = %v", got)
	}
	data, _ := os.ReadFile(st.NodePath("s-01"))
	if !strings.Contains(string(data), "refs:") {
		t.Fatalf("frontmatter lacks refs:\n%s", data)
	}
	if err := addRef(st, identity.Local, "s-01", "s-01"); err == nil || !strings.Contains(err.Error(), "itself") {
		t.Fatalf("self reference: %v", err)
	}
	if err := addRef(st, identity.Local, "s-01", "nope"); err == nil || !strings.Contains(err.Error(), "reference target") {
		t.Fatalf("missing target: %v", err)
	}
	var denied *ErrNodeWriteDenied
	if err := addRef(st, agentActor(identity.AgentWorker), "s-locked", "note-01"); !errors.As(err, &denied) {
		t.Fatalf("human-only node: %v", err)
	}
	// The target's access does not matter: the reference is the scene's.
	if err := addRef(st, agentActor(identity.AgentWorker), "s-01", "s-locked"); err != nil {
		t.Fatalf("referencing a human-only node: %v", err)
	}
	for i := 0; i < 2; i++ { // removing an absent reference is a no-op
		if _, _, err := st.ApplyGraphOps(identity.Local, []GraphOp{{Kind: "remove-ref", NodeID: "s-01", Ref: "note-01"}}); err != nil {
			t.Fatal(err)
		}
	}
	if got := refsOf(t, st, "s-01"); !reflect.DeepEqual(got, []string{"s-locked"}) {
		t.Fatalf("refs after remove = %v", got)
	}
	if _, _, err := st.ApplyGraphOps(agentActor(identity.AgentWorker), []GraphOp{{Kind: "remove-ref", NodeID: "s-locked", Ref: "x"}}); !errors.As(err, &denied) {
		t.Fatalf("remove on a human-only node: %v", err)
	}
}

func TestReferencesAreCapped(t *testing.T) {
	st := refStore(t)
	g, rev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= maxNodeRefs; i++ {
		g.Nodes = append(g.Nodes, &engine.Node{ID: fmt.Sprintf("n-%02d", i), Kind: "note"})
	}
	if _, err := st.SaveGraph(identity.Local, g, rev); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxNodeRefs; i++ {
		if err := addRef(st, identity.Local, "s-01", fmt.Sprintf("n-%02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	err = addRef(st, identity.Local, "s-01", fmt.Sprintf("n-%02d", maxNodeRefs))
	if err == nil || !strings.Contains(err.Error(), "too many references (maximum 32)") {
		t.Fatalf("33rd reference: %v", err)
	}
}

func TestDeletingANoteDropsTheReferencesToIt(t *testing.T) {
	st := refStore(t)
	if _, _, err := st.ApplyGraphOps(identity.Local, []GraphOp{
		{Kind: "add-ref", NodeID: "s-01", Ref: "note-01"}, {Kind: "add-ref", NodeID: "s-01", Ref: "note-02"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DeleteNodes(identity.Local, []string{"note-01"}); err != nil {
		t.Fatal(err)
	}
	if got := refsOf(t, st, "s-01"); !reflect.DeepEqual(got, []string{"note-02"}) {
		t.Fatalf("refs = %v", got)
	}
}

func TestDuplicateCopiesReferences(t *testing.T) {
	st := refStore(t)
	if err := addRef(st, identity.Local, "s-01", "note-01"); err != nil {
		t.Fatal(err)
	}
	id, err := st.DuplicateNode(identity.Local, "s-01")
	if err != nil {
		t.Fatal(err)
	}
	if got := refsOf(t, st, id); !reflect.DeepEqual(got, []string{"note-01"}) {
		t.Fatalf("duplicate refs = %v", got)
	}
}

func TestImportKeepsOnlyReferencesThatTravel(t *testing.T) {
	source := refStore(t)
	if _, _, err := source.ApplyGraphOps(identity.Local, []GraphOp{
		{Kind: "add-ref", NodeID: "s-01", Ref: "note-01"}, {Kind: "add-ref", NodeID: "s-01", Ref: "note-02"},
	}); err != nil {
		t.Fatal(err)
	}
	target := NewStore(t.TempDir())
	if _, err := target.SaveGraph(identity.Local, &engine.Graph{Version: 1, Nodes: []*engine.Node{
		{ID: "note-02", Title: "an unrelated node with the same id"},
	}}, ""); err != nil {
		t.Fatal(err)
	}
	payload, err := source.ExportNodes([]string{"s-01", "note-01"}, false)
	if err != nil {
		t.Fatal(err)
	}
	result, err := target.ImportNodes(identity.Local, payload)
	if err != nil {
		t.Fatal(err)
	}
	if got := refsOf(t, target, result.IDs["s-01"]); !reflect.DeepEqual(got, []string{result.IDs["note-01"]}) {
		t.Fatalf("imported refs = %v (ids %v)", got, result.IDs)
	}
}

func TestADanglingReferenceStillLoads(t *testing.T) {
	root := t.TempDir()
	write := func(refs string) {
		if err := os.WriteFile(filepath.Join(root, "graph.yaml"), []byte(
			"version: 1\nnodes:\n  - id: a\n    refs: "+refs+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("[gone]")
	g, _, err := NewStore(root).LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g.NodeByID("a").Refs, []string{"gone"}) {
		t.Fatalf("refs = %v", g.NodeByID("a").Refs)
	}
	for _, bad := range []string{"[a]", "[x, x]", `["bad id"]`} {
		write(bad)
		if _, _, err := NewStore(root).LoadGraph(); err == nil {
			t.Fatalf("refs %s loaded", bad)
		}
	}
}

func TestReferenceSummaries(t *testing.T) {
	st := NewStore(t.TempDir())
	g := &engine.Graph{Version: 1, Nodes: []*engine.Node{{ID: "scene", Kind: "scene", Refs: []string{"missing"}}}}
	for i := 0; i < 9; i++ {
		id := fmt.Sprintf("note-%d", i)
		g.Nodes = append(g.Nodes, &engine.Node{ID: id, Title: "Note " + id, Kind: "note"})
		g.Nodes[0].Refs = append(g.Nodes[0].Refs, id)
	}
	write := func(id, content string) {
		if err := os.MkdirAll(filepath.Dir(st.NodePath(id)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(st.NodePath(id), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("note-0", "---\nid: note-0\nsummary: 林曉怕水，左手有疤。\n---\n\nlong body that is not used\n")
	write("note-1", "---\nid: note-1\n---\n\n"+strings.Repeat("字", 500))
	write("note-2", "---\nbroken: [\n---\nraw   text\n")

	refs, truncated, err := st.ReferenceSummaries(g, "scene")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != MaxReferenceNotes || !truncated {
		t.Fatalf("got %d refs, truncated %v; want %d, true", len(refs), truncated, MaxReferenceNotes)
	}
	if refs[0].ID != "note-0" || refs[0].Summary != "林曉怕水，左手有疤。" || refs[0].TypeLabel != "Note" {
		t.Fatalf("frontmatter summary not used (or the missing target not skipped): %+v", refs[0])
	}
	if n := len([]rune(refs[1].Summary)); n != MaxReferenceSummaryRunes || !refs[1].Truncated ||
		!strings.HasSuffix(refs[1].Summary, "…") {
		t.Fatalf("body summary: %d runes, truncated %v", n, refs[1].Truncated)
	}
	if !strings.Contains(refs[2].Summary, "raw text") {
		t.Fatalf("unparseable note: %q", refs[2].Summary)
	}
	if refs[3].Summary != "" || refs[3].Truncated {
		t.Fatalf("a note without a document: %+v", refs[3])
	}

	g.NodeByID("scene").Refs = g.NodeByID("scene").Refs[:3]
	refs, truncated, err = st.ReferenceSummaries(g, "scene")
	if err != nil || len(refs) != 2 || truncated {
		t.Fatalf("under the caps: %d refs, truncated %v, %v", len(refs), truncated, err)
	}
}

// Eight 64 KiB titles must not crowd a run prompt: titles are cut too.
func TestReferenceTitlesAreCut(t *testing.T) {
	st := NewStore(t.TempDir())
	g := &engine.Graph{Version: 1, Nodes: []*engine.Node{{ID: "scene", Kind: "scene"}}}
	for i := 0; i < MaxReferenceNotes; i++ {
		id := fmt.Sprintf("note-%d", i)
		g.Nodes = append(g.Nodes, &engine.Node{ID: id, Title: strings.Repeat("名", 30_000), Kind: "note"})
		g.Nodes[0].Refs = append(g.Nodes[0].Refs, id)
	}
	refs, _, err := st.ReferenceSummaries(g, "scene")
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range refs {
		if n := len([]rune(ref.Title)); n > maxReferenceTitleRunes {
			t.Fatalf("title is %d runes", n)
		}
	}
}
