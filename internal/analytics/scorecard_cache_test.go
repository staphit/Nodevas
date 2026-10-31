package analytics

import (
	"context"
	"testing"
	"time"
)

func useScorecardCache(t *testing.T, ttl time.Duration, now func() time.Time) {
	t.Helper()
	prevTTL, prevNow := scorecardTTL, scorecardClock
	scorecardTTL, scorecardClock = ttl, now
	InvalidateScorecards()
	t.Cleanup(func() {
		scorecardTTL, scorecardClock = prevTTL, prevNow
		InvalidateScorecards()
	})
}

func executorRuns(t *testing.T, f *fixture) int {
	t.Helper()
	got, err := ScorecardsFor(context.Background(), f.db, Query{OwnerID: "u1", Days: 30, Track: TrackAll, Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	row, ok := findRow(got.Rows, "executor", TrackAll, "claude", "sonnet")
	if !ok {
		t.Fatalf("no executor row in %+v", got.Rows)
	}
	return row.Runs
}

// Scorecards load the whole window's history; asked again within a minute
// (a page's tabs, the autopilot routing a batch) they are served from
// memory, until the minute is up or a new verdict invalidates them.
func TestScorecardsAreCachedBrieflyAndInvalidatedByAVerdict(t *testing.T) {
	clock := testNow
	useScorecardCache(t, time.Minute, func() time.Time { return clock })
	f := fixtureS1(t)
	if got := executorRuns(t, f); got != 5 {
		t.Fatalf("executor runs = %d, want 5", got)
	}

	f.run(seedRun{id: "late1", node: "nD", role: "executor", cost: 0.10, created: at(11, 30), seconds: 60})
	clock = clock.Add(30 * time.Second)
	if got := executorRuns(t, f); got != 5 {
		t.Fatalf("within the minute executor runs = %d, want the cached 5", got)
	}

	InvalidateScorecards()
	if got := executorRuns(t, f); got != 6 {
		t.Fatalf("after a verdict executor runs = %d, want 6", got)
	}

	f.run(seedRun{id: "late2", node: "nE", role: "executor", cost: 0.10, created: at(11, 40), seconds: 60})
	clock = clock.Add(61 * time.Second)
	if got := executorRuns(t, f); got != 7 {
		t.Fatalf("after the minute executor runs = %d, want 7", got)
	}
}

// Different questions are different entries: another owner, window or
// track is never answered from someone else's cache.
func TestScorecardCacheKeysOnTheQuestion(t *testing.T) {
	useScorecardCache(t, time.Minute, func() time.Time { return testNow })
	f := fixtureS1(t)
	all, err := ScorecardsFor(context.Background(), f.db, Query{OwnerID: "u1", Days: 30, Track: TrackAll, Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	other, err := ScorecardsFor(context.Background(), f.db, Query{OwnerID: "u2", Days: 30, Track: TrackAll, Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Rows) == 0 || len(other.Rows) != 0 {
		t.Fatalf("u1 rows %d, u2 rows %d; want u1's and none for u2", len(all.Rows), len(other.Rows))
	}
	week, err := ScorecardsFor(context.Background(), f.db, Query{OwnerID: "u1", Days: 7, Track: TrackAll, Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	if week.Days != 7 {
		t.Fatalf("7-day scorecards answered with a %d-day window", week.Days)
	}
}
