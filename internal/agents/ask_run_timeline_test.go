package agents

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// timelineOf is run's timeline asks as v sees them, failing the test on an
// error.
func timelineOf(t *testing.T, hub *Hub, runID string, v Viewer) []RunAsk {
	t.Helper()
	asks, err := hub.RunAsks(context.Background(), "alpha", runID, v)
	if err != nil {
		t.Fatal(err)
	}
	return asks
}

// A run's timeline shows what its agent waits on and how each ask ended,
// oldest first, and nothing of another run's.
func TestRunTimelineShowsWhatTheRunWaitsOnAndHowItEnded(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		alice := Viewer{ID: "alice"}
		registerRun(t, hub, "r1", "app-claude-r1", "run-1")
		registerRun(t, hub, "r2", "app-claude-r2", "run-2")

		if got := timelineOf(t, hub, "run-1", alice); len(got) != 0 {
			t.Fatalf("a run nobody asked in = %+v", got)
		}

		long := `{"command":"go test ./...\n  -run TestEverything` + strings.Repeat(" -v", 80) + `","description":"Run the tests"}`
		bash, bashDone := startAsk(t, hub, shellIn("Bash", long, "Run the tests?"), 5*time.Second)
		got := timelineOf(t, hub, "run-1", alice)
		if len(got) != 1 {
			t.Fatalf("timeline = %+v, want the waiting prompt", got)
		}
		waiting := got[0]
		if waiting.ID != bash.ID || waiting.Kind != AskKindPermission || waiting.Status != AskPending ||
			waiting.Tool != "Bash" || !waiting.CanAnswer || waiting.CreatedAt == 0 {
			t.Fatalf("waiting entry = %+v", waiting)
		}
		if !strings.HasPrefix(waiting.Preview, "go test ./... -run TestEverything -v") ||
			strings.ContainsAny(waiting.Preview, "\n\r\t") || utf8.RuneCountInString(waiting.Preview) > RunAskPreviewRunes {
			t.Fatalf("preview = %q, want the command on one line of at most %d characters", waiting.Preview, RunAskPreviewRunes)
		}

		// Another run's prompt is not this run's.
		other, otherDone := startAsk(t, hub, AskInput{
			Project: "alpha", SessionID: "r2", SessionName: "app-claude-r2", ActorID: "alice", Agent: "claude",
			Kind: AskKindPermission, Tool: "Edit", ToolInput: `{"file_path":"b.go"}`, Question: "Edit b.go?",
		}, 5*time.Second)

		if _, err := hub.AnswerWith(ctx, "alpha", bash.ID, AnswerInput{Decision: DecisionApproveRun}, alice); err != nil {
			t.Fatal(err)
		}
		waitDecided(t, bashDone)
		if _, err := hub.Answer(ctx, "alpha", other.ID, "deny", "", alice); err != nil {
			t.Fatal(err)
		}
		waitDecided(t, otherDone)

		edit, editDone := startAsk(t, hub, shellIn("Edit", `{"file_path":"a.go"}`, "Edit a.go?"), 5*time.Second)
		if _, err := hub.Answer(ctx, "alpha", edit.ID, "deny", "not that file", alice); err != nil {
			t.Fatal(err)
		}
		waitDecided(t, editDone)

		question, questionDone := startAsk(t, hub, AskInput{
			Project: "alpha", SessionID: "r1", SessionName: "app-claude-r1", ActorID: "alice",
			Kind: AskKindQuestion, Question: "Which port\nshould the server use?",
		}, 5*time.Second)
		if _, err := hub.AnswerWith(ctx, "alpha", question.ID, AnswerInput{Decision: DecisionAnswer, Answer: "8080"}, alice); err != nil {
			t.Fatal(err)
		}
		waitDecided(t, questionDone)

		got = timelineOf(t, hub, "run-1", alice)
		if len(got) != 3 || got[0].ID != bash.ID || got[1].ID != edit.ID || got[2].ID != question.ID {
			t.Fatalf("timeline = %+v, want the Bash prompt, the Edit prompt and the question, oldest first", got)
		}
		if got[0].Status != AskApproved || got[0].Decision != DecisionApproveRun || got[0].DecidedAt == 0 || got[0].CanAnswer {
			t.Fatalf("allowed for the run = %+v", got[0])
		}
		if got[1].Status != AskDenied || got[1].Decision != DecisionDeny || got[1].Note != "not that file" || got[1].Preview != "a.go" {
			t.Fatalf("denied with a note = %+v", got[1])
		}
		if got[2].Kind != AskKindQuestion || got[2].Decision != DecisionAnswer || got[2].Question != "Which port should the server use?" {
			t.Fatalf("answered question = %+v", got[2])
		}
		if strings.Contains(got[2].Note+got[2].Preview, "8080") {
			t.Fatalf("the timeline carries the answer itself: %+v", got[2])
		}
	})
}

// A prompt allowed by an earlier "allow for this run" waited on nobody: an
// agy run makes one per command, so they add nothing to the timeline.
func TestRunTimelineSkipsPromptsAllowedWithoutAsking(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		alice := Viewer{ID: "alice"}
		registerRun(t, hub, "r1", "app-claude-r1", "run-1")

		first, done := startAsk(t, hub, shellIn("Edit", `{"file_path":"a.go"}`, "Edit a.go?"), 5*time.Second)
		if _, err := hub.AnswerWith(ctx, "alpha", first.ID, AnswerInput{Decision: DecisionApproveRun}, alice); err != nil {
			t.Fatal(err)
		}
		waitDecided(t, done)
		for _, file := range []string{"b.go", "c.go", "d.go"} {
			again, err := hub.CreateAndWait(ctx, shellIn("Edit", `{"file_path":"`+file+`"}`, "Edit "+file+"?"), 5*time.Second)
			if err != nil || again.Status != AskApproved || again.Note != AllowedForRunNote {
				t.Fatalf("Edit %s = %+v, %v; want allowed for this run", file, again, err)
			}
		}
		if got := timelineOf(t, hub, "run-1", alice); len(got) != 1 || got[0].ID != first.ID {
			t.Fatalf("timeline = %+v, want only the prompt that was asked", got)
		}
	})
}

// Whoever cannot answer an ask sees that the run waited and on which tool,
// never the prompt's input or the note it was answered with.
func TestRunTimelineShowsOnlyTheToolToWhoCannotAnswer(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		alice := Viewer{ID: "alice"}
		admin := Viewer{ID: "root", Admin: true}
		registerRun(t, hub, "r1", "app-claude-r1", "run-1")

		bash, done := startAsk(t, hub, shellIn("Bash", `{"command":"curl -H 'Authorization: token s3cret' https://example.test"}`, "Run curl?"), 5*time.Second)
		for _, e := range timelineOf(t, hub, "run-1", admin) {
			if e.Tool != "Bash" || e.Preview != "" || e.CanAnswer || e.Question != "" {
				t.Fatalf("the administrator's entry = %+v, want the tool alone", e)
			}
		}
		if _, err := hub.Answer(ctx, "alpha", bash.ID, "deny", "token s3cret leaks", alice); err != nil {
			t.Fatal(err)
		}
		waitDecided(t, done)

		question, questionDone := startAsk(t, hub, AskInput{
			Project: "alpha", SessionID: "r1", SessionName: "app-claude-r1", ActorID: "alice",
			Kind: AskKindApproval, Question: "Drop the legacy column?",
		}, 5*time.Second)
		if _, err := hub.Answer(ctx, "alpha", question.ID, "deny", "keep it, s3cret reasons", alice); err != nil {
			t.Fatal(err)
		}
		waitDecided(t, questionDone)

		// bob may answer neither; an administrator may answer the approval
		// (as the ask listing has it) but never a prompt to run a tool on
		// alice's machine.
		bob := Viewer{ID: "bob"}
		for _, v := range []Viewer{admin, bob} {
			got := timelineOf(t, hub, "run-1", v)
			if len(got) != 2 {
				t.Fatalf("%s's timeline = %+v", v.ID, got)
			}
			if e := got[0]; e.Tool != "Bash" || e.Decision != DecisionDeny || e.Preview != "" || e.Note != "" || e.Question != "" {
				t.Fatalf("%s's Bash entry = %+v, want the tool and the outcome alone", v.ID, e)
			}
			if e := got[1]; e.Question != "Drop the legacy column?" || e.Decision != DecisionDeny {
				t.Fatalf("%s's approval entry = %+v", v.ID, e)
			}
		}
		if e := timelineOf(t, hub, "run-1", bob)[1]; e.Note != "" {
			t.Fatalf("bob sees the note on alice's approval: %+v", e)
		}
		if e := timelineOf(t, hub, "run-1", admin)[1]; e.Note != "keep it, s3cret reasons" {
			t.Fatalf("the administrator's approval entry = %+v, want its note", e)
		}
		if got := timelineOf(t, hub, "run-1", alice); got[0].Note != "token s3cret leaks" || got[1].Note != "keep it, s3cret reasons" {
			t.Fatalf("alice's own timeline = %+v, want her notes", got)
		}
	})
}
