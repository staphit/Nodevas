package analytics

import (
	"context"
	"testing"
)

func costBucket(buckets []CostBucket, key string) (CostBucket, bool) {
	for _, b := range buckets {
		if b.Key == key {
			return b, true
		}
	}
	return CostBucket{}, false
}

// B-31: fixture S1's insights for project p, exactly; a viewer sees only
// their own runs.
func TestInsightsS1(t *testing.T) {
	f := fixtureS1(t)
	got, err := InsightsFor(context.Background(), f.db, Query{Project: "p", Days: 30, Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	if got.Currency != "USD" || got.Days != 30 {
		t.Errorf("header = %+v", got)
	}
	tot := got.Totals
	wantCost(t, "totals.costUsd", tot.CostUSD, 3.6)
	if tot.Units != 3 || tot.Delivered != 2 || tot.Runs != 8 {
		t.Errorf("units %d delivered %d runs %d, want 3 2 8", tot.Units, tot.Delivered, tot.Runs)
	}
	wantRate(t, "totals.costPerDelivered", tot.CostPerDelivered, 1.8)
	wantRate(t, "totals.firstPassRate", tot.FirstPassRate, 0.3333)
	wantRate(t, "totals.reworkRounds", tot.ReworkRounds, 0.3333)

	exec, ok := costBucket(got.ByRole, "executor")
	if !ok || exec.Delivered != 2 {
		t.Errorf("byRole executor = %+v (found %v)", exec, ok)
	}
	wantCost(t, "byRole executor costUsd", exec.CostUSD, 3.0)
	wantRate(t, "byRole executor costPerDelivered", exec.CostPerDelivered, 1.5)
	rev, ok := costBucket(got.ByRole, "reviewer")
	if !ok || rev.Delivered != 2 {
		t.Errorf("byRole reviewer = %+v (found %v)", rev, ok)
	}
	wantCost(t, "byRole reviewer costUsd", rev.CostUSD, 0.6)
	wantRate(t, "byRole reviewer costPerDelivered", rev.CostPerDelivered, 0.3)
	if b, ok := costBucket(got.ByNodeType, "task"); !ok || b.Delivered != 2 {
		t.Errorf("byNodeType task = %+v (found %v)", b, ok)
	}
	if b, ok := costBucket(got.ByAgent, "codex"); !ok || b.Runs != 3 {
		t.Errorf("byAgent codex = %+v (found %v)", b, ok)
	}

	w := got.Waste
	wantCost(t, "waste.failedCostUsd", w.FailedCostUSD, 0.5)
	wantCost(t, "waste.retriedCostUsd", w.RetriedCostUSD, 0.1)
	wantCost(t, "waste.abandonedCostUsd", w.AbandonedCostUSD, 0.5)
	wantCost(t, "waste.costUsd", w.CostUSD, 0.5)
	if len(w.Hotspots) == 0 || w.Hotspots[0].NodeID != "nC" || w.Hotspots[0].Runs != 2 || w.Hotspots[0].NodeTitle != "Title nC" {
		t.Errorf("hotspots = %+v, want nC first", w.Hotspots)
	} else {
		wantCost(t, "hotspot nC", w.Hotspots[0].CostUSD, 0.5)
	}

	// Another person's run on someone else's runner is not the viewer's.
	f.run(seedRun{id: "x1", node: "nX", owner: "u2", createdBy: "u2", role: "executor", cost: 9, created: at(11, 30), seconds: 60})
	mine, err := InsightsFor(context.Background(), f.db, Query{Project: "p", Viewer: "u1", Days: 30, Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	wantCost(t, "viewer u1 costUsd", mine.Totals.CostUSD, 3.6)
	everyone, _ := InsightsFor(context.Background(), f.db, Query{Project: "p", Days: 30, Now: testNow})
	wantCost(t, "admin costUsd", everyone.Totals.CostUSD, 12.6)
}

func TestInsightsEmpty(t *testing.T) {
	f := newFixture(t)
	got, err := InsightsFor(context.Background(), f.db, Query{Project: "p", Days: 7, Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	if got.Days != 7 || got.Totals.Units != 0 || got.Totals.CostPerDelivered != nil || got.Waste.Hotspots == nil {
		t.Fatalf("empty insights = %+v (hotspots must be [] not null)", got)
	}
}
