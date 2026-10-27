package runs

import (
	"context"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

func createIn(t *testing.T, s *Store, project, by string) Record {
	t.Helper()
	r, err := s.Create(context.Background(), New{
		Project: project, NodeID: "n1", NodeTitle: "Title", RunnerID: "r-" + by, RunnerName: "desk",
		OwnerID: by, Agent: "claude", Model: "default", RepoID: "repo", RepoName: "repo",
		JobPrompt: "# Title", CreatedBy: by, CreatedByName: by,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func ids(records []Record) []string {
	out := make([]string, len(records))
	for i, r := range records {
		out[i] = r.ID
	}
	return out
}

func sameIDs(got []Record, want ...Record) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i].ID != want[i].ID {
			return false
		}
	}
	return true
}

// ListBuilding is Station's one query over several projects: every run not
// yet ended, and the ended ones made since the window opened.
func TestListBuilding(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	base := time.Now().UTC()
	at := func(d time.Duration) { s.SetClock(func() time.Time { return base.Add(d) }) }

	at(-72 * time.Hour)
	active := createIn(t, s, "a", "u-ann") // old but still queued
	at(-48 * time.Hour)
	old := createIn(t, s, "a", "u-ann")
	if _, err := s.Transition(ctx, old.ID, runnerwire.RunCancelled, Change{}); err != nil {
		t.Fatal(err)
	}
	at(-time.Hour)
	recent := createIn(t, s, "a", "u-ann")
	if _, err := s.Transition(ctx, recent.ID, runnerwire.RunCancelled, Change{}); err != nil {
		t.Fatal(err)
	}
	bobs := createIn(t, s, "a", "u-bob")
	inB := createIn(t, s, "b", "u-ann")
	createIn(t, s, "c", "u-ann")
	at(0)
	if _, err := s.CreateMerge(ctx, recent.ID, "abc123", "u-ann", "ann"); err != nil {
		t.Fatal(err)
	}

	since := FormatStamp(base.Add(-24 * time.Hour))
	all, err := s.ListBuilding(ctx, BuildingFilter{Projects: []string{"a", "b"}, Since: since})
	if err != nil || !sameIDs(all, inB, bobs, recent, active) {
		t.Fatalf("admin view = %v, %v", ids(all), err)
	}
	if all[2].Merge == nil || all[2].Merge.Status != runnerwire.MergePending {
		t.Fatalf("merge not attached: %+v", all[2].Merge)
	}
	mine, err := s.ListBuilding(ctx, BuildingFilter{Projects: []string{"a", "b"}, Viewer: "u-ann", Since: since})
	if err != nil || !sameIDs(mine, inB, recent, active) {
		t.Fatalf("ann's view = %v, %v", ids(mine), err)
	}
	capped, err := s.ListBuilding(ctx, BuildingFilter{Projects: []string{"a", "b"}, Viewer: "u-ann", Since: since, Limit: 2})
	if err != nil || !sameIDs(capped, inB, recent) {
		t.Fatalf("capped = %v, %v", ids(capped), err)
	}
	wide, err := s.ListBuilding(ctx, BuildingFilter{Projects: []string{"a"}, Since: FormatStamp(base.Add(-50 * time.Hour))})
	if err != nil || !sameIDs(wide, bobs, recent, old, active) {
		t.Fatalf("a wider window = %v, %v", ids(wide), err)
	}
	if none, err := s.ListBuilding(ctx, BuildingFilter{Since: since}); err != nil || len(none) != 0 || none == nil {
		t.Fatalf("no projects = %v, %v", none, err)
	}
}

func TestCountOthersActive(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	createIn(t, s, "a", "u-ann")
	createIn(t, s, "a", "u-bob")
	createIn(t, s, "a", "u-bob")
	ended := createIn(t, s, "a", "u-bob")
	if _, err := s.Transition(ctx, ended.ID, runnerwire.RunCancelled, Change{}); err != nil {
		t.Fatal(err)
	}
	createIn(t, s, "b", "u-cal")
	createIn(t, s, "c", "u-cal") // not asked about
	// A run ann dispatched onto someone else's runner is hers to see.
	if _, err := s.Create(ctx, New{Project: "b", NodeID: "n1", RunnerID: "r-cal", OwnerID: "u-cal",
		Agent: "claude", RepoID: "repo", CreatedBy: "u-ann"}); err != nil {
		t.Fatal(err)
	}

	got, err := s.CountOthersActive(ctx, []string{"a", "b"}, "u-ann")
	if err != nil || len(got) != 2 || got["a"] != 2 || got["b"] != 1 {
		t.Fatalf("others active = %v, %v", got, err)
	}
	if got, _ := s.CountOthersActive(ctx, []string{"a"}, "u-bob"); got["a"] != 1 {
		t.Fatalf("bob sees = %v", got)
	}
	if got, _ := s.CountOthersActive(ctx, []string{"a"}, ""); len(got) != 0 {
		t.Fatalf("no viewer = %v", got)
	}
}
