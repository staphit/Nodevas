package agents

import (
	"context"
	"testing"
	"time"
)

// Every relayed prompt adds a row, auto-allowed ones included: a question
// still waiting must stay in the listing however many newer rows there are.
func TestListAsksKeepsEveryPendingAsk(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		alice := Viewer{ID: "alice"}
		registerRun(t, hub, "r1", "app-claude-r1", "run-1")
		question, doneQ := startAsk(t, hub, AskInput{
			Project: "alpha", SessionID: "s1", SessionName: "api-claude", ActorID: "alice",
			Kind: AskKindQuestion, Question: "Which port?",
		}, 30*time.Second)
		edit, done := startAsk(t, hub, shellIn("Edit", `{"file_path":"a.go"}`, "Edit a.go?"), 5*time.Second)
		if _, err := hub.AnswerWith(ctx, "alpha", edit.ID, AnswerInput{Decision: DecisionApproveRun}, alice); err != nil {
			t.Fatal(err)
		}
		waitDecided(t, done)
		for i := 0; i < 110; i++ {
			if got, err := hub.CreateAndWait(ctx, shellIn("Edit", `{"file_path":"b.go"}`, "Edit b.go?"), time.Second); err != nil || got.Status != AskApproved {
				t.Fatalf("auto-allowed edit %d = %+v, %v", i, got, err)
			}
		}
		all, err := hub.ListAsks(ctx, "alpha", "")
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, a := range all {
			if a.ID == question.ID {
				found = a.Status == AskPending
			}
		}
		if !found {
			t.Fatalf("the pending question is missing from the %d listed asks", len(all))
		}
		if _, err := hub.Answer(ctx, "alpha", question.ID, "deny", "", alice); err != nil {
			t.Fatal(err)
		}
		waitDecided(t, doneQ)
	})
}
