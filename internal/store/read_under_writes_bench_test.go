package store

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nodevas/internal/identity"
)

// BenchmarkReadsUnderMixedWrites is the hub's mixed load at store level: the
// reads a board makes (graph, a node, its page list) while auto-saving
// editors save pages and other people drag cards. ns/op is one read across
// all readers; read-p50-ms and read-p99-ms are what a reader waits, the
// number a write path that holds the store lock too long gives away.
func BenchmarkReadsUnderMixedWrites(b *testing.B) {
	const nodes, readers, pageWriters, movers = 200, 32, 8, 4
	st := benchPagesStore(b, nodes, 4)
	actor := identity.Actor{ID: "bench", Name: "bench"}
	stop := make(chan struct{})
	var background sync.WaitGroup
	var saves, moves atomic.Int64
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
				saves.Add(1)
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
				if _, _, err := st.ApplyGraphOps(actor, []GraphOp{{Kind: "move", NodeID: fmt.Sprintf("n%05d", (i*7+m)%nodes), X: &x, Y: &y}}); err != nil {
					b.Error(err)
					return
				}
				moves.Add(1)
			}
		}()
	}
	time.Sleep(200 * time.Millisecond) // writers up to speed

	var next atomic.Int64
	total := int64(b.N)
	latencies := make([][]time.Duration, readers)
	start := time.Now()
	savesAtStart, movesAtStart := saves.Load(), moves.Load()
	b.ResetTimer()
	var done sync.WaitGroup
	for r := range readers {
		done.Add(1)
		go func() {
			defer done.Done()
			for i := 0; next.Add(1) <= total; i++ {
				nodeID := fmt.Sprintf("n%05d", (r*31+i)%nodes)
				began := time.Now()
				var err error
				switch i % 3 {
				case 0:
					_, _, err = st.LoadGraph()
				case 1:
					_, _, err = st.LoadNodeContent(nodeID)
				default:
					_, err = st.ListNodePages(nodeID)
				}
				if err != nil {
					b.Error(err)
					return
				}
				latencies[r] = append(latencies[r], time.Since(began))
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
		b.ReportMetric(at(0.50), "read-p50-ms")
		b.ReportMetric(at(0.99), "read-p99-ms")
	}
	b.ReportMetric(float64(saves.Load()-savesAtStart)/elapsed, "saves/s")
	b.ReportMetric(float64(moves.Load()-movesAtStart)/elapsed, "moves/s")
}
