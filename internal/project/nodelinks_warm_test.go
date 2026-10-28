package project

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nodevas/internal/realtime"
)

// linkProject writes a project of n nodes under parent/name, each document
// linking to the next node, and returns its ProjectInfo.
func linkProject(t *testing.T, parent, name string, n int) ProjectInfo {
	t.Helper()
	root := filepath.Join(parent, name)
	if err := os.MkdirAll(filepath.Join(root, "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	graph := "version: 1\nnodes:\n"
	for i := 1; i <= n; i++ {
		graph += fmt.Sprintf("  - id: n%d\n    title: Node %d\n", i, i)
	}
	writeGraph(t, root, graph)
	for i := 1; i <= n; i++ {
		writeDocument(t, root, fmt.Sprintf("n%d", i), fmt.Sprintf("see [[n%d]]", i%n+1))
	}
	return ProjectInfo{Name: name, Path: root}
}

// countLinkReads swaps the document reader for one that counts, and calls
// during(path) before each read, for the length of the test.
func countLinkReads(t *testing.T, during func(path string)) *atomic.Int64 {
	t.Helper()
	var reads atomic.Int64
	previous := readLinkDocument
	readLinkDocument = func(root, path string) ([]byte, error) {
		reads.Add(1)
		if during != nil {
			during(path)
		}
		return previous(root, path)
	}
	t.Cleanup(func() { readLinkDocument = previous })
	return &reads
}

// The first node drawer opened after a start scanned every document in the
// workspace, one after another: 3,300 documents took a second with a warm
// disk cache and ten with a cold one. The reads of one project overlap.
func TestScanNodeLinksReadsAProjectsDocumentsInParallel(t *testing.T) {
	info := linkProject(t, t.TempDir(), "P", 6)
	var (
		mu        sync.Mutex
		active    int
		maxActive int
	)
	overlapping := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return active > 1
	}
	countLinkReads(t, func(string) {
		mu.Lock()
		active++
		maxActive = max(maxActive, active)
		mu.Unlock()
		// Wait for a second read to be in flight at the same time, or give
		// up: a scan that reads one document at a time never has one.
		for deadline := time.Now().Add(150 * time.Millisecond); !overlapping() && time.Now().Before(deadline); {
			time.Sleep(2 * time.Millisecond)
		}
		mu.Lock()
		active--
		mu.Unlock()
	})

	if links := ScanNodeLinks([]ProjectInfo{info}); len(links) != 6 {
		t.Fatalf("scan found %d links, want 6", len(links))
	}
	if maxActive < 2 {
		t.Fatalf("documents were read one at a time (at most %d in flight)", maxActive)
	}
}

// Two drawers opened while a project's first scan runs must not each read
// the whole project: the second waits for the first scan's result.
func TestConcurrentScansOfAColdProjectReadItOnce(t *testing.T) {
	const documents = 5
	info := linkProject(t, t.TempDir(), "P", documents)
	entered := make(chan struct{})
	release := make(chan struct{})
	var first sync.Once
	reads := countLinkReads(t, func(string) {
		first.Do(func() {
			close(entered)
			<-release
		})
	})

	var wg sync.WaitGroup
	results := make([]int, 2)
	wg.Add(1)
	go func() {
		defer wg.Done()
		results[0] = len(ScanNodeLinks([]ProjectInfo{info}))
	}()
	<-entered
	wg.Add(1)
	go func() {
		defer wg.Done()
		results[1] = len(ScanNodeLinks([]ProjectInfo{info}))
	}()
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()

	if results[0] != documents || results[1] != documents {
		t.Fatalf("scans found %v links, want %d each", results, documents)
	}
	if got := reads.Load(); got != documents {
		t.Fatalf("two concurrent scans read %d documents, want %d (each once)", got, documents)
	}
}

// A hub warms the link cache when it starts, so the first node drawer
// anyone opens is answered from it instead of from a read of every document
// in the workspace. Close waits for the warm-up: nothing reads the
// workspace after it.
func TestWarmNodeLinksFillsTheCacheBeforeTheFirstDrawer(t *testing.T) {
	workspace := t.TempDir()
	first := linkProject(t, workspace, "first", 4)
	second := linkProject(t, workspace, "second", 3)
	pm, err := NewManagerAt(workspace, realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pm.WarmNodeLinks()
	pm.Close()

	reads := countLinkReads(t, nil)
	links := ScanNodeLinks([]ProjectInfo{first, second})
	if len(links) != 7 {
		t.Fatalf("scan found %d links, want 7", len(links))
	}
	if got := reads.Load(); got != 0 {
		t.Fatalf("first scan after the warm-up read %d documents, want 0", got)
	}
}
