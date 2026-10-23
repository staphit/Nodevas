package runner

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"nodevas/internal/runnerwire"
)

const testToken = runnerwire.TokenPrefix + "abcdefghijklmnopqrstuvwxyz012345"

// fakeCommands answers LookPath and Run from tables instead of the machine.
type fakeCommands struct {
	mu      sync.Mutex
	paths   map[string]string
	outputs map[string]string
	errs    map[string]error
	calls   []string
}

func (f *fakeCommands) LookPath(name string) (string, error) {
	if p, ok := f.paths[name]; ok {
		return p, nil
	}
	return "", exec.ErrNotFound
}

func (f *fakeCommands) Run(_ context.Context, name string, args ...string) (string, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	f.mu.Lock()
	f.calls = append(f.calls, key)
	out, err := f.outputs[key], f.errs[key]
	f.mu.Unlock()
	return out, err
}

func TestConfigRoundTripIsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodevas", "runner.json")
	cfg, err := LoadConfig(path)
	if err != nil || cfg.Hub != "" {
		t.Fatalf("missing file: %+v, %v", cfg, err)
	}
	cfg = &Config{
		Hub: "https://hub.example", Token: testToken,
		AccessClientID: "id.access", AccessClientSecret: "s3cret",
		Repos:  []runnerwire.Repo{{ID: "abc", Name: "repo", Path: "/src/repo"}},
		Models: map[string][]runnerwire.Model{"codex": {{ID: "o4", Default: true}}},
	}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != testToken || got.AccessClientSecret != "s3cret" || len(got.Repos) != 1 || got.Models["codex"][0].ID != "o4" {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Fatalf("config mode %o, want 600", mode)
		}
	}
}

func TestValidateHub(t *testing.T) {
	ok := map[string]string{
		"https://hub.example/":      "https://hub.example",
		"http://127.0.0.1:5666":     "http://127.0.0.1:5666",
		"http://localhost:5666/":    "http://localhost:5666",
		"https://hub.example/base/": "https://hub.example/base",
	}
	for in, want := range ok {
		if got, err := ValidateHub(in); err != nil || got != want {
			t.Errorf("ValidateHub(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "hub.example", "http://hub.example", "ftp://hub.example",
		"https://u:p@hub.example", "https://hub.example/?x=1"} {
		if _, err := ValidateHub(bad); err == nil {
			t.Errorf("ValidateHub(%q) accepted", bad)
		}
	}
}

func TestValidateToken(t *testing.T) {
	if err := ValidateToken(testToken); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "abc", runnerwire.TokenPrefix, "nodevas_session_xxxxxxxxxxxxxxxxxxxx", testToken + " x"} {
		if ValidateToken(bad) == nil {
			t.Errorf("ValidateToken(%q) accepted", bad)
		}
	}
}

func TestDetect(t *testing.T) {
	cmds := &fakeCommands{
		paths: map[string]string{"claude": "/bin/claude", "codex": "/bin/codex"},
		outputs: map[string]string{
			"/bin/claude --version": "\n2.1.0 (Claude Code)\nextra\n",
		},
		errs: map[string]error{"/bin/codex --version": errors.New("exit status 1")},
	}
	cfg := &Config{Models: map[string][]runnerwire.Model{"gemini": {{ID: "gemini-x", Default: true}}}}
	agents := Detect(context.Background(), cmds, cfg)
	if len(agents) != 3 {
		t.Fatalf("agents = %+v", agents)
	}
	claude, codex, gemini := agents[0], agents[1], agents[2]
	if !claude.Installed || claude.Version != "2.1.0 (Claude Code)" || len(claude.Models) != 4 || claude.Models[0].ID != "default" {
		t.Errorf("claude = %+v", claude)
	}
	if codex.Installed || !strings.Contains(codex.Error, "exit status 1") || len(codex.Models) != 1 {
		t.Errorf("codex = %+v", codex)
	}
	if gemini.Installed || gemini.Error != "" || gemini.Models[0].ID != "gemini-x" {
		t.Errorf("gemini = %+v", gemini)
	}
}

func TestRepoAddRemove(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	run("remote", "add", "origin", "https://example.test/me/repo.git")
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	repo, err := InspectRepo(context.Background(), SystemCommands{}, sub)
	if err != nil {
		t.Fatal(err)
	}
	wantTop, _ := filepath.EvalSymlinks(dir)
	gotTop, _ := filepath.EvalSymlinks(repo.Path)
	if !strings.EqualFold(gotTop, wantTop) || repo.Remote != "https://example.test/me/repo.git" || len(repo.ID) != 12 {
		t.Fatalf("repo = %+v (want top %s)", repo, wantTop)
	}
	if _, err := InspectRepo(context.Background(), SystemCommands{}, t.TempDir()); err == nil {
		t.Fatal("a plain directory was accepted as a repository")
	}

	cfg := &Config{}
	if err := cfg.AddRepo(repo); err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddRepo(repo); err == nil {
		t.Fatal("same repository added twice")
	}
	if _, err := cfg.RemoveRepo("nope"); err == nil {
		t.Fatal("removed a repository that is not there")
	}
	if removed, err := cfg.RemoveRepo(repo.Name); err != nil || removed.ID != repo.ID || len(cfg.Repos) != 0 {
		t.Fatalf("remove by name: %+v, %v, %+v", removed, err, cfg.Repos)
	}
}

// fakeHub is a hub that speaks the runner protocol.
type fakeHub struct {
	t         *testing.T
	hellos    chan runnerwire.Info
	pings     atomic.Int32
	conns     atomic.Int32
	reject    atomic.Int32 // HTTP status to refuse with, 0 to accept
	bye       atomic.Value // string reason sent right after welcome
	dropAfter atomic.Int32 // close after welcome on the first N connections
	authSeen  atomic.Value
	accessID  atomic.Value
}

func newFakeHub(t *testing.T) (*fakeHub, *httptest.Server) {
	h := &fakeHub{t: t, hellos: make(chan runnerwire.Info, 16)}
	h.bye.Store("")
	srv := httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(srv.Close)
	return h, srv
}

func (h *fakeHub) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != runnerwire.ConnectPath {
		http.NotFound(w, r)
		return
	}
	h.authSeen.Store(r.Header.Get("Authorization"))
	h.accessID.Store(r.Header.Get(runnerwire.AccessClientIDHeader))
	if code := h.reject.Load(); code != 0 {
		http.Error(w, "no", int(code))
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	n := h.conns.Add(1)
	ctx := r.Context()
	for {
		var msg runnerwire.Envelope
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			return
		}
		switch msg.Type {
		case runnerwire.TypeHello:
			h.hellos <- *msg.Hello
			_ = wsjson.Write(ctx, conn, runnerwire.Envelope{Type: runnerwire.TypeWelcome,
				Welcome: &runnerwire.Welcome{RunnerID: "r1", Name: "pc", Owner: "me"}})
			if reason := h.bye.Load().(string); reason != "" {
				_ = wsjson.Write(ctx, conn, runnerwire.Envelope{Type: runnerwire.TypeBye, Reason: reason})
				return
			}
			if n <= h.dropAfter.Load() {
				_ = conn.Close(websocket.StatusGoingAway, "restarting")
				return
			}
		case runnerwire.TypePing:
			h.pings.Add(1)
			_ = wsjson.Write(ctx, conn, runnerwire.Envelope{Type: runnerwire.TypePong})
		}
	}
}

func testClient(srv *httptest.Server) *Client {
	return &Client{
		Config:       &Config{Hub: srv.URL, Token: testToken, AccessClientID: "cid", AccessClientSecret: "sec"},
		Commands:     &fakeCommands{paths: map[string]string{}},
		PingInterval: 20 * time.Millisecond,
		BackoffMin:   5 * time.Millisecond,
		BackoffMax:   20 * time.Millisecond,
	}
}

func TestClientHelloWelcomePing(t *testing.T) {
	hub, srv := newFakeHub(t)
	client := testClient(srv)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()

	select {
	case info := <-hub.hellos:
		if len(info.Agents) != 3 || info.OS != runtime.GOOS || info.Version == "" {
			t.Errorf("hello = %+v", info)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no hello")
	}
	deadline := time.Now().Add(5 * time.Second)
	for hub.pings.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hub.pings.Load() < 2 {
		t.Fatalf("pings = %d", hub.pings.Load())
	}
	if got := hub.authSeen.Load(); got != "Bearer "+testToken {
		t.Errorf("Authorization = %v", got)
	}
	if got := hub.accessID.Load(); got != "cid" {
		t.Errorf("access client id = %v", got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run after cancel = %v", err)
	}
}

func TestClientStopsWhenRevoked(t *testing.T) {
	hub, srv := newFakeHub(t)
	hub.bye.Store(runnerwire.ByeRevoked)
	err := runWithTimeout(t, testClient(srv))
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("Run = %v, want ErrRevoked", err)
	}
}

func TestClientStopsWhenReplaced(t *testing.T) {
	hub, srv := newFakeHub(t)
	hub.bye.Store(runnerwire.ByeReplaced)
	if err := runWithTimeout(t, testClient(srv)); !errors.Is(err, ErrReplaced) {
		t.Fatalf("Run = %v, want ErrReplaced", err)
	}
}

func TestClientStopsOn401(t *testing.T) {
	hub, srv := newFakeHub(t)
	hub.reject.Store(http.StatusUnauthorized)
	err := runWithTimeout(t, testClient(srv))
	if !errors.Is(err, ErrRevoked) || !strings.Contains(err.Error(), "nodevas runner login") {
		t.Fatalf("Run = %v, want ErrRevoked", err)
	}
}

func TestClientReconnectsAfterDrop(t *testing.T) {
	hub, srv := newFakeHub(t)
	hub.dropAfter.Store(2)
	client := testClient(srv)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()
	for i := 0; i < 3; i++ {
		select {
		case <-hub.hellos:
		case <-time.After(5 * time.Second):
			t.Fatalf("hello %d never came", i+1)
		}
	}
	if hub.conns.Load() < 3 {
		t.Fatalf("connections = %d", hub.conns.Load())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestClientRetriesWhileHubDown(t *testing.T) {
	hub, srv := newFakeHub(t)
	hub.reject.Store(http.StatusBadGateway)
	client := testClient(srv)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)
	hub.reject.Store(0)
	select {
	case <-hub.hellos:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not come back once the hub did")
	}
	cancel()
	<-done
}

func TestConnectURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://hub.example":      "wss://hub.example" + runnerwire.ConnectPath,
		"http://127.0.0.1:5666":    "ws://127.0.0.1:5666" + runnerwire.ConnectPath,
		"https://hub.example/base": "wss://hub.example/base" + runnerwire.ConnectPath,
	} {
		if got, err := connectURL(in); err != nil || got != want {
			t.Errorf("connectURL(%q) = %q, %v", in, got, err)
		}
	}
}

func runWithTimeout(t *testing.T, client *Client) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := client.Run(ctx)
	if ctx.Err() != nil {
		t.Fatal("Run did not stop on its own")
	}
	return err
}
