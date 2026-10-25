package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/agents"
	"nodevas/internal/agentsetup"
	agentsapi "nodevas/internal/httpapi/agents"
)

// agentsServer serves the agent network over loopback, as `nodevas serve`
// does on a desktop: every caller is the local user.
func agentsServer(t *testing.T) (string, *agents.Hub) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	hub := agents.NewHub(nil, nil)
	router := gin.New()
	agentsapi.New(nil, hub).Register(router.Group("/api"))
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv.URL, hub
}

// syncBuffer is a bytes.Buffer safe to write from the listen goroutine and
// read from the test.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestAgentsWhoListsTheRoster(t *testing.T) {
	url, hub := agentsServer(t)
	if _, err := hub.Touch(agents.Heartbeat{SessionID: "s1", ActorID: "mcp:alice", Name: "api-claude", Agent: "claude",
		Repo: "api", Branch: "main", Model: "claude-opus-4-1"}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := agentsWho(context.Background(), []string{"--server", url}, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"NAME", "INBOUND", "api-claude", "alice", "claude-opus-4-1", "api@main", "open"} {
		if !strings.Contains(text, want) {
			t.Fatalf("who output lacks %q:\n%s", want, text)
		}
	}
}

func TestAgentsSendIsAHumanMessage(t *testing.T) {
	url, hub := agentsServer(t)
	for _, id := range []string{"s1", "s2"} {
		if _, err := hub.Touch(agents.Heartbeat{SessionID: id, ActorID: "mcp:" + id, Name: "bot-" + id}); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(file, []byte("remember the milk"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	// `*` is a person's broadcast, refused to agents; a terminal is a person.
	err := agentsSend(context.Background(), []string{"--server", url, "--to", "*", "--notify", "--file", file, "stand", "up"}, &out)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if !strings.Contains(out.String(), "bot-s1: delivered") || !strings.Contains(out.String(), "bot-s2: delivered") {
		t.Fatalf("send output = %s", out.String())
	}
	msgs, _ := hub.CheckInbox(context.Background(), "", "bot-s1", "mcp:s1", 0)
	if len(msgs) != 1 || msgs[0].Body != "stand up" || !msgs[0].Notify || msgs[0].SenderAgent != "human" ||
		len(msgs[0].Files) != 1 || msgs[0].Files[0].Name != "notes.txt" {
		t.Fatalf("received = %+v", msgs)
	}

	if err := agentsSend(context.Background(), []string{"--server", url, "hello"}, &out); err != errUsage {
		t.Fatalf("send without --to = %v, want usage", err)
	}
	if err := agentsSend(context.Background(), []string{"--server", "http://192.0.2.1:5666", "--to", "x", "hi"}, &out); err == nil {
		t.Fatal("send to a remote server was allowed")
	}
}

func TestAgentsListenPrintsMailAsItArrives(t *testing.T) {
	saved := listenWait
	listenWait = 2 * time.Second
	t.Cleanup(func() { listenWait = saved })

	url, hub := agentsServer(t)
	if _, err := hub.Touch(agents.Heartbeat{SessionID: "s1", ActorID: "mcp:alice", Name: "api-claude"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var out, errOut syncBuffer
	done := make(chan error, 1)
	go func() { done <- agentsListen(ctx, []string{"--server", url, "--as", "api-claude"}, &out, &errOut) }()

	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(errOut.String(), "listening as") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := hub.Send(context.Background(), agents.SendInput{SenderActor: "bob", IsHuman: true, To: "api-claude", Body: "ping from bob",
		Files: []agents.FileInput{{Name: "a.txt", Content: "abc"}}}); err != nil {
		t.Fatal(err)
	}
	for !strings.Contains(out.String(), "ping from bob") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("listen did not stop when cancelled")
	}
	text := out.String()
	if !strings.Contains(text, "ping from bob") || !strings.Contains(text, "file: a.txt (3 bytes)") {
		t.Fatalf("listen output = %q (stderr %q)", text, errOut.String())
	}

	if err := agentsListen(context.Background(), []string{"--server", url, "--as", "nobody"}, &out, &errOut); err == nil {
		t.Fatal("listening as an unknown session succeeded")
	}
}

func TestAgentsExcludeEditsTheList(t *testing.T) {
	config := filepath.Join(t.TempDir(), "nodevas", "agent-network.json")
	saved := networkConfigPath
	networkConfigPath = func() (string, error) { return config, nil }
	t.Cleanup(func() { networkConfigPath = saved })

	private := t.TempDir()
	var out bytes.Buffer
	if err := agentsExclude([]string{"add", private}, &out); err != nil {
		t.Fatal(err)
	}
	if err := agentsExclude([]string{"add", private}, &out); err != nil || !strings.Contains(out.String(), "already excluded") {
		t.Fatalf("second add = %v, %s", err, out.String())
	}
	cfg, err := agentsetup.LoadNetworkConfig(config)
	if err != nil || len(cfg.ExcludeFolders) != 1 || cfg.Excluded(filepath.Join(private, "repo")) == "" {
		t.Fatalf("config = %+v, %v", cfg, err)
	}
	out.Reset()
	if err := agentsExclude([]string{"list"}, &out); err != nil || !strings.Contains(out.String(), filepath.Clean(private)) {
		t.Fatalf("list = %v, %s", err, out.String())
	}
	if err := agentsExclude([]string{"remove", private}, &out); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := agentsetup.LoadNetworkConfig(config); len(cfg.ExcludeFolders) != 0 {
		t.Fatalf("after remove = %+v", cfg)
	}
	if err := agentsExclude([]string{"frobnicate"}, &out); err != errUsage {
		t.Fatalf("unknown action = %v", err)
	}
}

func TestAgentsCmdUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := agentsCmd(nil, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "nodevas agents who") {
		t.Fatalf("no args = %d, %s", code, errOut.String())
	}
	if code := agentsCmd([]string{"send", "--bogus"}, &out, &errOut); code != 2 {
		t.Fatalf("bad flag = %d", code)
	}
}
