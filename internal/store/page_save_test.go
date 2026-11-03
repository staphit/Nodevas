package store

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nodevas/internal/identity"
)

// A page save changes one file. It used to take the store-wide write lock for
// the whole of it (the read, the history snapshot, the fsync and the rename),
// so every reader and every other page's save queued behind each one. It now
// shares the store lock with readers and serialises only on its own page.

// Asserted structurally: a reader holds the store's read lock on this very
// goroutine for the whole save, so a save that still needed the write lock
// would never finish.
func TestAPageSaveDoesNotWaitForReaders(t *testing.T) {
	st := benchPagesStore(t, 2, 1)
	actor := identity.Actor{ID: "writer", Name: "Writer"}
	_, _, rev, err := st.LoadNodePage("n00000", "page-0001")
	if err != nil {
		t.Fatal(err)
	}

	st.mu.RLock()
	released := false
	release := func() {
		if !released {
			released = true
			st.mu.RUnlock()
		}
	}
	defer release()

	saved := make(chan error, 1)
	go func() {
		_, err := st.SaveNodePage(actor, "n00000", "page-0001", "# saved beside a reader\n", rev)
		saved <- err
	}()
	select {
	case err := <-saved:
		if err != nil {
			t.Fatalf("save: %v", err)
		}
	case <-time.After(5 * time.Second):
		release()
		<-saved
		t.Fatal("a page save waited for a reader to finish")
	}
}

// Writers racing on one page each append a line and retry on a conflict, the
// way an editor would; writers on other pages and readers run alongside. No
// line may go missing, and every page ends with exactly what was appended.
func TestConcurrentPageSavesLoseNothing(t *testing.T) {
	const nodes, perPage, rounds = 3, 4, 6
	st := benchPagesStore(t, nodes, 1)
	actor := identity.Actor{ID: "writer", Name: "Writer"}
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for r := range 2 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				if _, _, err := st.LoadGraph(); err != nil {
					t.Error(err)
					return
				}
				if _, err := st.ListNodePages(fmt.Sprintf("n%05d", (r+i)%nodes)); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	var writers sync.WaitGroup
	for n := range nodes {
		nodeID := fmt.Sprintf("n%05d", n)
		for w := range perPage {
			writers.Add(1)
			go func() {
				defer writers.Done()
				for r := range rounds {
					line := fmt.Sprintf("%s w%d r%d\n", nodeID, w, r)
					for {
						_, content, rev, err := st.LoadNodePage(nodeID, "page-0001")
						if err != nil {
							t.Error(err)
							return
						}
						_, err = st.SaveNodePage(actor, nodeID, "page-0001", content+line, rev)
						var conflict *ErrConflict
						if errors.As(err, &conflict) {
							continue
						}
						if err != nil {
							t.Error(err)
							return
						}
						break
					}
				}
			}()
		}
	}
	writers.Wait()
	close(stop)
	readers.Wait()

	for n := range nodes {
		nodeID := fmt.Sprintf("n%05d", n)
		_, content, _, err := st.LoadNodePage(nodeID, "page-0001")
		if err != nil {
			t.Fatal(err)
		}
		for w := range perPage {
			for r := range rounds {
				if line := fmt.Sprintf("%s w%d r%d\n", nodeID, w, r); !strings.Contains(content, line) {
					t.Errorf("%s lost %q", nodeID, line)
				}
			}
		}
		if got, want := strings.Count(content, nodeID+" w"), perPage*rounds; got != want {
			t.Errorf("%s holds %d lines, want %d", nodeID, got, want)
		}
	}
}

// BenchmarkPageSaveUnderLoad is page saves while other clients read the
// graph and page lists and move cards. ns/op is one save across all writers;
// p50-ms and p99-ms are a save's latency, which is what an auto-saving editor
// waits on.
func BenchmarkPageSaveUnderLoad(b *testing.B) {
	const nodes, readers, movers, pageWriters = 12, 8, 2, 8
	st := benchPagesStore(b, nodes, 20)
	actor := identity.Actor{ID: "bench", Name: "bench"}
	stop := make(chan struct{})
	var background sync.WaitGroup
	var moves atomic.Int64
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
	for m := range movers {
		background.Add(1)
		go func() {
			defer background.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				x, y := float64(i%400), float64(m)
				if _, _, err := st.ApplyGraphOps(actor, []GraphOp{{Kind: "move", NodeID: fmt.Sprintf("n%05d", i%nodes), X: &x, Y: &y}}); err != nil {
					b.Error(err)
					return
				}
				moves.Add(1)
			}
		}()
	}

	var next atomic.Int64
	total := int64(b.N)
	latencies := make([][]time.Duration, pageWriters)
	start := time.Now()
	movesAtStart := moves.Load()
	b.ResetTimer()
	var done sync.WaitGroup
	for w := range pageWriters {
		done.Add(1)
		go func() {
			defer done.Done()
			nodeID := fmt.Sprintf("n%05d", w%nodes)
			pageID := fmt.Sprintf("page-%04d", w/nodes+1)
			_, _, rev, err := st.LoadNodePage(nodeID, pageID)
			if err != nil {
				b.Error(err)
				return
			}
			for i := 0; next.Add(1) <= total; i++ {
				began := time.Now()
				rev, err = st.SaveNodePage(actor, nodeID, pageID, fmt.Sprintf("# save %d\n", i), rev)
				if err != nil {
					b.Error(err)
					return
				}
				latencies[w] = append(latencies[w], time.Since(began))
			}
		}()
	}
	done.Wait()
	b.StopTimer()
	elapsed := time.Since(start).Seconds()
	close(stop)
	background.Wait()

	var all []time.Duration
	for _, each := range latencies {
		all = append(all, each...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	if len(all) > 0 {
		at := func(q float64) float64 {
			return float64(all[min(len(all)-1, int(q*float64(len(all))))].Microseconds()) / 1000
		}
		b.ReportMetric(at(0.50), "p50-ms")
		b.ReportMetric(at(0.99), "p99-ms")
	}
	b.ReportMetric(float64(b.N)/elapsed, "saves/s")
	b.ReportMetric(float64(moves.Load()-movesAtStart)/elapsed, "moves/s")
}
