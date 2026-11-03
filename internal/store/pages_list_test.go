package store

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nodevas/internal/identity"
)

// listingFileOps is how many file-system operations on its project (opens of
// the project root, one per contained path operation) one checked
// ListNodePages of nodeID makes: not the remembered verdict, a full check.
//
// Counted, not timed: a listing of a few pages takes less than one tick of
// the Windows clock, so a timed one read 0s against 0.5ms and failed as
// "+Infx" with nothing wrong.
func listingFileOps(t *testing.T, st *Store, nodeID string) int64 {
	t.Helper()
	st.InvalidatePageLists()
	var ops atomic.Int64
	root := filepath.Clean(st.Root())
	count := func(opened string) {
		if filepath.Clean(opened) == root {
			ops.Add(1)
		}
	}
	rootOpened.Store(&count)
	defer rootOpened.Store(nil)
	if _, err := st.ListNodePages(nodeID); err != nil {
		t.Fatal(err)
	}
	return ops.Load()
}

func TestListNodePagesCostDoesNotGrowWithPageCount(t *testing.T) {
	few := benchPagesStore(t, 2, 5)
	many := benchPagesStore(t, 2, 250)
	small, large := listingFileOps(t, few, "n00001"), listingFileOps(t, many, "n00001")
	// The pages endpoint is read on every node the board opens. Checking
	// each listed file one path at a time made it ~25x dearer at 250 pages
	// than at 5; one directory read keeps it flat.
	if small == 0 {
		t.Fatal("a listing made no file-system operation that was counted")
	}
	if large != small {
		t.Fatalf("listing 250 pages made %d file-system operations, 5 pages %d: want the same", large, small)
	}
}

func TestListNodePagesStillRefusesBrokenAndAllowsMissingPages(t *testing.T) {
	st := benchPagesStore(t, 2, 3)
	if err := os.Remove(st.NodePagePath("n00001", "page-0002", PageFormatMarkdown)); err != nil {
		t.Fatal(err)
	}
	pages, err := st.ListNodePages("n00001")
	if err != nil {
		t.Fatalf("a page whose file is missing is listed, not an error: %v", err)
	}
	if len(pages) != 3 {
		t.Fatalf("listed %d pages, want 3", len(pages))
	}
	breakPage(t, st, "n00001", "page-0003")
	st.InvalidatePageLists() // what the file watcher does on that change
	if _, err := st.ListNodePages("n00001"); err == nil || !strings.Contains(err.Error(), "page-0003") {
		t.Fatalf("ListNodePages with a page that is a directory = %v, want an error naming it", err)
	}
	// The other pages of an unbroken node are unaffected.
	if _, err := st.ListNodePages("n00000"); err != nil {
		t.Fatal(err)
	}
}

func TestPageListingIsServedFromTheLastCheckUntilTheManifestChanges(t *testing.T) {
	st := benchPagesStore(t, 2, 3)
	actor := identity.Actor{ID: "writer", Name: "Writer"}
	if _, err := st.ListNodePages("n00001"); err != nil {
		t.Fatal(err)
	}
	// Listing a directory of a few hundred pages costs milliseconds on
	// Windows, and the board asks for every node it opens: a repeat
	// listing of the same manifest reuses the last check of its files.
	// What the store itself writes there is always a regular file, so only
	// a change from outside can make that check stale, and the file watcher
	// reports those (realtime).
	breakPage(t, st, "n00001", "page-0003")
	pages, err := st.ListNodePages("n00001")
	if err != nil {
		t.Fatalf("a repeat listing re-read the pages directory: %v", err)
	}
	if len(pages) != 3 {
		t.Fatalf("cached listing has %d pages, want 3", len(pages))
	}
	// A new manifest is a new listing, checked from scratch.
	if _, _, _, err := st.CreateNodePage(actor, "n00001", "Fourth", PageFormatMarkdown); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ListNodePages("n00001"); err == nil || !strings.Contains(err.Error(), "page-0003") {
		t.Fatalf("listing after the manifest changed = %v, want the broken page reported", err)
	}
}

func TestPageListingRechecksAfterAnOutsideChangeOrTheRecheckInterval(t *testing.T) {
	st := benchPagesStore(t, 2, 3)
	if _, err := st.ListNodePages("n00001"); err != nil {
		t.Fatal(err)
	}
	breakPage(t, st, "n00001", "page-0003")
	st.InvalidatePageLists()
	if _, err := st.ListNodePages("n00001"); err == nil {
		t.Fatal("a listing after InvalidatePageLists missed the broken page")
	}

	// Repaired, rechecked and remembered again...
	path := st.NodePagePath("n00001", "page-0003", PageFormatMarkdown)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# fixed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st.InvalidatePageLists()
	if _, err := st.ListNodePages("n00001"); err != nil {
		t.Fatal(err)
	}
	// ...a verdict nobody invalidated still expires.
	breakPage(t, st, "n00001", "page-0003")
	defer func(previous time.Duration) { pageListRecheck = previous }(pageListRecheck)
	pageListRecheck = 0
	if _, err := st.ListNodePages("n00001"); err == nil {
		t.Fatal("a listing past the recheck interval reused the old verdict")
	}
}

func TestPageListingFollowsAMovedNode(t *testing.T) {
	_, st := folderProject(t)
	actor := identity.Actor{ID: "writer", Name: "Writer"}
	if _, _, _, err := st.CreateNodePage(actor, "alpha", "Page", PageFormatMarkdown); err != nil {
		t.Fatal(err)
	}
	before, err := st.ListNodePages("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateNodeFolder("drafts"); err != nil {
		t.Fatal(err)
	}
	if err := st.MoveNodesToFolder([]string{"alpha"}, "drafts"); err != nil {
		t.Fatal(err)
	}
	after, err := st.ListNodePages("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("after the move %d pages, want %d", len(after), len(before))
	}
	if _, _, _, err := st.LoadNodePage("alpha", after[0].ID); err != nil {
		t.Fatalf("the listed page does not open after the move: %v", err)
	}
}
