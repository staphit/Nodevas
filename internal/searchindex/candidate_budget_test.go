package searchindex

import (
	"context"
	"fmt"
	"math/rand/v2"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nodevas/internal/testpg"
)

// plantOversizeDocs writes count rows of about a megabyte of random Chinese
// each, marked oversize as the indexer marks a document whose tokens did not
// fit, every one ending in "zq marker". It writes the rows directly: indexing
// that much text takes a second a document. It returns the view a search runs
// on, and keeps none of the text.
func plantOversizeDocs(t *testing.T, count int) *indexView {
	t.Helper()
	ctx := context.Background()
	store := newDocStore(testpg.New(t))
	random := rand.New(rand.NewPCG(3, 4))
	order := make([]string, 0, count)
	for i := range count {
		var body strings.Builder
		for body.Len() < docTextLimit-4096 {
			body.WriteRune(rune(0x4E00 + random.IntN(0x9FA5-0x4E00)))
		}
		body.WriteString(" zq marker")
		id := fmt.Sprintf("node-%03d", i)
		order = append(order, id)
		row := putRow{nodeID: id, path: "nodes/" + id + ".md", stamp: "1", title: id,
			meta: "title " + id, body: body.String(), oversize: true,
			metaTokens: []string{}, bodyTokens: []string{}}
		if err := store.writeRows(ctx, "p", []putRow{row}); err != nil {
			t.Fatalf("plant %s: %v", id, err)
		}
	}
	return &indexView{project: "p", store: store, order: order}
}

// peakHeapGrowth runs fn and reports how far the live heap rose above where
// it started while fn ran.
func peakHeapGrowth(fn func()) uint64 {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	base := stats.HeapAlloc
	var peak atomic.Uint64
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var sample runtime.MemStats
		for {
			runtime.ReadMemStats(&sample)
			if sample.HeapAlloc > base && sample.HeapAlloc-base > peak.Load() {
				peak.Store(sample.HeapAlloc - base)
			}
			select {
			case <-done:
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	fn()
	close(done)
	wg.Wait()
	return peak.Load()
}

// Every oversize document is a candidate of every plain search, and a member
// can write as many megabyte documents as they like. A search used to load
// all of their text (up to 2,000 of them) before verifying any, once per
// project per request: hundreds of megabytes for a query that matches
// nothing, a few gigabytes at the cap.
func TestSearchMemoryIsBoundedWithManyOversizeDocuments(t *testing.T) {
	view := plantOversizeDocs(t, 72)
	ctx := context.Background()

	t.Run("no match holds no text", func(t *testing.T) {
		var matches []Match
		var err error
		growth := peakHeapGrowth(func() { matches, err = view.query(ctx, "xy") })
		if err != nil || len(matches) != 0 {
			t.Fatalf("query = %d matches, %v", len(matches), err)
		}
		if growth > 32<<20 {
			t.Fatalf("a search matching nothing raised the heap by %d MiB", growth>>20)
		}
	})

	t.Run("matches stop at the byte budget", func(t *testing.T) {
		matches, err := view.query(ctx, "zq marker")
		if err != nil {
			t.Fatal(err)
		}
		total := 0
		for _, match := range matches {
			total += len(match.Text)
		}
		if len(matches) == 0 || total > maxCandidateBytes {
			t.Fatalf("%d matches holding %d MiB, want some and at most %d MiB",
				len(matches), total>>20, maxCandidateBytes>>20)
		}
	})
}
