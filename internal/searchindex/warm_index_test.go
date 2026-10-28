package searchindex

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// pinRevalidate sets how long a query trusts the index for the length of one
// test, so a test can rule the walk-on-query net in or out.
func pinRevalidate(t *testing.T, after time.Duration) {
	t.Helper()
	previous := revalidateAfter
	revalidateAfter = after
	t.Cleanup(func() { revalidateAfter = previous })
}

// eventually polls search until want is the whole result or the deadline
// passes, and returns how long it took.
func eventually(t *testing.T, m *Manager, root, query string, want []string, within time.Duration) time.Duration {
	t.Helper()
	began := time.Now()
	for {
		got := ids(search(t, m, root, query))
		if len(got) == len(want) && (len(want) == 0 || got[0] == want[0]) {
			return time.Since(began)
		}
		if time.Since(began) > within {
			t.Fatalf("query %q = %v after %v, want %v", query, got, within, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A query answers from the last committed index while a reindex of the same
// project is running. Asserted structurally: the project's build lock is held
// on this goroutine for the whole search, so a query that waited for a
// reindex would never return.
func TestASearchAnswersWhileTheProjectReindexes(t *testing.T) {
	root := writeProject(t, fixtureNode{id: "alpha", title: "Alpha", body: "warm needle"})
	manager := newManager(t)
	if got := ids(search(t, manager, root, "needle")); len(got) != 1 {
		t.Fatalf("first search = %v", got)
	}

	held := manager.entryFor(root)
	held.mu.Lock()
	released := false
	release := func() {
		if !released {
			released = true
			held.mu.Unlock()
		}
	}
	defer release()

	answered := make(chan []Match, 1)
	go func() {
		matches, _ := manager.Search(root, "needle")
		answered <- matches
	}()
	select {
	case matches := <-answered:
		if got := ids(matches); len(got) != 1 || got[0] != "alpha" {
			t.Fatalf("search during a reindex = %v", got)
		}
	case <-time.After(5 * time.Second):
		release()
		<-answered
		t.Fatal("a search waited for the project's reindex to finish")
	}
}

// A change the index is told about (a server write, a watcher event) is
// searchable within a second without any query having to walk the project:
// the walk-on-query net is pinned out of reach here.
func TestANotifiedChangeIsSearchableWithinASecond(t *testing.T) {
	pinRevalidate(t, time.Hour)
	root := writeProject(t, fixtureNode{id: "alpha", title: "Alpha", body: "before body"})
	manager := newManager(t)
	if got := ids(search(t, manager, root, "before")); len(got) != 1 {
		t.Fatalf("initial results = %v", got)
	}
	if err := os.WriteFile(filepath.Join(root, "nodes", "alpha.md"), []byte("after body"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager.Notify(root, filepath.Join(root, "nodes", "alpha.md"))
	took := eventually(t, manager, root, "after", []string{"alpha"}, time.Second)
	eventually(t, manager, root, "before", nil, time.Second)
	t.Logf("searchable after %v", took)

	// graph.yaml: a node added to the graph is found the same way.
	writeGraph(t, root, fixtureNode{id: "alpha", title: "Alpha"}, fixtureNode{id: "beta", title: "Beta"})
	if err := os.WriteFile(filepath.Join(root, "nodes", "beta.md"), []byte("second lantern"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager.Notify(root, filepath.Join(root, "nodes", "beta.md"))
	manager.Notify(root, filepath.Join(root, "graph.yaml"))
	eventually(t, manager, root, "lantern", []string{"beta"}, time.Second)
}

// Opening a project starts its index in the background, so the first person
// to search does not pay for the build.
func TestWarmBuildsTheIndexBeforeTheFirstSearch(t *testing.T) {
	root := writeProject(t,
		fixtureNode{id: "alpha", title: "Alpha", body: "warm body"},
		fixtureNode{id: "beta", title: "Beta", body: "warm body"},
	)
	manager := newManager(t)
	manager.Warm(root)
	deadline := time.Now().Add(5 * time.Second)
	for count(t, manager.database, `SELECT count(*) FROM search_documents`) != 2 {
		if time.Now().After(deadline) {
			t.Fatal("the index was not built in the background after Warm")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// And the built index is the one the first search reads: it is ready.
	entry := manager.entryFor(root)
	if entry.view.Load() == nil {
		deadline = time.Now().Add(5 * time.Second)
		for entry.view.Load() == nil && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if entry.view.Load() == nil {
		t.Fatal("the warmed index was never published to queries")
	}
}

// An edit nobody announced is still found within a bounded delay: the query
// that notices the index is old asks for a walk in the background, and a
// later query sees its result.
func TestAnUnannouncedEditIsFoundWithinASecond(t *testing.T) {
	pinRevalidate(t, 50*time.Millisecond)
	root := writeProject(t, fixtureNode{id: "alpha", title: "Alpha", body: "before body"})
	manager := newManager(t)
	search(t, manager, root, "before")
	if err := os.WriteFile(filepath.Join(root, "nodes", "alpha.md"), []byte("after body"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	eventually(t, manager, root, "after", []string{"alpha"}, time.Second)
}
