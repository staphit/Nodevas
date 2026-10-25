package adapter

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

// recordingStdin is the agent's stdin in a test: what the runner wrote, and
// whether it closed it.
type recordingStdin struct {
	mu     sync.Mutex
	buf    strings.Builder
	closed chan struct{}
}

func newRecordingStdin() *recordingStdin { return &recordingStdin{closed: make(chan struct{})} }

func (r *recordingStdin) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Write(p)
}

func (r *recordingStdin) Close() error { close(r.closed); return nil }

// messages waits for stdin to close, then returns what was written to it.
func (r *recordingStdin) messages(t *testing.T) []map[string]any {
	t.Helper()
	select {
	case <-r.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the runner never closed the agent's stdin")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(r.buf.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("stdin line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// converse plays a recorded ACP session against a conversation.
func converse(t *testing.T, conv Conversation, name string) ([]runnerwire.RunEvent, Outcome, []map[string]any) {
	t.Helper()
	stdin := newRecordingStdin()
	conv.Begin(stdin)
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var events []runnerwire.RunEvent
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		events = append(events, conv.Parse(scanner.Bytes())...)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return events, conv.Outcome(), stdin.messages(t)
}

func testACPSession() Conversation {
	return OpenCodeACP{}.NewConversation(runnerwire.Job{RunID: "run-1", NodeID: "n-42", Prompt: "Read README.md"},
		RunEnv{Workdir: `C:\work\repo`, Branch: "agent/n-42"})
}

// opencode_acp_stream.jsonl is a real `opencode acp` session (opencode
// 1.18.23, paths sanitised): the model reads a file, then asks to run git
// status, which the runner refuses, and the turn ends.
func TestACPSessionStream(t *testing.T) {
	events, outcome, sent := converse(t, testACPSession(), "opencode_acp_stream.jsonl")
	const (
		log    = runnerwire.EventLog
		call   = runnerwire.EventToolCall
		result = runnerwire.EventToolResult
		usage  = runnerwire.EventUsage
	)
	want := []string{log, log, call, result, call, log, result, usage}
	if got := eventTypes(events); !slices.Equal(got, want) {
		t.Fatalf("event types = %v\nwant %v\n%+v", got, want, events)
	}
	if events[0].Text != "ACP: opencode 1.18.23, protocol version 1" ||
		events[1].Text != "opencode session started ses_e8e098febffe77arUVHDyg67YN" {
		t.Errorf("start = %q, %q", events[0].Text, events[1].Text)
	}
	// The call shows once its input is known, under the tool's name.
	if c := events[2]; c.Tool != "read" || c.ToolID != "call_00_87d1oa4mii34m69iwdzk840v" ||
		c.Input != `{"filePath":"C:\\work\\repo\\README.md"}` {
		t.Errorf("read call = %+v", c)
	}
	if r := events[3]; r.ToolID != events[2].ToolID || r.IsError || r.Text != "hello" {
		t.Errorf("read result = %+v", r)
	}
	if c := events[4]; c.Tool != "bash" || !strings.Contains(c.Input, `"command":"git status"`) {
		t.Errorf("bash call = %+v", c)
	}
	if !strings.Contains(events[5].Text, "asked to run git status; the runner refused") {
		t.Errorf("permission log = %q", events[5].Text)
	}
	if r := events[6]; r.ToolID != events[4].ToolID || !r.IsError || !strings.Contains(r.Text, "rejected permission") {
		t.Errorf("bash result = %+v", r)
	}
	if u := events[7].Usage; u == nil || u.InputTokens != 204 || u.OutputTokens != 38 || u.CacheReadTokens != 12416 {
		t.Errorf("usage = %+v", events[7].Usage)
	}
	if !outcome.Done || outcome.IsError || outcome.Usage == nil {
		t.Errorf("outcome = %+v", outcome)
	}

	// initialize, session/new, session/prompt, then the answer to the
	// permission request, then stdin closed.
	if len(sent) != 4 {
		t.Fatalf("sent %d messages: %v", len(sent), sent)
	}
	for i, method := range []string{"initialize", "session/new", "session/prompt"} {
		if sent[i]["method"] != method || sent[i]["id"] != float64(i) {
			t.Errorf("message %d = %v, want %s", i, sent[i], method)
		}
	}
	initParams := sent[0]["params"].(map[string]any)
	caps := initParams["clientCapabilities"].(map[string]any)
	if initParams["protocolVersion"] != float64(1) || caps["terminal"] != false ||
		caps["fs"].(map[string]any)["writeTextFile"] != false {
		t.Errorf("initialize = %v", initParams)
	}
	newParams := sent[1]["params"].(map[string]any)
	if newParams["cwd"] != `C:\work\repo` || len(newParams["mcpServers"].([]any)) != 0 {
		t.Errorf("session/new = %v", newParams)
	}
	promptParams := sent[2]["params"].(map[string]any)
	prompt := promptParams["prompt"].([]any)[0].(map[string]any)
	if promptParams["sessionId"] != "ses_e8e098febffe77arUVHDyg67YN" || prompt["type"] != "text" ||
		!strings.Contains(prompt["text"].(string), `claim_task with id "n-42"`) ||
		!strings.Contains(prompt["text"].(string), "Read README.md") {
		t.Errorf("session/prompt = %v", promptParams)
	}
	answer := sent[3]
	outcomeMap := answer["result"].(map[string]any)["outcome"].(map[string]any)
	if answer["id"] != float64(0) || outcomeMap["outcome"] != "selected" || outcomeMap["optionId"] != "reject" {
		t.Errorf("permission answer = %v", answer)
	}
}

// A turn that streams its answer a chunk at a time comes out as one text
// event, which is the run's summary.
func TestACPSessionChunks(t *testing.T) {
	events, outcome, _ := converse(t, testACPSession(), "opencode_acp_text.jsonl")
	var texts []runnerwire.RunEvent
	for _, ev := range events {
		if ev.Type == runnerwire.EventText {
			texts = append(texts, ev)
		}
	}
	if len(texts) != 1 || !strings.HasPrefix(texts[0].Text, "<tool_call name=\"functions.bash\">") ||
		!strings.HasSuffix(texts[0].Text, "</tool_call>") {
		t.Fatalf("text events = %+v", texts)
	}
	if outcome.Summary != texts[0].Text || outcome.IsError || !outcome.Done {
		t.Errorf("outcome = %+v", outcome)
	}
	// The thought tokens are billed output.
	if u := outcome.Usage; u == nil || u.InputTokens != 11364 || u.OutputTokens != 32+322 || u.CacheReadTokens != 73 {
		t.Errorf("usage = %+v", outcome.Usage)
	}
}

// opencode_acp_error.jsonl: the session's model is on a provider that cannot
// be reached. The prompt's error ends the run, classified as the model's.
func TestACPSessionError(t *testing.T) {
	events, outcome, sent := converse(t, testACPSession(), "opencode_acp_error.jsonl")
	last := events[len(events)-1]
	const want = "APIError: Internal error: Cannot connect to API: Unable to connect. Is the computer able to access the url?"
	if last.Type != runnerwire.EventError || last.Text != want {
		t.Errorf("last event = %+v", last)
	}
	if !outcome.IsError || outcome.Error != want || !outcome.Done {
		t.Errorf("outcome = %+v", outcome)
	}
	if got := (OpenCodeACP{}).Classify(outcome.Error); got != runnerwire.FailureModel {
		t.Errorf("classified %q", got)
	}
	if len(sent) != 3 {
		t.Errorf("sent %v", sent)
	}
}

// The runner offers no file system or terminal: such a request gets a
// method-not-found error, not a hang.
func TestACPSessionRefusesClientMethods(t *testing.T) {
	conv := testACPSession()
	stdin := newRecordingStdin()
	conv.Begin(stdin)
	conv.Parse([]byte(`{"jsonrpc":"2.0","id":0,"result":{"protocolVersion":1}}`))
	conv.Parse([]byte(`{"jsonrpc":"2.0","id":7,"method":"fs/write_text_file","params":{"path":"/etc/passwd","content":"x"}}`))
	conv.Parse([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"no"}}`))
	sent := stdin.messages(t)
	if len(sent) != 3 {
		t.Fatalf("sent %v", sent)
	}
	refusal := sent[2]
	if refusal["id"] != float64(7) || refusal["error"].(map[string]any)["code"] != float64(rpcMethodNotFound) {
		t.Errorf("refusal = %v", refusal)
	}
}

// A permission request without a one-off rejection is cancelled: nothing
// that grants the call is ever picked.
func TestACPSessionPermissionWithoutReject(t *testing.T) {
	conv := testACPSession()
	stdin := newRecordingStdin()
	conv.Begin(stdin)
	conv.Parse([]byte(`{"jsonrpc":"2.0","id":3,"method":"session/request_permission","params":{"toolCall":{"toolCallId":"c1","title":"rm -rf /"},` +
		`"options":[{"optionId":"yes","kind":"allow_always"},{"optionId":"no","kind":"reject_always"}]}}`))
	conv.Parse([]byte(`{"jsonrpc":"2.0","id":0,"error":{"code":1,"message":"stop"}}`))
	sent := stdin.messages(t)
	got := sent[1]["result"].(map[string]any)["outcome"].(map[string]any)
	if got["outcome"] != "cancelled" || got["optionId"] != nil {
		t.Errorf("answer = %v", sent[1])
	}
}

// An agent on another protocol version is not spoken to further.
func TestACPSessionVersionMismatch(t *testing.T) {
	conv := testACPSession()
	stdin := newRecordingStdin()
	conv.Begin(stdin)
	events := conv.Parse([]byte(`{"jsonrpc":"2.0","id":0,"result":{"protocolVersion":2}}`))
	if len(events) != 1 || events[0].Type != runnerwire.EventError || !strings.Contains(events[0].Text, "version 2") {
		t.Errorf("events = %+v", events)
	}
	if sent := stdin.messages(t); len(sent) != 1 {
		t.Errorf("sent %v", sent)
	}
	if out := conv.Outcome(); !out.IsError {
		t.Errorf("outcome = %+v", out)
	}
}

// The agent exiting mid-turn is a failure even with exit code 0.
func TestACPSessionEndsEarly(t *testing.T) {
	conv := testACPSession()
	conv.Begin(nopWriteCloser{io.Discard})
	conv.Parse([]byte(`{"jsonrpc":"2.0","id":0,"result":{"protocolVersion":1}}`))
	if out := conv.Outcome(); !out.IsError || out.Done || !strings.Contains(out.Error, "ended before") {
		t.Errorf("outcome = %+v", out)
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func TestOpenCodeACPCommand(t *testing.T) {
	job := runnerwire.Job{RunID: "run-1", NodeID: "n-42", Prompt: "x", Model: "opencode/big-pickle"}
	env := RunEnv{
		Binary:         "/usr/bin/opencode",
		Workdir:        "/data/worktrees/run-1",
		MCP:            MCPServer{Command: "/opt/nodevas", Args: []string{"mcp"}},
		PermissionMode: "read-only",
	}
	cmd, err := OpenCodeACP{}.Command(job, env)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/usr/bin/opencode", "acp", "--cwd", env.Workdir}; !slices.Equal(cmd.Args, want) {
		t.Fatalf("args = %q", cmd.Args)
	}
	if cmd.Stdin != nil || cmd.Dir != env.Workdir {
		t.Errorf("stdin = %v, dir = %q", cmd.Stdin, cmd.Dir)
	}
	if pwd, _ := envValue(cmd.Env, "PWD"); pwd != env.Workdir {
		t.Errorf("PWD = %q", pwd)
	}
	raw, _ := envValue(cmd.Env, openCodeConfigEnv)
	var config struct {
		Model      string            `json:"model"`
		Permission map[string]string `json:"permission"`
		MCP        map[string]struct {
			Command []string `json:"command"`
			Timeout int      `json:"timeout"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatalf("config %q: %v", raw, err)
	}
	// The same rules as `opencode run`: read-only denies edits and the shell.
	if config.Model != "opencode/big-pickle" || config.Permission["edit"] != "deny" || config.Permission["bash"] != "deny" ||
		config.MCP["nodevas"].Timeout != openCodeToolTimeoutMs || config.MCP["nodevas"].Command[0] != "/opt/nodevas" {
		t.Errorf("config = %s", raw)
	}

	job.Model = "default"
	cmd, err = OpenCodeACP{}.Command(job, env)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = envValue(cmd.Env, openCodeConfigEnv)
	if strings.Contains(raw, `"model"`) {
		t.Errorf("default model set one: %s", raw)
	}
}
