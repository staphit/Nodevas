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
// in the workspace.
func TestWarmNodeLinksFillsTheCacheBeforeTheFirstDrawer(t *testing.T) {
	workspace := t.TempDir()
	first := linkProject(t, workspace, "first", 4)
	second := linkProject(t, workspace, "second", 3)
	pm, err := NewManagerAt(workspace, realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pm.Close)
	pm.WarmNodeLinks()
	pm.linksWarm.Wait()

	reads := countLinkReads(t, nil)
	links := ScanNodeLinks([]ProjectInfo{first, second})
	if len(links) != 7 {
		t.Fatalf("scan found %d links, want 7", len(links))
	}
	if got := reads.Load(); got != 0 {
		t.Fatalf("first scan after the warm-up read %d documents, want 0", got)
	}
}

// Close stops a warm-up still reading and waits for it, so nothing reads the
// workspace once Close returns.
func TestCloseStopsALinkWarmUp(t *testing.T) {
	workspace := t.TempDir()
	for i := range 3 {
		linkProject(t, workspace, fmt.Sprintf("p%d", i), 3)
	}
	pm, err := NewManagerAt(workspace, realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	var first sync.Once
	reads := countLinkReads(t, func(string) {
		first.Do(func() {
			close(entered)
			<-release
		})
	})
	pm.WarmNodeLinks()
	<-entered
	closed := make(chan struct{})
	go func() {
		pm.Close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close returned while the warm-up was still reading")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-closed
	after := reads.Load()
	if after > 3 {
		t.Fatalf("warm-up read %d documents after Close began, want only the project it was on (3)", after)
	}
	time.Sleep(50 * time.Millisecond)
	if reads.Load() != after {
		t.Fatal("warm-up kept reading after Close returned")
	}
}

// Saving one page of a 2,000-document project changed the project's stamp,
// and the next drawer read all 2,000 documents again (300 ms and more). Only
// the documents that changed are read; the rest keep their links.
func TestScanAfterOneEditReadsOnlyTheEditedDocument(t *testing.T) {
	info := linkProject(t, t.TempDir(), "P", 6)
	if links := ScanNodeLinks([]ProjectInfo{info}); len(links) != 6 {
		t.Fatalf("first scan found %d links, want 6", len(links))
	}
	writeDocument(t, info.Path, "n3", "see [[n1]] and [[Other/x9]]")

	reads := countLinkReads(t, nil)
	got := scannedLinks(t, info)
	want := []string{"n1->P/n2", "n2->P/n3", "n3->Other/x9", "n3->P/n1", "n4->P/n5", "n5->P/n6", "n6->P/n1"}
	if len(got) != len(want) {
		t.Fatalf("scan after the edit = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("scan after the edit = %v, want %v", got, want)
		}
	}
	if n := reads.Load(); n != 1 {
		t.Fatalf("scan after one document changed read %d documents, want 1", n)
	}
}

// A retitled node keeps its document, so its links are not read again, but
// they name it by its new title.
func TestScanAfterARetitleNamesTheNewTitle(t *testing.T) {
	info := linkProject(t, t.TempDir(), "P", 2)
	ScanNodeLinks([]ProjectInfo{info})
	writeGraph(t, info.Path, "version: 1\nnodes:\n  - id: n1\n    title: Renamed\n  - id: n2\n    title: Node 2\n")
	future := time.Now().Add(2 * time.Minute)
	if err := os.Chtimes(filepath.Join(info.Path, "graph.yaml"), future, future); err != nil {
		t.Fatal(err)
	}
	reads := countLinkReads(t, nil)
	for _, link := range ScanNodeLinks([]ProjectInfo{info}) {
		if link.FromNode == "n1" && link.FromTitle != "Renamed" {
			t.Fatalf("link from n1 has title %q, want Renamed", link.FromTitle)
		}
	}
	if n := reads.Load(); n != 0 {
		t.Fatalf("a retitle read %d documents, want 0", n)
	}
}
