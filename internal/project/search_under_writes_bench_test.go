package project

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/realtime"
	"nodevas/internal/store"
	"nodevas/internal/testpg"
)

// searchLoadProject is a workspace with one opened project of nodes nodes,
// each with a few kilobytes of body, searched through the database index the
// way serve wires it.
func searchLoadProject(tb testing.TB, nodes int) (*ProjectManager, ProjectInfo, *store.Store) {
	tb.Helper()
	workspace := tb.TempDir()
	root := filepath.Join(workspace, "main")
	if err := os.MkdirAll(filepath.Join(root, "nodes"), 0o755); err != nil {
		tb.Fatal(err)
	}
	graph := &engine.Graph{Version: 1}
	body := strings.Repeat("这是一段中文说明文字，用来填充节点内容。 filler prose line.\n", 60)
	for i := range nodes {
		id := fmt.Sprintf("n%04d", i)
		graph.Nodes = append(graph.Nodes, &engine.Node{ID: id, Title: "Node " + id})
		text := body
		if i%50 == 7 {
			text += "\nburied needle here\n"
		}
		if err := os.WriteFile(filepath.Join(root, "nodes", id+".md"), []byte(text), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	data, err := engine.MarshalGraph(graph)
	if err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), data, 0o644); err != nil {
		tb.Fatal(err)
	}
	pm, err := NewManagerAt(workspace, realtime.NewHub(), tb.TempDir())
	if err != nil {
		tb.Fatal(err)
	}
	pm.UseDatabase(testpg.New(tb))
	if err := pm.Activate("main", false, ""); err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(pm.Close)
	return pm, ProjectInfo{Name: "main", Path: root}, pm.Store()
}

// BenchmarkSearchUnderWrites is search while the project is being edited:
// writers save node bodies and move cards (graph.yaml) as fast as the store
// lets them, and searchers query the index. ns/op is one search; p50-ms and
// p99-ms are search latency, which is what a person typing into Ctrl+K feels.
func BenchmarkSearchUnderWrites(b *testing.B) {
	const nodes, bodyWriters, movers, searchers = 400, 3, 1, 4
	pm, info, st := searchLoadProject(b, nodes)
	actor := identity.Actor{ID: "bench", Name: "bench"}
	// The cold build is not what is measured here.
	if _, err := pm.SearchProjectNodes(info, "needle"); err != nil {
		b.Fatal(err)
	}

	stop := make(chan struct{})
	var background sync.WaitGroup
	var writes atomic.Int64
	for w := range bodyWriters {
		background.Add(1)
		go func() {
			defer background.Done()
			revs := map[string]string{}
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				id := fmt.Sprintf("n%04d", (w*97+i*bodyWriters+w)%nodes)
				rev, ok := revs[id]
				if !ok {
					_, current, err := st.LoadNodeContent(id)
					if err != nil {
						b.Error(err)
						return
					}
					rev = current
				}
				_, next, err := st.SaveNodeContent(actor, id, fmt.Sprintf("edited %d by %d\n\nwritten body %d\n", i, w, i), rev)
				if err != nil {
					b.Error(err)
					return
				}
				revs[id] = next
				writes.Add(1)
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
				if _, _, err := st.ApplyGraphOps(actor, []store.GraphOp{{Kind: "move", NodeID: fmt.Sprintf("n%04d", i%nodes), X: &x, Y: &y}}); err != nil {
					b.Error(err)
					return
				}
				writes.Add(1)
			}
		}()
	}

	var next atomic.Int64
	total := int64(b.N)
	latencies := make([][]time.Duration, searchers)
	start := time.Now()
	writesAtStart := writes.Load()
	b.ResetTimer()
	var done sync.WaitGroup
	for s := range searchers {
		done.Add(1)
		go func() {
			defer done.Done()
			for {
				if next.Add(1) > total {
					return
				}
				began := time.Now()
				if _, err := pm.SearchProjectNodes(info, "needle"); err != nil {
					b.Error(err)
					return
				}
				latencies[s] = append(latencies[s], time.Since(began))
				// A person types; they do not hammer.
				time.Sleep(20 * time.Millisecond)
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
		b.ReportMetric(float64(all[len(all)-1].Microseconds())/1000, "max-ms")
	}
	b.ReportMetric(float64(writes.Load()-writesAtStart)/elapsed, "writes/s")
}
