package agents

import (
	"context"
	"testing"
	"time"
)

func (l *eventLog) count(event string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, e := range l.events {
		if e == event {
			n++
		}
	}
	return n
}

// Heartbeats keep a session alive many times a minute; only a change in how
// it looks from outside emits PresenceEvent, which drives the building.
func TestTouchEmitsPresenceOnlyOnTransitions(t *testing.T) {
	events := &eventLog{}
	hub := NewHub(nil, events.record)
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	hub.now = func() time.Time { return now }
	beat := func(hb Heartbeat) {
		t.Helper()
		hb.SessionID, hb.Project, hb.ActorID, hb.Agent = "s1", "p", "alice", "claude"
		if _, err := hub.Touch(hb); err != nil {
			t.Fatal(err)
		}
	}
	steps := []struct {
		name string
		do   func()
		want int
	}{
		{"new session", func() { beat(Heartbeat{}) }, 1},
		{"plain heartbeat", func() { beat(Heartbeat{}) }, 1},
		{"state change", func() { beat(Heartbeat{State: StateBusy}) }, 2},
		{"same state again", func() { beat(Heartbeat{State: StateBusy, Doing: "typing"}) }, 2},
		{"node change", func() { beat(Heartbeat{NodeID: "n1"}) }, 3},
		{"same node", func() { beat(Heartbeat{NodeID: "n1"}) }, 3},
		{"back online after the TTL", func() {
			now = now.Add(SessionTTL + time.Second)
			beat(Heartbeat{})
		}, 4},
		{"leave", func() {
			if err := hub.Leave("p", "s1", "alice"); err != nil {
				t.Fatal(err)
			}
		}, 5},
		{"rejoin", func() { beat(Heartbeat{}) }, 6},
	}
	for _, step := range steps {
		step.do()
		if got := events.count(PresenceEvent); got != step.want {
			t.Fatalf("%s: %d presence events, want %d (%v)", step.name, got, step.want, events.events)
		}
	}
	if got := events.count("agents-changed"); got != 9 {
		t.Fatalf("agents-changed = %d, want one per heartbeat and leave", got)
	}
	// An unknown session leaving is a no-op.
	if err := hub.Leave("p", "nobody", "alice"); err != nil || events.count(PresenceEvent) != 6 {
		t.Fatalf("unknown leave: %v, %d", err, events.count(PresenceEvent))
	}
}

// PendingAsksIn lists the pending asks of several projects at once.
func TestPendingAsksIn(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		type started struct {
			project string
			done    chan struct{}
		}
		var waits []started
		ask := func(project, session string) {
			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _ = hub.CreateAndWait(ctx, AskInput{
					Project: project, SessionID: session, SessionName: session, ActorID: "alice",
					Question: "Ship it?",
				}, 5*time.Second)
			}()
			waits = append(waits, started{project: project, done: done})
		}
		ask("alpha", "s1")
		ask("/beta/", "s2") // stored trimmed
		ask("gamma", "s3")
		var got []Ask
		for range 200 {
			got, _ = hub.PendingAsksIn(ctx, []string{"alpha", "beta/", "alpha"})
			if len(got) == 2 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if len(got) != 2 {
			t.Fatalf("pending in alpha and beta = %+v", got)
		}
		projects := map[string]bool{got[0].Project: true, got[1].Project: true}
		if !projects["alpha"] || !projects["beta"] {
			t.Fatalf("projects = %v", projects)
		}
		if none, err := hub.PendingAsksIn(ctx, nil); err != nil || none == nil || len(none) != 0 {
			t.Fatalf("no projects = %v, %v", none, err)
		}
		// Answer everything so no ask outlives the test.
		for _, project := range []string{"alpha", "beta", "gamma"} {
			for range 200 {
				if pending, _ := hub.ListAsks(ctx, project, AskPending); len(pending) == 1 {
					if _, err := hub.Answer(ctx, project, pending[0].ID, AskApproved, "", Viewer{ID: "alice"}); err != nil {
						t.Fatal(err)
					}
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		for _, w := range waits {
			<-w.done
		}
		if after, _ := hub.PendingAsksIn(ctx, []string{"alpha", "beta", "gamma"}); len(after) != 0 {
			t.Fatalf("answered asks still pending: %+v", after)
		}
	})
}

// HeldIn lists the held mail of several projects that the viewer may decide.
func TestHeldIn(t *testing.T) {
	bothHubs(t, func(t *testing.T, hub *Hub, _ *eventLog) {
		ctx := context.Background()
		hold := func(project string) {
			t.Helper()
			for _, hb := range []Heartbeat{
				{SessionID: "a1", Project: project, ActorID: "alice", Name: "alice-bot", Agent: "claude"},
				{SessionID: "b1", Project: project, ActorID: "bob", Name: "bob-bot", Agent: "claude"},
			} {
				if _, err := hub.Touch(hb); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := hub.SetInbound(project, "a1", InboundHold, "alice", false); err != nil {
				t.Fatal(err)
			}
			res, err := hub.Send(ctx, SendInput{
				Project: project, SenderActor: "bob", SenderSession: "bob-bot", To: "alice-bot", Body: "hello",
			})
			if err != nil || res.Deliveries[0].Status != StatusHeld {
				t.Fatalf("send in %s = %+v, %v", project, res, err)
			}
		}
		hold("p")
		hold("q")
		hold("r")
		if got, err := hub.HeldIn(ctx, []string{"p", "/q"}, alice); err != nil || len(got) != 2 {
			t.Fatalf("alice = %+v, %v", got, err)
		}
		if got, _ := hub.HeldIn(ctx, []string{"p", "q"}, bob); len(got) != 0 {
			t.Fatalf("bob decides alice's mail: %+v", got)
		}
		if got, _ := hub.HeldIn(ctx, []string{"p", "q", "r"}, adminUser); len(got) != 3 {
			t.Fatalf("admin = %+v", got)
		}
		if got, err := hub.HeldIn(ctx, nil, alice); err != nil || got == nil || len(got) != 0 {
			t.Fatalf("no projects = %v, %v", got, err)
		}
	})
}
