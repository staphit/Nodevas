package runs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

// groupHello returns a runner info with N repositories ("repo-1".."repo-N")
// and global-run capabilities enabled.
func groupHello(maxConcurrent int, repoNames ...string) runnerwire.Info {
	if len(repoNames) == 0 {
		repoNames = []string{"api", "web", "worker"}
	}
	repos := make([]runnerwire.Repo, 0, len(repoNames))
	for i, name := range repoNames {
		repos = append(repos, runnerwire.Repo{
			ID:     fmt.Sprintf("repo-%d", i+1),
			Name:   name,
			Remote: fmt.Sprintf("https://github.com/org/%s.git", name),
		})
	}
	return runnerwire.Info{
		Capabilities: []string{
			runnerwire.CapHeadless,
			runnerwire.CapInteractive,
			runnerwire.CapGlobalRuns,
			runnerwire.CapGlobalRunsOn,
		},
		MaxConcurrent: maxConcurrent,
		Agents: []runnerwire.Agent{{
			Kind: "claude", Installed: true,
			Models: []runnerwire.Model{{ID: "default", Default: true}, {ID: "opus"}},
		}},
		Repos: repos,
	}
}

func (f *stepUpFixture) groupRequest(task string, repoIDs ...string) GlobalGroupStartRequest {
	if len(repoIDs) == 0 {
		repoIDs = []string{"repo-1", "repo-2", "repo-3"}
	}
	return GlobalGroupStartRequest{
		Body: runnerwire.CreateGlobalGroupRequest{
			RunnerID: f.runner.ID,
			RepoIDs:  repoIDs,
			Agent:    "claude",
			Task:     task,
		},
		ActorID:   "u-ann",
		ActorName: "ann",
	}
}

func TestStartGlobalGroupCreatesNRunsInOneTx(t *testing.T) {
	f, log := newGlobalFixture(t, groupHello(2, "api", "web", "worker"))
	ctx := context.Background()

	req := f.groupRequest("Upgrade Go\n\nBump to 1.25 and fix tests.", "repo-1", "repo-2", "repo-3")
	req.Body.OpenPR = true
	res, err := f.d.StartGlobalGroup(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Group.ID == "" || res.Group.RunnerID != f.runner.ID || res.Group.OwnerID != "u-ann" ||
		res.Group.CreatedBy != "u-ann" || res.Group.CreatedByName != "ann" ||
		res.Group.Title != "Upgrade Go" || res.Group.Agent != "claude" || res.Group.Model != "default" ||
		res.Group.NoPR || res.Group.RepoCount != 3 || res.Group.HeldUntil != "" {
		t.Fatalf("group = %+v", res.Group)
	}
	if len(res.Runs) != 3 {
		t.Fatalf("len(runs) = %d, want 3", len(res.Runs))
	}
	// FIFO under maxConcurrent = 2: first two offered, third stays queued.
	if res.Runs[0].Status != runnerwire.RunOffered || res.Runs[1].Status != runnerwire.RunOffered ||
		res.Runs[2].Status != runnerwire.RunQueued {
		t.Fatalf("statuses = [%s, %s, %s], want [offered, offered, queued]",
			res.Runs[0].Status, res.Runs[1].Status, res.Runs[2].Status)
	}
	for i, r := range res.Runs {
		wantRepo := fmt.Sprintf("repo-%d", i+1)
		if !r.Global || r.GroupID != res.Group.ID || r.RepoID != wantRepo || r.NoPR ||
			r.Mode != runnerwire.ModeHeadless || r.Origin != runnerwire.OriginPerson || r.RepoKey == "" {
			t.Fatalf("run[%d] = %+v", i, r)
		}
	}

	// Stored group and members read back consistently.
	gotGroup, err := f.s.GetGroup(ctx, res.Group.ID)
	if err != nil || gotGroup.ID != res.Group.ID || gotGroup.RepoCount != 3 {
		t.Fatalf("GetGroup = %+v, %v", gotGroup, err)
	}
	gotRuns, err := f.s.GroupRuns(ctx, res.Group.ID)
	if err != nil || len(gotRuns) != 3 {
		t.Fatalf("GroupRuns = %d, %v", len(gotRuns), err)
	}

	// One audit entry, without any task text.
	if len(log.audited) != 1 {
		t.Fatalf("audit entries = %+v", log.audited)
	}
	entry := log.audited[0]
	detail := fmt.Sprint(entry.Detail)
	if entry.Action != "run.global.group" || entry.Target != res.Group.ID ||
		entry.Detail["group"] != res.Group.ID || entry.Detail["count"] != 3 ||
		strings.Contains(detail, "Bump to 1.25") || strings.Contains(detail, "Upgrade Go") {
		t.Fatalf("audit entry = %+v", entry)
	}
	if len(log.project) != 0 {
		t.Fatalf("project hooks notified: %v", log.project)
	}
}

func TestStartGlobalGroupOwnRunnerOnly(t *testing.T) {
	f, _ := newGlobalFixture(t, groupHello(2, "api", "web"))
	ctx := context.Background()

	req := f.groupRequest("Fix lint", "repo-1", "repo-2")
	req.ActorID = "u-bob"
	req.ActorName = "bob"
	_, err := f.d.StartGlobalGroup(ctx, req)
	var refused *StartError
	if !errors.As(err, &refused) || refused.Code != StartNotFound || refused.What != "runner" {
		t.Fatalf("err = %#v, want StartNotFound runner", err)
	}
	var groups, runs int
	_ = f.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_groups`).Scan(&groups)
	_ = f.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs`).Scan(&runs)
	if groups != 0 || runs != 0 {
		t.Fatalf("wrote %d groups, %d runs on refusal", groups, runs)
	}
}

func TestStartGlobalGroupRepoNotOnRunner(t *testing.T) {
	f, _ := newGlobalFixture(t, groupHello(2, "api", "web"))
	ctx := context.Background()

	_, err := f.d.StartGlobalGroup(ctx, f.groupRequest("Fix lint", "repo-1", "repo-missing"))
	var refused *StartError
	if !errors.As(err, &refused) || refused.Code != StartInvalid || refused.Field != "repoIds" {
		t.Fatalf("err = %#v, want StartInvalid field repoIds", err)
	}
}

func TestStartGlobalGroupRefusesDuplicateRepos(t *testing.T) {
	f, _ := newGlobalFixture(t, groupHello(2, "api", "web"))
	ctx := context.Background()

	_, err := f.d.StartGlobalGroup(ctx, f.groupRequest("Fix lint", "repo-1", "repo-1"))
	var refused *StartError
	if !errors.As(err, &refused) || refused.Code != StartInvalid || refused.Field != "repoIds" {
		t.Fatalf("err = %#v, want StartInvalid field repoIds", err)
	}
	// Also fewer than MinGroupRepos (2) is refused.
	_, err = f.d.StartGlobalGroup(ctx, f.groupRequest("Fix lint", "repo-1"))
	if !errors.As(err, &refused) || refused.Code != StartInvalid || refused.Field != "repoIds" {
		t.Fatalf("single repo err = %#v, want StartInvalid field repoIds", err)
	}
}

func TestStartGlobalGroupBudgetForN(t *testing.T) {
	f, _ := newGlobalFixture(t, groupHello(4, "api", "web", "worker", "cli"))
	ctx := context.Background()

	if err := f.s.SetBudget(ctx, "u-ann", runnerwire.Budget{DailyRuns: 3}); err != nil {
		t.Fatal(err)
	}
	// Consume 1 run first -> 2 left.
	if _, err := f.d.StartGlobal(ctx, GlobalStartRequest{
		Body: runnerwire.CreateGlobalRunRequest{
			RunnerID: f.runner.ID, RepoID: "repo-4", Agent: "claude", Task: "First",
		},
		ActorID: "u-ann", ActorName: "ann",
	}); err != nil {
		t.Fatal(err)
	}

	// Asking for 3 repos exceeds remaining 2 -> whole group refused with room == 2.
	_, err := f.d.StartGlobalGroup(ctx, f.groupRequest("Need three", "repo-1", "repo-2", "repo-3"))
	var refused *StartError
	if !errors.As(err, &refused) || refused.Code != StartBudget || refused.Budget.Exceeded != ExceededRuns {
		t.Fatalf("err = %#v, want StartBudget exceeded=runs", err)
	}
	if room := BudgetRoom(refused.Budget); room != 2 {
		t.Fatalf("room = %d, want 2", room)
	}
	if !strings.Contains(refused.Error(), "2") {
		t.Fatalf("budget error %q should mention room 2", refused.Error())
	}
	var groups, runsCount int
	_ = f.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_groups`).Scan(&groups)
	_ = f.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs`).Scan(&runsCount)
	if groups != 0 || runsCount != 1 {
		t.Fatalf("refused group wrote rows: groups=%d runs=%d", groups, runsCount)
	}

	// Asking for 2 repos fits the exact remaining room.
	res, err := f.d.StartGlobalGroup(ctx, f.groupRequest("Fits two", "repo-1", "repo-2"))
	if err != nil || len(res.Runs) != 2 {
		t.Fatalf("2-repo group = %+v, %v", res, err)
	}
}

func TestStartGlobalGroupRefusesInteractive(t *testing.T) {
	f, _ := newGlobalFixture(t, groupHello(2, "api", "web"))
	ctx := context.Background()

	req := f.groupRequest("Fix lint", "repo-1", "repo-2")
	req.Body.Mode = runnerwire.ModeInteractive
	_, err := f.d.StartGlobalGroup(ctx, req)
	var refused *StartError
	if !errors.As(err, &refused) || refused.Code != StartInvalid || refused.Reason != CodeGroupNeedsBackground {
		t.Fatalf("err = %#v, want StartInvalid reason %s", err, CodeGroupNeedsBackground)
	}
}

func TestStartGlobalGroupWritesUserEvents(t *testing.T) {
	f, _ := newGlobalFixture(t, groupHello(2, "api", "web", "worker"))
	ctx := context.Background()

	task := "Update CI workflow\n\nSwitch to Go 1.25."
	res, err := f.d.StartGlobalGroup(ctx, f.groupRequest(task, "repo-1", "repo-2", "repo-3"))
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range res.Runs {
		events, more, err := f.s.Events(ctx, r.ID, 0, 10)
		if err != nil || more || len(events) != 1 {
			t.Fatalf("run[%d] events = %+v, more=%v, err=%v", i, events, more, err)
		}
		if events[0].Seq != 0 || events[0].Type != runnerwire.EventUser || events[0].Text != task {
			t.Fatalf("run[%d] event[0] = %+v", i, events[0])
		}
	}
}

func TestStartGlobalGroupPromptListsSiblings(t *testing.T) {
	// Normal sibling list.
	f, _ := newGlobalFixture(t, groupHello(4, "api", "web", "worker"))
	ctx := context.Background()

	res, err := f.d.StartGlobalGroup(ctx, f.groupRequest("Upgrade dep", "repo-1", "repo-2", "repo-3"))
	if err != nil {
		t.Fatal(err)
	}
	wantLine := "This is one of 3 repositories changing together: api, web, worker"
	for i, r := range res.Runs {
		if !strings.Contains(r.JobPrompt, wantLine) {
			t.Fatalf("run[%d] prompt missing sibling line:\n%s", i, r.JobPrompt)
		}
	}

	// Truncated sibling list when repo names exceed 1 KiB.
	longNames := make([]string, 25)
	for i := range longNames {
		longNames[i] = fmt.Sprintf("service-%02d-%s", i+1, strings.Repeat("x", 60))
	}
	f2, _ := newGlobalFixture(t, groupHello(1, longNames...))
	repoIDs := make([]string, len(longNames))
	for i := range longNames {
		repoIDs[i] = fmt.Sprintf("repo-%d", i+1)
	}
	res2, err := f2.d.StartGlobalGroup(ctx, f2.groupRequest("Upgrade many", repoIDs...))
	if err != nil {
		t.Fatal(err)
	}
	prefix := "This is one of 25 repositories changing together: "
	prompt := res2.Runs[0].JobPrompt
	idx := strings.Index(prompt, prefix)
	if idx < 0 {
		t.Fatalf("prompt missing prefix %q:\n%s", prefix, prompt)
	}
	namesPart := prompt[idx+len(prefix):]
	if nl := strings.IndexByte(namesPart, '\n'); nl >= 0 {
		namesPart = namesPart[:nl]
	}
	if len(namesPart) > 1024 {
		t.Fatalf("sibling names length = %d, want <= 1024", len(namesPart))
	}
	if !strings.HasSuffix(namesPart, "…") {
		t.Fatalf("truncated sibling list should end with ellipsis: %q", namesPart)
	}
}

// Two groups started at once never take more runs than the budget has room
// for. Today this holds because the hub has one writer connection
// (db.OpenPostgres), which runs the two start transactions one after the
// other; the budget:<owner> advisory lock StartGlobalGroup takes is what
// would keep it true with more writers, and this test cannot tell the two
// apart.
func TestTwoConcurrentGroupsStayWithinBudget(t *testing.T) {
	f, _ := newGlobalFixture(t, groupHello(1, "api", "web", "worker", "cli", "docs", "infra"))
	ctx := context.Background()

	if err := f.s.SetBudget(ctx, "u-ann", runnerwire.Budget{DailyRuns: 5}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	start := make(chan struct{})
	sets := [][]string{
		{"repo-1", "repo-2", "repo-3"},
		{"repo-4", "repo-5", "repo-6"},
	}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			_, errs[idx] = f.d.StartGlobalGroup(ctx, f.groupRequest(fmt.Sprintf("Batch %d", idx), sets[idx]...))
		}(i)
	}
	close(start)
	wg.Wait()

	succeeded, budgetRefused := 0, 0
	for _, err := range errs {
		if err == nil {
			succeeded++
			continue
		}
		var refused *StartError
		if errors.As(err, &refused) && refused.Code == StartBudget && BudgetRoom(refused.Budget) == 2 {
			budgetRefused++
			continue
		}
		t.Fatalf("unexpected error: %v", err)
	}
	if succeeded != 1 || budgetRefused != 1 {
		t.Fatalf("succeeded=%d budgetRefused=%d, want 1 and 1 (errs=%v)", succeeded, budgetRefused, errs)
	}
	var totalRuns int
	if err := f.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE owner_id = 'u-ann'`).Scan(&totalRuns); err != nil {
		t.Fatal(err)
	}
	if totalRuns != 3 {
		t.Fatalf("total runs = %d, want 3 (no overshoot)", totalRuns)
	}
}

func TestPumpRechecksOnlyCostAndTokens(t *testing.T) {
	f, _ := newGlobalFixture(t, groupHello(1, "api", "web", "worker"))
	ctx := context.Background()

	// DailyRuns == 3 is filled completely by the 3-repo group itself.
	if err := f.s.SetBudget(ctx, "u-ann", runnerwire.Budget{DailyRuns: 3}); err != nil {
		t.Fatal(err)
	}
	res, err := f.d.StartGlobalGroup(ctx, f.groupRequest("Exact fit", "repo-1", "repo-2", "repo-3"))
	if err != nil {
		t.Fatal(err)
	}
	// First run is offered; accept and finish it.
	r0, r1, r2 := res.Runs[0], res.Runs[1], res.Runs[2]
	if r0.Status != runnerwire.RunOffered || r1.Status != runnerwire.RunQueued || r2.Status != runnerwire.RunQueued {
		t.Fatalf("initial statuses = %s, %s, %s", r0.Status, r1.Status, r2.Status)
	}
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: r0.ID, Base: "main", Branch: "agent/exact-fit-1"}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Finish(ctx, f.runner.ID, r0.ID, runnerwire.RunResult{Status: runnerwire.RunSucceeded}); err != nil {
		t.Fatal(err)
	}
	// DailyRuns is at 3/3 (Exceeded == "runs"), yet Pump MUST NOT cancel r1; it must offer r1!
	got1, err := f.s.Get(ctx, r1.ID)
	if err != nil || got1.Status != runnerwire.RunOffered {
		t.Fatalf("r1 after r0 finished = %s (err=%q, code=%q), want offered", got1.Status, got1.Error, got1.FailureCode)
	}
	got2, err := f.s.Get(ctx, r2.ID)
	if err != nil || got2.Status != runnerwire.RunQueued {
		t.Fatalf("r2 = %s, want queued", got2.Status)
	}
}

func TestPumpBudgetCancelStoresCode(t *testing.T) {
	for _, tc := range []struct {
		name   string
		budget runnerwire.Budget
		usage  runnerwire.Usage
	}{
		{
			name:   "cost limit",
			budget: runnerwire.Budget{DailyRuns: 10, DailyCostUSD: 1.0},
			usage:  runnerwire.Usage{InputTokens: 100, OutputTokens: 50, CostUSD: 1.25},
		},
		{
			name:   "token limit",
			budget: runnerwire.Budget{DailyRuns: 10, DailyTokens: 1000},
			usage:  runnerwire.Usage{InputTokens: 800, OutputTokens: 300, CostUSD: 0.01},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := newGlobalFixture(t, groupHello(1, "api", "web", "worker"))
			ctx := context.Background()
			if err := f.s.SetBudget(ctx, "u-ann", tc.budget); err != nil {
				t.Fatal(err)
			}
			res, err := f.d.StartGlobalGroup(ctx, f.groupRequest("Expensive", "repo-1", "repo-2", "repo-3"))
			if err != nil {
				t.Fatal(err)
			}
			r0, r1, r2 := res.Runs[0], res.Runs[1], res.Runs[2]
			if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: r0.ID, Base: "main", Branch: "agent/exp-1"}); err != nil {
				t.Fatal(err)
			}
			usage := tc.usage
			if err := f.d.Finish(ctx, f.runner.ID, r0.ID, runnerwire.RunResult{
				Status: runnerwire.RunSucceeded, Usage: &usage,
			}); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{r1.ID, r2.ID} {
				got, err := f.s.Get(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if got.Status != runnerwire.RunCancelled || got.FailureCode != FailureBudget {
					t.Fatalf("member %s: status=%s failureCode=%q err=%q, want cancelled/budget",
						id, got.Status, got.FailureCode, got.Error)
				}
			}
		})
	}
}

func TestQuotaOrRateLimitHoldsTheGroup(t *testing.T) {
	for _, kind := range []string{runnerwire.FailureQuota, runnerwire.FailureRateLimit} {
		t.Run(kind, func(t *testing.T) {
			f, _ := newGlobalFixture(t, groupHello(1, "api", "web", "worker"))
			ctx := context.Background()

			res, err := f.d.StartGlobalGroup(ctx, f.groupRequest("Rate limited", "repo-1", "repo-2", "repo-3"))
			if err != nil {
				t.Fatal(err)
			}
			r0, r1 := res.Runs[0], res.Runs[1]
			if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: r0.ID, Base: "main", Branch: "agent/rl-1"}); err != nil {
				t.Fatal(err)
			}
			reset := f.clock.Add(20 * time.Minute).UTC().Format(time.RFC3339)
			if err := f.d.Finish(ctx, f.runner.ID, r0.ID, runnerwire.RunResult{
				Status:      runnerwire.RunFailed,
				FailureKind: kind,
				Error:       "usage limit reached",
				ResetAt:     reset,
			}); err != nil {
				t.Fatal(err)
			}
			// Group is now held in the future; r1 was NOT offered by Finish's Pump.
			g, err := f.s.GetGroup(ctx, res.Group.ID)
			if err != nil || g.HeldUntil == "" {
				t.Fatalf("group after %s = %+v, %v", kind, g, err)
			}
			held, err := f.s.GroupHeld(ctx, res.Group.ID, f.clock)
			if err != nil || !held {
				t.Fatalf("GroupHeld = %v, %v, want true", held, err)
			}
			got1, err := f.s.Get(ctx, r1.ID)
			if err != nil || got1.Status != runnerwire.RunQueued {
				t.Fatalf("r1 after %s = %s, want queued", kind, got1.Status)
			}
		})
	}
}

func TestExpiredHoldIsPumpedOnTick(t *testing.T) {
	f, _ := newGlobalFixture(t, groupHello(1, "api", "web"))
	ctx := context.Background()

	res, err := f.d.StartGlobalGroup(ctx, f.groupRequest("Wait for reset", "repo-1", "repo-2"))
	if err != nil {
		t.Fatal(err)
	}
	r0, r1 := res.Runs[0], res.Runs[1]
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: r0.ID, Base: "main", Branch: "agent/w-1"}); err != nil {
		t.Fatal(err)
	}
	reset := f.clock.Add(10 * time.Minute).UTC().Format(time.RFC3339)
	if err := f.d.Finish(ctx, f.runner.ID, r0.ID, runnerwire.RunResult{
		Status:      runnerwire.RunFailed,
		FailureKind: runnerwire.FailureQuota,
		Error:       "quota exceeded",
		ResetAt:     reset,
	}); err != nil {
		t.Fatal(err)
	}
	// Tick before expiry keeps r1 queued.
	f.clock = f.clock.Add(5 * time.Minute)
	f.d.releaseGroupHolds(ctx)
	if got1, _ := f.s.Get(ctx, r1.ID); got1.Status != runnerwire.RunQueued {
		t.Fatalf("r1 offered early: %s", got1.Status)
	}

	// Advance past reset + slack and tick: hold clears and r1 is offered.
	f.clock = f.clock.Add(10 * time.Minute)
	f.d.releaseGroupHolds(ctx)
	g, err := f.s.GetGroup(ctx, res.Group.ID)
	if err != nil || g.HeldUntil != "" {
		t.Fatalf("group after expiry = %+v, %v", g, err)
	}
	got1, err := f.s.Get(ctx, r1.ID)
	if err != nil || got1.Status != runnerwire.RunOffered {
		t.Fatalf("r1 after expired hold tick = %s, want offered", got1.Status)
	}
}

func TestResumeGroupClearsHold(t *testing.T) {
	f, _ := newGlobalFixture(t, groupHello(1, "api", "web"))
	ctx := context.Background()

	res, err := f.d.StartGlobalGroup(ctx, f.groupRequest("Manual resume", "repo-1", "repo-2"))
	if err != nil {
		t.Fatal(err)
	}
	r0, r1 := res.Runs[0], res.Runs[1]
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: r0.ID, Base: "main", Branch: "agent/m-1"}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Finish(ctx, f.runner.ID, r0.ID, runnerwire.RunResult{
		Status:      runnerwire.RunFailed,
		FailureKind: runnerwire.FailureRateLimit,
		Error:       "429 rate limit",
	}); err != nil {
		t.Fatal(err)
	}
	if got1, _ := f.s.Get(ctx, r1.ID); got1.Status != runnerwire.RunQueued {
		t.Fatalf("r1 before resume = %s, want queued", got1.Status)
	}

	resumed, err := f.d.ResumeGroup(ctx, res.Group.ID)
	if err != nil || resumed.HeldUntil != "" {
		t.Fatalf("ResumeGroup = %+v, %v", resumed, err)
	}
	got1, err := f.s.Get(ctx, r1.ID)
	if err != nil || got1.Status != runnerwire.RunOffered {
		t.Fatalf("r1 after ResumeGroup = %s, want offered", got1.Status)
	}
}

func TestCancelGroup(t *testing.T) {
	f, _ := newGlobalFixture(t, groupHello(1, "api", "web", "worker", "other"))
	ctx := context.Background()

	// Unrelated global run on another repo must not be cancelled.
	other, err := f.d.StartGlobal(ctx, GlobalStartRequest{
		Body: runnerwire.CreateGlobalRunRequest{
			RunnerID: f.runner.ID, RepoID: "repo-4", Agent: "claude", Task: "Unrelated",
		},
		ActorID: "u-ann", ActorName: "ann",
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.d.StartGlobalGroup(ctx, f.groupRequest("Cancel me", "repo-1", "repo-2", "repo-3"))
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := f.d.CancelGroup(ctx, res.Group.ID, "ann")
	if err != nil || len(cancelled) != 3 {
		t.Fatalf("CancelGroup = %d runs, %v", len(cancelled), err)
	}
	for _, r := range cancelled {
		if r.Status != runnerwire.RunCancelled {
			t.Fatalf("member %s status = %s, want cancelled", r.ID, r.Status)
		}
	}
	if gotOther, _ := f.s.Get(ctx, other.ID); gotOther.Status == runnerwire.RunCancelled {
		t.Fatalf("unrelated global run was cancelled")
	}
}

func TestStartGlobalBusyShapeAndUserEvent(t *testing.T) {
	f, _ := newGlobalFixture(t, groupHello(2, "api", "web"))
	ctx := context.Background()

	// Bad onBusy value is rejected with field "onBusy".
	bad := GlobalStartRequest{
		Body: runnerwire.CreateGlobalRunRequest{
			RunnerID: f.runner.ID, RepoID: "repo-1", Agent: "claude", Task: "Fix", OnBusy: runnerwire.OnBusySkip,
		},
		ActorID: "u-ann", ActorName: "ann",
	}
	_, err := f.d.StartGlobal(ctx, bad)
	var refused *StartError
	if !errors.As(err, &refused) || refused.Code != StartInvalid || refused.Field != "onBusy" {
		t.Fatalf("onBusy=skip on single global run: %#v", err)
	}

	// onBusy == "continue" delegates to continueBusy before any tx (Wave 0 stub returns StartInvalid).
	cont := GlobalStartRequest{
		Body: runnerwire.CreateGlobalRunRequest{
			RunnerID: f.runner.ID, RepoID: "repo-1", Agent: "claude", Task: "Fix", OnBusy: runnerwire.OnBusyContinue,
		},
		ActorID: "u-ann", ActorName: "ann",
	}
	if _, err := f.d.StartGlobal(ctx, cont); !errors.As(err, &refused) || refused.Code != StartInvalid {
		t.Fatalf("onBusy=continue = %#v, want StartInvalid from continueBusy stub", err)
	}

	// Normal global start sets RepoKey and writes seq-0 user event.
	r, err := f.d.StartGlobal(ctx, GlobalStartRequest{
		Body: runnerwire.CreateGlobalRunRequest{
			RunnerID: f.runner.ID, RepoID: "repo-1", Agent: "claude", Task: "Hello world",
		},
		ActorID: "u-ann", ActorName: "ann",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.RepoKey == "" {
		t.Fatal("StartGlobal did not record RepoKey")
	}
	events, _, err := f.s.Events(ctx, r.ID, 0, 10)
	if err != nil || len(events) != 1 || events[0].Seq != 0 || events[0].Type != runnerwire.EventUser || events[0].Text != "Hello world" {
		t.Fatalf("events = %+v, %v", events, err)
	}
}
