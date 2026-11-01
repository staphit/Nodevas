package agents

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func actorSessions(hub *Hub, project, actorID string) int {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	n := 0
	for _, s := range hub.sessions {
		if s.Project == project && strings.EqualFold(s.ActorID, actorID) {
			n++
		}
	}
	return n
}

// Every heartbeat with a fresh session id registers a session that stays
// for a day after it goes offline, holding what the heartbeat said about
// itself, under the mutex every agent operation takes. One account's
// sessions in a project are bounded, and so is what each one holds.
func TestSessionsAreBoundedPerActor(t *testing.T) {
	hub := NewHub(nil, nil)
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	hub.now = func() time.Time { return now }
	doing := strings.Repeat("d", 12<<10)

	for i := 0; i < maxSessionsPerActor+10; i++ {
		s, err := hub.Touch(Heartbeat{SessionID: fmt.Sprintf("s-%d", i), Project: "alpha", ActorID: "mallory", Name: "x", Doing: doing,
			Repo: doing, Branch: doing, Cwd: doing, Role: doing})
		switch {
		case i < maxSessionsPerActor && err != nil:
			t.Fatalf("session %d refused under the cap: %v", i, err)
		case i >= maxSessionsPerActor && !errors.Is(err, ErrRateLimited):
			t.Fatalf("session %d past the cap: err = %v, want ErrRateLimited", i, err)
		case err == nil:
			for field, v := range map[string]string{"doing": s.Doing, "repo": s.Repo, "branch": s.Branch, "cwd": s.Cwd, "role": s.Role} {
				if len(v) > maxSessionFieldBytes {
					t.Fatalf("session %d keeps %d bytes of %s", i, len(v), field)
				}
			}
		}
	}
	if got := actorSessions(hub, "alpha", "mallory"); got != maxSessionsPerActor {
		t.Fatalf("mallory holds %d sessions, want %d", got, maxSessionsPerActor)
	}

	// Another account is not held up by mallory's.
	if _, err := hub.Touch(Heartbeat{SessionID: "alice-1", Project: "alpha", ActorID: "alice", Name: "x"}); err != nil {
		t.Fatalf("alice: %v", err)
	}
	// Nor is mallory's own session that is already registered.
	if _, err := hub.Touch(Heartbeat{SessionID: "s-0", Project: "alpha", ActorID: "mallory", State: StateBusy}); err != nil {
		t.Fatalf("a registered session's heartbeat: %v", err)
	}

	// Once some have gone offline, a new session takes the place of the
	// one offline longest.
	now = now.Add(SessionTTL + time.Second)
	if _, err := hub.Touch(Heartbeat{SessionID: "s-0", Project: "alpha", ActorID: "mallory"}); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Touch(Heartbeat{SessionID: "fresh", Project: "alpha", ActorID: "mallory", Name: "x"}); err != nil {
		t.Fatalf("a new session with offline ones to spare: %v", err)
	}
	if got := actorSessions(hub, "alpha", "mallory"); got != maxSessionsPerActor {
		t.Fatalf("mallory holds %d sessions after one took an offline one's place, want %d", got, maxSessionsPerActor)
	}
	if _, ok := hub.SessionByID("alpha", "s-0"); !ok {
		t.Fatal("the online session s-0 was evicted")
	}
	if _, ok := hub.SessionByID("alpha", "fresh"); !ok {
		t.Fatal("the new session is not registered")
	}
}
