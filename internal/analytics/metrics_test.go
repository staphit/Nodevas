package analytics

import (
	"context"
	"testing"
)

func scorecards(t *testing.T, f *fixture, track string) Scorecards {
	t.Helper()
	got, err := ScorecardsFor(context.Background(), f.db, Query{OwnerID: "u1", Days: 30, Track: track, Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// B-27: fixture S1's executor row, exactly.
func TestScorecardExecutorS1(t *testing.T) {
	f := fixtureS1(t)
	got := scorecards(t, f, TrackAll)
	if got.Days != 30 || got.MinSamples != MinSamples || got.Since != "2026-09-29T00:00:00Z" {
		t.Errorf("window = days %d since %q min %d", got.Days, got.Since, got.MinSamples)
	}
	row, ok := findRow(got.Rows, "executor", TrackAll, "claude", "sonnet")
	if !ok {
		t.Fatalf("no executor claude/sonnet row in %+v", got.Rows)
	}
	if row.Runs != 5 || row.Samples != 3 {
		t.Errorf("runs %d samples %d, want 5 and 3", row.Runs, row.Samples)
	}
	wantRate(t, "successRate", row.SuccessRate, 0.6)
	wantRate(t, "reliability", row.Reliability, 0.8)
	wantRate(t, "delivery", row.Delivery, 0.6667)
	wantRate(t, "deliveryAdjusted", row.DeliveryAdjusted, 0.7368)
	wantRate(t, "firstPassRate", row.FirstPassRate, 0.3333)
	wantRate(t, "reworkRounds", row.ReworkRounds, 0.3333)
	wantCost(t, "costUsd", row.CostUSD, 3.0)
	wantRate(t, "costPerDelivered", row.CostPerDelivered, 1.5)
	wantRate(t, "medianSeconds", row.MedianSeconds, 600)
	wantNull(t, "tddRate", row.TDDRate)
	wantNull(t, "accuracy", row.Accuracy)
	if row.Score != 0.6253 || row.Tier != TierProbation || row.CostEstimated {
		t.Errorf("score %v tier %q estimated %v, want 0.6253 probation false", row.Score, row.Tier, row.CostEstimated)
	}
}

// B-28: fixture S1's reviewer row; r3 is contradicted by CI failing after
// its pass (C1).
func TestScorecardReviewerS1(t *testing.T) {
	f := fixtureS1(t)
	row, ok := findRow(scorecards(t, f, TrackAll).Rows, "reviewer", TrackAll, "codex", "gpt-5.5")
	if !ok {
		t.Fatal("no reviewer codex/gpt-5.5 row")
	}
	if row.Runs != 3 || row.Verdicts != 3 || row.Samples != 3 {
		t.Errorf("runs %d verdicts %d samples %d, want 3 3 3", row.Runs, row.Verdicts, row.Samples)
	}
	wantRate(t, "accuracy", row.Accuracy, 0.6667)
	wantRate(t, "successRate", row.SuccessRate, 1)
	wantRate(t, "reliability", row.Reliability, 1)
	wantRate(t, "costPerDelivered", row.CostPerDelivered, 0.2)
	if row.Score != 0.8 || row.Tier != TierProbation {
		t.Errorf("score %v tier %q, want 0.8 probation (3 samples)", row.Score, row.Tier)
	}
	contradicted, err := Contradicted(context.Background(), f.db, []string{"r1", "r2", "r3"})
	if err != nil {
		t.Fatal(err)
	}
	if contradicted["r1"] || contradicted["r2"] || !contradicted["r3"] {
		t.Fatalf("contradicted = %v, want only r3", contradicted)
	}
}

// B-30: TDD discipline, the median of an even count, and the track split.
func TestScorecardTDDMedianAndTracks(t *testing.T) {
	f := newFixture(t)
	// Unit p1: red passed before green passed.
	f.pipeline("p1", "p", "n1", "approved", 1, at(9, 0))
	f.run(seedRun{id: "t1", node: "n1", pipeline: "p1", kind: "test", role: "tester", cost: 0.1, created: at(9, 0), seconds: 100})
	f.run(seedRun{id: "v1", node: "n1", pipeline: "p1", kind: "verify", agent: "-", model: "-", created: at(9, 5), seconds: 10})
	f.evidence("v1", "p1", "p", "n1", "verify_red", "pass", at(9, 6))
	f.run(seedRun{id: "e1", node: "n1", pipeline: "p1", kind: "revise", role: "executor", cost: 1, created: at(9, 10), seconds: 200})
	f.run(seedRun{id: "v2", node: "n1", pipeline: "p1", kind: "verify", agent: "-", model: "-", created: at(9, 20), seconds: 10})
	f.evidence("v2", "p1", "p", "n1", "verify_green", "pass", at(9, 21))
	// Unit p2: green with no red before it.
	f.pipeline("p2", "p", "n2", "approved", 1, at(10, 0))
	f.run(seedRun{id: "e2", node: "n2", pipeline: "p2", role: "executor", cost: 1, created: at(10, 0), seconds: 400})
	f.run(seedRun{id: "v3", node: "n2", pipeline: "p2", kind: "verify", agent: "-", model: "-", created: at(10, 10), seconds: 10})
	f.evidence("v3", "p2", "p", "n2", "verify_green", "pass", at(10, 11))
	// A story node: its own track.
	f.run(seedRun{id: "s1", node: "scene1", nodeType: "scene", role: "executor", cost: 2, created: at(11, 0), seconds: 1000})

	all := scorecards(t, f, TrackAll)
	exec, ok := findRow(all.Rows, "executor", TrackAll, "claude", "sonnet")
	if !ok {
		t.Fatal("no executor row")
	}
	// Units p1 (red then green) and p2 (green, no red); s1 has no verify.
	wantRate(t, "tddRate", exec.TDDRate, 0.5)
	// Succeeded executor runs: 200, 400, 1000 seconds; tester 100.
	wantRate(t, "medianSeconds", exec.MedianSeconds, 400)
	if _, ok := findRow(all.Rows, "verify", TrackAll, "-", "-"); ok {
		t.Error("verify runs scored as an agent")
	}

	code := scorecards(t, f, TrackCode)
	codeExec, ok := findRow(code.Rows, "executor", TrackCode, "claude", "sonnet")
	if !ok || codeExec.Runs != 2 {
		t.Fatalf("code track executor = %+v (found %v), want 2 runs", codeExec, ok)
	}
	// Two succeeded runs: the median is the mean of 200 and 400.
	wantRate(t, "code medianSeconds", codeExec.MedianSeconds, 300)
	story := scorecards(t, f, TrackStory)
	storyExec, ok := findRow(story.Rows, "executor", TrackStory, "claude", "sonnet")
	if !ok || storyExec.Runs != 1 {
		t.Fatalf("story track executor = %+v (found %v), want 1 run", storyExec, ok)
	}
	wantNull(t, "story tddRate", storyExec.TDDRate)

	// A tester whose red never came first.
	g := newFixture(t)
	g.pipeline("q1", "p", "n1", "approved", 1, at(9, 0))
	g.run(seedRun{id: "e9", node: "n1", pipeline: "q1", role: "executor", cost: 1, created: at(9, 0), seconds: 60})
	g.evidence("e9", "q1", "p", "n1", "verify_green", "pass", at(9, 2))
	g.evidence("e9", "q1", "p", "n1", "verify_red", "pass", at(9, 3))
	row, _ := findRow(scorecards(t, g, TrackAll).Rows, "executor", TrackAll, "claude", "sonnet")
	wantRate(t, "red after green", row.TDDRate, 0)
}
