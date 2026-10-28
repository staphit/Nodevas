package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nodevas/internal/identity"
)

// benchPagesStore is benchGraphStore with a document and pages pages for
// every node, written straight to disk: the shape the load generator leaves
// behind (a dozen nodes, each with a long tail of subpages).
func benchPagesStore(tb testing.TB, nodes, pages int) *Store {
	tb.Helper()
	st := benchGraphStore(tb, nodes)
	for i := range nodes {
		id := fmt.Sprintf("n%05d", i)
		if err := os.WriteFile(filepath.Join(st.Root(), "nodes", id+".md"), []byte("---\nid: "+id+"\n---\n\nbody\n"), 0o644); err != nil {
			tb.Fatal(err)
		}
		writeBenchPages(tb, st.Root(), id, pages)
	}
	InvalidateNodeTree(st.Root())
	return st
}

func writeBenchPages(tb testing.TB, root, id string, pages int) {
	tb.Helper()
	dir := filepath.Join(root, "nodes", id+".pages")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		tb.Fatal(err)
	}
	manifest := nodePagesManifest{}
	for p := 1; p <= pages; p++ {
		pageID := fmt.Sprintf("page-%04d", p)
		manifest.Pages = append(manifest.Pages, NodePageInfo{ID: pageID, Title: "Page " + pageID, Format: PageFormatMarkdown})
		if err := os.WriteFile(filepath.Join(dir, pageID+".md"), []byte("# "+pageID+"\n"), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pages.json"), data, 0o644); err != nil {
		tb.Fatal(err)
	}
}

// BenchmarkListNodePages is the pages endpoint's store call against a node
// with a growing page count.
func BenchmarkListNodePages(b *testing.B) {
	for _, pages := range []int{10, 100, 250} {
		b.Run(fmt.Sprintf("pages=%d", pages), func(b *testing.B) {
			st := benchPagesStore(b, 4, pages)
			b.ResetTimer()
			for range b.N {
				if _, err := st.ListNodePages("n00001"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkMoveUnderReadLoad is the mixed load that queued writes: board
// moves while other clients read the graph and page lists and save pages.
// ns/op is one move; moves/s and writes/s (moves plus page saves) are the
// throughput the store sustains under that read load.
func BenchmarkMoveUnderReadLoad(b *testing.B) {
	for _, pages := range []int{0, 150} {
		b.Run(fmt.Sprintf("pages=%d", pages), func(b *testing.B) {
			const nodes, readers, pageWriters, movers = 12, 8, 4, 4
			st := benchPagesStore(b, nodes, pages)
			actor := identity.Actor{ID: "bench", Name: "bench"}
			if pages == 0 {
				for i := range nodes {
					writeBenchPages(b, st.Root(), fmt.Sprintf("n%05d", i), 1)
				}
			}
			stop := make(chan struct{})
			var background sync.WaitGroup
			var pageSaves atomic.Int64
			for r := range readers {
				background.Add(1)
				go func() {
					defer background.Done()
					for i := 0; ; i++ {
						select {
						case <-stop:
							return
						default:
						}
						if _, _, err := st.LoadGraph(); err != nil {
							b.Error(err)
							return
						}
						if _, err := st.ListNodePages(fmt.Sprintf("n%05d", (r+i)%nodes)); err != nil {
							b.Error(err)
							return
						}
					}
				}()
			}
			for w := range pageWriters {
				background.Add(1)
				go func() {
					defer background.Done()
					nodeID := fmt.Sprintf("n%05d", w)
					_, _, rev, err := st.LoadNodePage(nodeID, "page-0001")
					if err != nil {
						b.Error(err)
						return
					}
					for i := 0; ; i++ {
						select {
						case <-stop:
							return
						default:
						}
						rev, err = st.SaveNodePage(actor, nodeID, "page-0001", fmt.Sprintf("# save %d\n", i), rev)
						if err != nil {
							b.Error(err)
							return
						}
						pageSaves.Add(1)
					}
				}()
			}
			var next atomic.Int64
			total := int64(b.N)
			start := time.Now()
			savesAtStart := pageSaves.Load()
			b.ResetTimer()
			var moversDone sync.WaitGroup
			for m := range movers {
				moversDone.Add(1)
				go func() {
					defer moversDone.Done()
					for {
						i := next.Add(1)
						if i > total {
							return
						}
						x, y := float64(i%400), float64(m)
						if _, _, err := st.ApplyGraphOps(actor, []GraphOp{{Kind: "move", NodeID: fmt.Sprintf("n%05d", int(i)%nodes), X: &x, Y: &y}}); err != nil {
							b.Error(err)
							return
						}
					}
				}()
			}
			moversDone.Wait()
			b.StopTimer()
			elapsed := time.Since(start).Seconds()
			saves := pageSaves.Load() - savesAtStart
			close(stop)
			background.Wait()
			b.ReportMetric(float64(b.N)/elapsed, "moves/s")
			b.ReportMetric(float64(int64(b.N)+saves)/elapsed, "writes/s")
		})
	}
}
