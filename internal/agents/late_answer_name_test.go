package agents

import (
	"context"
	"testing"
	"time"

	"nodevas/internal/testpg"
)

// After a restart a session name whose mail went to one account is that
// account's (uniqueNameLocked). Another account must not take it by asking
// under it and answering its own ask late: the late answer is mail to the
// name, stamped for the asker.
func TestLateAnswerDoesNotReserveAnotherAccountsName(t *testing.T) {
	ctx := context.Background()
	database := testpg.New(t)
	t.Cleanup(func() { _ = database.Close() })

	before := NewHub(database, nil)
	mustTouch(t, before, Heartbeat{SessionID: "alice-sess", Project: "p", ActorID: "u_alice", Name: "api-claude"})
	if _, err := before.Send(ctx, SendInput{Project: "p", SenderActor: "u_carol", IsHuman: true, To: "api-claude", Body: "for alice"}); err != nil {
		t.Fatal(err)
	}

	after := NewHub(database, nil)
	expired, err := after.CreateAndWait(ctx, AskInput{
		Project: "p", SessionID: "bob-x", SessionName: "api-claude", ActorID: "u_bob",
		Kind: AskKindQuestion, Question: "anything?",
	}, 20*time.Millisecond)
	if err == nil {
		if _, err := after.AnswerWith(ctx, "p", expired.ID, AnswerInput{Decision: DecisionAnswer, Answer: "x"}, Viewer{ID: "u_bob"}); err != nil {
			t.Fatal(err)
		}
	}

	bob := mustTouch(t, after, Heartbeat{SessionID: "bob-sess", Project: "p", ActorID: "u_bob", Name: "api-claude"})
	alice := mustTouch(t, after, Heartbeat{SessionID: "alice-sess", Project: "p", ActorID: "u_alice", Name: "api-claude"})
	if bob.Name == "api-claude" || alice.Name != "api-claude" {
		t.Fatalf("after a self-answered late ask u_bob holds %q and u_alice %q", bob.Name, alice.Name)
	}
}
