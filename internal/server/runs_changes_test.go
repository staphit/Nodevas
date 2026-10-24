package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"nodevas/internal/realtime"
	"nodevas/internal/runnerwire"
)

// dialHubAs opens the realtime socket as a signed-in person.
func dialHubAs(t *testing.T, base string, b browser) *wsPeer {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	header := http.Header{}
	for _, cookie := range b.cookies {
		header.Add("Cookie", cookie.Name+"="+cookie.Value)
	}
	url := "ws" + strings.TrimPrefix(base, "http") + "/ws"
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	conn.SetReadLimit(64 << 10)
	peer := &wsPeer{t: t, conn: conn, events: make(chan realtime.Event, 256)}
	go func() {
		defer close(peer.events)
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var event realtime.Event
			if json.Unmarshal(data, &event) == nil {
				peer.events <- event
			}
		}
	}()
	t.Cleanup(func() { _ = conn.CloseNow() })
	return peer
}

// startedRun dispatches a run and has the runner accept it.
func (f *runsFixture) startedRun(t *testing.T, conn *websocket.Conn) runnerwire.Run {
	t.Helper()
	// The hub reconciles a new connection (Attached) after its welcome; a run
	// dispatched before that finishes is offered, re-queued as unseen and
	// offered again. A pong means the reconcile is over.
	pingPong(t, conn)
	run := f.dispatch(t, f.bea, f.runBody(""))
	expectOffer(t, conn, run.ID)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeAccept, Accept: &runnerwire.Accept{
		RunID: run.ID, Base: "main", Branch: "agent/fix-login",
	}})
	return f.waitStatus(t, run.ID, runnerwire.RunRunning)
}

// waitRun polls bea's view of a run until ok holds.
func (f *runsFixture) waitRun(t *testing.T, id string, what string, ok func(runnerwire.Run) bool) runnerwire.Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		run, code := f.getRun(t, f.bea, id)
		if code == http.StatusOK && ok(run) {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s never got %s: %+v", id, what, run)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (f *runsFixture) getChanges(t *testing.T, b *browser, id string) (runnerwire.RunChangesResponse, *http.Response) {
	t.Helper()
	recorder := f.do(t, b, http.MethodGet, "/api/runs/"+id+"/changes", "", nil)
	var out runnerwire.RunChangesResponse
	_ = json.Unmarshal(recorder.Body.Bytes(), &out)
	return out, recorder.Result()
}

// pingPong proves the socket is still up and has processed everything sent
// before the ping: one runner's messages are handled in order.
func pingPong(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypePing})
	if env := readEnvelopeForTest(t, conn); env.Type != runnerwire.TypePong {
		t.Fatalf("after the messages: %+v, want pong", env)
	}
}

const testCommit = "0123456789abcdef0123456789abcdef01234567"

func testChanges() *runnerwire.RunChanges {
	return &runnerwire.RunChanges{
		Commit: testCommit, Base: "main",
		Files: []runnerwire.FileChange{
			{Path: "web/login.ts", Status: runnerwire.FileModified, Additions: 4, Deletions: 2},
			{Path: "web/login.test.ts", Status: runnerwire.FileAdded, Additions: 10},
		},
		Additions: 14, Deletions: 2,
		Patch: "diff --git a/web/login.ts b/web/login.ts\n--- a/web/login.ts\n+++ b/web/login.ts\n@@ -1 +1 @@\n-broken\n+fixed <ok>\n",
	}
}

func TestRunChangesAndPullRequest(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, runsHello(1))
	run := f.startedRun(t, conn)
	if run.FilesChanged != 0 || run.Commit != "" || run.PRURL != "" {
		t.Fatalf("fresh run = %+v", run)
	}
	if got, response := f.getChanges(t, &f.bea, run.ID); response.StatusCode != http.StatusOK || got.Changes != nil ||
		response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("changes before any report = %d %+v", response.StatusCode, got)
	}

	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeEvents, RunID: run.ID, Events: []runnerwire.RunEvent{
		{Seq: 1, Type: runnerwire.EventText, Text: "Fixing the button."},
	}})
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeChanges, RunID: run.ID, Changes: testChanges()})
	// A resend is ignored, not merged.
	resend := testChanges()
	resend.Commit, resend.Files = "ffff", resend.Files[:1]
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeChanges, RunID: run.ID, Changes: resend})
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypePR, RunID: run.ID, PR: &runnerwire.PRInfo{
		URL: "https://github.com/bea/visual-editor/pull/42", Number: 42, State: runnerwire.PROpen, CI: runnerwire.CIPending,
	}})
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: run.ID, Result: &runnerwire.RunResult{
		Status: runnerwire.RunSucceeded, Summary: "Fixed.",
	}})
	done := f.waitStatus(t, run.ID, runnerwire.RunSucceeded)
	if done.Commit != testCommit || done.FilesChanged != 2 || done.Additions != 14 || done.Deletions != 2 ||
		done.PRURL != "https://github.com/bea/visual-editor/pull/42" || done.PRNumber != 42 ||
		done.PRState != runnerwire.PROpen || done.CIStatus != runnerwire.CIPending || done.PRError != "" {
		t.Fatalf("finished run = %+v", done)
	}
	got, response := f.getChanges(t, &f.bea, run.ID)
	if response.StatusCode != http.StatusOK || got.Changes == nil {
		t.Fatalf("changes = %d %+v", response.StatusCode, got)
	}
	if c := got.Changes; c.Commit != testCommit || c.Base != "main" || len(c.Files) != 2 ||
		c.Files[0].Path != "web/login.ts" || c.Files[1].Status != runnerwire.FileAdded ||
		c.Additions != 14 || c.Patch != testChanges().Patch || c.PatchTruncated {
		t.Fatalf("changes = %+v", c)
	}
	// The list shows the counts too.
	response2 := f.do(t, &f.bea, http.MethodGet, "/api/runs?nodeId="+runsTestNode, "", nil)
	var list runnerwire.RunListResponse
	_ = json.Unmarshal(response2.Body.Bytes(), &list)
	if len(list.Runs) != 1 || list.Runs[0].FilesChanged != 2 || list.Runs[0].PRNumber != 42 {
		t.Fatalf("list = %+v", list.Runs)
	}

	// Long after the finish the runner still reports the PR; each move is
	// recorded and announced.
	peer := dialHubAs(t, f.liveURL, f.bea)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypePR, RunID: run.ID, PR: &runnerwire.PRInfo{
		URL: "https://github.com/bea/visual-editor/pull/42", Number: 42, State: runnerwire.PRMerged, CI: runnerwire.CISuccess,
	}})
	peer.await(runnerwire.RunsChangedEvent, func(event realtime.Event) bool { return event.ID == runsTestNode })
	merged := f.waitRun(t, run.ID, "merged", func(r runnerwire.Run) bool { return r.PRState == runnerwire.PRMerged })
	if merged.CIStatus != runnerwire.CISuccess || merged.Status != runnerwire.RunSucceeded || merged.FilesChanged != 2 {
		t.Fatalf("merged = %+v", merged)
	}

	// Visibility follows the run's.
	if _, response := f.getChanges(t, &f.cal, run.ID); response.StatusCode != http.StatusNotFound {
		t.Fatalf("cal reading bea's changes: %d", response.StatusCode)
	}
	if got, response := f.getChanges(t, &f.ann, run.ID); response.StatusCode != http.StatusOK || got.Changes == nil {
		t.Fatalf("the administrator reading bea's changes: %d", response.StatusCode)
	}
	if _, response := f.getChanges(t, nil, run.ID); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous changes: %d", response.StatusCode)
	}
	if _, response := f.getChanges(t, &f.bea, "nope"); response.StatusCode != http.StatusNotFound {
		t.Fatalf("changes of no run: %d", response.StatusCode)
	}
}

func TestRunChangesRefusals(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, runsHello(1))
	run := f.startedRun(t, conn)

	// Another runner cannot report for bea's run, whatever ID it sends.
	other := f.create(t, f.cal, "cal-pc")
	intruder := runnerSocket(t, f.liveURL, other.Token)
	sendEnvelope(t, intruder, runsHello(1))
	readEnvelopeForTest(t, intruder)
	sendEnvelope(t, intruder, runnerwire.Envelope{Type: runnerwire.TypeChanges, RunID: run.ID, Changes: testChanges()})
	sendEnvelope(t, intruder, runnerwire.Envelope{Type: runnerwire.TypePR, RunID: run.ID, PR: &runnerwire.PRInfo{
		URL: "https://evil.test/pull/1", Number: 1, State: runnerwire.PROpen,
	}})
	pingPong(t, intruder)

	// Malformed and out-of-limit reports are dropped; the socket stays up.
	tooMany := testChanges()
	tooMany.Files = make([]runnerwire.FileChange, runnerwire.MaxChangedFiles+1)
	for i := range tooMany.Files {
		tooMany.Files[i] = runnerwire.FileChange{Path: "f", Status: runnerwire.FileAdded}
	}
	badStatus := testChanges()
	badStatus.Files[0].Status = "exploded"
	longPath := testChanges()
	longPath.Files[0].Path = strings.Repeat("p", 1025)
	for _, env := range []runnerwire.Envelope{
		{Type: runnerwire.TypeChanges, RunID: run.ID},
		{Type: runnerwire.TypeChanges, Changes: testChanges()},
		{Type: runnerwire.TypeChanges, RunID: run.ID, Changes: tooMany},
		{Type: runnerwire.TypeChanges, RunID: run.ID, Changes: badStatus},
		{Type: runnerwire.TypeChanges, RunID: run.ID, Changes: longPath},
		{Type: runnerwire.TypePR, RunID: run.ID},
		{Type: runnerwire.TypePR, RunID: run.ID, PR: &runnerwire.PRInfo{URL: "javascript:alert(1)", State: runnerwire.PROpen}},
		{Type: runnerwire.TypePR, RunID: run.ID, PR: &runnerwire.PRInfo{URL: "http://github.com/x/y/pull/1", State: runnerwire.PROpen}},
		{Type: runnerwire.TypePR, RunID: run.ID, PR: &runnerwire.PRInfo{URL: "https://github.com/x/y/pull/1", State: "open", CI: "green"}},
	} {
		sendEnvelope(t, conn, env)
	}
	pingPong(t, conn)
	if got, _ := f.getRun(t, f.bea, run.ID); got.FilesChanged != 0 || got.Commit != "" || got.PRURL != "" {
		t.Fatalf("a refused report reached the run: %+v", got)
	}
	if got, _ := f.getChanges(t, &f.bea, run.ID); got.Changes != nil {
		t.Fatalf("a refused report was stored: %+v", got.Changes)
	}

	// A big but legal report fits through the socket: a full-size patch of
	// characters JSON escapes, plus the files.
	big := testChanges()
	big.Patch = strings.Repeat("<", runnerwire.MaxPatchBytes)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeChanges, RunID: run.ID, Changes: big})
	pingPong(t, conn)
	if got, _ := f.getChanges(t, &f.bea, run.ID); got.Changes == nil || len(got.Changes.Patch) != runnerwire.MaxPatchBytes {
		t.Fatal("the full-size patch was not stored")
	}

	// A PR that could not be opened is a report too.
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypePR, RunID: run.ID, PR: &runnerwire.PRInfo{
		Error: "gh is not signed in",
	}})
	f.waitRun(t, run.ID, "a PR error", func(r runnerwire.Run) bool { return r.PRError == "gh is not signed in" && r.PRURL == "" })
}
