package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

type syncFixture struct {
	mu                           sync.Mutex
	records                      map[string]Hit
	upserts, failUpsert, queries int
}

func (f *syncFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body struct {
		IDs       []string   `json:"ids"`
		Documents []string   `json:"documents"`
		Metadatas []Metadata `json:"metadatas"`
		Limit     int        `json:"limit"`
		Offset    int        `json:"offset"`
	}
	if r.Method == "POST" {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasSuffix(r.URL.Path, "/get"):
		ids := []string{}
		for id := range f.records {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		json.NewEncoder(w).Encode(map[string]any{"ids": ids[min(body.Offset, len(ids)):min(body.Offset+body.Limit, len(ids))]})
	case strings.HasSuffix(r.URL.Path, "/upsert"):
		f.upserts++
		if f.upserts == f.failUpsert {
			http.Error(w, "injected failure", 500)
			return
		}
		for i, id := range body.IDs {
			f.records[id] = Hit{Metadata: body.Metadatas[i], Text: body.Documents[i]}
		}
		w.Write([]byte("{}"))
	case strings.HasSuffix(r.URL.Path, "/delete"):
		for _, id := range body.IDs {
			delete(f.records, id)
		}
		w.Write([]byte("{}"))
	case strings.HasSuffix(r.URL.Path, "/query"):
		f.queries++
		json.NewEncoder(w).Encode(map[string]any{"ids": [][]string{{fmt.Sprint(f.queries)}}, "documents": [][]string{{fmt.Sprint(f.queries)}}, "metadatas": [][]Metadata{{{NodeID: "live"}}}, "distances": [][]float64{{0.1}}})
	default:
		w.Write([]byte(`{"id":"collection"}`))
	}
}

func TestIncrementalSyncBoundsBatchesAndRecoversFromPartialWrites(t *testing.T) {
	f := &syncFixture{records: map[string]Hit{}}
	httpServer := httptest.NewServer(http.HandlerFunc(f.serve))
	defer httpServer.Close()
	maxBatch, embeds, loads := 0, 0, 0
	s, err := New(httpServer.URL, "fixture", func(_ context.Context, input []string) ([][]float64, error) {
		maxBatch = max(maxBatch, len(input))
		embeds += len(input)
		return testVectors(input), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	docs := []Document{{ID: "large", Title: "Long document", Content: strings.Repeat("世🙂ab", 18000)}, {ID: "removed", Content: "keep until success"}}
	syncDocs := func() (int, error) {
		sources := []Source{}
		byID := map[string]Document{}
		for _, d := range docs {
			sources = append(sources, Source{ID: d.ID, Title: d.Title, Rev: Revision(d), Bytes: len(d.Content)})
			byID[d.ID] = d
		}
		return s.SyncSources(context.Background(), sources, func(_ context.Context, src Source) (Document, error) { loads++; return byID[src.ID], nil })
	}
	count, err := syncDocs()
	if err != nil {
		t.Fatal(err)
	}
	if count < 64 || maxBatch > 32 {
		t.Fatalf("chunks=%d maxBatch=%d", count, maxBatch)
	}
	initialLoads, initialEmbeds := loads, embeds
	if _, err := syncDocs(); err != nil {
		t.Fatal(err)
	}
	if loads != initialLoads || embeds != initialEmbeds {
		t.Fatal("unchanged index downloaded or embedded bodies")
	}
	// A collection modified by another process must not invalidate our manifest silently.
	f.mu.Lock()
	for id, h := range f.records {
		if h.NodeID == "large" {
			delete(f.records, id)
			break
		}
	}
	f.mu.Unlock()
	if _, err := syncDocs(); err != nil {
		t.Fatal(err)
	}
	if loads != initialLoads+1 || embeds != initialEmbeds+1 {
		t.Fatal("missing chunk was not selectively restored")
	}
	f.mu.Lock()
	original := map[string]bool{}
	for id := range f.records {
		original[id] = true
	}
	f.failUpsert = f.upserts + 2
	f.mu.Unlock()
	docs[0].Content = strings.Repeat("changed document. ", 6000)
	docs = docs[:1]
	if _, err := syncDocs(); err == nil {
		t.Fatal("late upsert failure ignored")
	}
	f.mu.Lock()
	for id := range original {
		if _, ok := f.records[id]; !ok {
			t.Error("failed sync deleted old chunks")
		}
	}
	f.failUpsert = 0
	f.mu.Unlock()
	count, err = syncDocs()
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.records) != count {
		t.Fatalf("obsolete chunks retained: %d != %d", len(f.records), count)
	}
	for _, hit := range f.records {
		if hit.NodeID != "large" || hit.Revision != Revision(docs[0]) {
			t.Fatal("obsolete content retained")
		}
	}
	t.Logf("largest embedding batch: %d; unchanged document downloads: 0", maxBatch)
}

func TestQueryCacheIsBoundedAndNeverCachesResults(t *testing.T) {
	f := &syncFixture{records: map[string]Hit{}}
	httpServer := httptest.NewServer(http.HandlerFunc(f.serve))
	defer httpServer.Close()
	embeds := 0
	s, _ := New(httpServer.URL, "fixture", func(_ context.Context, input []string) ([][]float64, error) { embeds++; return testVectors(input), nil })
	first, err := s.Search(context.Background(), "same question", 3)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Search(context.Background(), "same question", 3)
	if err != nil {
		t.Fatal(err)
	}
	if embeds != 1 || first[0].Text == second[0].Text {
		t.Fatal("query embedding not reused or stale results cached")
	}
	for i := 0; i < 65; i++ {
		if _, err := s.Search(context.Background(), fmt.Sprint(i), 3); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.queries) > 64 {
		t.Fatal("unbounded query cache")
	}
	before := embeds
	if _, err := s.Search(context.Background(), "same question", 3); err != nil {
		t.Fatal(err)
	}
	if embeds != before+1 {
		t.Fatal("old query never evicted")
	}
}
