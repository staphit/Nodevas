package store

import (
	"os"
	"strings"
	"testing"
	"time"
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
