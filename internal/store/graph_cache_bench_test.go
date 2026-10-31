package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"nodevas/internal/engine"
)

// benchGraphStore writes an n-node chain with positions and tags, roughly the
// shape of a real board, and opens a store over it.
func benchGraphStore(tb testing.TB, n int) *Store {
	tb.Helper()
	root := tb.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nodes"), 0o755); err != nil {
		tb.Fatal(err)
	}
	g := &engine.Graph{Version: 1, UI: &engine.UIState{Positions: map[string]engine.Position{}}}
	for i := range n {
		id := fmt.Sprintf("n%05d", i)
		g.Nodes = append(g.Nodes, &engine.Node{ID: id, Title: "Node " + id, Priority: "medium", Tags: []string{"bench", "t" + fmt.Sprint(i%7)}})
		g.UI.Positions[id] = engine.Position{X: float64(i * 10), Y: float64(i % 40 * 30)}
		if i > 0 {
			g.Edges = append(g.Edges, &engine.Edge{From: fmt.Sprintf("n%05d", i-1), To: id})
		}
	}
	data, err := engine.MarshalGraph(g)
	if err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), data, 0o644); err != nil {
		tb.Fatal(err)
	}
	return NewStore(root)
}

// BenchmarkLoadGraphConcurrent measures LoadGraph throughput with c readers on
// one store: c=1 is the per-call cost, c=8 shows whether readers serialize.
func BenchmarkLoadGraphConcurrent(b *testing.B) {
	for _, n := range []int{1000, 2000, 5000} {
		for _, c := range []int{1, 8} {
			b.Run(fmt.Sprintf("nodes=%d/c=%d", n, c), func(b *testing.B) {
				s := benchGraphStore(b, n)
				if _, _, err := s.LoadGraph(); err != nil {
					b.Fatal(err)
				}
				var next atomic.Int64
				total := int64(b.N)
				b.ResetTimer()
				var wg sync.WaitGroup
				for range c {
					wg.Add(1)
					go func() {
						defer wg.Done()
						for next.Add(1) <= total {
							if _, _, err := s.LoadGraph(); err != nil {
								b.Error(err)
								return
							}
						}
					}()
				}
				wg.Wait()
			})
		}
	}
}
