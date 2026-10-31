package agents

import (
	"context"
	"strings"
	"testing"
	"time"
)

func actionNames(t *testing.T, hub *Hub, project, session string) []string {
	t.Helper()
	got, err := hub.Actions(context.Background(), project, session, 200)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, a := range got {
		out = append(out, a.Action)
	}
	return out
}

// B-09: an external session's action log.
func TestSessionActionLog(t *testing.T) {
	hub := openTestHub(t)
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	hub.now = func() time.Time { now = now.Add(time.Second); return now }
	ctx := context.Background()

	hb := Heartbeat{SessionID: "claude:1", Project: "alpha", ActorID: "mcp:alice", Agent: "claude", Repo: "/work/nodevas"}
	if _, err := hub.Touch(hb); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(actionNames(t, hub, "alpha", "claude:1"), ","); got != ActionJoined {
		t.Fatalf("after joining: %q, want joined", got)
	}
	hb.Doing, hb.NodeID = "writing tests", "task-1"
	if _, err := hub.Touch(hb); err != nil {
		t.Fatal(err)
	}
	// The same doing again is not news.
	if _, err := hub.Touch(hb); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(actionNames(t, hub, "alpha", "claude:1"), ","); got != "joined,doing" {
		t.Fatalf("after doing twice: %q, want joined,doing", got)
	}
	list, _ := hub.Actions(ctx, "alpha", "claude:1", 200)
	if len(list) == 2 && (list[1].NodeID != "task-1" || !strings.Contains(list[1].Detail, "writing tests") || list[1].At == "") {
		t.Fatalf("doing action = %+v", list[1])
	}

	if _, err := hub.Touch(Heartbeat{SessionID: "claude:2", Project: "alpha", ActorID: "mcp:bob", Agent: "claude", Repo: "/work/other"}); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Send(ctx, SendInput{Project: "alpha", SenderActor: "mcp:alice", SenderSession: "claude:1", SenderAgent: "claude",
		To: "other-claude", Body: "I changed the API"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	askCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, _ = hub.CreateAndWait(askCtx, AskInput{Project: "alpha", SessionID: "claude:1", ActorID: "mcp:alice", Agent: "claude",
		Question: strings.Repeat("Should I keep the old endpoint? ", 20)}, 50*time.Millisecond)
	if err := hub.Leave("alpha", "claude:1", "mcp:alice"); err != nil {
		t.Fatal(err)
	}
	got, err := hub.Actions(ctx, "alpha", "claude:1", 200)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(got))
	for _, a := range got {
		names = append(names, a.Action)
		if a.Action == ActionMessage && a.Detail != "other-claude" {
			t.Errorf("message detail = %q, want the recipient", a.Detail)
		}
		if a.Action == ActionAsk && (len(a.Detail) > 200 || !strings.HasPrefix(a.Detail, "Should I keep")) {
			t.Errorf("ask detail = %q (%d bytes), want the question cut to 200", a.Detail, len(a.Detail))
		}
	}
	if strings.Join(names, ",") != "joined,doing,message,ask,left" {
		t.Fatalf("actions = %v", names)
	}

	// The detail is capped.
	hub.LogAction("alpha", "claude:1", "", ActionUsage, "", strings.Repeat("x", 1000))
	got, _ = hub.Actions(ctx, "alpha", "claude:1", 200)
	if last := got[len(got)-1]; last.Action != ActionUsage || len(last.Detail) != MaxActionDetail {
		t.Fatalf("capped usage action = %+v", last)
	}
}

func TestSessionActionLogWithoutDatabase(t *testing.T) {
	hub := NewHub(nil, nil)
	if _, err := hub.Touch(Heartbeat{SessionID: "claude:1", Project: "alpha", ActorID: "mcp:alice", Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	hub.LogAction("alpha", "claude:1", "", ActionUsage, "", "12")
	if got, err := hub.Actions(context.Background(), "alpha", "claude:1", 10); err != nil || len(got) != 0 {
		t.Fatalf("no database: %v, %v", got, err)
	}
}
