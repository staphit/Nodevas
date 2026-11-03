package runs

import (
	"context"
	"reflect"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
)

func TestRoleSettingsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	want := []runnerwire.RoleSettings{
		{Role: engine.RoleReviewer, Project: "", MaxConcurrent: 2, DailyCostUSD: 5},
		{Role: engine.RoleExecutor, Project: "web", MaxConcurrent: 1, DailyTokens: 2_000_000, DailyRuns: 20},
	}
	got, err := s.SetRoleSettings(ctx, "u-ann", want)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("stored = %+v", got)
	}
	read, err := s.RoleSettings(ctx, "u-ann")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(read, got) {
		t.Fatalf("read back %+v, want %+v", read, got)
	}
	if other, err := s.RoleSettings(ctx, "u-bea"); err != nil || len(other) != 0 {
		t.Fatalf("another owner sees %+v, %v", other, err)
	}
	for name, bad := range map[string][]runnerwire.RoleSettings{
		"unknown role": {{Role: "boss"}},
		"negative":     {{Role: engine.RoleTester, MaxConcurrent: -1}},
		"duplicate":    {{Role: engine.RoleTester}, {Role: engine.RoleTester}},
	} {
		if _, err := s.SetRoleSettings(ctx, "u-ann", bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Replacing with nothing clears.
	if _, err := s.SetRoleSettings(ctx, "u-ann", nil); err != nil {
		t.Fatal(err)
	}
	if read, _ := s.RoleSettings(ctx, "u-ann"); len(read) != 0 {
		t.Fatalf("after clearing: %+v", read)
	}
}

func TestRoleMaxConcurrentQueues(t *testing.T) {
	f := newRoleFixture(t, true)
	ctx := context.Background()
	if _, err := f.s.SetRoleSettings(ctx, "u-ann", []runnerwire.RoleSettings{{Role: engine.RoleReviewer, MaxConcurrent: 1}}); err != nil {
		t.Fatal(err)
	}
	create := func(role string) Record {
		r, err := f.s.Create(ctx, New{Project: ".", NodeID: "n1", NodeTitle: "T", RunnerID: f.runner.ID, RunnerName: "desk",
			OwnerID: "u-ann", Agent: "claude", Model: "opus", RepoID: "repo", RepoName: "repo", JobPrompt: "# T",
			CreatedBy: "u-ann", CreatedByName: "ann", Role: role})
		if err != nil {
			t.Fatal(err)
		}
		return f.d.Submit(ctx, r)
	}
	first := create(engine.RoleReviewer)
	second := create(engine.RoleReviewer)
	other := create(engine.RoleExecutor)
	if first.Status != runnerwire.RunOffered || other.Status != runnerwire.RunOffered {
		t.Fatalf("first %s, executor %s", first.Status, other.Status)
	}
	if second.Status != runnerwire.RunQueued {
		t.Fatalf("a second reviewer over the role's limit was %s", second.Status)
	}
	// The first ends: the second is offered.
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: first.ID, Base: "main", Branch: "b"}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Finish(ctx, f.runner.ID, first.ID, runnerwire.RunResult{Status: runnerwire.RunSucceeded}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.s.Get(ctx, second.ID); got.Status != runnerwire.RunOffered {
		t.Fatalf("after the first finished, the second is %s", got.Status)
	}
}

func TestRoleBudgetRefuses(t *testing.T) {
	f := newRoleFixture(t, true)
	ctx := context.Background()
	if _, err := f.s.SetRoleSettings(ctx, "u-ann", []runnerwire.RoleSettings{{Role: engine.RoleExecutor, DailyRuns: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.roleStart(t, lightStages(), nil, func(req *StartRequest) { req.Body.Role = engine.RoleExecutor }); err != nil {
		t.Fatal(err)
	}
	status, err := f.s.RoleBudgetStatus(ctx, "u-ann", engine.RoleExecutor)
	if err != nil || status.Exceeded == "" || status.Used.Runs != 1 {
		t.Fatalf("role budget = %+v, %v", status, err)
	}
	_, err = f.roleStart(t, lightStages(), nil, func(req *StartRequest) { req.Body.Role = engine.RoleExecutor })
	wantStartRefusal(t, err, StartBudget, "executor")
	// Another role is not held back by the executor's budget.
	if _, err := f.roleStart(t, lightStages(), nil, func(req *StartRequest) {
		req.Body.Role = engine.RolePlanner
		req.Body.Kind = runnerwire.JobPlan
	}); err != nil {
		t.Fatalf("planner after the executor budget: %v", err)
	}
}
