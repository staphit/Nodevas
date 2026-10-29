package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"nodevas/internal/runner/adapter"
	"nodevas/internal/runnerwire"
)

// The fake agent: this test binary, re-run with helperEnv set, standing in
// for an agent CLI. It records what it was given and prints the CLI's
// recorded output from the adapter fixtures.
const helperEnv = "NODEVAS_RUNNER_TEST_HELPER"

func TestHelperProcess(t *testing.T) {
	mode := os.Getenv(helperEnv)
	if mode == "" {
		return
	}
	record := os.Getenv("NODEVAS_TEST_RECORD")
	_ = os.MkdirAll(record, 0o700)
	put := func(name, value string) { _ = os.WriteFile(filepath.Join(record, name), []byte(value), 0o600) }
	wd, _ := os.Getwd()
	put("cwd", wd)
	// The adapter's arguments follow "--" (see fakeAgent.Command).
	if i := slices.Index(os.Args, "--"); i >= 0 {
		raw, _ := json.Marshal(os.Args[i+1:])
		put("args", string(raw))
	}
	if mode == "acp" {
		// An ACP agent: talks over stdin, so it cannot read it all first.
		acpHelper(put)
	}
	stdin, _ := io.ReadAll(os.Stdin)
	put("stdin", string(stdin))
	if path := os.Getenv("NODEVAS_TEST_MCP"); path != "" {
		mcp, _ := os.ReadFile(path)
		put("mcp.json", string(mcp))
		put("mcp-path", path)
	}
	put("pid", strconv.Itoa(os.Getpid()))

	fixture := func(name string) {
		raw, err := os.ReadFile(filepath.Join(os.Getenv("NODEVAS_TEST_FIXTURES"), name))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(9)
		}
		_, _ = os.Stdout.Write(raw)
	}
	edit := func() {
		for name, content := range map[string]string{"README": "hi\nmore\n", "hello.txt": "hello\n", "data.bin": "\x00\x01\x02bin"} {
			if err := os.WriteFile(filepath.Join(wd, name), []byte(content), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(9)
			}
		}
	}
	switch mode {
	case "ok":
		fixture("claude_stream.jsonl")
		os.Exit(0)
	case "edit":
		edit()
		fixture("claude_stream.jsonl")
		os.Exit(0)
	case "edit-fail":
		edit()
		fixture("claude_error.jsonl")
		os.Exit(3)
	case "fail":
		fixture("claude_error.jsonl")
		fmt.Fprintln(os.Stderr, "boom on stderr")
		os.Exit(3)
	case "codex-ok":
		fixture("codex_stream.jsonl")
		os.Exit(0)
	case "edit-quota", "quota":
		// Some work, a plan, a command that printed secrets, then Claude
		// Code's usage limit (handoff_test.go).
		if mode == "edit-quota" {
			edit()
		}
		line := func(v any) { raw, _ := json.Marshal(v); fmt.Println(string(raw)) }
		type m = map[string]any
		assistant := func(blocks ...m) {
			line(m{"type": "assistant", "message": m{"role": "assistant", "content": blocks}})
		}
		assistant(m{"type": "text", "text": "I added hello.txt; next I wire it into the README."})
		assistant(m{"type": "tool_use", "id": "t1", "name": "TodoWrite", "input": m{"todos": []m{
			{"content": "Add hello.txt", "status": "completed"},
			{"content": "Wire hello into the README", "status": "in_progress"},
			{"content": "Write the tests", "status": "pending"},
		}}})
		assistant(m{"type": "tool_use", "id": "t2", "name": "Bash", "input": m{"command": "env | grep TOKEN"}})
		line(m{"type": "user", "message": m{"role": "user", "content": []m{{"type": "tool_result", "tool_use_id": "t2",
			"content": "API_TOKEN=supersecretvalue\nGH=ghp_abcdefghijklmnopqrstuvwxyz0123"}}}})
		line(m{"type": "result", "subtype": "success", "is_error": true,
			"result": fmt.Sprintf("Claude AI usage limit reached|%d", time.Now().Add(2*time.Hour).Unix())})
		os.Exit(1)
	case "continue":
		// What the worktree starts from, then more work on top.
		out, _ := exec.Command("git", "-C", wd, "log", "--format=%s").Output()
		put("log", string(out))
		if err := os.WriteFile(filepath.Join(wd, "more.txt"), []byte("more\n"), 0o644); err != nil {
			os.Exit(9)
		}
		fixture("claude_stream.jsonl")
		os.Exit(0)
	case "repoconfig":
		// Which of the repository's own agent config the agent found, then
		// some of its own, written into the worktree.
		var found []string
		for _, path := range adapter.RepoConfigPaths() {
			if _, err := os.Lstat(filepath.Join(wd, filepath.FromSlash(path))); err == nil {
				found = append(found, path)
			}
		}
		put("found", strings.Join(found, ",")+".")
		if err := os.MkdirAll(filepath.Join(wd, ".codex"), 0o700); err != nil {
			os.Exit(9)
		}
		if err := os.WriteFile(filepath.Join(wd, ".codex", "config.toml"), []byte("x = 1\n"), 0o644); err != nil {
			os.Exit(9)
		}
		fixture("claude_stream.jsonl")
		os.Exit(0)
	case "codex-fail":
		fixture("codex_model_error.jsonl")
		fmt.Fprintln(os.Stderr, "ERROR rmcp::transport::worker: worker quit")
		os.Exit(1)
	case "login-fail":
		// Synthesized: Claude Code signed out.
		fmt.Println(`{"type":"result","subtype":"success","is_error":true,"result":"Invalid API key · Please run /login"}`)
		os.Exit(1)
	case "review-approve", "review-changes", "review-silent":
		// A reviewer that also scribbles in its worktree, which is thrown away.
		if err := os.WriteFile(filepath.Join(wd, "scratch.txt"), []byte("notes\n"), 0o644); err != nil {
			os.Exit(9)
		}
		text := "The change does what the node asks.\n\n**VERDICT: approve**"
		switch mode {
		case "review-changes":
			text = "Missing the empty case in hello.\nVERDICT: approve? no.\n\nVERDICT: changes"
		case "review-silent":
			text = "Looks fine to me."
		}
		raw, _ := json.Marshal(map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": text,
			"usage": map[string]int{"input_tokens": 10, "output_tokens": 5}})
		fmt.Println(string(raw))
		os.Exit(0)
	case "replan":
		// A planner with a long final message ending in its reconciliation:
		// the cap on the summary must not cut the block.
		text := strings.Repeat("I read the code and the goal's tasks. ", 200) +
			"\n\nRECONCILE:\n- keep t1\n- skip t3: the PDF export was dropped\n- add t9: Add the SSO button\nEND RECONCILE"
		raw, _ := json.Marshal(map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": text,
			"usage": map[string]int{"input_tokens": 10, "output_tokens": 5}})
		fmt.Println(string(raw))
		os.Exit(0)
	case "race":
		// Someone pushes to the PR branch while the agent works: the runner's
		// push must then be refused, not forced.
		_, rest, _ := strings.Cut(string(stdin), "race:")
		branch := strings.Fields(rest)[0]
		gitRun := func(args ...string) {
			cmd := exec.Command("git", append([]string{"-c", "user.name=o", "-c", "user.email=o@example.test"}, args...)...)
			cmd.Dir = wd
			if out, err := cmd.CombinedOutput(); err != nil {
				fmt.Fprintln(os.Stderr, string(out))
				os.Exit(9)
			}
		}
		_ = os.WriteFile(filepath.Join(wd, "race.txt"), []byte("theirs\n"), 0o644)
		gitRun("add", "race.txt")
		gitRun("commit", "-q", "-m", "someone else")
		gitRun("push", "-q", "origin", "HEAD:refs/heads/"+branch)
		gitRun("reset", "-q", "--hard", "HEAD~1")
		edit()
		fixture("claude_stream.jsonl")
		os.Exit(0)
	case "hang":
		fmt.Println(`{"type":"system","subtype":"init","session_id":"s-hang","model":"m"}`)
		// A grandchild holding our stdout, like node's children do: killing
		// only this process would leave it running and the pipe open.
		child := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
		child.Env = append(os.Environ(), helperEnv+"=sleep", "NODEVAS_TEST_RECORD="+filepath.Join(record, "child"),
			"NODEVAS_TEST_MCP=")
		child.Stdout = os.Stdout
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(8)
		}
		put("child-pid", strconv.Itoa(child.Process.Pid))
		time.Sleep(time.Hour)
	case "sleep":
		time.Sleep(time.Hour)
	}
	os.Exit(7)
}

// acpHelper replays testdata/opencode_acp_stream.jsonl as an ACP agent would:
// it reads each of the runner's requests before writing the response, writes
// its own requests and reads their answers, then reads stdin to its end (the
// runner closing it is how the agent learns to exit), records all it read and
// exits.
func acpHelper(put func(name, value string)) {
	raw, err := os.ReadFile(filepath.Join(os.Getenv("NODEVAS_TEST_FIXTURES"), "opencode_acp_stream.jsonl"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(9)
	}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64<<10), 1<<20)
	var got []string
	readOne := func() {
		if !in.Scan() {
			fmt.Fprintln(os.Stderr, "stdin closed early")
			os.Exit(6)
		}
		got = append(got, in.Text())
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal([]byte(line), &msg)
		isResponse := len(msg.ID) > 0 && msg.Method == ""
		if isResponse {
			readOne()
		}
		fmt.Println(line)
		if len(msg.ID) > 0 && msg.Method != "" {
			readOne()
		}
	}
	for in.Scan() {
		got = append(got, in.Text())
	}
	if err := in.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(6)
	}
	put("stdin", strings.Join(got, "\n"))
	os.Exit(0)
}

// fakeAgent wraps a real adapter: its command (arguments, MCP wiring, stdin
// prompt, workdir) and its parser, with the program swapped for the helper.
type fakeAgent struct {
	real             adapter.Adapter
	record, fixtures string
}

func (f fakeAgent) Kind() string                { return f.real.Kind() }
func (f fakeAgent) NewParser() adapter.Parser   { return f.real.NewParser() }
func (f fakeAgent) Classify(text string) string { return f.real.Classify(text) }

// Command is the real adapter's command run by the helper, which gets the
// real arguments after "--". The job's prompt picks the mode.
func (f fakeAgent) Command(job runnerwire.Job, env adapter.RunEnv) (*exec.Cmd, error) {
	cmd, err := f.real.Command(job, env)
	if err != nil {
		return nil, err
	}
	mode, _, _ := strings.Cut(job.Prompt, ":")
	cmd.Path = os.Args[0]
	cmd.Args = append([]string{os.Args[0], "-test.run=^TestHelperProcess$", "--"}, cmd.Args[1:]...)
	cmd.Err = nil // the made-up claude path did not resolve; the helper does
	cmd.Env = append(cmd.Env, helperEnv+"="+mode,
		"NODEVAS_TEST_RECORD="+filepath.Join(f.record, job.RunID),
		"NODEVAS_TEST_MCP="+filepath.Join(env.TempDir, "mcp.json"),
		"NODEVAS_TEST_FIXTURES="+f.fixtures)
	return cmd, nil
}

// fakeConversation is fakeAgent for an adapter that talks to its CLI (ACP).
type fakeConversation struct{ fakeAgent }

func (f fakeConversation) NewConversation(job runnerwire.Job, env adapter.RunEnv) adapter.Conversation {
	return f.real.(adapter.Conversational).NewConversation(job, env)
}

type acceptFunc func(context.Context, Offer) (bool, string)

func (f acceptFunc) Accept(ctx context.Context, o Offer) (bool, string) { return f(ctx, o) }

// jobHub is a hub that offers jobs and records what the runner says.
type jobHub struct {
	t      *testing.T
	hellos chan runnerwire.Envelope

	mu    sync.Mutex
	conn  *websocket.Conn
	ctx   context.Context
	byRun map[string][]runnerwire.Envelope
	all   []runnerwire.Envelope
}

func newJobHub(t *testing.T) (*jobHub, *httptest.Server) {
	h := &jobHub{t: t, hellos: make(chan runnerwire.Envelope, 64), byRun: map[string][]runnerwire.Envelope{}}
	srv := httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(srv.Close)
	return h, srv
}

func (h *jobHub) serve(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(runnerwire.MaxChangesMessageBytes)
	ctx := r.Context()
	for {
		var msg runnerwire.Envelope
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			return
		}
		switch msg.Type {
		case runnerwire.TypeHello:
			_ = wsjson.Write(ctx, conn, runnerwire.Envelope{Type: runnerwire.TypeWelcome,
				Welcome: &runnerwire.Welcome{RunnerID: "r1", Name: "pc", Owner: "me"}})
			h.mu.Lock()
			h.conn, h.ctx = conn, ctx
			h.mu.Unlock()
			h.hellos <- msg
		case runnerwire.TypePing:
			_ = wsjson.Write(ctx, conn, runnerwire.Envelope{Type: runnerwire.TypePong})
		default:
			h.mu.Lock()
			h.byRun[msg.RunID] = append(h.byRun[msg.RunID], msg)
			h.all = append(h.all, msg)
			h.mu.Unlock()
		}
	}
}

func (h *jobHub) send(msg runnerwire.Envelope) {
	h.t.Helper()
	h.mu.Lock()
	conn, ctx := h.conn, h.ctx
	h.mu.Unlock()
	if conn == nil {
		h.t.Fatal("no runner connected")
	}
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		h.t.Fatalf("send %s: %v", msg.Type, err)
	}
}

func (h *jobHub) offer(job runnerwire.Job) {
	h.t.Helper()
	h.send(runnerwire.Envelope{Type: runnerwire.TypeOffer, Job: &job})
}

// kick drops the current connection, as a hub restart would.
func (h *jobHub) kick() {
	h.mu.Lock()
	conn := h.conn
	h.conn = nil
	h.mu.Unlock()
	_ = conn.Close(websocket.StatusGoingAway, "restarting")
}

func (h *jobHub) messages(runID string) []runnerwire.Envelope {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]runnerwire.Envelope(nil), h.byRun[runID]...)
}

// wait returns the first message of that type for the run.
func (h *jobHub) wait(runID, typ string) runnerwire.Envelope {
	h.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, msg := range h.messages(runID) {
			if msg.Type == typ {
				return msg
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("run %s: no %s; got %+v", runID, typ, h.messages(runID))
	return runnerwire.Envelope{}
}

func (h *jobHub) hello() runnerwire.Envelope {
	h.t.Helper()
	select {
	case msg := <-h.hellos:
		return msg
	case <-time.After(10 * time.Second):
		h.t.Fatal("no hello")
	}
	return runnerwire.Envelope{}
}

type jobEnv struct {
	hub    *jobHub
	client *Client
	repo   string
	record string
}

func startJobRunner(t *testing.T, tweak func(*Client)) *jobEnv {
	t.Helper()
	repo := newTestRepo(t)
	fixtures, err := filepath.Abs(filepath.Join("adapter", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	hub, srv := newJobHub(t)
	env := &jobEnv{hub: hub, repo: repo, record: t.TempDir()}
	env.client = &Client{
		Config: &Config{
			Hub: srv.URL, Token: testToken,
			Repos:   []runnerwire.Repo{{ID: "r1", Name: "demo", Path: repo}},
			DataDir: t.TempDir(),
		},
		Commands: &fakeCommands{
			paths: map[string]string{"claude": "/bin/claude", "codex": "/bin/codex"},
			outputs: map[string]string{
				"/bin/claude --version": "2.1.299 (Claude Code)\n",
				"/bin/codex --version":  "codex 0.1\n",
			},
		},
		Adapter: func(kind string) adapter.Adapter {
			if real := adapter.For(kind); real != nil {
				return fakeAgent{real: real, record: env.record, fixtures: fixtures}
			}
			return nil
		},
		Accepter:      acceptFunc(func(context.Context, Offer) (bool, string) { return true, "" }),
		PingInterval:  time.Second,
		BackoffMin:    5 * time.Millisecond,
		BackoffMax:    20 * time.Millisecond,
		FlushInterval: 20 * time.Millisecond,
	}
	if tweak != nil {
		tweak(env.client)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- env.client.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Error("runner did not stop")
		}
	})
	return env
}

func testJob(runID, prompt string) runnerwire.Job {
	return runnerwire.Job{RunID: runID, Project: "demo", NodeID: "n-1", NodeTitle: "Fix it", Prompt: prompt,
		Agent: "claude", Model: "sonnet", RepoID: "r1", RequestedBy: "patrick"}
}

func (e *jobEnv) recorded(t *testing.T, runID, name string) string {
	t.Helper()
	path := filepath.Join(e.record, runID, name)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
			return string(raw)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("helper never recorded %s", path)
	return ""
}

// samePath compares two paths that may no longer exist (a run that changed
// nothing has its worktree removed): symlinks resolve as far as they exist.
func samePath(a, b string) bool {
	return strings.EqualFold(resolvePath(a), resolvePath(b))
}

func resolvePath(p string) string {
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(resolved)
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(resolvePath(parent), filepath.Base(p))
}

func TestRunSucceeds(t *testing.T) {
	env := startJobRunner(t, nil)
	hello := env.hub.hello()
	if hello.Hello == nil || !slices.Contains(hello.Hello.Capabilities, runnerwire.CapHeadless) ||
		hello.Hello.MaxConcurrent != 1 || len(hello.Active) != 0 {
		t.Fatalf("hello = %+v", hello)
	}

	env.hub.offer(testJob("run-ok", "ok: do the thing"))
	finish := env.hub.wait("run-ok", runnerwire.TypeFinish)
	msgs := env.hub.messages("run-ok")

	// accept, then events, then changes (none), then finish; nothing else.
	n := len(msgs)
	if msgs[0].Type != runnerwire.TypeAccept || msgs[n-2].Type != runnerwire.TypeChanges || msgs[n-1].Type != runnerwire.TypeFinish {
		t.Fatalf("message order: %+v", msgs)
	}
	if ch := msgs[n-2].Changes; ch == nil || ch.Commit != "" || len(ch.Files) != 0 || ch.Files == nil || ch.Patch != "" {
		t.Fatalf("changes of a run that changed nothing = %+v", ch)
	}
	accept := msgs[0].Accept
	if accept == nil || accept.RunID != "run-ok" || accept.Base != "main" || accept.Branch != "agent/n-1-run-ok" {
		t.Fatalf("accept = %+v", accept)
	}
	var events []runnerwire.RunEvent
	for _, msg := range msgs[1 : n-2] {
		if msg.Type != runnerwire.TypeEvents || len(msg.Events) == 0 || len(msg.Events) > runnerwire.MaxEventsPerMessage {
			t.Fatalf("unexpected message %+v", msg)
		}
		events = append(events, msg.Events...)
	}
	var types []string
	for i, ev := range events {
		if ev.Seq != int64(i+1) {
			t.Fatalf("event %d has seq %d", i, ev.Seq)
		}
		if _, err := time.Parse(time.RFC3339Nano, ev.At); err != nil {
			t.Fatalf("event at %q: %v", ev.At, err)
		}
		types = append(types, ev.Type)
	}
	for _, want := range []string{runnerwire.EventThinking, runnerwire.EventToolCall, runnerwire.EventToolResult,
		runnerwire.EventText, runnerwire.EventUsage} {
		if !slices.Contains(types, want) {
			t.Errorf("no %s event in %v", want, types)
		}
	}
	if !strings.Contains(events[0].Text, "worktree") || !strings.Contains(events[len(events)-2].Text, "exited with code 0") ||
		events[len(events)-1].Text != "no changes; worktree removed" {
		t.Errorf("first/last events: %+v / %+v", events[0], events[len(events)-2:])
	}

	result := finish.Result
	if result == nil || result.Status != runnerwire.RunSucceeded || result.ExitCode != 0 ||
		result.Summary != `Content: "hello fixture"` || result.Usage == nil || result.Usage.OutputTokens != 130 {
		t.Fatalf("result = %+v", result)
	}

	// The agent ran in the worktree, on its branch, with the prompt on stdin
	// and the MCP server pointed at the proxy.
	wtPath := filepath.Join(env.client.Config.DataDir, "worktrees", "run-ok")
	if cwd := env.recorded(t, "run-ok", "cwd"); !samePath(cwd, wtPath) {
		t.Errorf("agent ran in %s, want %s", cwd, wtPath)
	}
	// It changed nothing, so its worktree and branch are gone.
	if _, err := os.Stat(wtPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("worktree of a run with no changes left behind: %v", err)
	}
	if got := gitT(t, env.repo, "branch", "--list", "agent/*"); got != "" {
		t.Errorf("branches left behind: %q", got)
	}
	stdin := env.recorded(t, "run-ok", "stdin")
	if !strings.Contains(stdin, "ok: do the thing") || !strings.Contains(stdin, `claim_task with id "n-1"`) {
		t.Errorf("stdin = %q", stdin)
	}
	var mcp struct {
		MCPServers map[string]adapter.MCPServer `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(env.recorded(t, "run-ok", "mcp.json")), &mcp); err != nil {
		t.Fatal(err)
	}
	server := mcp.MCPServers["nodevas"]
	exe, _ := os.Executable()
	if server.Command != exe || len(server.Args) != 13 || server.Args[0] != "mcp" || server.Args[1] != "--server" {
		t.Fatalf("mcp server = %+v", server)
	}
	proxyURL := server.Args[2]
	if !strings.HasPrefix(proxyURL, "http://127.0.0.1:") || !strings.Contains(proxyURL, "/r/") {
		t.Errorf("proxy url %q", proxyURL)
	}
	if want := []string{"--project", "demo", "--agent-role", "worker", "--actor", "run:claude",
		"--session-name", "demo-claude-sonnet-run-ok", "--toolset", "run"}; !slices.Equal(server.Args[3:], want) {
		t.Errorf("mcp args = %q", server.Args)
	}
	// The run's MCP config and its door through the proxy are gone.
	if _, err := os.Stat(env.recorded(t, "run-ok", "mcp-path")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("mcp config left behind: %v", err)
	}
	resp, err := http.Get(proxyURL + "/api/auth/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("finished run's proxy secret answered %d", resp.StatusCode)
	}
}

func TestRunFails(t *testing.T) {
	env := startJobRunner(t, nil)
	env.hub.hello()
	env.hub.offer(testJob("run-fail", "fail:"))
	result := env.hub.wait("run-fail", runnerwire.TypeFinish).Result
	if result.Status != runnerwire.RunFailed || result.ExitCode != 3 ||
		result.Error != "Reached maximum number of turns (50)" || result.Usage.CostUSD != 1.25 ||
		result.FailureKind != runnerwire.FailureTask {
		t.Fatalf("result = %+v", result)
	}
}

// A Codex run through the runner: the real Codex adapter's command and
// parser, the helper printing a real codex --json recording.
func TestRunCodexSucceeds(t *testing.T) {
	env := startJobRunner(t, func(c *Client) { c.Config.CodexSandbox = "read-only" })
	env.hub.hello()
	job := testJob("run-codex", "codex-ok: add hello.txt")
	job.Agent, job.Model = "codex", "gpt-5.5"
	env.hub.offer(job)
	finish := env.hub.wait("run-codex", runnerwire.TypeFinish)

	var events []runnerwire.RunEvent
	for _, msg := range env.hub.messages("run-codex") {
		events = append(events, msg.Events...)
	}
	var types []string
	for _, ev := range events {
		types = append(types, ev.Type)
	}
	for _, want := range []string{runnerwire.EventToolCall, runnerwire.EventToolResult, runnerwire.EventText,
		runnerwire.EventUsage} {
		if !slices.Contains(types, want) {
			t.Errorf("no %s event in %v", want, types)
		}
	}
	started := slices.IndexFunc(events, func(ev runnerwire.RunEvent) bool { return strings.HasPrefix(ev.Text, "started codex") })
	if started < 0 || !strings.Contains(events[started].Text, "model gpt-5.5, sandbox read-only") {
		t.Errorf("no start line with the sandbox in %+v", events)
	}
	claim := slices.IndexFunc(events, func(ev runnerwire.RunEvent) bool { return ev.Tool == "mcp__nodevas__claim_task" })
	if claim < 0 {
		t.Errorf("no claim_task call in %v", types)
	}

	result := finish.Result
	if result == nil || result.Status != runnerwire.RunSucceeded || result.ExitCode != 0 ||
		!strings.HasPrefix(result.Summary, "Done. Added [hello.txt]") || result.Usage == nil ||
		result.Usage.OutputTokens != 1703 || result.Usage.CacheReadTokens != 155648 {
		t.Fatalf("result = %+v", result)
	}

	// The real codex command line: settings from the runner's config, the
	// model from the job, the MCP server through the proxy, prompt on stdin.
	var args []string
	if err := json.Unmarshal([]byte(env.recorded(t, "run-codex", "args")), &args); err != nil {
		t.Fatal(err)
	}
	wtPath := filepath.Join(env.client.Config.DataDir, "worktrees", "run-codex")
	if len(args) < 9 || !slices.Equal(args[:6], []string{"exec", "--json", "--color", "never", "-s", "read-only"}) ||
		args[6] != "-C" || !samePath(args[7], wtPath) || args[len(args)-1] != "-" {
		t.Fatalf("args = %q", args)
	}
	joined := strings.Join(args, " ")
	exe, _ := os.Executable()
	for _, want := range []string{"-m gpt-5.5", "-c approval_policy='never'",
		"-c mcp_servers.nodevas.command='" + exe + "'",
		"'--actor','run:codex','--session-name','demo-codex-gpt-5-5-run-co'",
		"-c mcp_servers.nodevas.default_tools_approval_mode='approve'"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "--ignore-user-config") {
		t.Errorf("ignored the user config unasked: %s", joined)
	}
	if cwd := env.recorded(t, "run-codex", "cwd"); !samePath(cwd, wtPath) {
		t.Errorf("agent ran in %s, want %s", cwd, wtPath)
	}
	if stdin := env.recorded(t, "run-codex", "stdin"); !strings.Contains(stdin, "codex-ok: add hello.txt") {
		t.Errorf("stdin = %q", stdin)
	}
}

func TestRunCodexFails(t *testing.T) {
	env := startJobRunner(t, nil)
	env.hub.hello()
	job := testJob("run-codex-fail", "codex-fail:")
	job.Agent, job.Model = "codex", "default"
	env.hub.offer(job)
	result := env.hub.wait("run-codex-fail", runnerwire.TypeFinish).Result
	if result.Status != runnerwire.RunFailed || result.ExitCode != 1 || result.Usage != nil ||
		result.Error != "The 'gpt-6-astra' model is not supported when using Codex with a ChatGPT account. (HTTP 400)" ||
		result.FailureKind != runnerwire.FailureModel {
		t.Fatalf("result = %+v", result)
	}
	var args []string
	if err := json.Unmarshal([]byte(env.recorded(t, "run-codex-fail", "args")), &args); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(args, "-m") || !slices.Contains(args, "workspace-write") {
		t.Errorf("default model / default sandbox: args = %q", args)
	}
}

// An OpenCode run over ACP (opencodeProtocol acp): the real adapter's command
// and conversation, the helper replaying a recorded `opencode acp` session.
func TestRunOpenCodeACP(t *testing.T) {
	env := startJobRunner(t, func(c *Client) {
		if err := c.Config.Set("opencodeProtocol", []string{"acp"}); err != nil {
			t.Fatal(err)
		}
		c.Commands.(*fakeCommands).paths["opencode"] = "/bin/opencode"
		c.Commands.(*fakeCommands).outputs["/bin/opencode --version"] = "1.18.23\n"
		fake := c.Adapter
		c.Adapter = func(kind string) adapter.Adapter {
			// What adapterFor picks for this setting, swapped for the helper.
			real := (&Client{Config: c.Config}).adapterFor(kind)
			if _, ok := real.(adapter.OpenCodeACP); !ok {
				t.Errorf("opencodeProtocol acp: adapter for %s = %T", kind, real)
			}
			agent := fake(kind).(fakeAgent)
			agent.real = real
			return fakeConversation{agent}
		}
	})
	env.hub.hello()
	job := testJob("run-acp", "acp: read the readme")
	job.Agent, job.Model = "opencode", "default"
	env.hub.offer(job)
	finish := env.hub.wait("run-acp", runnerwire.TypeFinish)

	var events []runnerwire.RunEvent
	for _, msg := range env.hub.messages("run-acp") {
		events = append(events, msg.Events...)
	}
	refused := slices.IndexFunc(events, func(ev runnerwire.RunEvent) bool {
		return strings.Contains(ev.Text, "asked to run git status; the runner refused")
	})
	read := slices.IndexFunc(events, func(ev runnerwire.RunEvent) bool { return ev.Type == runnerwire.EventToolCall && ev.Tool == "read" })
	if refused < 0 || read < 0 {
		t.Errorf("events = %+v", events)
	}
	result := finish.Result
	if result == nil || result.Status != runnerwire.RunSucceeded || result.ExitCode != 0 || result.Usage == nil ||
		result.Usage.CacheReadTokens != 12416 {
		t.Fatalf("result = %+v", result)
	}

	var args []string
	if err := json.Unmarshal([]byte(env.recorded(t, "run-acp", "args")), &args); err != nil {
		t.Fatal(err)
	}
	wtPath := filepath.Join(env.client.Config.DataDir, "worktrees", "run-acp")
	if len(args) != 3 || args[0] != "acp" || args[1] != "--cwd" || !samePath(args[2], wtPath) {
		t.Fatalf("args = %q", args)
	}
	// What the agent read: the handshake, the prompt, the refusal; then the
	// runner closed stdin, or the helper would still be reading.
	stdin := env.recorded(t, "run-acp", "stdin")
	for _, want := range []string{`"method":"initialize"`, `"method":"session/new"`, `"method":"session/prompt"`,
		`acp: read the readme`, `"optionId":"reject"`} {
		if !strings.Contains(stdin, want) {
			t.Errorf("stdin lacks %s:\n%s", want, stdin)
		}
	}
}

// OpenCode 2 never runs over ACP; 1.x and other agents keep their adapter.
func TestOpenCode2SkipsACP(t *testing.T) {
	if ad, note := withoutOpenCode2ACP(adapter.OpenCodeACP{}, "opencode v2.1.4"); ad != (adapter.OpenCode{}) || note == "" {
		t.Errorf("2.x over ACP = %T, %q", ad, note)
	}
	for _, c := range []struct {
		ad      adapter.Adapter
		version string
	}{{adapter.OpenCodeACP{}, "1.18.23"}, {adapter.OpenCodeACP{}, ""}, {adapter.OpenCode{}, "2.1.4"}, {adapter.For("claude"), "2.1.4"}} {
		if ad, note := withoutOpenCode2ACP(c.ad, c.version); ad != c.ad || note != "" {
			t.Errorf("%T %q = %T, %q", c.ad, c.version, ad, note)
		}
	}
}

func TestRunWorktreeFailure(t *testing.T) {
	env := startJobRunner(t, nil)
	env.hub.hello()
	job := testJob("run-nobase", "ok:")
	job.Base = "no-such-branch"
	env.hub.offer(job)
	result := env.hub.wait("run-nobase", runnerwire.TypeFinish).Result
	if result.Status != runnerwire.RunFailed || !strings.Contains(result.Error, "could not create the worktree") ||
		!strings.Contains(result.Error, "no-such-branch") {
		t.Fatalf("result = %+v", result)
	}
	for _, msg := range env.hub.messages("run-nobase") {
		if msg.Type == runnerwire.TypeAccept {
			t.Fatal("accepted a run with no worktree")
		}
	}
}

func TestRunDeclines(t *testing.T) {
	env := startJobRunner(t, func(c *Client) {
		c.Accepter = acceptFunc(func(_ context.Context, o Offer) (bool, string) {
			if o.Job.RunID == "d-no" {
				return false, ReasonDeclined
			}
			return true, ""
		})
	})
	env.hub.hello()
	cases := map[string]struct {
		mutate func(*runnerwire.Job)
		reason string
	}{
		"d-repo":   {func(j *runnerwire.Job) { j.RepoID = "elsewhere" }, "not on this runner's allowlist"},
		"d-aider":  {func(j *runnerwire.Job) { j.Agent = "aider" }, `agent "aider" not supported yet`},
		"d-gemini": {func(j *runnerwire.Job) { j.Agent = "gemini" }, `agent "gemini" is not installed on this runner`},
		// codex is supported and installed, but sonnet is not one of its models.
		"d-codex": {func(j *runnerwire.Job) { j.Agent = "codex" }, `model "sonnet" is not one this runner offers for codex`},
		"d-model": {func(j *runnerwire.Job) { j.Model = "gpt-9" }, `model "gpt-9" is not one this runner offers`},
		"d-base":  {func(j *runnerwire.Job) { j.Base = "--upload-pack=x" }, "not a plain branch name"},
		"d-id/..": {func(*runnerwire.Job) {}, "run id is not usable"},
		"d-no":    {func(*runnerwire.Job) {}, ReasonDeclined},
	}
	for id, c := range cases {
		job := testJob(id, "ok:")
		c.mutate(&job)
		env.hub.offer(job)
	}
	for id, c := range cases {
		msg := env.hub.wait(id, runnerwire.TypeDecline)
		if !strings.Contains(msg.Reason, c.reason) {
			t.Errorf("%s declined with %q, want %q", id, msg.Reason, c.reason)
		}
		if msgs := env.hub.messages(id); len(msgs) != 1 {
			t.Errorf("%s: %+v", id, msgs)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(env.client.Config.DataDir, "worktrees")); len(entries) != 0 {
		t.Errorf("declined runs made worktrees: %v", entries)
	}
}

func TestRunNoAccepterNoTerminal(t *testing.T) {
	env := startJobRunner(t, func(c *Client) { c.Accepter = &TerminalAccepter{In: strings.NewReader(""), Out: io.Discard} })
	env.hub.hello()
	env.hub.offer(testJob("run-x", "ok:"))
	if msg := env.hub.wait("run-x", runnerwire.TypeDecline); msg.Reason != ReasonNoTerminal {
		t.Fatalf("reason %q", msg.Reason)
	}
}

func TestRunAutoAccept(t *testing.T) {
	policy := runnerwire.AutoAcceptPolicy{Enabled: true, RequireStepUp: true,
		Repos: []string{"r1"}, Agents: []string{"claude"}}
	env := startJobRunner(t, func(c *Client) {
		c.Config.AutoAccept = &policy
		c.Accepter = acceptFunc(func(context.Context, Offer) (bool, string) {
			t.Error("asked despite auto-accept covering the offer")
			return false, "asked"
		})
	})
	hello := env.hub.hello()
	if got := hello.Hello.AutoAccept; got == nil || !reflect.DeepEqual(*got, policy) {
		t.Fatalf("hello autoAccept = %+v, want %+v", got, policy)
	}
	job := testJob("run-auto", "ok:")
	job.SteppedUp = true
	env.hub.offer(job)
	if r := env.hub.wait("run-auto", runnerwire.TypeFinish).Result; r.Status != runnerwire.RunSucceeded {
		t.Fatalf("result = %+v", r)
	}
}

func TestRunAutoAcceptNeedsStepUp(t *testing.T) {
	env := startJobRunner(t, func(c *Client) {
		c.Config.AutoAccept = &runnerwire.AutoAcceptPolicy{Enabled: true, RequireStepUp: true,
			Repos: []string{"r1"}, Agents: []string{"claude"}}
		c.Accepter = &TerminalAccepter{In: strings.NewReader(""), Out: io.Discard}
	})
	env.hub.hello()
	env.hub.offer(testJob("run-nostep", "ok:"))
	msg := env.hub.wait("run-nostep", runnerwire.TypeDecline)
	if !strings.HasPrefix(msg.Reason, ReasonNeedsStepUp) || !strings.Contains(msg.Reason, "nobody is at the runner's terminal") {
		t.Fatalf("reason %q", msg.Reason)
	}
}

// waitDead waits for the helper and its grandchild to be gone.
func waitDead(t *testing.T, env *jobEnv, runID string) {
	t.Helper()
	for _, name := range []string{"pid", "child-pid"} {
		pid, err := strconv.Atoi(env.recorded(t, runID, name))
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(10 * time.Second)
		for processAlive(pid) && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if processAlive(pid) {
			t.Errorf("%s %d still running after the run ended", name, pid)
		}
	}
}

func TestRunCancelKillsTree(t *testing.T) {
	env := startJobRunner(t, nil)
	env.hub.hello()
	env.hub.offer(testJob("run-cancel", "hang:"))
	env.hub.wait("run-cancel", runnerwire.TypeAccept)
	env.recorded(t, "run-cancel", "child-pid")
	start := time.Now()
	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeCancel, RunID: "run-cancel", Reason: "changed my mind"})
	result := env.hub.wait("run-cancel", runnerwire.TypeFinish).Result
	if result.Status != runnerwire.RunCancelled || result.Error != "changed my mind" {
		t.Fatalf("result = %+v", result)
	}
	if took := time.Since(start); took > waitDelay {
		t.Errorf("cancel took %s: the tree was not killed, Wait waited out the pipe", took)
	}
	waitDead(t, env, "run-cancel")
}

func TestRunTimeout(t *testing.T) {
	env := startJobRunner(t, func(c *Client) { c.RunTimeout = 500 * time.Millisecond })
	env.hub.hello()
	env.hub.offer(testJob("run-slow", "hang:"))
	result := env.hub.wait("run-slow", runnerwire.TypeFinish).Result
	if result.Status != runnerwire.RunFailed || result.Error != "timed out after 500ms" {
		t.Fatalf("result = %+v", result)
	}
	waitDead(t, env, "run-slow")
}

func TestRunDedupeBusyAndReconnect(t *testing.T) {
	release := make(chan struct{})
	var asked atomic.Int32
	env := startJobRunner(t, func(c *Client) {
		c.Accepter = acceptFunc(func(ctx context.Context, o Offer) (bool, string) {
			asked.Add(1)
			select {
			case <-release:
				return true, ""
			case <-ctx.Done():
				return false, ReasonCancelled
			}
		})
	})
	if hello := env.hub.hello(); len(hello.Active) != 0 {
		t.Fatalf("first hello active = %v", hello.Active)
	}
	job := testJob("run-a", "hang:")
	env.hub.offer(job)
	env.hub.offer(job) // while it is being asked about
	time.Sleep(100 * time.Millisecond)
	// Being asked about counts against the limit of one.
	env.hub.offer(testJob("run-b", "ok:"))
	if msg := env.hub.wait("run-b", runnerwire.TypeDecline); !strings.Contains(msg.Reason, "busy") {
		t.Fatalf("run-b declined with %q", msg.Reason)
	}
	close(release)
	env.hub.wait("run-a", runnerwire.TypeAccept)
	env.recorded(t, "run-a", "child-pid")
	env.hub.offer(job) // while it runs

	// The hub restarts: the runner comes back and says run-a is still going.
	env.hub.kick()
	if hello := env.hub.hello(); !slices.Equal(hello.Active, []string{"run-a"}) {
		t.Fatalf("hello after reconnect: active = %v", hello.Active)
	}
	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeCancel, RunID: "run-a", Reason: "stop"})
	if r := env.hub.wait("run-a", runnerwire.TypeFinish).Result; r.Status != runnerwire.RunCancelled {
		t.Fatalf("result = %+v", r)
	}
	env.hub.offer(job) // after it finished: declined, never asked about again
	if msg := env.hub.wait("run-a", runnerwire.TypeDecline); !strings.Contains(msg.Reason, "already") {
		t.Fatalf("re-offer after finish declined with %q", msg.Reason)
	}

	if n := asked.Load(); n != 1 {
		t.Errorf("asked %d times about run-a", n)
	}
	counts := map[string]int{}
	var seqs []int64
	for _, msg := range env.hub.messages("run-a") {
		counts[msg.Type]++
		for _, ev := range msg.Events {
			seqs = append(seqs, ev.Seq)
		}
	}
	if counts[runnerwire.TypeAccept] != 1 || counts[runnerwire.TypeFinish] != 1 || counts[runnerwire.TypeDecline] != 1 {
		t.Errorf("run-a messages: %v", counts)
	}
	// Across the reconnect, seqs only go up.
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Errorf("seq went from %d to %d", seqs[i-1], seqs[i])
		}
	}
	if hello := env.hub.messages(""); len(hello) != 0 {
		t.Errorf("messages without a run id: %+v", hello)
	}
	waitDead(t, env, "run-a")
}

func TestJobsBufferWhileDisconnected(t *testing.T) {
	j := newJobs(context.Background(), &Client{Config: &Config{}})
	r := &run{job: runnerwire.Job{RunID: "x"}, accepted: true, accept: &runnerwire.Accept{RunID: "x"}}
	j.runs["x"], j.order = r, []string{"x"}
	for i := 0; i < 2500; i++ {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventText, Text: "t"})
	}
	if len(r.events) > maxBufferedEvents || r.dropped < 500 {
		t.Fatalf("buffered %d, dropped %d", len(r.events), r.dropped)
	}
	// Big events still fit a message.
	for i := 0; i < 100; i++ {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventToolCall, Text: strings.Repeat("\"", runnerwire.MaxEventText+10),
			Input: strings.Repeat("<", runnerwire.MaxEventText)})
	}
	j.finish(r, runnerwire.RunResult{Status: runnerwire.RunSucceeded})
	if got := j.active(); !slices.Equal(got, []string{"x"}) {
		t.Fatalf("active with an unsent finish = %v", got)
	}

	var sent []runnerwire.Envelope
	writes := 0
	collect := func(msg runnerwire.Envelope) error {
		writes++
		if writes == 3 {
			return errors.New("socket down")
		}
		raw, _ := json.Marshal(msg)
		if len(raw) > runnerwire.MaxMessageBytes {
			t.Fatalf("message of %d bytes", len(raw))
		}
		sent = append(sent, msg)
		return nil
	}
	if err := j.flush(collect); err == nil {
		t.Fatal("flush hid a write error")
	}
	if err := j.flush(collect); err != nil {
		t.Fatal(err)
	}
	if sent[0].Type != runnerwire.TypeAccept || sent[len(sent)-1].Type != runnerwire.TypeFinish {
		t.Fatalf("first %s, last %s", sent[0].Type, sent[len(sent)-1].Type)
	}
	last := int64(0)
	for _, msg := range sent[1 : len(sent)-1] {
		if msg.Type != runnerwire.TypeEvents || len(msg.Events) > runnerwire.MaxEventsPerMessage {
			t.Fatalf("message %s with %d events", msg.Type, len(msg.Events))
		}
		for _, ev := range msg.Events {
			if ev.Seq <= last {
				t.Fatalf("seq %d after %d", ev.Seq, last)
			}
			last = ev.Seq
			if len(ev.Text) > runnerwire.MaxEventText {
				t.Fatalf("event text %d bytes", len(ev.Text))
			}
		}
	}
	if last != 2600 {
		t.Fatalf("last seq %d", last)
	}
	if len(j.active()) != 0 || len(j.runs) != 0 || !j.seen["x"] {
		t.Fatalf("run not forgotten after its finish went out: %v %v", j.active(), j.runs)
	}
}

func TestLineWriter(t *testing.T) {
	var lines []string
	w := &lineWriter{fn: func(line []byte) { lines = append(lines, string(line)) }}
	for _, chunk := range []string{"a", "b\nc", "\n\n", "d\r\ne"} {
		_, _ = w.Write([]byte(chunk))
	}
	w.flush()
	if want := []string{"ab", "c", "d\r", "e"}; !slices.Equal(lines, want) {
		t.Fatalf("lines = %q, want %q", lines, want)
	}

	tail := &tailBuffer{max: 8}
	_, _ = tail.Write([]byte("0123456789"))
	_, _ = tail.Write([]byte("ab "))
	if got := tail.String(); got != "56789ab" {
		t.Fatalf("tail = %q", got)
	}
}

// A run's MCP sidecar serves the run toolset, as a worker: tool choice and
// write permission are separate flags.
func TestMCPArgsServeTheRunToolset(t *testing.T) {
	job := runnerwire.Job{RunID: "run-abcdef12", Agent: "claude", Model: "sonnet", Project: "demo"}
	got := mcpArgs("http://127.0.0.1:5/r/s", job, runnerwire.Repo{Name: "web"})
	want := []string{"mcp", "--server", "http://127.0.0.1:5/r/s", "--project", "demo", "--agent-role", "worker",
		"--actor", "run:claude", "--session-name", "web-claude-sonnet-run-ab", "--toolset", "run"}
	if !slices.Equal(got, want) {
		t.Fatalf("mcpArgs = %q, want %q", got, want)
	}
}

// permissionRelay reaches the adapter: the Claude run asks the hub's
// approve_tool, and its start line says so. Off (TestRunSucceeds' runner),
// the arguments are today's.
func TestRunRelaysPermissions(t *testing.T) {
	env := startJobRunner(t, func(c *Client) { c.Config.PermissionRelay = true })
	env.hub.hello()
	env.hub.offer(testJob("run-relay", "ok: do the thing"))
	env.hub.wait("run-relay", runnerwire.TypeFinish)
	var args []string
	if err := json.Unmarshal([]byte(env.recorded(t, "run-relay", "args")), &args); err != nil {
		t.Fatal(err)
	}
	if i := slices.Index(args, "--permission-prompt-tool"); i < 0 || args[i+1] != adapter.ClaudeApproveTool {
		t.Errorf("args = %q", args)
	}
	var events []runnerwire.RunEvent
	for _, msg := range env.hub.messages("run-relay") {
		events = append(events, msg.Events...)
	}
	started := slices.IndexFunc(events, func(ev runnerwire.RunEvent) bool { return strings.HasPrefix(ev.Text, "started claude") })
	if started < 0 || !strings.Contains(events[started].Text, "permission prompts relayed to Nodevas") {
		t.Errorf("no start line with the relay in %+v", events)
	}

	off := startJobRunner(t, nil)
	off.hub.hello()
	off.hub.offer(testJob("run-norelay", "ok: do the thing"))
	off.hub.wait("run-norelay", runnerwire.TypeFinish)
	if raw := off.recorded(t, "run-norelay", "args"); strings.Contains(raw, "--permission-prompt-tool") {
		t.Errorf("relay off: args = %s", raw)
	}
}
