package agents

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"nodevas/internal/testpg"
)

// storeAsk writes a as it is, as a hub that stopped while a waited left it.
func storeAsk(t *testing.T, hub *Hub, a Ask) {
	t.Helper()
	if hub.database == nil {
		copied := a
		hub.mu.Lock()
		hub.memAsks[a.ID] = &copied
		hub.mu.Unlock()
		return
	}
	if err := hub.database.Tx(context.Background(), func(tx *sql.Tx) error { return insertAsk(context.Background(), tx, a) }); err != nil {
		t.Fatal(err)
	}
}

func statusOf(t *testing.T, hub *Hub, id string) string {
	t.Helper()
	a, err := hub.lookupAsk(context.Background(), "alpha", id)
	if err != nil {
		t.Fatal(err)
	}
	return a.Status
}

// A pending ask whose waiter is gone (the hub stopped while it waited) and
// whose time is up is expired: it no longer holds its session's one
// question, fills its permission slots, or waits on anyone in a listing.
func TestPendingAsksPastTheirExpiryAreExpired(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
		hub.now = func() time.Time { return now }
		past := now.Add(-time.Minute).UnixMilli()
		storeAsk(t, hub, Ask{ID: "stale-q", Project: "alpha", SessionID: "r1", SessionName: "app-claude-r1", ActorID: "alice",
			Kind: AskKindQuestion, Question: "Which port?", Status: AskPending, CreatedAt: past - 1000, ExpiresAt: past})
		// An ask from before expiry times expires MaxAskTimeout after it was made.
		storeAsk(t, hub, Ask{ID: "legacy", Project: "alpha", SessionID: "s9", SessionName: "old", ActorID: "alice",
			Kind: AskKindApproval, Question: "Old?", Status: AskPending, CreatedAt: now.Add(-MaxAskTimeout - time.Second).UnixMilli()})
		for i := 0; i < maxPendingPermissions; i++ {
			storeAsk(t, hub, Ask{ID: "stale-p" + string(rune('a'+i)), Project: "alpha", SessionID: "r1", SessionName: "app-claude-r1",
				ActorID: "alice", Kind: AskKindPermission, Tool: "Bash", Question: "Allow Bash?", Status: AskPending,
				CreatedAt: past - 1000, ExpiresAt: past})
		}

		if pending, _ := hub.ListAsks(ctx, "alpha", AskPending); len(pending) != 0 {
			t.Fatalf("pending listing = %+v, want none", pending)
		}
		if waiting, _ := hub.PendingAsksIn(ctx, []string{"alpha"}); len(waiting) != 0 {
			t.Fatalf("building's pending asks = %+v, want none", waiting)
		}
		for _, id := range []string{"stale-q", "legacy", "stale-pa"} {
			if got := statusOf(t, hub, id); got != AskExpired {
				t.Fatalf("%s status = %q, want expired", id, got)
			}
		}

		question, done := startAsk(t, hub, AskInput{
			Project: "alpha", SessionID: "r1", SessionName: "app-claude-r1", ActorID: "alice",
			Kind: AskKindQuestion, Question: "Which port now?",
		}, 5*time.Second)
		perm, doneP := startAsk(t, hub, permissionIn("Bash", "Run go test?"), 5*time.Second)
		for _, id := range []string{question.ID, perm.ID} {
			if _, err := hub.Answer(ctx, "alpha", id, "deny", "", Viewer{ID: "alice"}); err != nil {
				t.Fatal(err)
			}
		}
		waitDecided(t, done)
		waitDecided(t, doneP)
	})
}

// Asking straight away on a session whose stale ask nothing listed yet works
// too: the count of waiting asks skips the ones past their time.
func TestAStaleAskDoesNotBlockItsSession(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		now := time.Now()
		storeAsk(t, hub, Ask{ID: "stale-q", Project: "alpha", SessionID: "r1", SessionName: "app-claude-r1", ActorID: "alice",
			Kind: AskKindQuestion, Question: "Which port?", Status: AskPending,
			CreatedAt: now.Add(-2 * time.Minute).UnixMilli(), ExpiresAt: now.Add(-time.Minute).UnixMilli()})
		got, err := hub.CreateAndWait(ctx, AskInput{
			Project: "alpha", SessionID: "r1", SessionName: "app-claude-r1", ActorID: "alice",
			Kind: AskKindQuestion, Question: "Which port now?",
		}, 30*time.Millisecond)
		if errors.Is(err, ErrAskBusy) || got.Status != AskExpired {
			t.Fatalf("a new question behind a stale one = %+v, %v; want asked (and expired)", got, err)
		}
	})
}

// A hub that starts finds no waiter for any ask a hub before it left
// pending: they are expired at once, not left blocking their sessions until
// their time runs out.
func TestAHubStartingExpiresAsksLeftPending(t *testing.T) {
	ctx := context.Background()
	database := testpg.New(t)
	t.Cleanup(func() { _ = database.Close() })
	before := NewHub(database, nil)
	now := time.Now()
	storeAsk(t, before, Ask{ID: "orphan", Project: "alpha", SessionID: "r1", SessionName: "app-claude-r1", ActorID: "alice",
		Kind: AskKindQuestion, Question: "Which port?", Status: AskPending,
		CreatedAt: now.UnixMilli(), ExpiresAt: now.Add(MaxAskTimeout).UnixMilli()})

	for name, start := range map[string]func() *Hub{
		"NewHub":      func() *Hub { return NewHub(database, nil) },
		"SetDatabase": func() *Hub { h := NewHub(nil, nil); h.SetDatabase(database); return h },
	} {
		if _, err := database.ExecContext(ctx, `UPDATE agent_asks SET status = 'pending', decided_at = 0 WHERE id = 'orphan'`); err != nil {
			t.Fatal(err)
		}
		hub := start()
		if got := statusOf(t, hub, "orphan"); got != AskExpired {
			t.Fatalf("%s: the orphaned ask is %q, want expired", name, got)
		}
	}
}
