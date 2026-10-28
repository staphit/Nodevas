package project

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/realtime"
)

var lookupActor = identity.Actor{ID: "writer", Name: "Writer"}

// StoreFor runs on every request that names a project. It used to stat the
// project directory and its graph.yaml each time, which costs milliseconds on
// Windows right after a write (the scanner holds the fresh file). It now
// remembers a project it found, until something says otherwise.

func lookupWorkspace(t *testing.T) (*ProjectManager, string) {
	t.Helper()
	pm, err := NewManagerAt(t.TempDir(), realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pm.Close)
	if err := pm.Create("alpha", "", ""); err != nil {
		t.Fatal(err)
	}
	dir, err := pm.Resolve("alpha")
	if err != nil {
		t.Fatal(err)
	}
	return pm, dir
}

func pinLookupTTL(t *testing.T, ttl time.Duration) {
	t.Helper()
	previous := projectLookupTTL
	projectLookupTTL = ttl
	t.Cleanup(func() { projectLookupTTL = previous })
}

// Asserted by taking graph.yaml away behind the manager's back: a lookup that
// still went to the disk would refuse the project; one answered from memory
// does not. Evicting the project's stores is the signal a rename or a delete
// sends, and the next lookup goes back to the disk.
func TestStoreForAnswersARepeatLookupFromMemory(t *testing.T) {
	pinLookupTTL(t, time.Hour)
	pm, dir := lookupWorkspace(t)
	first, err := pm.StoreFor("alpha")
	if err != nil {
		t.Fatal(err)
	}
	graph := filepath.Join(dir, "graph.yaml")
	if err := os.Rename(graph, graph+".away"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(graph+".away", graph) })

	again, err := pm.StoreFor("alpha")
	if err != nil {
		t.Fatalf("a repeat lookup went back to the disk: %v", err)
	}
	if again != first {
		t.Fatal("a repeat lookup handed out a different store")
	}

	pm.EvictStores(dir)
	if _, err := pm.StoreFor("alpha"); err == nil {
		t.Fatal("after the project's stores were evicted, a lookup still trusted memory")
	}
}

// The store's own graph writes leave graph.yaml in place, so they keep the
// lookup fresh instead of sending the next request to stat a file the
// scanner has just locked.
func TestAStoreGraphWriteKeepsTheLookupFresh(t *testing.T) {
	pinLookupTTL(t, 200*time.Millisecond)
	pm, dir := lookupWorkspace(t)
	st, err := pm.StoreFor("alpha")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	g, rev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	next := g.Clone()
	next.Nodes = append(next.Nodes, &engine.Node{ID: "fresh", Title: "Fresh"})
	if _, err := st.SaveGraph(lookupActor, next, rev); err != nil {
		t.Fatal(err)
	}
	graph := filepath.Join(dir, "graph.yaml")
	if err := os.Rename(graph, graph+".away"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(graph+".away", graph) })
	time.Sleep(100 * time.Millisecond)
	// 250ms after the first lookup, but only 100ms after the write.
	if _, err := pm.StoreFor("alpha"); err != nil {
		t.Fatalf("a graph write did not keep the lookup fresh: %v", err)
	}
}

// A project nobody watches can still lose its graph.yaml from outside; the
// remembered lookup lasts projectLookupTTL at most.
func TestALookupIsRecheckedAfterItsTTL(t *testing.T) {
	pinLookupTTL(t, 50*time.Millisecond)
	pm, dir := lookupWorkspace(t)
	if _, err := pm.StoreFor("alpha"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "graph.yaml")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	if _, err := pm.StoreFor("alpha"); err == nil {
		t.Fatal("a lookup outlived its TTL")
	}
}

// The watcher reports graph.yaml going away from a watched project at once.
func TestTheWatcherDropsALookupWhoseGraphWentAway(t *testing.T) {
	pinLookupTTL(t, time.Hour)
	pm, dir := lookupWorkspace(t)
	if err := pm.Activate("alpha", false, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := pm.StoreFor("alpha"); err != nil {
		t.Fatal(err)
	}
	// The watcher takes anything at a path the store wrote in the last two
	// seconds for the store's own echo (Store.WasSelfWrite), and Create just
	// wrote graph.yaml: only a removal after that is an outside one.
	time.Sleep(2100 * time.Millisecond)
	if err := os.Remove(filepath.Join(dir, "graph.yaml")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := pm.StoreFor("alpha"); err != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the watcher saw graph.yaml removed but the lookup was kept")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// BenchmarkStoreFor is the per-request project lookup, remembered and (with
// the cache off, as every request was before it) looked up on disk.
func BenchmarkStoreFor(b *testing.B) {
	for _, bench := range []struct {
		name string
		ttl  time.Duration
	}{{"disk", 0}, {"remembered", time.Hour}} {
		b.Run(bench.name, func(b *testing.B) {
			previous := projectLookupTTL
			projectLookupTTL = bench.ttl
			b.Cleanup(func() { projectLookupTTL = previous })
			pm, err := NewManagerAt(b.TempDir(), realtime.NewHub(), b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(pm.Close)
			if err := pm.Create("alpha", "", ""); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for b.Loop() {
				if _, err := pm.StoreFor("alpha"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
