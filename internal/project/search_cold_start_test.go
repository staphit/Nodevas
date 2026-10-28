package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/realtime"
	"nodevas/internal/searchindex"
	"nodevas/internal/store"
	"nodevas/internal/testpg"
)

// A hub opens the workspace (and so its last project) before the workspace
// database is up, so the search index the project's opening asked to warm had
// no database to build into. Plugging the database in must start that build:
// otherwise the first person to search pays for all of it.
func TestUsingTheDatabaseBuildsTheOpenProjectsIndex(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "main")
	if err := os.MkdirAll(filepath.Join(root, "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	graph := &engine.Graph{Version: 1, Nodes: []*engine.Node{
		{ID: "alpha", Title: "Alpha"}, {ID: "beta", Title: "Beta"}, {ID: "gamma", Title: "Gamma"},
	}}
	data, err := engine.MarshalGraph(graph)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	pm, err := NewManagerAt(workspace, realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pm.Close)
	database := testpg.New(t)
	pm.UseDatabase(database)

	deadline := time.Now().Add(10 * time.Second)
	for {
		var rows int
		if err := database.Search().QueryRow(`SELECT count(*) FROM search_documents`).Scan(&rows); err == nil && rows == 3 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no search was made, and the open project's index was never built (%d rows)", rows)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// coldSearchBudget is how long the first search of a project with no index
// yet may take. Building a large project's index takes seconds; the search
// must not wait for it.
const coldSearchBudget = 200 * time.Millisecond

// The first search of a large project whose index is still being built
// answers from the titles in the cached graph at once, instead of waiting
// seconds for the build; once the build is in, the bodies are searchable.
func TestFirstSearchBeforeTheIndexIsBuiltAnswersFromTitles(t *testing.T) {
	pm, info, _ := searchLoadProject(t, 3000)

	began := time.Now()
	results, err := pm.SearchProjectNodes(info, "n1234")
	took := time.Since(began)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if took > coldSearchBudget {
		t.Fatalf("first search took %v, waiting for the index build (budget %v)", took, coldSearchBudget)
	}
	if len(results) != 1 || results[0].NodeID != "n1234" {
		t.Fatalf("first search = %+v, want the node titled n1234", results)
	}

	began = time.Now()
	ranked, err := pm.RankProjectNodes(info, "n2345", 10)
	took = time.Since(began)
	if err != nil {
		t.Fatalf("ranked search: %v", err)
	}
	if took > coldSearchBudget {
		t.Fatalf("first ranked search took %v (budget %v)", took, coldSearchBudget)
	}
	if len(ranked) == 0 || ranked[0].NodeID != "n2345" {
		t.Fatalf("first ranked search = %+v, want the node titled n2345 first", ranked)
	}

	// The build goes on in the background and is what later searches read:
	// text only in the bodies is found.
	deadline := time.Now().Add(60 * time.Second)
	for {
		results, err := pm.SearchProjectNodes(info, "buried needle")
		if err != nil {
			t.Fatal(err)
		}
		if len(results) == 60 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("body search = %d results after the build, want 60", len(results))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A project of a few nodes can hold a thousand pages. The first search before
// its index is built answers from the graph just the same, instead of reading
// every page of every node, which takes seconds and competes with the build.
func TestFirstSearchOfAFewNodesWithManyPagesAnswersFromTitles(t *testing.T) {
	pm, info, _ := searchLoadProject(t, 4)
	page := []byte(strings.Repeat("page prose that only the index should read.\n", 40))
	for n := range 4 {
		dir := store.NodePagesPath(info.Path, fmt.Sprintf("n%04d", n))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for p := range 500 {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("page-%04d.md", p)), page, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	coldIndexFor(t, pm)

	began := time.Now()
	results, err := pm.SearchProjectNodes(info, "n0002")
	took := time.Since(began)
	if err != nil {
		t.Fatal(err)
	}
	if took > coldSearchBudget {
		t.Fatalf("first search took %v reading pages (budget %v)", took, coldSearchBudget)
	}
	if len(results) != 1 || results[0].NodeID != "n0002" {
		t.Fatalf("first search = %+v, want the node titled n0002", results)
	}
}

// coldIndexFor gives the project a fresh index manager over a new database,
// so its first search finds no index, as after a hub start.
func coldIndexFor(t *testing.T, pm *ProjectManager) {
	t.Helper()
	pm.mu.Lock()
	index := &searchIndex{manager: searchindex.NewManagerWithDB(testpg.New(t))}
	pm.searchIndex = index
	pm.searchLive.Store(index)
	pm.mu.Unlock()
	t.Cleanup(func() { _ = index.manager.Close() })
}
