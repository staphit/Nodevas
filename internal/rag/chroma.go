// Package rag indexes document chunks in a local ChromaDB v2 server.
package rag

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const collectionsPath = "/api/v2/tenants/default_tenant/databases/default_database/collections"

type Store struct {
	chroma, name string
	embedding    EmbeddingFunc
	http         *http.Client
	synced       map[string]documentIndex
	queries      map[string][]float64
	queryOrder   []string
}

type Document struct{ ID, Title, Content string }

type Metadata struct {
	NodeID   string `json:"node_id"`
	Revision string `json:"revision"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
}

type Hit struct {
	Metadata
	Text     string  `json:"text"`
	Distance float64 `json:"distance"`
}

// New keeps Chroma local, matching the MCP transport's trust boundary.
// Scope must uniquely identify the Nodevas server and its pinned project.
func New(chroma, scope string, embedding EmbeddingFunc) (*Store, error) {
	u, err := url.Parse(chroma)
	if err != nil || u == nil {
		return nil, fmt.Errorf("invalid Chroma URL")
	}
	ip := net.ParseIP(u.Hostname())
	if (u.Scheme != "http" && u.Scheme != "https") ||
		(!strings.EqualFold(u.Hostname(), "localhost") && (ip == nil || !ip.IsLoopback())) ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("Chroma requires a local HTTP(S) URL without credentials, query or fragment")
	}
	if embedding == nil || scope == "" {
		return nil, fmt.Errorf("embedding function and project scope are required")
	}
	return &Store{
		chroma: strings.TrimRight(chroma, "/"), embedding: embedding,
		name: fmt.Sprintf("nodevas-%x", sha256.Sum256([]byte("chunks-v2-minilm-window-mean\x00"+scope))),
		http: &http.Client{Timeout: 2 * time.Minute, Transport: &http.Transport{Proxy: nil},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

func Revision(d Document) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(d.Title+"\x00"+d.Content)))
}

// Chunks uses character offsets so citations work for Unicode documents too.
func Chunks(d Document) []Hit {
	var chunks []Hit
	_ = eachChunk(d, func(hit Hit) error { chunks = append(chunks, hit); return nil })
	return chunks
}

func eachChunk(d Document, visit func(Hit) error) error {
	text := []rune(d.Content)
	revision := Revision(d)
	for start := 0; start < len(text); {
		end := min(start+1200, len(text))
		if strings.TrimSpace(string(text[start:end])) != "" {
			if err := visit(Hit{Metadata: Metadata{NodeID: d.ID, Revision: revision, Start: start, End: end}, Text: string(text[start:end])}); err != nil {
				return err
			}
		}
		if end == len(text) {
			break
		}
		start = end - 200
	}
	return nil
}

func (s *Store) request(ctx context.Context, method, target string, body, out any) error {
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, payload)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("RAG service unavailable: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		// Service responses may echo document content; do not include them in errors.
		return fmt.Errorf("RAG %s %s: HTTP %d (check service availability and embedding model)", method, req.URL.Path, res.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(out); err != nil {
		return fmt.Errorf("invalid RAG service response: %w", err)
	}
	return nil
}

func (s *Store) collection(ctx context.Context, create bool) (string, error) {
	var result struct {
		ID string `json:"id"`
	}
	method, target := "GET", s.chroma+collectionsPath+"/"+s.name
	var body any
	if create {
		method, target = "POST", s.chroma+collectionsPath
		body = map[string]any{"name": s.name, "get_or_create": true,
			"configuration": map[string]any{"hnsw": map[string]any{"space": "cosine"}}}
	}
	if err := s.request(ctx, method, target, body, &result); err != nil {
		return "", err
	}
	if result.ID == "" {
		return "", fmt.Errorf("Chroma returned no collection ID")
	}
	return s.chroma + collectionsPath + "/" + url.PathEscape(result.ID), nil
}

func (s *Store) embed(ctx context.Context, input []string) ([][]float64, error) {
	vectors, err := s.embedding(ctx, input)
	if err != nil {
		return nil, err
	}
	if len(vectors) != len(input) {
		return nil, fmt.Errorf("MiniLM returned an unexpected embedding count")
	}
	for _, vector := range vectors {
		if len(vector) != 384 {
			return nil, fmt.Errorf("MiniLM returned invalid embedding dimensions")
		}
		for _, v := range vector {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, fmt.Errorf("MiniLM returned nonfinite embeddings")
			}
		}
	}
	return vectors, nil
}

func (s *Store) Search(ctx context.Context, query string, limit int) ([]Hit, error) {
	query = strings.TrimSpace(query)
	if strings.TrimSpace(query) == "" || len([]rune(query)) > 1200 {
		return nil, fmt.Errorf("query must contain 1 to 1200 characters")
	}
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("search limit must be between 1 and 100")
	}
	base, err := s.collection(ctx, false)
	if err != nil {
		return nil, fmt.Errorf("index_documents must succeed before searching: %w", err)
	}
	vector, err := s.queryVector(ctx, query)
	if err != nil {
		return nil, err
	}
	var result struct {
		IDs       [][]string   `json:"ids"`
		Documents [][]string   `json:"documents"`
		Metadatas [][]Metadata `json:"metadatas"`
		Distances [][]float64  `json:"distances"`
	}
	if err := s.request(ctx, "POST", base+"/query", map[string]any{"query_embeddings": [][]float64{vector}, "n_results": limit, "include": []string{"documents", "metadatas", "distances"}}, &result); err != nil {
		return nil, err
	}
	if len(result.IDs) != 1 || len(result.Documents) != 1 || len(result.Metadatas) != 1 || len(result.Distances) != 1 {
		return nil, fmt.Errorf("invalid Chroma query response")
	}
	n := len(result.IDs[0])
	if n > limit || len(result.Documents[0]) != n || len(result.Metadatas[0]) != n || len(result.Distances[0]) != n {
		return nil, fmt.Errorf("invalid Chroma result lengths")
	}
	hits := make([]Hit, 0, n)
	for i := 0; i < n; i++ {
		hits = append(hits, Hit{Metadata: result.Metadatas[0][i], Text: result.Documents[0][i], Distance: result.Distances[0][i]})
	}
	return hits, nil
}

// Cache embeddings only; search results always come from the current collection.
func (s *Store) queryVector(ctx context.Context, query string) ([]float64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if vector, ok := s.queries[query]; ok {
		return vector, nil
	}
	vectors, err := s.embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if s.queries == nil {
		s.queries = map[string][]float64{}
	}
	if len(s.queryOrder) == 64 {
		delete(s.queries, s.queryOrder[0])
		s.queryOrder = s.queryOrder[1:]
	}
	s.queryOrder = append(s.queryOrder, query)
	s.queries[query] = vectors[0]
	return vectors[0], nil
}
