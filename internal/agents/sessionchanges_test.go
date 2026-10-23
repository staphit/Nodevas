package agents

import (
	"context"
	"fmt"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

func TestClaimedNode(t *testing.T) {
	hub := NewHub(nil, nil)
	touch := func(id, actor, name, node string) {
		t.Helper()
		if _, err := hub.Touch(Heartbeat{SessionID: id, Project: "p", ActorID: actor, Name: name, NodeID: node}); err != nil {
			t.Fatal(err)
		}
	}
	touch("claude:a", "mcp:alice", "alpha", "n1")
	claims := []ClaimInfo{{NodeID: "n1", Owner: "mcp:alice"}}

	if _, node, ok := hub.ClaimedNode("p", "claude:a", claims); !ok || node != "n1" {
		t.Fatalf("reported node with a live claim = %q %v", node, ok)
	}
	// The reported node without a live claim is no node.
	if _, node, _ := hub.ClaimedNode("p", "claude:a", nil); node != "" {
		t.Fatalf("no live claims, node = %q", node)
	}
	if _, _, ok := hub.ClaimedNode("p", "nobody", claims); ok {
		t.Fatal("an unknown session was found")
	}

	// A session that reported nothing falls back to the claim's owner: its
	// actor, while it is the actor's only online session...
	touch("codex:b", "mcp:bob", "beta", "")
	bobClaims := []ClaimInfo{{NodeID: "n2", Owner: "mcp:bob"}}
	if _, node, _ := hub.ClaimedNode("p", "codex:b", bobClaims); node != "n2" {
		t.Fatalf("actor fallback node = %q", node)
	}
	// ...but not once a second session of the same actor is online.
	touch("codex:c", "mcp:bob", "gamma", "")
	if _, node, _ := hub.ClaimedNode("p", "codex:b", bobClaims); node != "" {
		t.Fatalf("ambiguous actor fallback node = %q", node)
	}
	// A claim owned by the session's own name is never ambiguous.
	if _, node, _ := hub.ClaimedNode("p", "codex:c", []ClaimInfo{{NodeID: "n3", Owner: "gamma"}}); node != "n3" {
		t.Fatalf("name fallback node = %q", node)
	}
}

func testSession() Session {
	return Session{ID: "claude:a", Name: "alpha", Agent: "claude", ActorID: "mcp:alice", Repo: "nodevas", Branch: "main"}
}

func exerciseSessionChanges(t *testing.T, hub *Hub) {
	ctx := context.Background()
	var events []string
	hub.notify = func(project, eventType string, payload any) {
		events = append(events, fmt.Sprintf("%s %s %v", project, eventType, payload))
	}
	record := func(in SessionChangeInput) runnerwire.SessionChange {
		t.Helper()
		if in.Project == "" {
			in.Project, in.Room, in.NodeID, in.Session = ".", "/ws/root", "n1", testSession()
		}
		out, err := hub.RecordSessionChange(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	first := record(SessionChangeInput{Files: []string{"a.go", "b.go"}})
	if first.ID == "" || first.Session != "alpha" || first.Agent != "claude" || first.ActorID != "mcp:alice" ||
		first.Repo != "nodevas" || first.Branch != "main" || first.Snapshot != nil || len(first.Files) != 2 {
		t.Fatalf("first = %+v", first)
	}
	if len(events) != 1 || events[0] != "/ws/root session-changes n1" {
		t.Fatalf("events = %v", events)
	}

	// Files accumulate without repeats.
	second := record(SessionChangeInput{Files: []string{"b.go", "c.go", "a.go"}})
	if second.ID != first.ID || fmt.Sprint(second.Files) != "[a.go b.go c.go]" || second.CreatedAt != first.CreatedAt {
		t.Fatalf("second = %+v", second)
	}

	// The snapshot is replaced at every Stop; files stay.
	snapOne := &runnerwire.RunChanges{Base: "abc", Files: []runnerwire.FileChange{{Path: "a.go", Status: runnerwire.FileModified}}}
	record(SessionChangeInput{Snapshot: snapOne})
	snapTwo := &runnerwire.RunChanges{Base: "def", Files: []runnerwire.FileChange{}, Patch: "diff"}
	third := record(SessionChangeInput{Snapshot: snapTwo})
	if third.Snapshot == nil || third.Snapshot.Base != "def" || third.Snapshot.Patch != "diff" || len(third.Files) != 3 {
		t.Fatalf("third = %+v", third)
	}

	// Capped at MaxSessionFiles: the first ones are kept.
	many := make([]string, runnerwire.MaxSessionFiles+10)
	for i := range many {
		many[i] = fmt.Sprintf("f%04d", i)
	}
	capped := record(SessionChangeInput{Files: many})
	if len(capped.Files) != runnerwire.MaxSessionFiles || capped.Files[0] != "a.go" {
		t.Fatalf("capped to %d files, first %q", len(capped.Files), capped.Files[0])
	}

	// Another session on the same node is a record of its own, and the newer
	// one lists first.
	other := testSession()
	other.ID, other.Name = "codex:b", "beta"
	record(SessionChangeInput{Project: ".", Room: "/ws/root", NodeID: "n1", Session: other, Files: []string{"z.go"}})
	record(SessionChangeInput{Project: ".", Room: "/ws/root", NodeID: "n2", Session: other, Files: []string{"y.go"}})

	listed, err := hub.NodeSessionChanges(ctx, ".", "n1")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].SessionID != "codex:b" || listed[1].SessionID != "claude:a" {
		t.Fatalf("listed = %+v", listed)
	}
	if listed[1].Snapshot == nil || listed[1].Snapshot.Base != "def" || len(listed[1].Files) != runnerwire.MaxSessionFiles {
		t.Fatalf("stored claude:a = %+v", listed[1])
	}
	if none, _ := hub.NodeSessionChanges(ctx, "other", "n1"); len(none) != 0 {
		t.Fatalf("another project's list = %+v", none)
	}

	if _, err := hub.RecordSessionChange(ctx, SessionChangeInput{Project: ".", Session: testSession()}); err == nil {
		t.Fatal("a record without a node was accepted")
	}
}

func TestSessionChangesStored(t *testing.T) {
	hub := openTestHub(t)
	tick := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	hub.now = func() time.Time { tick = tick.Add(time.Second); return tick }
	exerciseSessionChanges(t, hub)
}

func TestSessionChangesInMemory(t *testing.T) {
	hub := NewHub(nil, nil)
	tick := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	hub.now = func() time.Time { tick = tick.Add(time.Second); return tick }
	exerciseSessionChanges(t, hub)
}
