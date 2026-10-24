package agents

import (
	"context"
	"errors"
	"testing"
	"time"
)

// eachHub runs fn against the database-backed hub and the in-memory one.
func eachHub(t *testing.T, fn func(t *testing.T, hub *Hub)) {
	t.Run("db", func(t *testing.T) { fn(t, openTestHub(t)) })
	t.Run("memory", func(t *testing.T) { fn(t, NewHub(nil, nil)) })
}

// pendingAsk starts alice's ask on her session api-claude and returns it
// once it is listed, with a channel for its outcome.
func pendingAsk(t *testing.T, hub *Hub, about string) (Ask, chan Ask) {
	t.Helper()
	ctx := context.Background()
	done := make(chan Ask, 1)
	go func() {
		decided, err := hub.CreateAndWait(ctx, AskInput{
			Project: "alpha", SessionID: "s1", SessionName: "api-claude", ActorID: "alice",
			Question: "Drop the legacy column?", AboutMsgID: about,
		}, 2*time.Second)
		if err != nil {
			t.Errorf("CreateAndWait: %v", err)
		}
		done <- decided
	}()
	for range 100 {
		if pending, _ := hub.ListAsks(ctx, "alpha", AskPending); len(pending) == 1 {
			return pending[0], done
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the ask never became pending")
	return Ask{}, nil
}

// An editor who is not the asker must not answer for them: the answer is
// alice's decision, and with aboutMsgId it goes out as her verified approval.
func TestOnlyTheAskersPersonOrAnAdminMayAnswer(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		sent, err := hub.Send(ctx, SendInput{
			Project: "alpha", SenderActor: "bob", SenderSession: "web-codex",
			To: "api-claude", Body: "Can we drop the legacy column?",
		})
		if err != nil {
			t.Fatal(err)
		}
		ask, done := pendingAsk(t, hub, sent.Deliveries[0].MessageID)

		if _, err := hub.Answer(ctx, "alpha", ask.ID, "approved", "", Viewer{ID: "bob"}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("another person answered alice's ask: %v", err)
		}
		if _, err := hub.Answer(ctx, "alpha", ask.ID, "approved", "", Viewer{ID: "alice", Agent: true}); !errors.Is(err, ErrRefused) {
			t.Fatalf("an agent answered an ask: %v", err)
		}
		if inbox, _ := hub.CheckInbox(ctx, "alpha", "web-codex", "bob", 0); len(inbox) != 0 {
			t.Fatalf("a refused answer forwarded an approval: %+v", inbox)
		}
		if _, err := hub.Answer(ctx, "alpha", ask.ID, "denied", "no", Viewer{ID: "root", Admin: true}); err != nil {
			t.Fatalf("an administrator could not answer: %v", err)
		}
		if got := <-done; got.Status != AskDenied || got.DecidedBy != "root" {
			t.Fatalf("decided ask = %+v", got)
		}
		// A decided ask stays decided.
		again, err := hub.Answer(ctx, "alpha", ask.ID, "approved", "", Viewer{ID: "alice"})
		if err != nil || again.Status != AskDenied {
			t.Fatalf("a decided ask was decided again: %+v, %v", again, err)
		}
	})
}

func TestAskRefusesASessionAnotherAccountHolds(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		if _, err := hub.Touch(Heartbeat{SessionID: "s-alice", Project: "alpha", ActorID: "alice", Name: "api-claude"}); err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		for _, in := range []AskInput{
			{Project: "alpha", SessionID: "s-bob", SessionName: "api-claude", ActorID: "bob", Question: "q"},
			{Project: "alpha", SessionID: "s-alice", SessionName: "elsewhere", ActorID: "bob", Question: "q"},
		} {
			if _, err := hub.CreateAndWait(ctx, in, time.Second); !errors.Is(err, ErrRefused) {
				t.Fatalf("bob asked as alice's session (%s/%s): %v", in.SessionID, in.SessionName, err)
			}
		}
	})
}

// aboutMsgId must name mail this session received; otherwise an ask could
// make the answer go out as an approval of somebody else's thread.
func TestAskAboutMustNameAMessageTheSessionReceived(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		if _, err := hub.Touch(Heartbeat{SessionID: "s-carol", Project: "alpha", ActorID: "carol", Name: "carol-bot"}); err != nil {
			t.Fatal(err)
		}
		sent, err := hub.Send(ctx, SendInput{
			Project: "alpha", SenderActor: "bob", SenderSession: "web-codex",
			To: "carol-bot", Body: "for carol",
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, about := range []string{sent.Deliveries[0].MessageID, "no-such-message"} {
			_, err := hub.CreateAndWait(ctx, AskInput{
				Project: "alpha", SessionID: "s1", SessionName: "api-claude", ActorID: "alice",
				Question: "q", AboutMsgID: about,
			}, time.Second)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("an ask about %q was accepted: %v", about, err)
			}
		}
	})
}

// A run acts as its runner's owner, but mail for that person, or for
// everyone, is not the run's to read.
func TestARunReadsOnlyItsOwnSessionsMail(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		send := func(to string) {
			t.Helper()
			if _, err := hub.Send(ctx, SendInput{
				Project: "alpha", SenderActor: "bob", SenderSession: "web", IsHuman: true, To: to, Body: "to " + to,
			}); err != nil {
				t.Fatalf("send to %s: %v", to, err)
			}
		}
		// With nobody online a broadcast waits under "*" for whoever looks.
		send("*")
		if _, err := hub.Touch(Heartbeat{SessionID: "r1", Project: "alpha", ActorID: "alice", Name: "app-claude-r1", Run: true}); err != nil {
			t.Fatal(err)
		}
		send("alice")
		send("app-claude-r1")
		if n := hub.UnreadCount(ctx, "alpha", "app-claude-r1", "alice"); n != 1 {
			t.Fatalf("the run counts %d unread, want its own 1", n)
		}
		inbox, err := hub.CheckInbox(ctx, "alpha", "app-claude-r1", "alice", 0)
		if err != nil || len(inbox) != 1 || inbox[0].Recipient != "app-claude-r1" {
			t.Fatalf("the run's inbox = %+v, %v", inbox, err)
		}
		// A run the transport recognised but that never registered reads
		// nothing addressed to its owner either.
		if msgs, _ := hub.CheckRunInbox(ctx, "alpha", "unregistered", "alice", 0); len(msgs) != 0 {
			t.Fatalf("an unregistered run read its owner's mail: %+v", msgs)
		}
		// The owner's own session still gets both.
		if msgs, _ := hub.CheckInbox(ctx, "alpha", "alice-laptop", "alice", 0); len(msgs) != 2 {
			t.Fatalf("the owner's session got %d messages, want 2", len(msgs))
		}
	})
}
