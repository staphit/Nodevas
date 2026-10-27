package localllm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestPickModelPrefersToolCallers(t *testing.T) {
	cases := []struct {
		names []string
		want  string
	}{
		{[]string{"gemma3:4b", "nomic-embed-text", "qwen3:8b", "llama3.1:8b"}, "qwen3:8b"},
		{[]string{"gemma3:4b", "llama3.2:3b"}, "llama3.2:3b"},
		{[]string{"nomic-embed-text", "gemma3:4b"}, "gemma3:4b"},
		{[]string{"phi3", "hf.co/org/Qwen3-8B-GGUF"}, "hf.co/org/Qwen3-8B-GGUF"},
		{[]string{"library/qwen2.5:7b"}, "library/qwen2.5:7b"},
		{[]string{"text-embedding-nomic", "bad name"}, ""},
		{nil, ""},
	}
	for _, tc := range cases {
		if got := PickModel(tc.names); got != tc.want {
			t.Errorf("PickModel(%v) = %q, want %q", tc.names, got, tc.want)
		}
	}
}

func TestLoopback(t *testing.T) {
	for url, want := range map[string]bool{
		"http://127.0.0.1:11434":   true,
		"http://127.9.9.9:1":       true,
		"http://[::1]:1234":        true,
		"http://localhost:1234":    true,
		"http://192.168.1.5:11434": false,
		"http://example.com":       false,
		"ftp://127.0.0.1":          false,
		"http://u:p@127.0.0.1:1":   false,
		"http://127.0.0.1.nip.io":  false,
	} {
		if got := Loopback(url); got != want {
			t.Errorf("Loopback(%q) = %v, want %v", url, got, want)
		}
	}
}

// ollamaLike serves /api/tags and counts requests.
func ollamaLike(t *testing.T, models string, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/api/tags":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(models))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestDetectFindsOllamaAndCaches(t *testing.T) {
	var hits atomic.Int32
	server := ollamaLike(t, `{"models":[{"name":"gemma3:4b"},{"name":"qwen3:8b"}]}`, &hits)
	now := time.Unix(1000, 0)
	d := &Detector{
		Candidates: []Candidate{{Server: ServerOllama, Root: server.URL}},
		Now:        func() time.Time { return now },
	}
	found, ok := d.Detect(context.Background())
	if !ok || found.Server != ServerOllama || found.Model != "qwen3:8b" || found.BaseURL != server.URL+"/v1" {
		t.Fatalf("Detect = %+v, %v", found, ok)
	}
	first := hits.Load()
	if _, ok := d.Detect(context.Background()); !ok || hits.Load() != first {
		t.Fatalf("second Detect probed again (%d → %d)", first, hits.Load())
	}
	now = now.Add(DefaultFoundTTL + time.Second)
	if _, ok := d.Detect(context.Background()); !ok || hits.Load() == first {
		t.Fatal("an expired cache was not re-checked")
	}
}

func TestDetectFallsBackToOpenAIModelsAndNextCandidate(t *testing.T) {
	lmstudio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"text-embedding-x"},{"id":"mistral-nemo-instruct"}]}`))
	}))
	defer lmstudio.Close()
	// Ollama's port answers nothing useful.
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer empty.Close()
	d := &Detector{Candidates: []Candidate{
		{Server: ServerOllama, Root: empty.URL},
		{Server: ServerLMStudio, Root: lmstudio.URL},
	}}
	found, ok := d.Detect(context.Background())
	if !ok || found.Server != ServerLMStudio || found.Model != "mistral-nemo-instruct" {
		t.Fatalf("Detect = %+v, %v", found, ok)
	}
}

func TestDetectSkipsNonLoopbackCandidates(t *testing.T) {
	var asked atomic.Bool
	d := &Detector{
		Candidates: []Candidate{{Server: ServerOllama, Root: "http://192.0.2.1:11434"}},
		Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			asked.Store(true)
			return nil, http.ErrHandlerTimeout
		})},
	}
	if _, ok := d.Detect(context.Background()); ok || asked.Load() {
		t.Fatalf("a non-loopback candidate was probed (found %v, asked %v)", ok, asked.Load())
	}
}

func TestDetectNothingIsCachedBriefly(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	now := time.Unix(1000, 0)
	d := &Detector{Candidates: []Candidate{{Server: ServerOllama, Root: server.URL}},
		Now: func() time.Time { return now }}
	if _, ok := d.Detect(context.Background()); ok {
		t.Fatal("a failing server was detected")
	}
	n := hits.Load()
	if _, ok := d.Detect(context.Background()); ok || hits.Load() != n {
		t.Fatal("a miss was not cached")
	}
	now = now.Add(DefaultMissingTTL + time.Second)
	d.Detect(context.Background())
	if hits.Load() == n {
		t.Fatal("a stale miss was not re-checked")
	}
}

func TestDetectTimesOut(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)
	d := &Detector{Candidates: []Candidate{{Server: ServerOllama, Root: server.URL}},
		ProbeTimeout: 50 * time.Millisecond}
	started := time.Now()
	if _, ok := d.Detect(context.Background()); ok {
		t.Fatal("a hanging server was detected")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("probe took %s", elapsed)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
