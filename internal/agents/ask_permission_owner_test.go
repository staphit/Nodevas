package agents

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A permission prompt runs a tool on its runner owner's machine: only that
// person answers it. An administrator still answers anyone's question or
// approval, but not someone else's permission prompt.
func TestOnlyTheRunnerOwnerAnswersAPermissionPrompt(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		root := Viewer{ID: "root", Admin: true}
		perm, done := startAsk(t, hub, permissionIn("Bash", "Run go test?"), 5*time.Second)
		for _, decision := range []string{DecisionApprove, DecisionApproveRun, DecisionDeny} {
			if _, err := hub.AnswerWith(ctx, "alpha", perm.ID, AnswerInput{Decision: decision}, root); !errors.Is(err, ErrNotFound) {
				t.Fatalf("an admin answered alice's permission prompt with %s: %v", decision, err)
			}
		}
		if root.MayAnswer(perm) {
			t.Fatal("MayAnswer: an admin may answer alice's permission prompt")
		}
		if !(Viewer{ID: "ALICE"}).MayAnswer(perm) {
			t.Fatal("MayAnswer: alice may not answer her own permission prompt")
		}
		if got, err := hub.AnswerWith(ctx, "alpha", perm.ID, AnswerInput{Decision: DecisionApprove}, Viewer{ID: "alice"}); err != nil || got.Status != AskApproved {
			t.Fatalf("alice's answer = %+v, %v", got, err)
		}
		waitDecided(t, done)

		question, doneQ := startAsk(t, hub, AskInput{
			Project: "alpha", SessionID: "s1", SessionName: "api-claude", ActorID: "alice",
			Kind: AskKindQuestion, Question: "Which port?",
		}, 5*time.Second)
		if !root.MayAnswer(question) {
			t.Fatal("MayAnswer: an admin may not answer alice's question")
		}
		if got, err := hub.AnswerWith(ctx, "alpha", question.ID, AnswerInput{Decision: DecisionAnswer, Answer: "8080"}, root); err != nil || got.Status != AskAnswered {
			t.Fatalf("admin's answer to alice's question = %+v, %v", got, err)
		}
		waitDecided(t, doneQ)
	})
}

// Without accounts the one local person owns every machine, whatever actor
// a run's sidecar names.
func TestTheLocalPersonAnswersARunsPermissionPrompt(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		in := permissionIn("run_command", "Run git status?")
		in.ActorID = "run:antigravity"
		perm, done := startAsk(t, hub, in, 5*time.Second)
		local := Viewer{ID: "local", Admin: true}
		if !local.MayAnswer(perm) {
			t.Fatal("MayAnswer: the local person may not answer a run's permission prompt")
		}
		if got, err := hub.AnswerWith(ctx, "alpha", perm.ID, AnswerInput{Decision: DecisionApprove}, local); err != nil || got.Status != AskApproved {
			t.Fatalf("local answer = %+v, %v", got, err)
		}
		waitDecided(t, done)
	})
}
