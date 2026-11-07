package agents

import (
	"strings"
	"testing"
	"time"
)

// A session remembers the last tool its agent hook reported and when, until
// the next one; a heartbeat about anything else keeps it, and going idle or
// leaving clears it, as it clears what the session is doing.
func TestTouchKeepsTheLastToolUntilIdle(t *testing.T) {
	hub := NewHub(nil, nil)
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	hub.now = func() time.Time { return now }
	touch := func(hb Heartbeat) Session {
		t.Helper()
		hb.SessionID, hb.Project, hb.ActorID, hb.Agent = "claude:1", "alpha", "mcp:alice", "claude"
		s, err := hub.Touch(hb)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	if s := touch(Heartbeat{State: StateBusy}); s.LastTool != "" || s.LastToolAt != 0 {
		t.Fatalf("a session that called no tool has %q at %d", s.LastTool, s.LastToolAt)
	}
	if s := touch(Heartbeat{Tool: " Read "}); s.LastTool != "Read" || s.LastToolAt != now.UnixMilli() {
		t.Fatalf("after Read: %q at %d", s.LastTool, s.LastToolAt)
	}
	at := now.UnixMilli()
	now = now.Add(30 * time.Second)
	// A periodic refresh (no tool) keeps the last one and its time.
	if s := touch(Heartbeat{}); s.LastTool != "Read" || s.LastToolAt != at {
		t.Fatalf("a refresh changed the tool: %q at %d", s.LastTool, s.LastToolAt)
	}
	if s := touch(Heartbeat{Tool: strings.Repeat("t", 500)}); len(s.LastTool) != maxToolBytes || s.LastToolAt != now.UnixMilli() {
		t.Fatalf("a long tool name kept %d bytes at %d", len(s.LastTool), s.LastToolAt)
	}
	if s := touch(Heartbeat{State: StateIdle}); s.LastTool != "" || s.LastToolAt != 0 {
		t.Fatalf("idle kept %q at %d", s.LastTool, s.LastToolAt)
	}

	touch(Heartbeat{Tool: "Bash"})
	if err := hub.Leave("alpha", "claude:1", "mcp:alice"); err != nil {
		t.Fatal(err)
	}
	if s, ok := hub.SessionByID("alpha", "claude:1"); !ok || s.LastTool != "" || s.LastToolAt != 0 {
		t.Fatalf("a session that left kept %q at %d (found %v)", s.LastTool, s.LastToolAt, ok)
	}
	for _, s := range hub.ListSessions("alpha", "", true, nil) {
		if s.LastTool != "" || s.LastToolAt != 0 {
			t.Fatalf("a session that left kept %q at %d", s.LastTool, s.LastToolAt)
		}
	}
}
