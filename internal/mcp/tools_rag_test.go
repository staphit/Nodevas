package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"nodevas/internal/rag"
)

func TestDocumentToolsIndexSearchAndRejectStaleSources(t *testing.T) {
	nodevasURL, _ := liveServer(t)
	client, err := NewClient(ClientOptions{Server: nodevasURL})
	if err != nil {
		t.Fatal(err)
	}
	projects, err := client.Projects(context.Background())
	if err != nil || len(projects) == 0 {
		t.Fatalf("projects: %v", err)
	}
	projectName := projects[0].Name
	client.project = projectName
	var documents []string
	var metadata []rag.Metadata
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/get"):
			w.Write([]byte(`{"ids":[]}`))
		case strings.HasSuffix(r.URL.Path, "/upsert"):
			var body struct {
				Documents []string       `json:"documents"`
				Metadatas []rag.Metadata `json:"metadatas"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			documents, metadata = body.Documents, body.Metadatas
			w.Write([]byte("{}"))
		case strings.HasSuffix(r.URL.Path, "/query"):
			if len(documents) == 0 {
				t.Error("query before index")
				http.Error(w, "empty", 500)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"ids": [][]string{{"chunk"}}, "documents": [][]string{{documents[0]}}, "metadatas": [][]rag.Metadata{{metadata[0]}}, "distances": [][]float64{{0.1}}})
		default:
			w.Write([]byte(`{"id":"collection"}`))
		}
	}))
	defer service.Close()
	server, err := NewServer(context.Background(), Options{Server: nodevasURL, Project: projectName})
	if err != nil {
		t.Fatal(err)
	}
	index, err := rag.New(service.URL, projectName, func(_ context.Context, input []string) ([][]float64, error) {
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
	addRAGTools(server, client, index)
	serverSide, clientSide := sdk.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), serverSide, nil); err != nil {
		t.Fatal(err)
	}
	agent := sdk.NewClient(&sdk.Implementation{Name: "rag-test", Version: "v1"}, nil)
	session, err := agent.Connect(context.Background(), clientSide, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	var indexed indexDocumentsOutput
	if result := call(t, session, "index_documents", nil, &indexed); result.IsError {
		t.Fatal(errorText(result))
	}
	if indexed.Chunks < 1 {
		t.Fatalf("empty index: %+v", indexed)
	}
	var found searchDocumentsOutput
	if result := call(t, session, "search_documents", map[string]any{"query": "How should we design this?"}, &found); result.IsError {
		t.Fatal(errorText(result))
	}
	if len(found.Matches) != 1 || found.Matches[0].NodeID != "design" || found.Matches[0].Rev == "" {
		t.Fatalf("missing citation: %+v", found)
	}
	if _, err := client.WriteNodeBody(context.Background(), "design", "New document body", found.Matches[0].Rev); err != nil {
		t.Fatal(err)
	}
	if result := call(t, session, "search_documents", map[string]any{"query": "design"}, &found); result.IsError {
		t.Fatal(errorText(result))
	}
	if len(found.Matches) != 0 || found.Stale != 1 {
		t.Fatalf("stale text exposed: %+v", found)
	}
	metadata[0].NodeID = "deleted-node"
	call(t, session, "search_documents", map[string]any{"query": "design"}, &found)
	if len(found.Matches) != 0 || found.Stale != 1 {
		t.Fatalf("deleted text exposed: %+v", found)
	}
	if result := call(t, session, "search_documents", map[string]any{"query": "design", "limit": 21}, nil); !result.IsError {
		t.Fatal("invalid limit accepted")
	}
}

func TestDocumentToolsRequirePinnedProjectAndRemainOptIn(t *testing.T) {
	url, _ := liveServer(t)
	if _, err := NewServer(context.Background(), Options{Server: url, ChromaURL: "http://127.0.0.1:8000"}); err == nil {
		t.Fatal("unpinned RAG enabled")
	}
	session := mcpSession(t, url, "")
	list, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if tool.Name == "index_documents" || tool.Name == "search_documents" {
			t.Fatal("RAG enabled without configuration")
		}
	}
}
