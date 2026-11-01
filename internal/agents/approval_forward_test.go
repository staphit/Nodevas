package agents

import (
	"context"
	"strings"
	"testing"
)

// The verified approval an ask's aboutMsgId forwards is for the peer that
// sent that message, whether or not its session is still known: its
// account and admins see the person's note in the project's mail, another
// member only the decision.
func TestApprovalForwardIsForTheSendersAccount(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		sent, err := hub.Send(ctx, SendInput{
			Project: "alpha", SenderActor: "u_bob", SenderSession: "bob-bot",
			To: "api-claude", Body: "may I push?",
		})
		if err != nil {
			t.Fatal(err)
		}
		ask, done := pendingAsk(t, hub, sent.Deliveries[0].MessageID)
		if _, err := hub.AnswerWith(ctx, "alpha", ask.ID, AnswerInput{Decision: DecisionApprove, Note: "SECRET-NOTE"}, Viewer{ID: "alice"}); err != nil {
			t.Fatal(err)
		}
		<-done

		msgs, err := hub.ListMessages(ctx, "alpha", "", "", 10)
		if err != nil {
			t.Fatal(err)
		}
		var forward *Message
		for i, m := range msgs {
			if m.HubNotice() {
				forward = &msgs[i]
			}
		}
		if forward == nil {
			t.Fatalf("no forwarded approval in %+v", msgs)
		}
		for viewer, sees := range map[Viewer]bool{
			{ID: "u_bob"}: true, {ID: "alice"}: true, {ID: "root", Admin: true}: true, {ID: "u_cal"}: false,
		} {
			body := viewer.ShownMessage(*forward).Body
			if !strings.Contains(body, `decision="approved"`) {
				t.Errorf("%s does not see the decision: %q", viewer.ID, body)
			}
			if leaked := strings.Contains(body, "SECRET-NOTE"); leaked != sees {
				t.Errorf("%s sees the note = %v, want %v: %q", viewer.ID, leaked, sees, body)
			}
		}
	})
}
