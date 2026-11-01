package agents

import (
	"context"
	"testing"

	"nodevas/internal/testpg"
)

func mustTouch(t *testing.T, hub *Hub, hb Heartbeat) Session {
	t.Helper()
	s, err := hub.Touch(hb)
	if err != nil {
		t.Fatalf("Touch(%+v): %v", hb, err)
	}
	return s
}

func inboxBodies(t *testing.T, hub *Hub, name, actorID string) []string {
	t.Helper()
	got, err := hub.CheckInbox(context.Background(), "p", name, actorID, 0)
	if err != nil {
		return nil
	}
	var out []string
	for _, m := range got {
		out = append(out, m.Body)
	}
	return out
}

// A session's name is bound to its account in memory only. After a restart
// (or once it was pruned) another account must neither pop the mail that
// waits for it nor take the name and with it the mail addressed to it.
func TestInboxMailStaysWithItsAccountAcrossRestart(t *testing.T) {
	ctx := context.Background()
	database := testpg.New(t)
	t.Cleanup(func() { _ = database.Close() })

	before := NewHub(database, nil)
	mustTouch(t, before, Heartbeat{SessionID: "alice-sess", Project: "p", ActorID: "u_alice", Name: "api-claude"})
	if _, err := before.Send(ctx, SendInput{Project: "p", SenderActor: "u_carol", IsHuman: true, To: "u_alice/api-claude", Body: "queued"}); err != nil {
		t.Fatal(err)
	}

	after := NewHub(database, nil)
	if got := inboxBodies(t, after, "api-claude", "u_bob"); len(got) > 0 {
		t.Fatalf("after a restart u_bob popped u_alice's mail: %q", got)
	}
	if bob := mustTouch(t, after, Heartbeat{SessionID: "bob-sess", Project: "p", ActorID: "u_bob", Name: "api-claude"}); bob.Name == "api-claude" {
		t.Errorf("after a restart u_bob took u_alice's session name %q", bob.Name)
	}
	alice := mustTouch(t, after, Heartbeat{SessionID: "alice-sess", Project: "p", ActorID: "u_alice", Name: "api-claude"})
	if _, err := after.Send(ctx, SendInput{Project: "p", SenderActor: "u_carol", IsHuman: true, To: "u_alice/api-claude", Body: "after"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"api-claude", "api-claude-2"} {
		if got := inboxBodies(t, after, name, "u_bob"); len(got) > 0 {
			t.Fatalf("u_alice's mail reached u_bob as %q: %q", name, got)
		}
	}
	if alice.Name == "api-claude" {
		if got := inboxBodies(t, after, alice.Name, "u_alice"); len(got) != 2 {
			t.Fatalf("u_alice's session read %q, want both messages", got)
		}
	}
}

// Mail addressed to an account's session goes to that account, even while
// another account's session holds the name.
func TestAddressedMailIsNotRedirectedToTheNameHolder(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		mustTouch(t, hub, Heartbeat{SessionID: "bob-sess", Project: "p", ActorID: "u_bob", Name: "api-claude"})
		mustTouch(t, hub, Heartbeat{SessionID: "alice-sess", Project: "p", ActorID: "u_alice", Name: "api-claude"})
		if _, err := hub.Send(ctx, SendInput{Project: "p", SenderActor: "u_carol", IsHuman: true, To: "u_alice/api-claude", Body: "for alice",
			Files: []FileInput{{Name: "creds.txt", Content: "attachment-secret"}}}); err != nil {
			t.Fatal(err)
		}
		if got := inboxBodies(t, hub, "api-claude", "u_bob"); len(got) > 0 {
			t.Fatalf("mail for u_alice/api-claude reached u_bob's session: %q", got)
		}
		msgs, err := hub.ListMessages(ctx, "p", "", "", 10)
		if err != nil || len(msgs) != 1 {
			t.Fatalf("messages = %+v, %v", msgs, err)
		}
		if _, _, err := hub.FileContent(ctx, "p", msgs[0].ID, "creds.txt", Viewer{ID: "u_bob"}); err == nil {
			t.Fatal("u_bob reads the attachment of mail addressed to u_alice")
		}
		if _, _, err := hub.FileContent(ctx, "p", msgs[0].ID, "creds.txt", Viewer{ID: "u_alice"}); err != nil {
			t.Fatalf("u_alice cannot read her own attachment: %v", err)
		}
	})
}
