package runner

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

type seenRequest struct {
	method, uri, auth, accessID, accessSecret, role, project, contentType, cookie, body, runID string
}

func TestProxyForwards(t *testing.T) {
	var mu sync.Mutex
	var seen []seenRequest
	release := make(chan struct{})
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, seenRequest{
			method: r.Method, uri: r.URL.RequestURI(), auth: r.Header.Get("Authorization"),
			accessID: r.Header.Get(runnerwire.AccessClientIDHeader), accessSecret: r.Header.Get(runnerwire.AccessClientSecretHeader),
			role: r.Header.Get("X-Nodevas-Agent-Role"), project: r.Header.Get("X-Nodevas-Project"),
			contentType: r.Header.Get("Content-Type"), cookie: r.Header.Get("Cookie"), body: string(body),
			runID: r.Header.Get(runnerwire.RunIDHeader),
		})
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/agents/inbox") {
			<-release // a long poll
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "session=nope")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer hub.Close()

	cfg := &Config{Hub: hub.URL + "/base", Token: testToken, AccessClientID: "cid", AccessClientSecret: "csec"}
	proxy, err := StartProxy(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	if !strings.HasPrefix(proxy.Addr(), "127.0.0.1:") {
		t.Fatalf("proxy listens on %s", proxy.Addr())
	}
	secret := proxy.Register("run-1")
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(secret) {
		t.Fatalf("secret %q", secret)
	}
	base := proxy.URL(secret)

	req, _ := http.NewRequest(http.MethodPost, base+"/api/nodes/n%2F1/claim?project=demo&x=1", strings.NewReader(`{"a":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nodevas-Agent-Role", "worker")
	req.Header.Set("X-Nodevas-Project", "demo")
	req.Header.Set("Authorization", "Bearer agent-made-this-up")
	req.Header.Set("Cookie", "session=stolen")
	req.Header.Set(runnerwire.RunIDHeader, "someone-elses-run")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || string(body) != `{"ok":true}` || resp.Header.Get("Set-Cookie") != "" {
		t.Fatalf("response %d %q %v", resp.StatusCode, body, resp.Header)
	}
	mu.Lock()
	got := seen[0]
	mu.Unlock()
	want := seenRequest{
		method: "POST", uri: "/base/api/runner/proxy/nodes/n%2F1/claim?project=demo&x=1", auth: "Bearer " + testToken,
		accessID: "cid", accessSecret: "csec", role: "worker", project: "demo", contentType: "application/json",
		body: `{"a":1}`, runID: "run-1",
	}
	if got != want {
		t.Fatalf("hub saw %+v\nwant     %+v", got, want)
	}

	// A long poll is not cut short by any client timeout of the proxy's.
	done := make(chan int, 1)
	go func() {
		resp, err := http.Get(base + "/api/agents/inbox")
		if err != nil {
			done <- -1
			return
		}
		resp.Body.Close()
		done <- resp.StatusCode
	}()
	select {
	case code := <-done:
		t.Fatalf("long poll answered early: %d", code)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if code := <-done; code != http.StatusCreated {
		t.Fatalf("long poll: %d", code)
	}

	// Wrong secret, no secret, outside /api/, dot segments: never forwarded.
	mu.Lock()
	before := len(seen)
	mu.Unlock()
	for path, status := range map[string]int{
		"/r/" + strings.Repeat("0", 32) + "/api/nodes": http.StatusNotFound,
		"/r//api/nodes":                          http.StatusNotFound,
		"/api/nodes":                             http.StatusNotFound,
		"/r/" + secret + "/other":                http.StatusNotFound,
		"/r/" + secret + "/api/":                 http.StatusNotFound,
		"/r/" + secret + "/api/a/../../connect":  http.StatusBadRequest,
		"/r/" + secret + "/api/a/%2e%2e/connect": http.StatusBadRequest,
	} {
		req, _ := http.NewRequest(http.MethodGet, "http://"+proxy.Addr()+path, nil)
		req.URL.Opaque = path // keep the dots: the client would clean them otherwise
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != status {
			t.Errorf("%s → %d, want %d", path, resp.StatusCode, status)
		}
	}
	mu.Lock()
	after := len(seen)
	mu.Unlock()
	if after != before {
		t.Fatalf("%d refused requests reached the hub", after-before)
	}

	// A revoked secret is dead.
	proxy.Revoke(secret)
	resp, err = http.Get(base + "/api/nodes")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("revoked secret: %d", resp.StatusCode)
	}
}

func TestProxyHubDown(t *testing.T) {
	hub := httptest.NewServer(http.NotFoundHandler())
	url := hub.URL
	hub.Close()
	proxy, err := StartProxy(&Config{Hub: url, Token: testToken}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	resp, err := http.Get(proxy.URL(proxy.Register("r")) + "/api/auth/status")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), `"error":"cannot reach the hub`) {
		t.Fatalf("hub down: %d %s", resp.StatusCode, body)
	}
}
