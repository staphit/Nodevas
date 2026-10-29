package agents

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// permissionIn is a permission prompt from alice's run session r1 (run-1).
func permissionIn(tool, question string) AskInput {
	return AskInput{
		Project: "alpha", SessionID: "r1", SessionName: "app-claude-r1", ActorID: "alice", Agent: "claude",
		Kind: AskKindPermission, Tool: tool, ToolInput: `{"command":"go test ./..."}`, Question: question,
	}
}

func registerRun(t *testing.T, hub *Hub, sessionID, name, runID string) {
	t.Helper()
	if _, err := hub.Touch(Heartbeat{SessionID: sessionID, Project: "alpha", ActorID: "alice", Name: name, RunID: runID}); err != nil {
		t.Fatal(err)
	}
}

func TestPermissionAskWaitsFiveMinutesAtMost(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		alice := Viewer{ID: "alice"}
		for _, timeout := range []time.Duration{0, 20 * time.Minute} {
			ask, done := startAsk(t, hub, permissionIn("Bash", "Run go test?"), timeout)
			if got := time.Duration(ask.ExpiresAt-ask.CreatedAt) * time.Millisecond; got != PermissionAskTimeout || PermissionAskTimeout != 5*time.Minute {
				t.Fatalf("timeout %v: permission ask waits %v", timeout, got)
			}
			if _, err := hub.Answer(ctx, "alpha", ask.ID, "deny", "", alice); err != nil {
				t.Fatal(err)
			}
			waitDecided(t, done)
		}
		question, done := startAsk(t, hub, AskInput{
			Project: "alpha", SessionID: "s1", SessionName: "api-claude", ActorID: "alice",
			Kind: AskKindQuestion, Question: "Which port?",
		}, 0)
		if got := time.Duration(question.ExpiresAt-question.CreatedAt) * time.Millisecond; got != MaxAskTimeout {
			t.Fatalf("a question waits %v, want %v", got, MaxAskTimeout)
		}
		if _, err := hub.Answer(ctx, "alpha", question.ID, "deny", "", alice); err != nil {
			t.Fatal(err)
		}
		waitDecided(t, done)
	})
}

func TestPermissionAskNamesItsTool(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		for name, mutate := range map[string]func(*AskInput){
			"no tool":            func(in *AskInput) { in.Tool = "  " },
			"tool name too long": func(in *AskInput) { in.Tool = strings.Repeat("t", MaxAskToolRunes+1) },
			"options":            func(in *AskInput) { in.Options = []string{"a", "b"} },
		} {
			in := permissionIn("Bash", "Run it?")
			mutate(&in)
			if _, err := hub.CreateAndWait(ctx, in, time.Second); !errors.Is(err, ErrInvalid) {
				t.Errorf("%s: err = %v, want ErrInvalid", name, err)
			}
		}
		// The question may be left out; the tool's input is cut to size.
		in := permissionIn("Write", "")
		in.ToolInput = `{"content":"` + strings.Repeat("é", MaxAskToolInputBytes) + `"}`
		done := make(chan Ask, 1)
		go func() {
			decided, _ := hub.CreateAndWait(ctx, in, 5*time.Second)
			done <- decided
		}()
		var ask Ask
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && ask.ID == ""; {
			if pending, _ := hub.ListAsks(ctx, "alpha", AskPending); len(pending) == 1 {
				ask = pending[0]
			}
			time.Sleep(10 * time.Millisecond)
		}
		if ask.Kind != AskKindPermission || ask.Tool != "Write" || !strings.Contains(ask.Question, "Write") {
			t.Fatalf("listed permission ask = %+v", ask)
		}
		if len(ask.ToolInput) > MaxAskToolInputBytes || !strings.HasPrefix(ask.ToolInput, `{"content":"é`) {
			t.Fatalf("tool input is %d bytes: %.40q", len(ask.ToolInput), ask.ToolInput)
		}
		if _, err := hub.Answer(ctx, "alpha", ask.ID, "deny", "", Viewer{ID: "alice"}); err != nil {
			t.Fatal(err)
		}
		waitDecided(t, done)
	})
}

func TestPermissionDecisions(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		alice := Viewer{ID: "alice"}
		ask, done := startAsk(t, hub, permissionIn("Bash", "Run go test?"), 5*time.Second)
		if _, err := hub.AnswerWith(ctx, "alpha", ask.ID, AnswerInput{Decision: "answer", Answer: "yes"}, alice); !errors.Is(err, ErrInvalid) {
			t.Fatalf("answer on a permission ask: %v", err)
		}
		if _, err := hub.AnswerWith(ctx, "alpha", ask.ID, AnswerInput{Decision: "approve_run"}, Viewer{ID: "alice", Agent: true}); !errors.Is(err, ErrRefused) {
			t.Fatalf("an agent allowed a tool: %v", err)
		}
		if _, err := hub.AnswerWith(ctx, "alpha", ask.ID, AnswerInput{Decision: "approve"}, alice); err != nil {
			t.Fatal(err)
		}
		if got := waitDecided(t, done); got.Status != AskApproved || got.Answer != "" || got.DecidedBy != "alice" {
			t.Fatalf("approved = %+v", got)
		}
		// approve_run is a permission decision only.
		approval, done2 := startAsk(t, hub, AskInput{
			Project: "alpha", SessionID: "s1", SessionName: "api-claude", ActorID: "alice", Question: "Ship it?",
		}, 5*time.Second)
		if _, err := hub.AnswerWith(ctx, "alpha", approval.ID, AnswerInput{Decision: "approve_run"}, alice); !errors.Is(err, ErrInvalid) {
			t.Fatalf("approve_run on an approval: %v", err)
		}
		if _, err := hub.Answer(ctx, "alpha", approval.ID, "deny", "", alice); err != nil {
			t.Fatal(err)
		}
		waitDecided(t, done2)
	})
}

// "Allow for this run" allows the same tool again in the same run without
// asking; another tool, or another run, still asks.
func TestApproveRunAllowsTheToolForTheRestOfTheRun(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		alice := Viewer{ID: "alice"}
		registerRun(t, hub, "r1", "app-claude-r1", "run-1")
		registerRun(t, hub, "r2", "app-claude-r2", "run-2")

		first, done := startAsk(t, hub, permissionIn("Bash", "Run go test?"), 5*time.Second)
		if first.RunID != "run-1" {
			t.Fatalf("runId = %q", first.RunID)
		}
		if _, err := hub.AnswerWith(ctx, "alpha", first.ID, AnswerInput{Decision: "approve_run", Note: "tests are fine"}, alice); err != nil {
			t.Fatal(err)
		}
		if got := waitDecided(t, done); got.Status != AskApproved || got.Answer != DecisionApproveRun || got.Note != "tests are fine" {
			t.Fatalf("approved for the run = %+v", got)
		}

		again, err := hub.CreateAndWait(ctx, permissionIn("Bash", "Run go vet?"), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if again.Status != AskApproved || again.DecidedBy != "alice" || again.Note != AllowedForRunNote ||
			again.Answer != DecisionApproveRun || again.RunID != "run-1" {
			t.Fatalf("second Bash in run-1 = %+v, want allowed for this run", again)
		}
		if approved, _ := hub.ListAsks(ctx, "alpha", AskApproved); len(approved) != 2 {
			t.Fatalf("approved asks recorded = %d, want 2", len(approved))
		}

		// Another tool in the same run asks.
		edit, doneEdit := startAsk(t, hub, permissionIn("Edit", "Edit main.go?"), 5*time.Second)
		if _, err := hub.Answer(ctx, "alpha", edit.ID, "deny", "", alice); err != nil {
			t.Fatal(err)
		}
		waitDecided(t, doneEdit)
		// The same tool in another run asks.
		other := permissionIn("Bash", "Run go test in run-2?")
		other.SessionID, other.SessionName = "r2", "app-claude-r2"
		bash2, doneBash2 := startAsk(t, hub, other, 5*time.Second)
		if bash2.RunID != "run-2" {
			t.Fatalf("runId = %q", bash2.RunID)
		}
		if _, err := hub.Answer(ctx, "alpha", bash2.ID, "deny", "", alice); err != nil {
			t.Fatal(err)
		}
		if got := waitDecided(t, doneBash2); got.Status != AskDenied {
			t.Fatalf("run-2's Bash = %+v", got)
		}
	})
}

// Without a run there is nothing to remember the allow for.
func TestApproveRunWithoutARunAllowsOnce(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		alice := Viewer{ID: "alice"}
		first, done := startAsk(t, hub, permissionIn("Bash", "Run go test?"), 5*time.Second)
		if first.RunID != "" {
			t.Fatalf("runId = %q", first.RunID)
		}
		if _, err := hub.AnswerWith(ctx, "alpha", first.ID, AnswerInput{Decision: "approve_run"}, alice); err != nil {
			t.Fatal(err)
		}
		if got := waitDecided(t, done); got.Status != AskApproved {
			t.Fatalf("approved = %+v", got)
		}
		second, done2 := startAsk(t, hub, permissionIn("Bash", "Run go vet?"), 5*time.Second)
		if _, err := hub.Answer(ctx, "alpha", second.ID, "deny", "", alice); err != nil {
			t.Fatal(err)
		}
		waitDecided(t, done2)
	})
}

// Prompts of the run's tool already waiting when the person allows it for
// the run are allowed with it.
func TestApproveRunReleasesTheRunsOtherWaitingPrompts(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		registerRun(t, hub, "r1", "app-claude-r1", "run-1")
		first, done1 := startAsk(t, hub, permissionIn("Bash", "Run go test?"), 5*time.Second)
		_, done2 := startAsk(t, hub, permissionIn("Bash", "Run go vet?"), 5*time.Second)
		edit, done3 := startAsk(t, hub, permissionIn("Edit", "Edit main.go?"), 5*time.Second)
		if _, err := hub.AnswerWith(ctx, "alpha", first.ID, AnswerInput{Decision: "approve_run"}, Viewer{ID: "alice"}); err != nil {
			t.Fatal(err)
		}
		if got := waitDecided(t, done1); got.Status != AskApproved {
			t.Fatalf("first = %+v", got)
		}
		if got := waitDecided(t, done2); got.Status != AskApproved || got.Note != AllowedForRunNote || got.DecidedBy != "alice" {
			t.Fatalf("the other waiting Bash prompt = %+v", got)
		}
		if pending, _ := hub.ListAsks(ctx, "alpha", AskPending); len(pending) != 1 || pending[0].ID != edit.ID {
			t.Fatalf("still pending = %+v, want only the Edit prompt", pending)
		}
		if _, err := hub.Answer(ctx, "alpha", edit.ID, "deny", "", Viewer{ID: "alice"}); err != nil {
			t.Fatal(err)
		}
		waitDecided(t, done3)
	})
}

// An agent blocks on one tool call at a time, but its CLI may raise a
// permission prompt while ask_user waits (or several for parallel tool
// calls): those never wait behind the session's one pending question.
func TestPermissionAsksDoNotWaitBehindAQuestion(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		alice := Viewer{ID: "alice"}
		question, doneQ := startAsk(t, hub, AskInput{
			Project: "alpha", SessionID: "r1", SessionName: "app-claude-r1", ActorID: "alice",
			Kind: AskKindQuestion, Question: "Which port?",
		}, 5*time.Second)
		perm, doneP := startAsk(t, hub, permissionIn("Bash", "Run go test?"), 5*time.Second)
		if _, err := hub.CreateAndWait(ctx, AskInput{
			Project: "alpha", SessionID: "r1", SessionName: "app-claude-r1", ActorID: "alice", Question: "Another?",
		}, time.Second); !errors.Is(err, ErrAskBusy) {
			t.Fatalf("a second question while one waits: %v", err)
		}
		for _, id := range []string{perm.ID, question.ID} {
			if _, err := hub.Answer(ctx, "alpha", id, "deny", "", alice); err != nil {
				t.Fatal(err)
			}
		}
		waitDecided(t, doneP)
		waitDecided(t, doneQ)
	})
}

// A permission prompt that timed out was denied; answering it later
// changes nothing, and no late answer goes out as mail.
func TestExpiredPermissionAskStaysExpired(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		expired, err := hub.CreateAndWait(ctx, permissionIn("Bash", "Run go test?"), 30*time.Millisecond)
		if err != nil || expired.Status != AskExpired {
			t.Fatalf("expired = %+v, %v", expired, err)
		}
		late, err := hub.AnswerWith(ctx, "alpha", expired.ID, AnswerInput{Decision: "approve"}, Viewer{ID: "alice"})
		if err != nil || late.Status != AskExpired {
			t.Fatalf("late answer = %+v, %v", late, err)
		}
		if inbox, _ := hub.CheckInbox(ctx, "alpha", "app-claude-r1", "alice", 0); len(inbox) != 0 {
			t.Fatalf("a late permission answer was mailed: %+v", inbox)
		}
	})
}
