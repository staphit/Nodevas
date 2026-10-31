package adapter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeHub is the hub's POST /api/agents/asks behind a run's proxy door: it
// records each ask and answers with the next ask object queued.
type fakeHub struct {
	t       *testing.T
	mu      sync.Mutex
	asks    []map[string]any
	paths   []string
	queries []string
	roles   []string
	answers []string // the "ask" object of each answer, as JSON
	status  int
}

func newFakeHub(t *testing.T, answers ...string) (*fakeHub, *httptest.Server) {
	h := &fakeHub{t: t, answers: answers, status: http.StatusOK}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return h, srv
}

func (h *fakeHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	if r.Method != http.MethodPost || json.Unmarshal(raw, &body) != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	h.asks = append(h.asks, body)
	h.paths = append(h.paths, r.URL.Path)
	h.queries = append(h.queries, r.URL.RawQuery)
	h.roles = append(h.roles, r.Header.Get("X-Nodevas-Agent-Role"))
	answer := `{"id":"a1","status":"denied"}`
	if len(h.answers) > 0 {
		answer, h.answers = h.answers[0], h.answers[1:]
	}
	status := h.status
	h.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if status >= 400 {
		_, _ = io.WriteString(w, `{"error":"no such route"}`)
		return
	}
	_, _ = io.WriteString(w, `{"ask":`+answer+`,"unreadCount":0}`)
}

func (h *fakeHub) last() map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.asks) == 0 {
		h.t.Fatal("the hub was never asked")
	}
	return h.asks[len(h.asks)-1]
}

func (h *fakeHub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.asks)
}

func testMCPArgs(server string) []string {
	return []string{"mcp", "--server", server + "/r/0123456789abcdef0123456789abcdef", "--project", "demo",
		"--agent-role", "worker", "--actor", "run:antigravity", "--session-name", "demo-antigravity-default-run1",
		"--toolset", "run"}
}

// The asker reads the run's `nodevas mcp` arguments and long-polls the hub
// through the same proxy door: a permission ask in the contract's shape.
func TestHubAskerPermission(t *testing.T) {
	hub, srv := newFakeHub(t,
		`{"id":"a1","status":"approved","decidedBy":"patrick"}`,
		`{"id":"a2","status":"approved","decision":"approve_run"}`,
		`{"id":"a3","status":"denied","decidedBy":"patrick","note":"use go test instead"}`,
		`{"id":"a4","status":"expired"}`)
	asker, ok := HubAskerFromMCPArgs(testMCPArgs(srv.URL))
	if !ok {
		t.Fatal("no asker from the run's MCP arguments")
	}
	asker.NodeID = "n-42"
	req := PermissionRequest{Tool: "run_command", Input: `{"CommandLine":"go build ./..."}`}

	got := asker.AskPermission(context.Background(), req)
	if !got.Allow || got.ForRun {
		t.Errorf("approved: %+v", got)
	}
	ask := hub.last()
	for key, want := range map[string]any{"kind": "permission", "tool": "run_command", "toolInput": req.Input,
		"sessionName": "demo-antigravity-default-run1", "sessionId": "antigravity:demo-antigravity-default-run1",
		"actor": "run:antigravity", "agent": "antigravity", "nodeId": "n-42",
		"timeoutSeconds": float64(PermissionAskTimeout / time.Second)} {
		if ask[key] != want {
			t.Errorf("ask[%s] = %v, want %v", key, ask[key], want)
		}
	}
	if q, _ := ask["question"].(string); !strings.Contains(q, "run_command") || !strings.Contains(q, "go build") {
		t.Errorf("question = %q", q)
	}
	hub.mu.Lock()
	path, query, role := hub.paths[0], hub.queries[0], hub.roles[0]
	hub.mu.Unlock()
	if path != "/r/0123456789abcdef0123456789abcdef/api/agents/asks" || query != "project=demo" || role != "worker" {
		t.Errorf("request: path %q query %q role %q", path, query, role)
	}

	if got := asker.AskPermission(context.Background(), req); !got.Allow || !got.ForRun {
		t.Errorf("approved for the run: %+v", got)
	}
	if got := asker.AskPermission(context.Background(), req); got.Allow || !strings.Contains(got.Reason, "use go test instead") {
		t.Errorf("denied: %+v", got)
	}
	if got := asker.AskPermission(context.Background(), req); got.Allow || !strings.Contains(got.Reason, "in time") {
		t.Errorf("expired: %+v", got)
	}

	// The tool input is cut to the contract's 4 KiB.
	long := strings.Repeat("x", 10000)
	asker.AskPermission(context.Background(), PermissionRequest{Tool: "Bash", Input: long})
	if in, _ := hub.last()["toolInput"].(string); len(in) > 4096 || len(in) == 0 {
		t.Errorf("toolInput is %d bytes", len(in))
	}
}

// Anything but an approval is a denial: the hub refusing, the hub gone, a
// wait past the asker's limit.
func TestHubAskerFailsClosed(t *testing.T) {
	hub, srv := newFakeHub(t)
	hub.status = http.StatusNotFound
	asker, _ := HubAskerFromMCPArgs(testMCPArgs(srv.URL))
	if got := asker.AskPermission(context.Background(), PermissionRequest{Tool: "Bash"}); got.Allow || got.Reason == "" {
		t.Errorf("hub refused: %+v", got)
	}

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the body first: only then does the server notice the
		// client hanging up.
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	defer slow.Close()
	asker, _ = HubAskerFromMCPArgs(testMCPArgs(slow.URL))
	asker.Wait = 50 * time.Millisecond
	start := time.Now()
	if got := asker.AskPermission(context.Background(), PermissionRequest{Tool: "Bash"}); got.Allow || got.Reason == "" {
		t.Errorf("timed out: %+v", got)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("the asker did not give up at its wait")
	}

	asker, _ = HubAskerFromMCPArgs([]string{"mcp", "--server", "http://127.0.0.1:9/r/x"})
	if got := asker.AskPermission(context.Background(), PermissionRequest{Tool: "Bash"}); got.Allow {
		t.Errorf("hub gone: %+v", got)
	}
	if _, ok := HubAskerFromMCPArgs([]string{"mcp", "--project", "demo"}); ok {
		t.Error("an asker without a server")
	}
	if PermissionWait < 330*time.Second {
		t.Errorf("PermissionWait = %v", PermissionWait)
	}
}
