package agents

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A person's answer and note are for the asker (Viewer.Shown hides them from
// everyone else). An answer that comes after the ask expired goes to the
// asking session as mail, and delivered mail is listed to everyone in the
// project: the answer must not ride in its body.
func TestLateAnswerStaysWithTheAsker(t *testing.T) {
	for _, registered := range []bool{true, false} {
		name := "unregistered"
		if registered {
			name = "registered"
		}
		t.Run(name, func(t *testing.T) {
			eachHub(t, func(t *testing.T, hub *Hub) {
				ctx := context.Background()
				if registered {
					mustTouch(t, hub, Heartbeat{SessionID: "s1", Project: "alpha", ActorID: "u_alice", Name: "api-claude"})
				}
				expired, err := hub.CreateAndWait(ctx, AskInput{
					Project: "alpha", SessionID: "s1", SessionName: "api-claude", ActorID: "u_alice",
					Kind: AskKindQuestion, Question: "Which staging password?",
				}, 20*time.Millisecond)
				if err != nil || expired.Status != AskExpired {
					t.Fatalf("ask = %+v, %v; want expired", expired, err)
				}
				if _, err := hub.AnswerWith(ctx, "alpha", expired.ID, AnswerInput{
					Decision: DecisionAnswer, Answer: "SECRET-ANSWER", Note: "SECRET-NOTE",
				}, Viewer{ID: "u_alice"}); err != nil {
					t.Fatal(err)
				}

				msgs, err := hub.ListMessages(ctx, "alpha", "", "", 100)
				if err != nil || len(msgs) != 1 {
					t.Fatalf("messages = %+v, %v; want the late answer", msgs, err)
				}
				cal := Viewer{ID: "u_cal"}
				for _, m := range msgs {
					if cal.MaySee(m) && (strings.Contains(m.Body, "SECRET") || strings.Contains(strings.Join(fileNames(m), ","), "SECRET")) {
						t.Fatalf("another member reads the late answer: %q", m.Body)
					}
				}
				if _, _, err := hub.FileContent(ctx, "alpha", msgs[0].ID, lateAnswerFile, cal); err == nil {
					t.Fatal("another member reads the late answer's file")
				}

				// The asker's session still gets it.
				inbox, err := hub.CheckInbox(ctx, "alpha", "api-claude", "u_alice", 0)
				if err != nil || len(inbox) != 1 {
					t.Fatalf("asker's inbox = %+v, %v", inbox, err)
				}
				_, content, err := hub.FileContent(ctx, "alpha", inbox[0].ID, lateAnswerFile, Viewer{ID: "u_alice", Agent: true})
				if err != nil || !strings.Contains(content, "SECRET-ANSWER") || !strings.Contains(content, "SECRET-NOTE") {
					t.Fatalf("asker reads %q, %v; want the answer and note", content, err)
				}
			})
		})
	}
}

func fileNames(m Message) []string {
	var out []string
	for _, f := range m.Files {
		out = append(out, f.Name)
	}
	return out
}
