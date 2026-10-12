package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChunksPreserveUnicodeAndOverlap(t *testing.T) {
	d := Document{ID: "doc", Content: strings.Repeat("文件🌍", 900)}
	chunks := Chunks(d)
	if len(chunks) != 3 {
		t.Fatalf("chunks: %d", len(chunks))
	}
	for i, c := range chunks {
		if c.Text != string([]rune(d.Content)[c.Start:c.End]) {
			t.Fatal("incorrect citation")
		}
		if i > 0 && chunks[i-1].End-c.Start != 200 {
			t.Fatal("missing overlap")
		}
	}
	if chunks[len(chunks)-1].End != len([]rune(d.Content)) {
		t.Fatal("lost final characters")
	}
	if len(Chunks(Document{Content: " \n\t"})) != 0 {
		t.Fatal("indexed whitespace")
	}
}

func TestSyncReusesEmbeddingsAndDeletesOnlyAfterSuccessfulWrites(t *testing.T) {
	ctx := context.Background()
	records := map[string]Hit{}
	embeds, fail := 0, false
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			IDs        []string    `json:"ids"`
			Documents  []string    `json:"documents"`
			Metadatas  []Metadata  `json:"metadatas"`
			Input      []string    `json:"input"`
			Embeddings [][]float64 `json:"embeddings"`
		}
		if r.Method == "POST" {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/get"):
			ids := []string{}
			for id := range records {
				ids = append(ids, id)
			}
			json.NewEncoder(w).Encode(map[string]any{"ids": ids})
		case strings.HasSuffix(r.URL.Path, "/upsert"):
			if len(body.Embeddings) != len(body.IDs) {
				t.Error("missing vectors")
			}
			for i, id := range body.IDs {
				records[id] = Hit{Text: body.Documents[i], Metadata: body.Metadatas[i]}
			}
			w.Write([]byte("{}"))
		case strings.HasSuffix(r.URL.Path, "/delete"):
			for _, id := range body.IDs {
				delete(records, id)
			}
			w.Write([]byte("{}"))
		default:
			json.NewEncoder(w).Encode(map[string]string{"id": "collection"})
		}
	}))
	defer service.Close()
	s, err := New(service.URL, "project", func(_ context.Context, input []string) ([][]float64, error) {
		embeds += len(input)
		if fail {
			return nil, fmt.Errorf("model unavailable")
		}
		return testVectors(input), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	docs := []Document{{ID: "a", Content: "initial document"}, {ID: "b", Content: "another document"}}
	if n, err := s.Sync(ctx, docs); err != nil || n != 2 {
		t.Fatalf("index: %d %v", n, err)
	}
	if _, err := s.Sync(ctx, docs); err != nil {
		t.Fatal(err)
	}
	if embeds != 2 || len(records) != 2 {
		t.Fatalf("reindexed unchanged documents: %d %d", embeds, len(records))
	}
	fail = true
	docs[0].Content = "replacement"
	if _, err := s.Sync(ctx, docs[:1]); err == nil {
		t.Fatal("embedding failure ignored")
	}
	if len(records) != 2 {
		t.Fatal("failed sync deleted old data")
	}
	fail = false
	if _, err := s.Sync(ctx, docs[:1]); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("obsolete chunks retained: %d", len(records))
	}
	for _, hit := range records {
		if hit.Text != "replacement" {
			t.Fatal("old text retained")
		}
	}
	if _, err := s.Sync(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatal("empty project retained data")
	}
}

func TestServiceValidationAndScope(t *testing.T) {
	for _, raw := range []string{"https://example.com", "file:///tmp/chroma", "http://localhost?key=secret", "http://user:secret@localhost"} {
		if _, err := New(raw, "scope", testEmbedding); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	a, _ := New("http://localhost:8000", "server\x00a", testEmbedding)
	b, _ := New("http://localhost:8000", "server\x00b", testEmbedding)
	c, _ := New("http://localhost:8000", "other-server\x00a", testEmbedding)
	if a.name == b.name || a.name == c.name {
		t.Fatal("scope/model collision")
	}
}

func TestMalformedQueryAndEmbeddingResponsesFail(t *testing.T) {
	for _, badEmbedding := range []bool{false, true} {
		t.Run(map[bool]string{false: "query", true: "embedding"}[badEmbedding], func(t *testing.T) {
			service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/query"):
					w.Write([]byte(`{"ids":[["a"]],"documents":[[]],"metadatas":[[]],"distances":[[]]}`))
				default:
					w.Write([]byte(`{"id":"collection"}`))
				}
			}))
			defer service.Close()
			s, _ := New(service.URL, "scope", func(_ context.Context, input []string) ([][]float64, error) {
				if badEmbedding {
					return nil, nil
				}
				return testVectors(input), nil
			})
			if _, err := s.Search(context.Background(), "question", 5); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}

func testVectors(input []string) [][]float64 {
	vectors := make([][]float64, len(input))
	for i := range vectors {
		vectors[i] = make([]float64, 384)
		vectors[i][0] = 1
	}
	return vectors
}
func testEmbedding(_ context.Context, input []string) ([][]float64, error) {
	return testVectors(input), nil
}
