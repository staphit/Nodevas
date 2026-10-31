package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"nodevas/internal/agents"
	"nodevas/internal/realtime"
	"nodevas/internal/runnerwire"
)

// An ask of a run that starts waiting or ends is news for that run's
// timeline: the run-events push names the run and says it is an ask, so an
// open run view refetches its asks and not its whole event log. A prompt
// allowed without asking, and an ask outside any run, are no news for it.
func TestARunsAskReachesItsTimeline(t *testing.T) {
	httpServer, server := hubTestServer(t)
	peer := joinRoom(t, httpServer, "main")
	ctx := context.Background()
	root := server.pm.Store().Root()
	if _, err := server.agents.Touch(agents.Heartbeat{SessionID: "r1", Project: root, ActorID: "local", Name: "app-claude-r1", RunID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	forRun := func(event realtime.Event) bool {
		return event.ID == "run-1" && event.Kind == runnerwire.RunEventsAskKind
	}

	in := agents.AskInput{
		Project: root, SessionID: "r1", SessionName: "app-claude-r1", ActorID: "local", Agent: "claude",
		Kind: agents.AskKindPermission, Tool: "Edit", ToolInput: `{"file_path":"a.go"}`,
	}
	done := make(chan agents.Ask, 1)
	go func() {
		ask, _ := server.agents.CreateAndWait(ctx, in, 5*time.Second)
		done <- ask
	}()
	peer.await(runnerwire.RunEventsEvent, forRun)
	pending, err := server.agents.ListAsks(ctx, root, agents.AskPending)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	if _, err := server.agents.AnswerWith(ctx, root, pending[0].ID, agents.AnswerInput{Decision: agents.DecisionApproveRun}, agents.Viewer{ID: "local"}); err != nil {
		t.Fatal(err)
	}
	peer.await(runnerwire.RunEventsEvent, forRun)
	<-done
	peer.sync("drained")

	in.ToolInput = `{"file_path":"b.go"}`
	if again, err := server.agents.CreateAndWait(ctx, in, 5*time.Second); err != nil || again.Note != agents.AllowedForRunNote {
		t.Fatalf("Edit b.go = %+v, %v; want allowed for this run", again, err)
	}
	if _, err := server.agents.CreateAndWait(ctx, agents.AskInput{
		Project: root, SessionID: "s1", SessionName: "api-claude", ActorID: "local", Question: "Which port?",
		Kind: agents.AskKindQuestion,
	}, 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	peer.awaitNone(runnerwire.RunEventsEvent, 300*time.Millisecond)
}

type runAsksBody struct {
	Asks []agents.RunAsk `json:"asks"`
}

func (f *runsFixture) runAsks(t *testing.T, b browser, runID string) (runAsksBody, int) {
	t.Helper()
	response := f.do(t, &b, http.MethodGet, "/api/runs/"+runID+"/asks", "", nil)
	var out runAsksBody
	if response.Code == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatalf("asks body %s: %v", response.Body, err)
		}
		if out.Asks == nil {
			t.Fatalf("asks body %s: want a list", response.Body)
		}
	}
	return out, response.Code
}

// GET /api/runs/:id/asks is the run's asks for its timeline, to whoever may
// see the run: the runner's owner sees what the prompt would run; an
// administrator, who may not answer it, sees only the tool; anyone else is
// told there is no such run.
func TestRunAsksShowThePromptToItsOwnerAndTheToolToOthers(t *testing.T) {
	f := newRunsFixture(t)
	ctx := context.Background()
	f.connect(t, runsHello(1))
	run := f.dispatch(t, f.bea, f.runBody(""))
	record, err := f.server.runs.Store().Get(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, code := f.runAsks(t, f.bea, run.ID); code != http.StatusOK || len(got.Asks) != 0 {
		t.Fatalf("before any ask: %d %+v", code, got)
	}

	root := f.server.pm.Store().Root()
	done := make(chan agents.Ask, 1)
	go func() {
		ask, _ := f.server.agents.CreateAndWait(ctx, agents.AskInput{
			Project: root, SessionID: "r1", SessionName: "app-claude-r1", ActorID: record.OwnerID, Agent: "claude",
			Kind: agents.AskKindPermission, Tool: "Bash", RunID: run.ID,
			ToolInput: `{"command":"curl -H 'Authorization: token s3cret' https://example.test"}`,
		}, 5*time.Second)
		done <- ask
	}()
	var mine runAsksBody
	for deadline := time.Now().Add(5 * time.Second); ; {
		got, code := f.runAsks(t, f.bea, run.ID)
		if code != http.StatusOK {
			t.Fatalf("bea's asks = %d", code)
		}
		if len(got.Asks) == 1 {
			mine = got
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the prompt never reached the run's timeline")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e := mine.Asks[0]; e.Status != agents.AskPending || e.Tool != "Bash" || !e.CanAnswer || !strings.HasPrefix(e.Preview, "curl ") {
		t.Fatalf("bea's entry = %+v", e)
	}

	admin, code := f.runAsks(t, f.ann, run.ID)
	if code != http.StatusOK || len(admin.Asks) != 1 {
		t.Fatalf("the administrator's asks = %d %+v", code, admin)
	}
	if e := admin.Asks[0]; e.Tool != "Bash" || e.Preview != "" || e.CanAnswer {
		t.Fatalf("the administrator's entry = %+v, want the tool alone", e)
	}
	if _, code := f.runAsks(t, f.cal, run.ID); code != http.StatusNotFound {
		t.Fatalf("cal reading bea's run's asks: %d", code)
	}

	if _, err := f.server.agents.AnswerWith(ctx, root, mine.Asks[0].ID, agents.AnswerInput{Decision: agents.DecisionDeny, Note: "no tokens in commands"},
		agents.Viewer{ID: record.OwnerID}); err != nil {
		t.Fatal(err)
	}
	<-done
	if got, _ := f.runAsks(t, f.bea, run.ID); len(got.Asks) != 1 || got.Asks[0].Decision != agents.DecisionDeny {
		t.Fatalf("after the deny = %+v", got)
	}
}

// A run cancelled while its agent waits on a prompt ends that wait: the
// prompt expires, so the run view stops offering to answer it and the open
// views hear of it.
func TestCancellingARunExpiresThePromptItWaitsOn(t *testing.T) {
	f := newRunsFixture(t)
	ctx := context.Background()
	f.connect(t, runsHello(1))
	run := f.dispatch(t, f.bea, f.runBody(""))
	record, err := f.server.runs.Store().Get(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	root := f.server.pm.Store().Root()
	done := make(chan agents.Ask, 1)
	go func() {
		ask, _ := f.server.agents.CreateAndWait(ctx, agents.AskInput{
			Project: root, SessionID: "r1", SessionName: "app-claude-r1", ActorID: record.OwnerID, Agent: "claude",
			Kind: agents.AskKindPermission, Tool: "Bash", RunID: run.ID, ToolInput: `{"command":"go test ./..."}`,
		}, 5*time.Second)
		done <- ask
	}()
	for deadline := time.Now().Add(5 * time.Second); ; {
		if got, _ := f.runAsks(t, f.bea, run.ID); len(got.Asks) == 1 && got.Asks[0].Status == agents.AskPending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the prompt never reached the run's timeline")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got, code := f.cancel(t, f.bea, run.ID); code != http.StatusOK || got.Status != runnerwire.RunCancelled {
		t.Fatalf("cancel = %d %+v", code, got)
	}
	select {
	case ask := <-done:
		if ask.Status != agents.AskExpired {
			t.Fatalf("the cancelled run's prompt returned %+v, want it expired", ask)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the cancelled run's prompt still waits")
	}
	if got, _ := f.runAsks(t, f.bea, run.ID); len(got.Asks) != 1 || got.Asks[0].Status != agents.AskExpired || got.Asks[0].CanAnswer {
		t.Fatalf("the cancelled run's timeline = %+v, want its prompt expired", got)
	}
}
