package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/rag"
)

type ragRequestCounter struct {
	base  http.RoundTripper
	mu    sync.Mutex
	paths map[string]int
}

func (c *ragRequestCounter) RoundTrip(r *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.paths[r.URL.Path]++
	c.mu.Unlock()
	base := c.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(r)
}
func (c *ragRequestCounter) reset() { c.mu.Lock(); c.paths = map[string]int{}; c.mu.Unlock() }
func (c *ragRequestCounter) count(path string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.paths[path]
}

func TestRAGUsesCompactReadsAndBoundedValidatedExcerpts(t *testing.T) {
	endpoint, pm := liveServer(t)
	g, rev, err := pm.Store().LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"more1", "more2", "more3"} {
		g.Nodes = append(g.Nodes, &engine.Node{ID: id, Title: "Reference " + id})
	}
	if _, err := pm.Store().SaveGraph(identity.Local, g, rev); err != nil {
		t.Fatal(err)
	}
	byNode := map[string][]rag.Hit{}
	for _, id := range []string{"design", "more1", "more2", "more3"} {
		_, rev, err := pm.Store().LoadNodeContent(id)
		if err != nil {
			t.Fatal(err)
		}
		body, _, err := pm.Store().SaveNodeContent(identity.Local, id, strings.Repeat("Reference 文🙂. ", 300), rev)
		if err != nil {
			t.Fatal(err)
		}
		byNode[id] = rag.Chunks(rag.Document{ID: id, Title: g.NodeByID(id).Title, Content: body})
	}
	client, err := NewClient(ClientOptions{Server: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	projects, err := client.Projects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	client.project = projects[0].Name
	counter := &ragRequestCounter{base: client.http.Transport, paths: map[string]int{}}
	client.http.Transport = counter
	var mu sync.Mutex
	ids := map[string]bool{}
	ordered := []rag.Hit{{Metadata: rag.Metadata{NodeID: "deleted-node", Revision: "old", Start: 0, End: 1}, Text: "x"}, byNode["design"][0], byNode["design"][1], byNode["more1"][0], byNode["more2"][0], byNode["more3"][0]}
	var queryLimits []int
	churn := false
	chroma := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var input struct {
			IDs   []string `json:"ids"`
			Limit int      `json:"n_results"`
		}
		if r.Method == "POST" {
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/get"):
			all := []string{}
			for id := range ids {
				all = append(all, id)
			}
			json.NewEncoder(w).Encode(map[string]any{"ids": all})
		case strings.HasSuffix(r.URL.Path, "/upsert"):
			for _, id := range input.IDs {
				ids[id] = true
			}
			io.WriteString(w, "{}")
		case strings.HasSuffix(r.URL.Path, "/delete"):
			for _, id := range input.IDs {
				delete(ids, id)
			}
			io.WriteString(w, "{}")
		case strings.HasSuffix(r.URL.Path, "/query"):
			queryLimits = append(queryLimits, input.Limit)
			hits := ordered[:min(len(ordered), input.Limit)]
			if churn {
				hits = make([]rag.Hit, input.Limit)
				for i := range hits {
					hits[i] = rag.Hit{Metadata: rag.Metadata{NodeID: fmt.Sprintf("stale-%d-%d", len(queryLimits), i), Start: 0, End: 1}, Text: "x"}
				}
			}
			texts, meta, distances, resultIDs := []string{}, []rag.Metadata{}, []float64{}, []string{}
			for _, hit := range hits {
				texts = append(texts, hit.Text)
				meta = append(meta, hit.Metadata)
				distances = append(distances, 0.1)
				resultIDs = append(resultIDs, hit.NodeID)
			}
			json.NewEncoder(w).Encode(map[string]any{"ids": [][]string{resultIDs}, "documents": [][]string{texts}, "metadatas": [][]rag.Metadata{meta}, "distances": [][]float64{distances}})
		default:
			io.WriteString(w, `{"id":"collection"}`)
		}
	}))
	defer chroma.Close()
	index, err := rag.New(chroma.URL, client.project, func(_ context.Context, input []string) ([][]float64, error) {
		vectors := make([][]float64, len(input))
		for i := range vectors {
			vectors[i] = make([]float64, 384)
			vectors[i][0] = 1
		}
		return vectors, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	server := sdk.NewServer(&sdk.Implementation{Name: "rag-light-test", Version: "v1"}, nil)
	addRAGTools(server, client, index)
	serverSide, clientSide := sdk.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), serverSide, nil); err != nil {
		t.Fatal(err)
	}
	agent := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "v1"}, nil)
	session, err := agent.Connect(context.Background(), clientSide, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if result := call(t, session, "index_documents", nil, nil); result.IsError {
		t.Fatal(errorText(result))
	}
	counter.reset()
	if result := call(t, session, "index_documents", nil, nil); result.IsError {
		t.Fatal(errorText(result))
	}
	if counter.count("/api/documents") != 1 {
		t.Fatal("missing revision manifest")
	}
	for _, n := range g.Nodes {
		if counter.count("/api/nodes/"+n.ID) != 0 {
			t.Fatal("unchanged indexing downloaded a body")
		}
	}
	counter.reset()
	var found searchDocumentsOutput
	if result := call(t, session, "search_documents", map[string]any{"query": "reference"}, &found); result.IsError {
		t.Fatal(errorText(result))
	}
	if len(found.Matches) != 3 || found.Stale != 1 {
		t.Fatalf("matches=%d stale=%d", len(found.Matches), found.Stale)
	}
	chars := 0
	for _, match := range found.Matches {
		chars += len([]rune(match.Text))
		body, _, err := pm.Store().LoadNodeContent(match.NodeID)
		if err != nil {
			t.Fatal(err)
		}
		if match.Text != string([]rune(body)[match.Offset:match.End]) || !match.Truncated || match.Rev == "" {
			t.Fatal("excerpt lost Unicode citation or continuation")
		}
	}
	if chars > 1800 {
		t.Fatal("excerpt budget exceeded")
	}
	if counter.count("/api/graph") != 0 || counter.count("/api/nodes/design/context") != 1 || counter.count("/api/nodes/more3/context") != 0 {
		t.Fatal("retrieval fetched excess context")
	}
	mu.Lock()
	limits := append([]int{}, queryLimits...)
	mu.Unlock()
	if len(limits) != 2 || limits[0] != 3 || limits[1] != 6 {
		t.Fatalf("unbounded candidate requests: %v", limits)
	}
	compact, _ := json.Marshal(found)
	legacy := searchDocumentsOutput{Project: client.project, Matches: []documentMatch{}, Stale: 1, Note: found.Note}
	for _, hit := range ordered[1:] {
		_, rev, _ := pm.Store().LoadNodeContent(hit.NodeID)
		legacy.Matches = append(legacy.Matches, documentMatch{NodeID: hit.NodeID, Title: g.NodeByID(hit.NodeID).Title, Rev: rev, Offset: hit.Start, End: hit.End, Text: hit.Text, Distance: 0.1})
	}
	full, _ := json.Marshal(legacy)
	if len(compact)*2 > len(full) {
		t.Fatal("RAG payload did not shrink by at least half")
	}
	t.Logf("RAG payload: %d -> %d bytes; text: %d characters; candidate requests: %v", len(full), len(compact), chars, limits)
	found = searchDocumentsOutput{}
	if result := call(t, session, "search_documents", map[string]any{"query": "reference", "maxChars": 24000}, &found); result.IsError {
		t.Fatal(errorText(result))
	}
	if len(found.Matches) != 3 || found.Matches[0].Truncated {
		t.Fatal("explicit fuller excerpts unavailable")
	}
	for _, budget := range []int{-1, 199, 24001} {
		if result := call(t, session, "search_documents", map[string]any{"query": "reference", "maxChars": budget}, nil); !result.IsError {
			t.Fatal("invalid budget accepted")
		}
	}
	mu.Lock()
	ordered = []rag.Hit{byNode["design"][0]}
	ordered[0].Text = "tampered"
	mu.Unlock()
	call(t, session, "search_documents", map[string]any{"query": "reference"}, &found)
	if len(found.Matches) != 0 || found.Stale != 1 {
		t.Fatal("tampered chunk text exposed")
	}
	mu.Lock()
	churn = true
	mu.Unlock()
	found = searchDocumentsOutput{}
	if result := call(t, session, "search_documents", map[string]any{"query": "changing ANN results"}, &found); result.IsError {
		t.Fatal(errorText(result))
	}
	if len(found.Matches) != 0 || found.Stale != 15 {
		t.Fatalf("unique candidate budget: %+v", found)
	}
	// The regular node reader also uses the compact endpoint.
	counter.reset()
	if _, err := readNode(context.Background(), client, getNodeInput{ID: "design"}); err != nil {
		t.Fatal(err)
	}
	if counter.count("/api/nodes/design/context") != 1 || counter.count("/api/graph") != 0 {
		t.Fatal("get_node downloaded the graph")
	}
}
