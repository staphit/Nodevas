package agents

import (
	"context"
	"testing"
)

type fakeClaimStore struct {
	claims   map[string]string
	released [][2]string
}

func (f *fakeClaimStore) LiveClaimOwner(nodeID string) string {
	return f.claims[nodeID]
}

func (f *fakeClaimStore) ReleaseClaim(nodeID, owner string) error {
	if f.claims[nodeID] != owner {
		return ErrNotFound
	}
	delete(f.claims, nodeID)
	f.released = append(f.released, [2]string{nodeID, owner})
	return nil
}

func TestReleaseRunClaimOnlyItsOwnSession(t *testing.T) {
	ctx := context.Background()
	hub := NewHub(nil, nil)

	cs := &fakeClaimStore{claims: map[string]string{}}
	hub.SetClaimStore(func(project string) ClaimStore {
		if project == "proj-a" {
			return cs
		}
		return nil
	})

	// Register two run sessions and one external session in proj-a.
	s1, err := hub.Touch(Heartbeat{
		SessionID: "sess-run-1",
		Project:   "proj-a",
		ActorID:   "u-ann",
		ActorName: "ann",
		Name:      "api-claude-run-1",
		Agent:     "claude",
		NodeID:    "node-1",
		Run:       true,
		RunID:     "run-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := hub.Touch(Heartbeat{
		SessionID: "sess-run-2",
		Project:   "proj-a",
		ActorID:   "u-bob",
		ActorName: "bob",
		Name:      "api-codex-run-2",
		Agent:     "codex",
		NodeID:    "node-2",
		Run:       true,
		RunID:     "run-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Touch(Heartbeat{
		SessionID: "sess-ext",
		Project:   "proj-a",
		ActorID:   "u-ann",
		ActorName: "ann",
		Name:      "ann-terminal",
		Agent:     "claude",
		NodeID:    "node-3",
	}); err != nil {
		t.Fatal(err)
	}

	// 1. Live claim owned by run-1's own session name is released.
	cs.claims["node-1"] = s1.Name
	hub.ReleaseRunClaim(ctx, "proj-a", "node-1", "run-1")
	if owner := cs.claims["node-1"]; owner != "" {
		t.Fatalf("node-1 still claimed by %q after releasing run-1", owner)
	}
	if len(cs.released) != 1 || cs.released[0] != [2]string{"node-1", s1.Name} {
		t.Fatalf("released = %+v", cs.released)
	}

	// 2. Live claim owned by another run's session is NOT released when run-1 ends.
	cs.claims["node-1"] = s2.Name
	hub.ReleaseRunClaim(ctx, "proj-a", "node-1", "run-1")
	if owner := cs.claims["node-1"]; owner != s2.Name {
		t.Fatalf("run-1 released run-2's claim! owner = %q", owner)
	}

	// 3. Live claim owned by an external session (no RunID) is NOT released.
	cs.claims["node-3"] = "ann-terminal"
	hub.ReleaseRunClaim(ctx, "proj-a", "node-3", "run-1")
	if owner := cs.claims["node-3"]; owner != "ann-terminal" {
		t.Fatalf("run-1 released external session's claim! owner = %q", owner)
	}

	// 4. Unknown runID releases nothing.
	cs.claims["node-2"] = s2.Name
	hub.ReleaseRunClaim(ctx, "proj-a", "node-2", "run-unknown")
	if owner := cs.claims["node-2"]; owner != s2.Name {
		t.Fatalf("unknown run released claim! owner = %q", owner)
	}

	// 5. Run-2's own claim by session ID is released when run-2 ends.
	cs.claims["node-2"] = s2.ID
	hub.ReleaseRunClaim(ctx, "proj-a", "node-2", "run-2")
	if owner := cs.claims["node-2"]; owner != "" {
		t.Fatalf("node-2 still claimed by %q after releasing run-2", owner)
	}
}
