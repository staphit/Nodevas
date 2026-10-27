package runs

import (
	"context"
	"testing"
	"time"

	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
)

// Active lists every run that has not ended, past the 50 a plain list stops
// at, and still honours the viewer and node filters.
func TestListActiveIsUncapped(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	live := map[string]bool{}
	for i := 0; i < 60; i++ {
		r := createRun(t, s, "n1")
		live[r.ID] = true
	}
	offered, err := s.Transition(ctx, createRun(t, s, "n2").ID, runnerwire.RunOffered, Change{})
	if err != nil {
		t.Fatal(err)
	}
	live[offered.ID] = true
	running := startRun(t, s)
	live[running.ID] = true
	ended := createRun(t, s, "n1")
	if _, err := s.Transition(ctx, ended.ID, runnerwire.RunCancelled, Change{}); err != nil {
		t.Fatal(err)
	}
	// Someone else's run, on someone else's runner.
	other, err := s.Create(ctx, New{
		Project: ".", NodeID: "n1", RunnerID: "r9", OwnerID: "u-cal", Agent: "codex",
		RepoID: "repo", CreatedBy: "u-cal", CreatedByName: "cal",
	})
	if err != nil {
		t.Fatal(err)
	}

	all, err := s.List(ctx, Filter{Project: ".", Active: true, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(live)+1 {
		t.Fatalf("active runs = %d, want %d", len(all), len(live)+1)
	}
	for _, r := range all {
		if runnerwire.RunFinal(r.Status) {
			t.Fatalf("an ended run in the active list: %+v", r.Run)
		}
	}

	mine, err := s.List(ctx, Filter{Project: ".", Active: true, Viewer: "u-ann"})
	if err != nil {
		t.Fatal(err)
	}
	if len(mine) != len(live) {
		t.Fatalf("ann's active runs = %d, want %d", len(mine), len(live))
	}
	for _, r := range mine {
		if r.ID == other.ID {
			t.Fatal("ann sees cal's run")
		}
	}

	onNode, err := s.List(ctx, Filter{Project: ".", Active: true, NodeID: "n2"})
	if err != nil || len(onNode) != 1 || onNode[0].ID != offered.ID {
		t.Fatalf("active on n2 = %v, %v", onNode, err)
	}
	if capped, _ := s.List(ctx, Filter{Project: "."}); len(capped) != 50 {
		t.Fatalf("a plain list is still capped at 50: %d", len(capped))
	}
	if none, _ := s.List(ctx, Filter{Project: "elsewhere", Active: true}); len(none) != 0 {
		t.Fatalf("another project's active runs: %d", len(none))
	}
}

// A running run's live usage lands on the run with every batch that carries
// it, and is announced as a runs change at most once per interval; the end of
// the run announces whatever the interval.
func TestDispatcherAnnouncesLiveUsageThrottled(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := startRun(t, s)
	var announced []string
	d := NewDispatcher(s, runners.NewRegistry(s.db, func() {}),
		func(project, nodeID string) { announced = append(announced, project+"/"+nodeID) }, nil)
	clock := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return clock }

	seq := int64(0)
	send := func(events ...runnerwire.RunEvent) {
		t.Helper()
		for i := range events {
			seq++
			events[i].Seq = seq
		}
		if err := d.Events(ctx, "r1", r.ID, events); err != nil {
			t.Fatal(err)
		}
	}
	usage := func(in, out int64) runnerwire.RunEvent {
		return runnerwire.RunEvent{Type: runnerwire.EventUsage, Usage: &runnerwire.Usage{InputTokens: in, OutputTokens: out}}
	}

	send(runnerwire.RunEvent{Type: runnerwire.EventText, Text: "thinking"})
	if len(announced) != 0 {
		t.Fatalf("a batch without usage announced %v", announced)
	}
	send(usage(100, 10))
	if len(announced) != 1 || announced[0] != "./n1" {
		t.Fatalf("first usage: announced %v", announced)
	}

	clock = clock.Add(time.Second)
	send(usage(200, 20))
	if len(announced) != 1 {
		t.Fatalf("usage inside the interval announced again: %v", announced)
	}
	// Stored all the same: the next announcement, or anyone reading the run,
	// gets the latest figure.
	if got, _ := s.Get(ctx, r.ID); got.Usage == nil || got.Usage.InputTokens != 200 || got.Usage.OutputTokens != 20 {
		t.Fatalf("live usage on the run = %+v", got.Usage)
	}

	clock = clock.Add(UsageAnnounceInterval)
	send(usage(300, 30), runnerwire.RunEvent{Type: runnerwire.EventText, Text: "more"})
	if len(announced) != 2 {
		t.Fatalf("usage after the interval: announced %v", announced)
	}

	// The end announces at once and forgets the run.
	if _, err := d.Cancel(ctx, r.ID, "stop"); err != nil {
		t.Fatal(err)
	}
	if len(announced) != 3 {
		t.Fatalf("the end: announced %v", announced)
	}
	d.usageMu.Lock()
	_, kept := d.usageAt[r.ID]
	d.usageMu.Unlock()
	if kept {
		t.Fatal("an ended run's announcement time is kept")
	}
}
