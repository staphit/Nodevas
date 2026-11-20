package runs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
)

func contestHello(maxConcurrent int, caps ...string) runnerwire.Info {
	if caps == nil {
		caps = []string{
			runnerwire.CapHeadless,
			runnerwire.CapInteractive,
			runnerwire.CapGlobalRuns,
			runnerwire.CapGlobalRunsOn,
			runnerwire.CapRunWorkspace,
			runnerwire.CapWorkspaceActions,
			runnerwire.CapCoord,
		}
	}
	return runnerwire.Info{
		Capabilities:  caps,
		MaxConcurrent: maxConcurrent,
		Agents: []runnerwire.Agent{
			{Kind: "claude", Installed: true, Models: []runnerwire.Model{{ID: "opus", Default: true}, {ID: "sonnet"}}},
			{Kind: "codex", Installed: true, Models: []runnerwire.Model{{ID: "gpt-5", Default: true}}},
			{Kind: "gemini", Installed: true, Models: []runnerwire.Model{{ID: "pro", Default: true}}},
		},
		Repos: []runnerwire.Repo{
			{ID: "repo", Name: "app", Remote: "https://github.com/org/app.git"},
		},
	}
}

// workspaceConn records both offers and workspace actions sent to a runner.
type workspaceConn struct {
	mu      sync.Mutex
	offers  []runnerwire.Job
	actions []runnerwire.WorkspaceActionJob
	cancels []string
	onAct   func(runnerwire.WorkspaceActionJob)
}

func (c *workspaceConn) Bye(string) {}

func (c *workspaceConn) Send(env runnerwire.Envelope) error {
	c.mu.Lock()
	var cb func(runnerwire.WorkspaceActionJob)
	var act runnerwire.WorkspaceActionJob
	switch env.Type {
	case runnerwire.TypeOffer:
		if env.Job != nil {
			c.offers = append(c.offers, *env.Job)
		}
	case runnerwire.TypeWorkspaceAction:
		if env.WorkspaceAction != nil {
			act = *env.WorkspaceAction
			c.actions = append(c.actions, act)
			cb = c.onAct
		}
	case runnerwire.TypeCancel:
		c.cancels = append(c.cancels, env.RunID)
	}
	c.mu.Unlock()
	if cb != nil {
		go cb(act)
	}
	return nil
}

func (c *workspaceConn) offerFor(t *testing.T, runID string) runnerwire.Job {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.offers) - 1; i >= 0; i-- {
		if c.offers[i].RunID == runID {
			return c.offers[i]
		}
	}
	t.Fatalf("no offer for %s in %+v", runID, c.offers)
	return runnerwire.Job{}
}

func (c *workspaceConn) actionFor(runID string) (runnerwire.WorkspaceActionJob, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.actions) - 1; i >= 0; i-- {
		if c.actions[i].RunID == runID {
			return c.actions[i], true
		}
	}
	return runnerwire.WorkspaceActionJob{}, false
}

type contestFixture struct {
	*stepUpFixture
	log    *hookLog
	wconn  *workspaceConn
	detach func()
}

func newContestFixture(t *testing.T, info runnerwire.Info) *contestFixture {
	t.Helper()
	f, log := newGlobalFixture(t, info)
	wconn := &workspaceConn{}
	detach := f.d.reg.Attach(f.runner.ID, wconn)
	return &contestFixture{stepUpFixture: f, log: log, wconn: wconn, detach: detach}
}

func (f *contestFixture) nodeRequest(edit func(*StartContestRequest)) StartContestRequest {
	req := StartContestRequest{
		Body: runnerwire.CreateContestRequest{
			NodeID: "n1",
			RepoID: "repo",
			Base:   "main",
			Prompt: "Keep it clean.",
			Entries: []runnerwire.ContestEntrySpec{
				{RunnerID: f.runner.ID, Agent: "claude", Model: "opus"},
				{RunnerID: f.runner.ID, Agent: "codex", Model: "gpt-5"},
			},
		},
		Project:   ".",
		ActorID:   "u-ann",
		ActorName: "ann",
		LoadNode: func() (NodeInfo, bool, error) {
			return NodeInfo{
				Title:     "Fix login",
				Content:   "The login button fails on Safari.",
				Type:      "task",
				AgentMode: engine.AgentDispatch,
			}, true, nil
		},
	}
	if edit != nil {
		edit(&req)
	}
	return req
}

func (f *contestFixture) globalRequest(edit func(*StartGlobalContestRequest)) StartGlobalContestRequest {
	req := StartGlobalContestRequest{
		Body: runnerwire.CreateGlobalContestRequest{
			RunnerID: f.runner.ID,
			RepoID:   "repo",
			Base:     "main",
			Task:     "Fix login\n\nSecret note: Safari click fails.",
			Entries: []runnerwire.ContestEntrySpec{
				{Agent: "claude", Model: "opus"},
				{Agent: "codex", Model: "gpt-5"},
			},
		},
		ActorID:   "u-ann",
		ActorName: "ann",
	}
	if edit != nil {
		edit(&req)
	}
	return req
}

func (f *contestFixture) finishEntry(t *testing.T, r Record, commit, summary string, status string) Record {
	t.Helper()
	ctx := context.Background()
	branch := "agent/fix-login-" + r.ID[:8]
	if err := f.d.Accept(ctx, r.RunnerID, runnerwire.Accept{RunID: r.ID, Base: "main", Branch: branch}); err != nil {
		t.Fatalf("Accept(%s): %v", r.ID, err)
	}
	if commit != "" {
		if err := f.d.Changes(ctx, r.RunnerID, r.ID, runnerwire.RunChanges{
			Commit:    commit,
			Base:      "main",
			Additions: 5,
			Deletions: 1,
			Files:     []runnerwire.FileChange{{Path: "login.go", Status: runnerwire.FileModified, Additions: 5, Deletions: 1}},
			Patch:     "diff --git a/login.go b/login.go\n--- a/login.go\n+++ b/login.go\n@@ -1 +1 @@\n-old\n+new\n",
		}); err != nil {
			t.Fatalf("Changes(%s): %v", r.ID, err)
		}
	}
	if err := f.d.Finish(ctx, r.RunnerID, r.ID, runnerwire.RunResult{Status: status, Summary: summary}); err != nil {
		t.Fatalf("Finish(%s): %v", r.ID, err)
	}
	got, err := f.s.Get(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestContestStartsNEntriesInOneTx(t *testing.T) {
	f := newContestFixture(t, contestHello(4))
	ctx := context.Background()

	// 3-entry node contest.
	res, err := f.d.StartContest(ctx, f.nodeRequest(func(r *StartContestRequest) {
		r.Body.Entries = append(r.Body.Entries, runnerwire.ContestEntrySpec{
			RunnerID: f.runner.ID, Agent: "gemini", Model: "pro",
		})
	}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Contest.ID == "" || res.Contest.State != runnerwire.ContestRunning ||
		res.Contest.EntryCount != 3 || res.Contest.Global || res.Contest.Project != "." ||
		res.Contest.NodeID != "n1" || res.Contest.Title != "Fix login" || res.Contest.CreatedBy != "u-ann" {
		t.Fatalf("node contest = %+v", res.Contest)
	}
	if len(res.Runs) != 3 {
		t.Fatalf("len(runs) = %d, want 3", len(res.Runs))
	}
	wantLabels := []string{"A", "B", "C"}
	for i, r := range res.Runs {
		if r.ContestID != res.Contest.ID || r.ContestLabel != wantLabels[i] || r.ContestOf != 3 {
			t.Fatalf("run[%d] contest fields = id=%q label=%q of=%d", i, r.ContestID, r.ContestLabel, r.ContestOf)
		}
		if r.Status != runnerwire.RunOffered {
			t.Fatalf("run[%d] status = %s, want offered", i, r.Status)
		}
	}
	gotContest, err := f.s.GetContest(ctx, res.Contest.ID)
	if err != nil || gotContest.ID != res.Contest.ID || gotContest.EntryCount != 3 {
		t.Fatalf("GetContest = %+v, %v", gotContest, err)
	}
	gotRuns, err := f.s.ContestRuns(ctx, res.Contest.ID)
	if err != nil || len(gotRuns) != 3 {
		t.Fatalf("ContestRuns = %d, %v", len(gotRuns), err)
	}

	// 2-entry global contest writes seq-0 user events and audits without task text.
	gf := newContestFixture(t, contestHello(4))
	gf.log.audited = nil
	gres, err := gf.d.StartGlobalContest(ctx, gf.globalRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !gres.Contest.Global || gres.Contest.Project != "" || gres.Contest.NodeID != "" || len(gres.Runs) != 2 {
		t.Fatalf("global contest = %+v, len(runs)=%d", gres.Contest, len(gres.Runs))
	}
	for _, r := range gres.Runs {
		if !r.Global || r.ContestID != gres.Contest.ID || !r.NoPR {
			t.Fatalf("global contest run = %+v", r)
		}
		events, _, err := gf.s.Events(ctx, r.ID, 0, 5)
		if err != nil || len(events) != 1 || events[0].Seq != 0 || events[0].Type != runnerwire.EventUser {
			t.Fatalf("global contest seq-0 user event = %+v, %v", events, err)
		}
	}
	if len(gf.log.audited) != 1 || gf.log.audited[0].Action != "run.contest.start" {
		t.Fatalf("audit = %+v", gf.log.audited)
	}
	if detail := fmt.Sprint(gf.log.audited[0].Detail); strings.Contains(detail, "Secret note") || strings.Contains(detail, "Fix login") {
		t.Fatalf("audit leaked task text: %s", detail)
	}
}

func TestContestBudgetWhole(t *testing.T) {
	f := newContestFixture(t, contestHello(4))
	ctx := context.Background()

	if err := f.s.SetBudget(ctx, "u-ann", runnerwire.Budget{DailyRuns: 2}); err != nil {
		t.Fatal(err)
	}

	// 2 entries + 1 reviewer needs 3 runs, but room is 2 -> refused whole, 0 rows written.
	_, err := f.d.StartContest(ctx, f.nodeRequest(func(r *StartContestRequest) {
		r.Body.Reviewer = &runnerwire.ContestEntrySpec{RunnerID: f.runner.ID, Agent: "gemini", Model: "pro"}
	}))
	var se *StartError
	if !errors.As(err, &se) || se.Code != StartBudget || BudgetRoom(se.Budget) != 2 {
		t.Fatalf("2+1 over budget=2: err = %#v, want StartBudget room=2", err)
	}
	var contests, runsCount int
	_ = f.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_contests`).Scan(&contests)
	_ = f.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs`).Scan(&runsCount)
	if contests != 0 || runsCount != 0 {
		t.Fatalf("budget refusal left %d contests and %d runs", contests, runsCount)
	}

	// 2 entries without reviewer needs 2 runs -> fits budget=2 and succeeds.
	res, err := f.d.StartContest(ctx, f.nodeRequest(nil))
	if err != nil {
		t.Fatalf("2 entries with budget=2: %v", err)
	}
	if len(res.Runs) != 2 {
		t.Fatalf("len(runs) = %d, want 2", len(res.Runs))
	}
}

func TestTwoConcurrentContestsCannotOvershootBudget(t *testing.T) {
	f := newContestFixture(t, contestHello(6))
	ctx := context.Background()

	if err := f.s.SetBudget(ctx, "u-ann", runnerwire.Budget{DailyRuns: 3}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = f.d.StartGlobalContest(ctx, f.globalRequest(nil))
		}(i)
	}
	wg.Wait()

	succeeded := 0
	budgetRefused := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
			continue
		}
		var se *StartError
		if errors.As(err, &se) && se.Code == StartBudget {
			budgetRefused++
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if succeeded != 1 || budgetRefused != 1 {
		t.Fatalf("succeeded=%d budgetRefused=%d, want 1 and 1 (errs=%v)", succeeded, budgetRefused, errs)
	}
	var totalRuns int
	if err := f.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs`).Scan(&totalRuns); err != nil {
		t.Fatal(err)
	}
	if totalRuns != 2 {
		t.Fatalf("total runs = %d, want 2 (must not overshoot dailyRuns=3)", totalRuns)
	}
}

func TestContestRefusesStagedPipelineInteractive(t *testing.T) {
	f := newContestFixture(t, contestHello(4))
	ctx := context.Background()

	wantShape := func(name string, err error) {
		t.Helper()
		var se *StartError
		if !errors.As(err, &se) || se.Code != StartInvalid || se.Reason != CodeContestShape {
			t.Fatalf("%s: err = %#v, want StartInvalid reason=%s", name, err, CodeContestShape)
		}
	}

	// Staged workflow on node -> CONTEST_SHAPE.
	_, err := f.d.StartContest(ctx, f.nodeRequest(func(r *StartContestRequest) {
		r.LoadNode = func() (NodeInfo, bool, error) {
			return NodeInfo{
				Title:     "Staged",
				Type:      "task",
				AgentMode: engine.AgentDispatch,
				Workflow:  standardStages(),
			}, true, nil
		}
	}))
	wantShape("staged workflow", err)

	// Pipeline -> CONTEST_SHAPE.
	_, err = f.d.StartContest(ctx, f.nodeRequest(func(r *StartContestRequest) {
		r.Pipeline = &runnerwire.Pipeline{Implement: runnerwire.AgentModel{Agent: "claude"}}
	}))
	wantShape("pipeline", err)

	// Fallbacks -> CONTEST_SHAPE.
	_, err = f.d.StartContest(ctx, f.nodeRequest(func(r *StartContestRequest) {
		r.Fallbacks = []runnerwire.AgentModel{{Agent: "codex"}}
	}))
	wantShape("fallbacks", err)

	// Interactive mode (node and global) -> CONTEST_SHAPE.
	_, err = f.d.StartContest(ctx, f.nodeRequest(func(r *StartContestRequest) {
		r.Mode = runnerwire.ModeInteractive
	}))
	wantShape("node interactive", err)

	_, err = f.d.StartGlobalContest(ctx, f.globalRequest(func(r *StartGlobalContestRequest) {
		r.Mode = runnerwire.ModeInteractive
	}))
	wantShape("global interactive", err)

	// Duplicate entries -> CONTEST_SHAPE.
	_, err = f.d.StartContest(ctx, f.nodeRequest(func(r *StartContestRequest) {
		r.Body.Entries = []runnerwire.ContestEntrySpec{
			{RunnerID: f.runner.ID, Agent: "claude", Model: "opus"},
			{RunnerID: f.runner.ID, Agent: "claude", Model: ""}, // resolves to default "opus"
		}
	}))
	wantShape("duplicate entry", err)

	// Entry count outside 2..3 -> StartInvalid field entries.
	_, err = f.d.StartContest(ctx, f.nodeRequest(func(r *StartContestRequest) {
		r.Body.Entries = r.Body.Entries[:1]
	}))
	var se *StartError
	if !errors.As(err, &se) || se.Code != StartInvalid || se.Field != "entries" {
		t.Fatalf("1 entry: err = %#v, want StartInvalid field=entries", err)
	}
}

func TestContestOwnRunnersOnly(t *testing.T) {
	f := newContestFixture(t, contestHello(4))
	ctx := context.Background()

	// Create another runner owned by bob.
	bobRunner, _, err := f.d.reg.Create(ctx, identity.Actor{ID: "u-bob", Name: "bob"}, "bob-pc")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.d.reg.SetHello(ctx, bobRunner.ID, contestHello(4)); err != nil {
		t.Fatal(err)
	}

	// Ann cannot use Bob's runner as an entry.
	_, err = f.d.StartContest(ctx, f.nodeRequest(func(r *StartContestRequest) {
		r.Body.Entries[1].RunnerID = bobRunner.ID
	}))
	var se *StartError
	if !errors.As(err, &se) || se.Code != StartNotFound || se.What != "runner" {
		t.Fatalf("foreign entry runner: err = %#v, want StartNotFound runner", err)
	}

	// Ann cannot use Bob's runner as the reviewer.
	_, err = f.d.StartContest(ctx, f.nodeRequest(func(r *StartContestRequest) {
		r.Body.Reviewer = &runnerwire.ContestEntrySpec{RunnerID: bobRunner.ID, Agent: "claude", Model: "opus"}
	}))
	if !errors.As(err, &se) || se.Code != StartNotFound || se.What != "runner" {
		t.Fatalf("foreign reviewer runner: err = %#v, want StartNotFound runner", err)
	}

	// Global contest on Bob's runner is refused.
	_, err = f.d.StartGlobalContest(ctx, f.globalRequest(func(r *StartGlobalContestRequest) {
		r.Body.RunnerID = bobRunner.ID
	}))
	if !errors.As(err, &se) || se.Code != StartNotFound || se.What != "runner" {
		t.Fatalf("foreign global runner: err = %#v, want StartNotFound runner", err)
	}

	var contests, runsCount int
	_ = f.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_contests`).Scan(&contests)
	_ = f.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs`).Scan(&runsCount)
	if contests != 0 || runsCount != 0 {
		t.Fatalf("wrote %d contests, %d runs on foreign runner refusal", contests, runsCount)
	}
}

func TestContestEntriesForcedNoPRAndCoordinated(t *testing.T) {
	f := newContestFixture(t, contestHello(4))
	ctx := context.Background()

	res, err := f.d.StartContest(ctx, f.nodeRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range res.Runs {
		if !r.NoPR || r.Mode != runnerwire.ModeHeadless || r.Kind != runnerwire.JobImplement ||
			r.PipelineID != "" || len(r.Fallbacks) != 0 {
			t.Fatalf("run[%d] = %+v, want NoPR headless implement without pipeline/fallbacks", i, r)
		}
		job := f.wconn.offerFor(t, r.ID)
		if !job.NoPR || job.Contest == nil || job.Contest.ID != res.Contest.ID ||
			job.Contest.Label != r.ContestLabel || job.Contest.Of != 2 {
			t.Fatalf("job[%d] = %+v", i, job)
		}
	}

	// Runner without CapCoord is refused with StartOutdated (412 RUNNER_OUTDATED).
	oldRunner, _, err := f.d.reg.Create(ctx, identity.Actor{ID: "u-ann", Name: "ann"}, "old-pc")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.d.reg.SetHello(ctx, oldRunner.ID, contestHello(4,
		runnerwire.CapHeadless, runnerwire.CapGlobalRuns, runnerwire.CapGlobalRunsOn,
	)); err != nil {
		t.Fatal(err)
	}
	_, err = f.d.StartContest(ctx, f.nodeRequest(func(r *StartContestRequest) {
		r.Body.Entries[1].RunnerID = oldRunner.ID
	}))
	var se *StartError
	if !errors.As(err, &se) || se.Code != StartOutdated {
		t.Fatalf("entry without CapCoord: err = %#v, want StartOutdated", err)
	}
	_, err = f.d.StartContest(ctx, f.nodeRequest(func(r *StartContestRequest) {
		r.Body.Reviewer = &runnerwire.ContestEntrySpec{RunnerID: oldRunner.ID, Agent: "claude", Model: "opus"}
	}))
	if !errors.As(err, &se) || se.Code != StartOutdated {
		t.Fatalf("reviewer without CapCoord: err = %#v, want StartOutdated", err)
	}
	_, err = f.d.StartGlobalContest(ctx, f.globalRequest(func(r *StartGlobalContestRequest) {
		r.Body.RunnerID = oldRunner.ID
	}))
	if !errors.As(err, &se) || se.Code != StartOutdated {
		t.Fatalf("global without CapCoord: err = %#v, want StartOutdated", err)
	}
}

func TestContestReviewerStartsWhenTwoSucceeded(t *testing.T) {
	f := newContestFixture(t, contestHello(4))
	ctx := context.Background()

	res, err := f.d.StartContest(ctx, f.nodeRequest(func(r *StartContestRequest) {
		r.Body.Reviewer = &runnerwire.ContestEntrySpec{RunnerID: f.runner.ID, Agent: "gemini", Model: "pro"}
	}))
	if err != nil {
		t.Fatal(err)
	}

	// Entry A finishes with a commit -> contest still running, no reviewer yet.
	f.finishEntry(t, res.Runs[0], "1111111111111111111111111111111111111111", "Approach A", runnerwire.RunSucceeded)
	mid, err := f.s.GetContest(ctx, res.Contest.ID)
	if err != nil || mid.State != runnerwire.ContestRunning || mid.JudgeRunID != "" {
		t.Fatalf("after 1 entry: contest = %+v, %v", mid, err)
	}

	// Entry B finishes with a commit -> contest moves to judging and starts compare reviewer run.
	f.finishEntry(t, res.Runs[1], "2222222222222222222222222222222222222222", "Approach B", runnerwire.RunSucceeded)
	judging, err := f.s.GetContest(ctx, res.Contest.ID)
	if err != nil || judging.State != runnerwire.ContestJudging || judging.JudgeRunID == "" {
		t.Fatalf("after 2 entries: contest = %+v, %v", judging, err)
	}
	judgeRun, err := f.s.Get(ctx, judging.JudgeRunID)
	if err != nil {
		t.Fatal(err)
	}
	if judgeRun.Kind != runnerwire.JobCompare || judgeRun.ContestLabel != "J" || judgeRun.ContestID != res.Contest.ID ||
		!judgeRun.NoPR || judgeRun.Mode != runnerwire.ModeHeadless || judgeRun.Origin != runnerwire.OriginPipeline ||
		judgeRun.Agent != "gemini" || judgeRun.Model != "pro" || judgeRun.Global {
		t.Fatalf("judgeRun = %+v", judgeRun)
	}
	job := f.wconn.offerFor(t, judgeRun.ID)
	if job.Kind != runnerwire.JobCompare || !job.NoPR || !strings.Contains(job.Prompt, "## Entry A") ||
		!strings.Contains(job.Prompt, "## Entry B") {
		t.Fatalf("judge offer = %+v", job)
	}

	// Reviewer finishes with fenced JSON scores -> contest becomes ready with parsed scores.
	if err := f.d.Accept(ctx, judgeRun.RunnerID, runnerwire.Accept{RunID: judgeRun.ID, Base: "main"}); err != nil {
		t.Fatal(err)
	}
	reviewSummary := "Both work, A is cleaner.\n```json\n" +
		`{"scores":[{"entry":"A","score":9,"why":"clean"},{"entry":"B","score":7,"why":"verbose"}],"pick":"A"}` + "\n```"
	if err := f.d.Finish(ctx, judgeRun.RunnerID, judgeRun.ID, runnerwire.RunResult{
		Status: runnerwire.RunSucceeded, Summary: reviewSummary,
	}); err != nil {
		t.Fatal(err)
	}
	ready, err := f.s.GetContest(ctx, res.Contest.ID)
	if err != nil || ready.State != runnerwire.ContestReady || ready.Scores == nil || ready.Scores.Pick != "A" || len(ready.Scores.Scores) != 2 {
		t.Fatalf("after reviewer finish: contest = %+v, %v", ready, err)
	}
}

func TestContestSkipsReviewerWhenFewerThanTwo(t *testing.T) {
	f := newContestFixture(t, contestHello(4))
	ctx := context.Background()

	// Case 1: 1 succeeded with commit, 1 failed -> skips reviewer, goes straight to ready.
	res1, err := f.d.StartContest(ctx, f.nodeRequest(func(r *StartContestRequest) {
		r.Body.Reviewer = &runnerwire.ContestEntrySpec{RunnerID: f.runner.ID, Agent: "gemini", Model: "pro"}
	}))
	if err != nil {
		t.Fatal(err)
	}
	f.finishEntry(t, res1.Runs[0], "1111111111111111111111111111111111111111", "Done", runnerwire.RunSucceeded)
	f.finishEntry(t, res1.Runs[1], "", "Crashed", runnerwire.RunFailed)

	got1, err := f.s.GetContest(ctx, res1.Contest.ID)
	if err != nil || got1.State != runnerwire.ContestReady || got1.JudgeRunID != "" {
		t.Fatalf("1 succeeded + 1 failed: contest = %+v, %v", got1, err)
	}

	// Case 2: 1 succeeded with commit, 1 succeeded with NO commit -> fewer than 2 with commit -> skips reviewer.
	res2, err := f.d.StartContest(ctx, f.nodeRequest(func(r *StartContestRequest) {
		r.Body.Reviewer = &runnerwire.ContestEntrySpec{RunnerID: f.runner.ID, Agent: "gemini", Model: "pro"}
	}))
	if err != nil {
		t.Fatal(err)
	}
	f.finishEntry(t, res2.Runs[0], "3333333333333333333333333333333333333333", "Done", runnerwire.RunSucceeded)
	f.finishEntry(t, res2.Runs[1], "", "Nothing to change", runnerwire.RunSucceeded)

	got2, err := f.s.GetContest(ctx, res2.Contest.ID)
	if err != nil || got2.State != runnerwire.ContestReady || got2.JudgeRunID != "" {
		t.Fatalf("1 with commit + 1 empty: contest = %+v, %v", got2, err)
	}
}

func TestGlobalContestReviewerIsGlobalCompareRow(t *testing.T) {
	f := newContestFixture(t, contestHello(4))
	ctx := context.Background()

	res, err := f.d.StartGlobalContest(ctx, f.globalRequest(func(r *StartGlobalContestRequest) {
		r.Body.Reviewer = &runnerwire.ContestEntrySpec{Agent: "gemini", Model: "pro"}
	}))
	if err != nil {
		t.Fatal(err)
	}
	f.finishEntry(t, res.Runs[0], "1111111111111111111111111111111111111111", "Global A", runnerwire.RunSucceeded)
	f.finishEntry(t, res.Runs[1], "2222222222222222222222222222222222222222", "Global B", runnerwire.RunSucceeded)

	judging, err := f.s.GetContest(ctx, res.Contest.ID)
	if err != nil || judging.State != runnerwire.ContestJudging || judging.JudgeRunID == "" {
		t.Fatalf("global contest = %+v, %v", judging, err)
	}
	judgeRun, err := f.s.Get(ctx, judging.JudgeRunID)
	if err != nil {
		t.Fatal(err)
	}
	if !judgeRun.Global || judgeRun.Project != "" || judgeRun.NodeID != "" ||
		judgeRun.Kind != runnerwire.JobCompare || judgeRun.ContestID != res.Contest.ID ||
		judgeRun.ContestLabel != "J" || !judgeRun.NoPR || judgeRun.BranchSlug == "" ||
		judgeRun.Origin != runnerwire.OriginPipeline {
		t.Fatalf("global reviewer run = %+v", judgeRun)
	}
	var isGlobal bool
	var kind, label string
	if err := f.s.db.QueryRowContext(ctx, `SELECT is_global, kind, contest_label FROM runs WHERE id = ?`, judgeRun.ID).
		Scan(&isGlobal, &kind, &label); err != nil || !isGlobal || kind != runnerwire.JobCompare || label != "J" {
		t.Fatalf("db row = global:%v kind:%q label:%q err:%v", isGlobal, kind, label, err)
	}
}

func TestPickAppliesWinnerDiscardsOthers(t *testing.T) {
	f := newContestFixture(t, contestHello(4))
	ctx := context.Background()

	// Test apply="pr" (open_pr) + discardOthers=true.
	res, err := f.d.StartContest(ctx, f.nodeRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	win := f.finishEntry(t, res.Runs[0], "1111111111111111111111111111111111111111", "Winner A", runnerwire.RunSucceeded)
	lose := f.finishEntry(t, res.Runs[1], "2222222222222222222222222222222222222222", "Loser B", runnerwire.RunSucceeded)

	f.log.audited = nil
	picked, err := f.d.PickContest(ctx, PickContestRequest{
		ContestID: res.Contest.ID,
		Body: runnerwire.PickRequest{
			RunID:         win.ID,
			Apply:         runnerwire.ContestApplyPR,
			DiscardOthers: true,
		},
		ActorID:   "u-ann",
		ActorName: "ann",
	})
	if err != nil {
		t.Fatal(err)
	}
	if picked.Contest.State != runnerwire.ContestDecided || picked.Contest.WinnerRunID != win.ID || picked.Contest.DecidedAt == "" {
		t.Fatalf("picked contest = %+v", picked.Contest)
	}
	if len(picked.Actions) != 2 {
		t.Fatalf("actions = %+v, want 2 (open_pr + discard)", picked.Actions)
	}
	if picked.Actions[0].RunID != win.ID || picked.Actions[0].Action != runnerwire.WorkspaceOpenPR || picked.Actions[0].Status != runnerwire.WorkspacePending {
		t.Fatalf("winner action = %+v", picked.Actions[0])
	}
	if picked.Actions[1].RunID != lose.ID || picked.Actions[1].Action != runnerwire.WorkspaceDiscard || picked.Actions[1].Status != runnerwire.WorkspacePending {
		t.Fatalf("loser action = %+v", picked.Actions[1])
	}
	if act, ok := f.wconn.actionFor(win.ID); !ok || act.Action != runnerwire.WorkspaceOpenPR {
		t.Fatalf("sent winner action = %+v, ok=%v", act, ok)
	}
	if act, ok := f.wconn.actionFor(lose.ID); !ok || act.Action != runnerwire.WorkspaceDiscard {
		t.Fatalf("sent loser action = %+v, ok=%v", act, ok)
	}
	foundAudit := false
	for _, e := range f.log.audited {
		if e.Action == "run.contest.pick" && e.Target == res.Contest.ID && e.Detail["winner"] == win.ID && e.Detail["apply"] == runnerwire.ContestApplyPR {
			foundAudit = true
		}
	}
	if !foundAudit {
		t.Fatalf("missing run.contest.pick audit in %+v", f.log.audited)
	}

	// Test apply="local" (merge_local) + discardOthers=false.
	res2, err := f.d.StartContest(ctx, f.nodeRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	win2 := f.finishEntry(t, res2.Runs[0], "3333333333333333333333333333333333333333", "Winner A", runnerwire.RunSucceeded)
	f.finishEntry(t, res2.Runs[1], "4444444444444444444444444444444444444444", "Loser B", runnerwire.RunSucceeded)

	picked2, err := f.d.PickContest(ctx, PickContestRequest{
		ContestID: res2.Contest.ID,
		Body: runnerwire.PickRequest{
			RunID:         win2.ID,
			Apply:         runnerwire.ContestApplyLocal,
			DiscardOthers: false,
		},
		ActorID:   "u-ann",
		ActorName: "ann",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(picked2.Actions) != 1 || picked2.Actions[0].Action != runnerwire.WorkspaceMergeLocal || picked2.Actions[0].RunID != win2.ID {
		t.Fatalf("local pick actions = %+v", picked2.Actions)
	}
}

func TestPickReportsRefusedDiscard(t *testing.T) {
	f := newContestFixture(t, contestHello(4))
	ctx := context.Background()

	res, err := f.d.StartContest(ctx, f.nodeRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	win := f.finishEntry(t, res.Runs[0], "1111111111111111111111111111111111111111", "Winner A", runnerwire.RunSucceeded)
	lose := f.finishEntry(t, res.Runs[1], "2222222222222222222222222222222222222222", "Loser B", runnerwire.RunSucceeded)

	// Simulate runner refusing discard with unsaved_commits.
	f.wconn.onAct = func(act runnerwire.WorkspaceActionJob) {
		if act.RunID == lose.ID && act.Action == runnerwire.WorkspaceDiscard {
			_ = f.d.WorkspaceResult(ctx, f.runner.ID, runnerwire.WorkspaceOutcome{
				ID:     act.ID,
				RunID:  act.RunID,
				Action: act.Action,
				Code:   runnerwire.WorkspaceErrUnsaved,
				Error:  "branch has commits added after the run",
			})
		}
	}

	picked, err := f.d.PickContest(ctx, PickContestRequest{
		ContestID: res.Contest.ID,
		Body: runnerwire.PickRequest{
			RunID:         win.ID,
			Apply:         runnerwire.ContestApplyKeep,
			DiscardOthers: true,
		},
		ActorID:   "u-ann",
		ActorName: "ann",
		Wait:      2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if picked.Contest.State != runnerwire.ContestDecided || picked.Contest.WinnerRunID != win.ID {
		t.Fatalf("contest = %+v", picked.Contest)
	}
	if len(picked.Actions) != 1 || picked.Actions[0].RunID != lose.ID ||
		picked.Actions[0].Action != runnerwire.WorkspaceDiscard ||
		picked.Actions[0].Status != runnerwire.WorkspaceFailed ||
		picked.Actions[0].Code != runnerwire.WorkspaceErrUnsaved {
		t.Fatalf("refused discard actions = %+v, want failed unsaved_commits", picked.Actions)
	}
	// The refused entry stays live (not discarded).
	kept, err := f.s.Get(ctx, lose.ID)
	if err != nil || kept.Workspace != runnerwire.RunWorkspaceLive {
		t.Fatalf("loser workspace = %q, want live (err=%v)", kept.Workspace, err)
	}

	// Also verify hub-gate refusal (e.g. runner offline) is reported in actions[] without failing pick.
	res2, err := f.d.StartContest(ctx, f.nodeRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	win2 := f.finishEntry(t, res2.Runs[0], "3333333333333333333333333333333333333333", "Winner A", runnerwire.RunSucceeded)
	lose2 := f.finishEntry(t, res2.Runs[1], "4444444444444444444444444444444444444444", "Loser B", runnerwire.RunSucceeded)
	f.detach()

	picked2, err := f.d.PickContest(ctx, PickContestRequest{
		ContestID: res2.Contest.ID,
		Body: runnerwire.PickRequest{
			RunID:         win2.ID,
			Apply:         runnerwire.ContestApplyKeep,
			DiscardOthers: true,
		},
		ActorID:   "u-ann",
		ActorName: "ann",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(picked2.Actions) != 1 || picked2.Actions[0].RunID != lose2.ID ||
		picked2.Actions[0].Status != runnerwire.WorkspaceFailed ||
		picked2.Actions[0].Code != runnerwire.WorkspaceGateOffline {
		t.Fatalf("offline discard actions = %+v, want failed offline", picked2.Actions)
	}
}
