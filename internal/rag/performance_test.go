package rag

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestMiniLMPerformance(t *testing.T) {
	python := os.Getenv("NODEVAS_TEST_RAG_PYTHON")
	if python == "" {
		t.Skip("set NODEVAS_TEST_RAG_PYTHON for model timing")
	}
	worker := MiniLM(python, os.Stderr)
	defer worker.Close()
	embed := worker.Embed
	for _, query := range []string{"How do I recover my account?", "How do I reset a forgotten password?", "Where is the login recovery link?"} {
		start := time.Now()
		if _, err := embed(context.Background(), []string{query}); err != nil {
			t.Fatal(err)
		}
		t.Logf("embedding: %s", time.Since(start))
	}
}
