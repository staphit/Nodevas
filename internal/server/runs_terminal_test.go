package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
)

func TestRunTerminalPathClassification(t *testing.T) {
	for path, want := range map[string]bool{
		"/api/runs/abc123/terminal":   true,
		"/api/runs//terminal":         false,
		"/api/runs/a/b/terminal":      false,
		"/api/runs/abc123/events":     false,
		"/api/runs/abc123/terminal/x": false,
	} {
		if got := runTerminalPath(path); got != want {
			t.Errorf("runTerminalPath(%q) = %v", path, got)
		}
	}
	if class, _ := ClassifyRoute(http.MethodGet, "/api/runs/abc123/terminal"); class != ClassWSUpgrade {
		t.Fatalf("terminal socket class = %s", class)
	}
}

func interactiveHello() runnerwire.Envelope {
	hello := runsHello(2)
	hello.Hello.Capabilities = append(hello.Hello.Capabilities, runnerwire.CapInteractive)
	return hello
}

func (f *runsFixture) interactiveBody() string {
	body, _ := json.Marshal(runnerwire.CreateRunRequest{
		NodeID: runsTestNode, RunnerID: f.runner.Runner.ID, Agent: "claude", RepoID: "repo-1",
		Mode: runnerwire.ModeInteractive,
	})
	return string(body)
}

// startInteractive dispatches an interactive run and has the runner accept it.
func (f *runsFixture) startInteractive(t *testing.T, conn *websocket.Conn) runnerwire.Run {
	t.Helper()
	run := f.dispatch(t, f.bea, f.interactiveBody())
	job := expectOffer(t, conn, run.ID)
	if job.Mode != runnerwire.ModeInteractive {
		t.Fatalf("offer mode = %q", job.Mode)
	}
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeAccept, Accept: &runnerwire.Accept{RunID: run.ID, Branch: "agent/x"}})
	return f.waitStatus(t, run.ID, runnerwire.RunRunning)
}

// dialTerminal opens a run's terminal socket as b would from the page.
func (f *runsFixture) dialTerminal(b browser, runID string, extra http.Header) (*websocket.Conn, *http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	header := http.Header{"Cookie": {cookieHeader(b.cookies)}}
	for key, values := range extra {
		header[key] = values
	}
	url := "ws" + strings.TrimPrefix(f.liveURL, "http") + "/api/runs/" + runID + "/terminal"
	return websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
}

func (f *runsFixture) terminal(t *testing.T, b browser, runID string) *websocket.Conn {
	t.Helper()
	conn, response, err := f.dialTerminal(b, runID, nil)
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("dial terminal: %v (status %d)", err, status)
	}
	conn.SetReadLimit(1 << 20)
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

func readFrame(t *testing.T, conn *websocket.Conn) (websocket.MessageType, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	kind, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	return kind, data
}

func expectOutput(t *testing.T, conn *websocket.Conn, want string) {
	t.Helper()
	kind, data := readFrame(t, conn)
	if kind != websocket.MessageBinary || string(data) != want {
		t.Fatalf("got %v %q, want output %q", kind, data, want)
	}
}

func expectControl(t *testing.T, conn *websocket.Conn, want runnerwire.TermControl) {
	t.Helper()
	kind, data := readFrame(t, conn)
	var got runnerwire.TermControl
	if kind != websocket.MessageText || json.Unmarshal(data, &got) != nil || got != want {
		t.Fatalf("got %v %q, want %+v", kind, data, want)
	}
}

func termOut(t *testing.T, conn *websocket.Conn, runID string, seq int64, data string) {
	t.Helper()
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeTermOut, RunID: runID, Term: &runnerwire.TermChunk{Seq: seq, Data: []byte(data)}})
}

func writeFrame(t *testing.T, conn *websocket.Conn, kind websocket.MessageType, data []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, kind, data); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

func TestInteractiveRunNeedsTheCapability(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, runsHello(1))

	response := f.do(t, &f.bea, http.MethodPost, "/api/runs", f.interactiveBody(), nil)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "cannot run interactive sessions") {
		t.Fatalf("interactive on a headless-only runner: %d %s", response.Code, response.Body)
	}
	bad := strings.Replace(f.interactiveBody(), `"interactive"`, `"tty"`, 1)
	if code := f.do(t, &f.bea, http.MethodPost, "/api/runs", bad, nil).Code; code != http.StatusBadRequest {
		t.Fatalf("unknown mode: %d", code)
	}

	// The new hello is applied on the socket's goroutine, a moment later.
	sendEnvelope(t, conn, interactiveHello())
	var run runnerwire.Run
	deadline := time.Now().Add(5 * time.Second)
	for {
		response := f.do(t, &f.bea, http.MethodPost, "/api/runs", f.interactiveBody(), nil)
		if response.Code == http.StatusCreated {
			_ = json.Unmarshal(response.Body.Bytes(), &run)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the interactive hello: %d %s", response.Code, response.Body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if run.Mode != runnerwire.ModeInteractive {
		t.Fatalf("run mode = %q", run.Mode)
	}
	if job := expectOffer(t, conn, run.ID); job.Mode != runnerwire.ModeInteractive {
		t.Fatalf("job mode = %q", job.Mode)
	}
	if got, _ := f.getRun(t, f.bea, run.ID); got.Mode != runnerwire.ModeInteractive {
		t.Fatalf("GET run mode = %q", got.Mode)
	}

	// A headless run has no terminal.
	headless := f.dispatch(t, f.bea, f.runBody(""))
	expectOffer(t, conn, headless.ID)
	if code := f.do(t, &f.bea, http.MethodGet, "/api/runs/"+headless.ID+"/terminal", "", nil).Code; code != http.StatusConflict {
		t.Fatalf("terminal of a headless run: %d", code)
	}
}

func TestRunTerminalRelay(t *testing.T) {
	f := newRunsFixture(t)
	f.server.runs.Terminals().SetKeep(300 * time.Millisecond)
	conn := f.connect(t, interactiveHello())
	run := f.startInteractive(t, conn)

	// The dispatcher attaches before any output: status only.
	bea := f.terminal(t, f.bea, run.ID)
	expectControl(t, bea, runnerwire.TermControl{Type: "status", Status: runnerwire.RunRunning})

	termOut(t, conn, run.ID, 1, "hello\r\n")
	expectOutput(t, bea, "hello\r\n")
	// A repeated seq is dropped; so is output for this run from another runner.
	termOut(t, conn, run.ID, 1, "again\r\n")
	other := f.create(t, f.cal, "cal-pc")
	intruder := runnerSocket(t, f.liveURL, other.Token)
	sendEnvelope(t, intruder, interactiveHello())
	readEnvelopeForTest(t, intruder)
	termOut(t, intruder, run.ID, 7, "evil\r\n")
	termOut(t, conn, run.ID, 2, "world")
	expectOutput(t, bea, "world")

	// A late viewer gets the scrollback, then the status.
	late := f.terminal(t, f.bea, run.ID)
	expectOutput(t, late, "hello\r\nworld")
	expectControl(t, late, runnerwire.TermControl{Type: "status", Status: runnerwire.RunRunning})

	// The dispatcher types; it arrives at the runner as term_in.
	writeFrame(t, bea, websocket.MessageBinary, []byte("ls\r"))
	if env := readEnvelopeForTest(t, conn); env.Type != runnerwire.TypeTermIn || env.RunID != run.ID || env.Term == nil || string(env.Term.Data) != "ls\r" {
		t.Fatalf("runner got %+v", env)
	}
	// A resize is relayed, clamped.
	writeFrame(t, late, websocket.MessageText, []byte(`{"type":"resize","cols":9999,"rows":30}`))
	if env := readEnvelopeForTest(t, conn); env.Type != runnerwire.TypeTermResize || env.Term == nil ||
		env.Term.Cols != runnerwire.MaxTermCols || env.Term.Rows != 30 {
		t.Fatalf("runner got %+v", env)
	}

	// The administrator may watch bea's run but not type into it.
	ann := f.terminal(t, f.ann, run.ID)
	expectOutput(t, ann, "hello\r\nworld")
	expectControl(t, ann, runnerwire.TermControl{Type: "status", Status: runnerwire.RunRunning})
	expectControl(t, ann, runnerwire.TermControl{Type: "readonly"})
	writeFrame(t, ann, websocket.MessageBinary, []byte("rm -rf /\r"))
	writeFrame(t, ann, websocket.MessageText, []byte(`{"type":"resize","cols":10,"rows":10}`))
	time.Sleep(50 * time.Millisecond)
	writeFrame(t, bea, websocket.MessageBinary, []byte("y"))
	if env := readEnvelopeForTest(t, conn); env.Type != runnerwire.TypeTermIn || string(env.Term.Data) != "y" {
		t.Fatalf("after the read-only viewer typed, the runner got %+v", env)
	}

	// Another member does not see the run; an agent gets nothing; a page on
	// another origin is refused.
	if code := f.do(t, &f.cal, http.MethodGet, "/api/runs/"+run.ID+"/terminal", "", nil).Code; code != http.StatusNotFound {
		t.Fatalf("cal: %d", code)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/terminal", nil)
	request = request.WithContext(auth.WithInProcessAgent(request.Context(), identity.AgentWorker))
	withCookies(request, f.bea.cookies)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("agent: %d %s", response.Code, response.Body)
	}
	if _, response, err := f.dialTerminal(f.bea, run.ID, http.Header{"Origin": {"https://evil.example"}}); err == nil ||
		response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin dial: %v %+v", err, response)
	}

	// The run ends: viewers hear it, then the sockets close once the output
	// expires.
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: run.ID, Result: &runnerwire.RunResult{Status: runnerwire.RunSucceeded}})
	expectControl(t, bea, runnerwire.TermControl{Type: "status", Status: runnerwire.RunSucceeded})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := bea.Read(ctx); websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Fatalf("after expiry: %v", err)
	}
	// Attaching now tells how it ended and closes.
	gone := f.terminal(t, f.bea, run.ID)
	expectControl(t, gone, runnerwire.TermControl{Type: "status", Status: runnerwire.RunSucceeded})
	if _, _, err := gone.Read(ctx); websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Fatalf("attach after expiry: %v", err)
	}

	// Every attach and detach is on the audit trail, with typing rights. The
	// three viewers of the live terminal detach as their handlers return,
	// which on a machine busy with the rest of the suite takes a while.
	deadline := time.Now().Add(20 * time.Second)
	for {
		events, err := f.server.audit.Query(context.Background(), audit.Filter{ActionPrefix: "run.terminal."})
		if err != nil {
			t.Fatal(err)
		}
		attach, detach, annTyping := 0, 0, false
		for _, event := range events {
			switch event.Action {
			case "run.terminal.attach":
				attach++
			case "run.terminal.detach":
				detach++
			}
			if event.ActorName == "ann" && event.Detail["canType"] != false {
				annTyping = true
			}
		}
		if annTyping {
			t.Fatalf("ann recorded as typing: %+v", events)
		}
		if attach == 3 && detach == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("audit: %d attaches, %d detaches: %+v", attach, detach, events)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A size asked for while the run waits for its runner reaches the runner as
// soon as the run starts.
func TestRunTerminalSizeBeforeStart(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, interactiveHello())
	run := f.dispatch(t, f.bea, f.interactiveBody())
	expectOffer(t, conn, run.ID)

	bea := f.terminal(t, f.bea, run.ID)
	expectControl(t, bea, runnerwire.TermControl{Type: "status", Status: runnerwire.RunOffered})
	writeFrame(t, bea, websocket.MessageText, []byte(`{"type":"resize","cols":100,"rows":30}`))
	writeFrame(t, bea, websocket.MessageBinary, []byte("too early"))
	time.Sleep(50 * time.Millisecond)

	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeAccept, Accept: &runnerwire.Accept{RunID: run.ID, Branch: "agent/x"}})
	if env := readEnvelopeForTest(t, conn); env.Type != runnerwire.TypeTermResize || env.RunID != run.ID ||
		env.Term == nil || env.Term.Cols != 100 || env.Term.Rows != 30 {
		t.Fatalf("runner got %+v, want the waiting size", env)
	}
	expectControl(t, bea, runnerwire.TermControl{Type: "status", Status: runnerwire.RunRunning})
}

// A browser that stops reading is dropped; the runner's socket never waits.
func TestRunTerminalSlowBrowserDropped(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, interactiveHello())
	run := f.startInteractive(t, conn)
	slow := f.terminal(t, f.bea, run.ID) // never read
	_ = slow

	chunk := string(bytes.Repeat([]byte("o"), runnerwire.MaxTermChunk))
	start := time.Now()
	for seq := int64(1); seq <= 256; seq++ {
		termOut(t, conn, run.ID, seq, chunk)
	}
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypePing})
	if env := readEnvelopeForTest(t, conn); env.Type != runnerwire.TypePong {
		t.Fatalf("runner got %+v", env)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("the runner was held up for %s", elapsed)
	}
	term := f.server.runs.Terminals().Get(run.ID)
	deadline := time.Now().Add(20 * time.Second)
	for term.Viewers() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the slow browser was never dropped")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// A fresh connection still works and starts from the scrollback.
	fresh := f.terminal(t, f.bea, run.ID)
	kind, data := readFrame(t, fresh)
	if kind != websocket.MessageBinary || len(data) == 0 || len(data) > runnerwire.TermScrollbackBytes {
		t.Fatalf("fresh viewer got %v, %d bytes", kind, len(data))
	}
}
