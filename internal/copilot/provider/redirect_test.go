package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// An endpoint that redirects to another host never gets the key sent there.
func TestProvidersRefuseCrossHostRedirects(t *testing.T) {
	var leaked atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("api-key") != "" {
			leaked.Add(1)
		}
		w.WriteHeader(http.StatusTeapot)
	}))
	defer elsewhere.Close()
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer endpoint.Close()

	req := Request{Messages: []Message{{Role: RoleUser, Blocks: []Block{{Type: BlockText, Text: "hi"}}}}}
	for _, kind := range []string{"openai", "anthropic"} {
		p, err := New(Config{Kind: kind, BaseURL: endpoint.URL, Model: "m", APIKey: "sk-test"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Stream(context.Background(), req, nil); err == nil {
			t.Errorf("%s: a cross-host redirect was followed", kind)
		}
	}
	if n := leaked.Load(); n != 0 {
		t.Fatalf("the key reached the redirect target %d times", n)
	}
}
