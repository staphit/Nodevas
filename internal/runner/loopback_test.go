package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// tokenRecorder is a hub that knows nothing: it notes the Authorization
// header of every request and answers none of them.
type tokenRecorder struct {
	mu   sync.Mutex
	seen []string
}

func (h *tokenRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.seen = append(h.seen, r.Header.Get("Authorization"))
	h.mu.Unlock()
	w.WriteHeader(http.StatusBadGateway)
}

func (h *tokenRecorder) tokens() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, v := range h.seen {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// runBriefly runs a client until it stops or about a second has passed.
func runBriefly(t *testing.T, c *Client) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return c.Run(ctx)
}

// A plain-http hub on this machine is whatever process holds the port: while
// the real one is down, another local user can take it. A runner that serve
// did not start sends it no token, and stops with the reason, unless its
// owner said otherwise.
func TestRunnerSendsNoTokenToAPlainLocalHub(t *testing.T) {
	squatter := &tokenRecorder{}
	srv := httptest.NewServer(squatter)
	defer srv.Close()
	client := &Client{
		Config:     &Config{Hub: srv.URL, Token: testToken, AccessClientID: "cid", AccessClientSecret: "sec"},
		Commands:   &fakeCommands{paths: map[string]string{}},
		BackoffMin: 5 * time.Millisecond, BackoffMax: 20 * time.Millisecond,
	}
	err := runBriefly(t, client)
	if got := squatter.tokens(); len(got) > 0 {
		t.Fatalf("a plain-http hub on this machine got the runner token (%d requests): %q", len(got), got[0])
	}
	if err == nil {
		t.Fatal("the runner kept trying a hub it would not send its token to, without saying why")
	}

	// Serve's own runner: serve holds the port while it runs.
	served := &tokenRecorder{}
	srv2 := httptest.NewServer(served)
	defer srv2.Close()
	client.Config.Hub, client.ServeManaged = srv2.URL, true
	_ = runBriefly(t, client)
	if len(served.tokens()) == 0 {
		t.Error("serve's own runner did not connect")
	}

	// The owner's opt-in, `nodevas runner set httpLoopbackHub on`.
	if err := client.Config.Set("httpLoopbackHub", []string{"on"}); err != nil || !client.Config.HTTPLoopbackHub {
		t.Fatalf("set httpLoopbackHub on: %v", err)
	}
	opted := &tokenRecorder{}
	srv3 := httptest.NewServer(opted)
	defer srv3.Close()
	client.Config.Hub, client.ServeManaged = srv3.URL, false
	_ = runBriefly(t, client)
	if len(opted.tokens()) == 0 {
		t.Error("a runner whose owner allowed the plain-http hub did not connect")
	}

	// https needs nothing.
	secure := &tokenRecorder{}
	tls := httptest.NewTLSServer(secure)
	defer tls.Close()
	client.Config.Hub, client.Config.HTTPLoopbackHub, client.HTTPClient = tls.URL, false, tls.Client()
	_ = runBriefly(t, client)
	if len(secure.tokens()) == 0 {
		t.Error("a runner did not connect to an https hub")
	}
}
