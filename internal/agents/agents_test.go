package agents

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nodevas/internal/db"
)

func openTestHub(t *testing.T) *Hub {
	t.Helper()
	database, err := db.OpenAt(filepath.Join(t.TempDir(), db.FileName))
	if err != nil {
		t.Fatalf("OpenAt: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return NewHub(database, nil)
}

func TestSessionNamingTTLAndClaimEnrichment(t *testing.T) {
	hub := openTestHub(t)
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	hub.now = func() time.Time { return now }

	s1, err := hub.Touch(Heartbeat{
		SessionID: "claude:1",
		Project:   "alpha",
		ActorID:   "mcp:alice",
		Agent:     "claude",
		Repo:      "/work/nodevas",
		Branch:    "main",
	})
	if err != nil {
		t.Fatalf("Touch s1: %v", err)
	}
	if s1.Name != "nodevas-claude" {
		t.Fatalf("s1.Name = %q, want nodevas-claude", s1.Name)
	}

	// Second session on same repo/agent gets -2 suffix.
	s2, err := hub.Touch(Heartbeat{
		SessionID: "claude:2",
		Project:   "alpha",
		ActorID:   "mcp:bob",
		Agent:     "claude",
		Repo:      "/work/nodevas",
		Branch:    "feat/api",
	})
	if err != nil {
		t.Fatalf("Touch s2: %v", err)
	}
	if s2.Name != "nodevas-claude-2" {
		t.Fatalf("s2.Name = %q, want nodevas-claude-2", s2.Name)
	}

	// Enrich with board claims.
	claims := []ClaimInfo{
		{NodeID: "task-42", Title: "Wire auth middleware", Owner: "mcp:alice"},
	}
	list := hub.ListSessions("alpha", "", false, claims)
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2", len(list))
	}
	if list[0].NodeID != "task-42" || list[0].Doing != "Wire auth middleware" {
		t.Fatalf("s1 claim enrichment = (%q, %q), want (task-42, Wire auth middleware)", list[0].NodeID, list[0].Doing)
	}

	// Advance past SessionTTL: online=false, hidden unless includeOffline=true.
	now = now.Add(SessionTTL + 5*time.Second)
	active := hub.ListSessions("alpha", "", false, nil)
	if len(active) != 0 {
		t.Fatalf("active after TTL = %d, want 0", len(active))
	}
	all := hub.ListSessions("alpha", "", true, nil)
	if len(all) != 2 || all[0].Online {
		t.Fatalf("all after TTL = %+v, want 2 offline sessions", all)
	}
}

func TestMessagingAddressingLongPollAndLoopGuard(t *testing.T) {
	hub := openTestHub(t)
	ctx := context.Background()

	_, _ = hub.Touch(Heartbeat{
		SessionID: "s1",
		Project:   "alpha",
		ActorID:   "alice",
		Name:      "api-claude",
		Agent:     "claude",
		Repo:      "acme/api",
		NodeID:    "node-1",
	})
	_, _ = hub.Touch(Heartbeat{
		SessionID: "s2",
		Project:   "alpha",
		ActorID:   "bob",
		Name:      "web-codex",
		Agent:     "codex",
		Repo:      "acme/web",
	})

	// Agents cannot broadcast with `*`.
	_, err := hub.Send(ctx, SendInput{
		Project:       "alpha",
		SenderActor:   "alice",
		SenderSession: "api-claude",
		SenderAgent:   "claude",
		IsHuman:       false,
		To:            "*",
		Body:          "hello all",
	})
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("agent broadcast err = %v, want ErrRefused", err)
	}

	// Send by node:<id> address.
	res, err := hub.Send(ctx, SendInput{
		Project:       "alpha",
		SenderActor:   "bob",
		SenderSession: "web-codex",
		SenderAgent:   "codex",
		To:            "node:node-1",
		Body:          "Need the new schema on node-1",
	})
	if err != nil {
		t.Fatalf("Send to node:node-1: %v", err)
	}
	if len(res.Deliveries) != 1 || res.Deliveries[0].Recipient != "api-claude" || res.Deliveries[0].Status != "delivered" {
		t.Fatalf("deliveries = %+v, want delivered to api-claude", res.Deliveries)
	}

	if unread := hub.UnreadCount(ctx, "alpha", "api-claude", "alice"); unread != 1 {
		t.Fatalf("UnreadCount = %d, want 1", unread)
	}

	msgs, err := hub.CheckInbox(ctx, "alpha", "api-claude", "alice", 0)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("CheckInbox = %v, %v; want 1 msg", msgs, err)
	}
	if unread := hub.UnreadCount(ctx, "alpha", "api-claude", "alice"); unread != 0 {
		t.Fatalf("UnreadCount after read = %d, want 0", unread)
	}

	// Long-poll wakes when a new message arrives.
	done := make(chan []Message, 1)
	go func() {
		polled, _ := hub.CheckInbox(ctx, "alpha", "web-codex", "bob", 2*time.Second)
		done <- polled
	}()
	time.Sleep(40 * time.Millisecond)
	_, err = hub.Send(ctx, SendInput{
		Project:       "alpha",
		SenderActor:   "alice",
		SenderSession: "api-claude",
		SenderAgent:   "claude",
		To:            "bob/web-codex",
		ReplyTo:       msgs[0].ID,
		Body:          "Schema is ready",
	})
	if err != nil {
		t.Fatalf("reply Send: %v", err)
	}
	select {
	case got := <-done:
		if len(got) != 1 || got[0].Body != "Schema is ready" || got[0].ThreadID != msgs[0].ThreadID {
			t.Fatalf("long-poll got = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("long-poll did not wake on message arrival")
	}

	// Loop guard caps consecutive agent turns in the same thread.
	lastID := msgs[0].ID
	var loopErr error
	for i := 0; i < maxConsecutiveAgentTurn+2; i++ {
		r, sErr := hub.Send(ctx, SendInput{
			Project:       "alpha",
			SenderActor:   "alice",
			SenderSession: "api-claude",
			SenderAgent:   "claude",
			To:            "web-codex",
			ReplyTo:       lastID,
			Body:          "ping",
		})
		if sErr != nil {
			loopErr = sErr
			break
		}
		lastID = r.Deliveries[0].MessageID
	}
	if !errors.Is(loopErr, ErrLoopDetected) {
		t.Fatalf("loopErr = %v, want ErrLoopDetected", loopErr)
	}
}

func TestAskUserAndAboutApprovalForwarding(t *testing.T) {
	hub := openTestHub(t)
	ctx := context.Background()

	// Peer sends a request message first.
	sent, err := hub.Send(ctx, SendInput{
		Project:       "alpha",
		SenderActor:   "bob",
		SenderSession: "web-codex",
		SenderAgent:   "codex",
		To:            "api-claude",
		Body:          "Can we drop the legacy column?",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	msgID := sent.Deliveries[0].MessageID

	// Agent calls ask_user referencing aboutMsgId.
	resultCh := make(chan Ask, 1)
	go func() {
		decided, askErr := hub.CreateAndWait(ctx, AskInput{
			Project:     "alpha",
			SessionID:   "s1",
			SessionName: "api-claude",
			ActorID:     "alice",
			Agent:       "claude",
			Question:    "Bob's agent wants to drop legacy column. Approve?",
			AboutMsgID:  msgID,
			NodeID:      "node-7",
		}, 2*time.Second)
		if askErr != nil {
			t.Errorf("CreateAndWait: %v", askErr)
		}
		resultCh <- decided
	}()

	// Wait until pending ask is visible, then answer as human.
	var pending []Ask
	for range 20 {
		pending, _ = hub.ListAsks(ctx, "alpha", AskPending)
		if len(pending) == 1 {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if len(pending) != 1 {
		t.Fatalf("pending asks = %d, want 1", len(pending))
	}

	_, err = hub.Answer(ctx, "alpha", pending[0].ID, "approved", "Proceed after backup", "alice")
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}

	select {
	case got := <-resultCh:
		if got.Status != AskApproved || got.Note != "Proceed after backup" {
			t.Fatalf("decided ask = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("CreateAndWait did not unblock after Answer")
	}

	// Verify that web-codex received the <nodevas-approval> message.
	inbox, err := hub.CheckInbox(ctx, "alpha", "web-codex", "bob", 0)
	if err != nil || len(inbox) != 1 {
		t.Fatalf("bob inbox = %+v, err=%v; want 1 approval message", inbox, err)
	}
	if !strings.Contains(inbox[0].Body, "<nodevas-approval") || !strings.Contains(inbox[0].Body, "Proceed after backup") {
		t.Fatalf("approval body = %q", inbox[0].Body)
	}
}

func TestOfflineSessionNameIsReusableBySameActorOnly(t *testing.T) {
	hub := openTestHub(t)
	if _, err := hub.Touch(Heartbeat{SessionID: "claude:old", Project: "p", ActorID: "mcp:alice", Name: "api"}); err != nil {
		t.Fatal(err)
	}
	if err := hub.Leave("p", "claude:old", "mcp:alice"); err != nil {
		t.Fatal(err)
	}
	other, err := hub.Touch(Heartbeat{SessionID: "claude:bob", Project: "p", ActorID: "mcp:bob", Name: "api"})
	if err != nil {
		t.Fatal(err)
	}
	if other.Name == "api" {
		t.Fatalf("another actor took the offline session's name")
	}
	next, err := hub.Touch(Heartbeat{SessionID: "claude:new", Project: "p", ActorID: "mcp:alice", Name: "api"})
	if err != nil {
		t.Fatal(err)
	}
	if next.Name != "api" {
		t.Fatalf("same actor's new session named %q, want api", next.Name)
	}
}
