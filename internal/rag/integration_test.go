package rag

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

// Opt in with NODEVAS_TEST_CHROMA_URL and NODEVAS_TEST_RAG_PYTHON. This uses
// real Chroma HTTP endpoints and downloads MiniLM on first use.
func TestChromaMiniLMIntegration(t *testing.T) {
	endpoint, python := os.Getenv("NODEVAS_TEST_CHROMA_URL"), os.Getenv("NODEVAS_TEST_RAG_PYTHON")
	if endpoint == "" || python == "" {
		t.Skip("set NODEVAS_TEST_CHROMA_URL and NODEVAS_TEST_RAG_PYTHON for live integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	worker := MiniLM(python, os.Stderr)
	defer worker.Close()
	embedding := worker.Embed
	vectors, err := embedding(ctx, []string{"The cat sleeps on a sofa.", strings.Repeat("Long document about software. ", 200) + " The cat sleeps on a sofa."})
	if err != nil {
		t.Fatal(err)
	}
	for _, vector := range vectors {
		if len(vector) != 384 {
			t.Fatalf("dimension: %d", len(vector))
		}
		norm := 0.0
		for _, v := range vector {
			norm += v * v
		}
		if math.Abs(norm-1) > 0.001 {
			t.Fatalf("unnormalized vector: %f", norm)
		}
	}
	scope := fmt.Sprintf("integration-%d", time.Now().UnixNano())
	s, err := New(endpoint, scope, embedding)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := s.request(cleanupCtx, "DELETE", s.chroma+collectionsPath+"/"+s.name, nil, nil); err != nil {
			t.Logf("test collection cleanup: %v", err)
		}
	})
	docs := []Document{
		{ID: "access", Title: "Account access", Content: "If you forget your password, use the reset link on the sign-in screen. A recovery email will restore access to your account."},
		{ID: "cooking", Title: "Pasta recipe", Content: "Boil salted water, add pasta and cook for ten minutes. Serve with tomato sauce."},
	}
	if n, err := s.Sync(ctx, docs); err != nil || n != 2 {
		t.Fatalf("index: %d %v", n, err)
	}
	hits, err := s.Search(ctx, "How can I recover a lost login?", 5)
	if err != nil || len(hits) == 0 || hits[0].NodeID != "access" {
		t.Fatalf("semantic retrieval: %+v %v", hits, err)
	}
	docs[0].Content = "Contact the support desk to recover your account."
	if n, err := s.Sync(ctx, docs[:1]); err != nil || n != 1 {
		t.Fatalf("refresh: %d %v", n, err)
	}
	reopened, err := New(endpoint, scope, embedding)
	if err != nil {
		t.Fatal(err)
	}
	hits, err = reopened.Search(ctx, "recover account", 5)
	if err != nil || len(hits) != 1 || hits[0].Text != docs[0].Content {
		t.Fatalf("persisted refresh: %+v %v", hits, err)
	}
	if _, err := s.Sync(ctx, nil); err != nil {
		t.Fatal(err)
	}
	hits, err = s.Search(ctx, "recover account", 5)
	if err != nil || len(hits) != 0 {
		t.Fatalf("empty index: %+v %v", hits, err)
	}
}

func TestMiniLMMissingPythonIsActionable(t *testing.T) {
	_, err := MiniLM("nodevas-nonexistent-python", nil).Embed(context.Background(), []string{"text"})
	if err == nil || !strings.Contains(err.Error(), "--rag-python") {
		t.Fatalf("error: %v", err)
	}
}

// TestMiniLMEmbedsChinese runs the real helper on text a Big5/cp950 console
// cannot encode. Under mojibake both words arrive as "??" and embed alike.
func TestMiniLMEmbedsChinese(t *testing.T) {
	python := os.Getenv("NODEVAS_TEST_RAG_PYTHON")
	if python == "" {
		t.Skip("set NODEVAS_TEST_RAG_PYTHON to run the MiniLM helper")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	worker := MiniLM(python, os.Stderr)
	defer worker.Close()
	distinct, err := worker.Embed(ctx, []string{"这个", "简体"})
	if err != nil && strings.Contains(err.Error(), "rag-requirements") {
		t.Skipf("helper dependencies missing: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	same, err := worker.Embed(ctx, []string{"林曉怕水", "林曉怕水"})
	if err != nil {
		t.Fatal(err)
	}
	for _, vector := range append(distinct, same...) {
		if len(vector) != 384 {
			t.Fatalf("dimension: %d", len(vector))
		}
	}
	if cosine(distinct[0], distinct[1]) >= 0.999 {
		t.Fatal("这个 and 简体 embed alike: the helper is not reading UTF-8")
	}
	for i := range same[0] {
		if same[0][i] != same[1][i] {
			t.Fatal("identical inputs gave different vectors")
		}
	}
}

func cosine(a, b []float64) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	return dot / math.Sqrt(na*nb)
}
