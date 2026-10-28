package store

import (
	"os"
	"strings"
	"testing"
	"time"

	"nodevas/internal/identity"
)

// fastestListing is the quickest of a few ListNodePages calls: the floor of
// what one listing costs, with scheduler noise taken out.
func fastestListing(t *testing.T, st *Store, nodeID string) time.Duration {
	t.Helper()
	best := time.Duration(1<<63 - 1)
	for range 7 {
		start := time.Now()
		if _, err := st.ListNodePages(nodeID); err != nil {
			t.Fatal(err)
		}
		best = min(best, time.Since(start))
	}
	return best
}

func TestListNodePagesCostDoesNotGrowWithPageCount(t *testing.T) {
	few := benchPagesStore(t, 2, 5)
	many := benchPagesStore(t, 2, 250)
	small, large := fastestListing(t, few, "n00001"), fastestListing(t, many, "n00001")
	// The pages endpoint is read on every node the board opens. Checking
	// each listed file one path at a time made it ~25x dearer at 250 pages
	// than at 5; one directory read keeps it near flat.
	if large > 6*small {
		t.Fatalf("listing 250 pages took %v, 5 pages %v: %.1fx, want under 6x", large, small, float64(large)/float64(small))
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
