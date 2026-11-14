package runs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
)

// globalHello is a runner that takes global runs.
func globalHello(caps ...string) runnerwire.Info {
	if caps == nil {
		caps = []string{runnerwire.CapHeadless, runnerwire.CapInteractive, runnerwire.CapGlobalRuns, runnerwire.CapGlobalRunsOn}
	}
	return runnerwire.Info{
		Capabilities: caps, MaxConcurrent: 4,
		Agents: []runnerwire.Agent{{Kind: "claude", Installed: true,
			Models: []runnerwire.Model{{ID: "default", Default: true}, {ID: "opus"}}}},
		Repos: []runnerwire.Repo{{ID: "repo", Name: "app"}},
	}
}

// hookLog records what a dispatcher announces, project hooks and global.
type hookLog struct {
	mu      sync.Mutex
	project []string
	global  []string
	audited []AuditEntry
}

func (h *hookLog) add(list *[]string, item string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	*list = append(*list, item)
}

func (h *hookLog) globals(evType string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, item := range h.global {
		if strings.HasPrefix(item, evType+" ") {
			n++
		}
	}
	return n
}

// newGlobalFixture is the step-up fixture whose runner takes global runs,
// with every hook recorded.
func newGlobalFixture(t *testing.T, info runnerwire.Info) (*stepUpFixture, *hookLog) {
	t.Helper()
	f := newStepUpFixture(t)
	if err := f.d.reg.SetHello(context.Background(), f.runner.ID, info); err != nil {
		t.Fatal(err)
	}
	log := &hookLog{}
	f.d.changed = func(project, nodeID string) { log.add(&log.project, "changed "+project) }
	f.d.events = func(project, runID string) { log.add(&log.project, "events "+project) }
	f.d.SetNotify(func(project, runID string) { log.add(&log.project, "notify "+project) })
	f.d.SetGlobalNotify(func(evType string, r Record) { log.add(&log.global, evType+" "+r.ID) })
	f.d.SetAudit(func(e AuditEntry) {
		log.mu.Lock()
		log.audited = append(log.audited, e)
		log.mu.Unlock()
	})
	return f, log
}

func (f *stepUpFixture) globalRequest(task string) GlobalStartRequest {
	return GlobalStartRequest{
		Body:    runnerwire.CreateGlobalRunRequest{RunnerID: f.runner.ID, RepoID: "repo", Agent: "claude", Task: task},
		ActorID: "u-ann", ActorName: "ann",
	}
}

func TestStartGlobalRecordsAndOffersAGlobalRun(t *testing.T) {
	f, log := newGlobalFixture(t, globalHello())
	ctx := context.Background()
	task := "\n  Fix the login bug  \r\n\r\nThe form keeps the old password.\nSee issue 12.\n"
	req := f.globalRequest(task)
	req.Body.Base = "main"
	r, err := f.d.StartGlobal(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Global || r.Project != "" || r.NodeID != "" || r.BranchSlug != "fix-the-login-bug" || !r.NoPR ||
		r.NodeTitle != "Fix the login bug" || r.Title != "Fix the login bug" || r.Prompt != "" ||
		r.JobPrompt != "# Fix the login bug\n\nThe form keeps the old password.\nSee issue 12." ||
		r.Model != "default" || r.RepoName != "app" || r.Base != "main" || r.Kind != "" || r.Role != "" ||
		r.PipelineID != "" || len(r.Fallbacks) != 0 || r.CreatedBy != "u-ann" || r.OwnerID != "u-ann" {
		t.Fatalf("global run = %+v", r)
	}
	if r.Status != runnerwire.RunOffered {
		t.Fatalf("status = %s, want offered", r.Status)
	}
	job := f.conn.last(t, r.ID)
	if !job.Global || job.Slug != "fix-the-login-bug" || !job.NoPR || job.Project != "" || job.NodeID != "" ||
		job.NodeTitle != "Fix the login bug" || job.Kind != "" || job.Role != "" || len(job.Tools) != 0 {
		t.Fatalf("offer = %+v", job)
	}
	var global, noPR bool
	var slug, project, node string
	if err := f.s.db.QueryRowContext(ctx, `SELECT is_global, branch_slug, no_pr, project, node_id FROM runs WHERE id = ?`, r.ID).
		Scan(&global, &slug, &noPR, &project, &node); err != nil || !global || slug != "fix-the-login-bug" || !noPR || project != "" || node != "" {
		t.Fatalf("row = %v %q %v %q %q, %v", global, slug, noPR, project, node, err)
	}
	// Audited, without a word of the task.
	if len(log.audited) != 1 {
		t.Fatalf("audit = %+v", log.audited)
	}
	entry := log.audited[0]
	detail := fmt.Sprint(entry.Detail)
	if entry.Action != "run.global.start" || entry.Target != r.ID || entry.ActorID != "u-ann" || entry.Project != "" ||
		entry.Detail["taskBytes"] != len(strings.TrimSpace(task)) || entry.Detail["slug"] != "fix-the-login-bug" ||
		entry.Detail["noPR"] != true || entry.Detail["repo"] != "repo" || entry.Detail["runner"] != f.runner.ID ||
		strings.Contains(detail, "password") || strings.Contains(detail, "Fix the") {
		t.Fatalf("audit entry = %+v", entry)
	}

	// A chosen slug and a pull request.
	req = f.globalRequest("修正登入")
	req.Body.Slug, req.Body.OpenPR, req.Body.Mode = "login-fix", true, runnerwire.ModeInteractive
	asked, err := f.d.StartGlobal(ctx, req)
	if err != nil || asked.BranchSlug != "login-fix" || asked.NoPR || asked.Title != "修正登入" || asked.Mode != runnerwire.ModeInteractive {
		t.Fatalf("second run = %+v, %v", asked, err)
	}
	// A title with nothing ASCII derives the default slug.
	plain, err := f.d.StartGlobal(ctx, f.globalRequest("修正登入"))
	if err != nil || plain.BranchSlug != runnerwire.DefaultGlobalSlug {
		t.Fatalf("CJK title slug = %q, %v", plain.BranchSlug, err)
	}
	// Nothing went to a project room.
	if len(log.project) != 0 {
		t.Fatalf("project hooks told about global runs: %v", log.project)
	}
	if log.globals(runnerwire.GlobalRunsChangedEvent) == 0 {
		t.Fatal("no global-runs-changed for a new global run")
	}
}

func TestStartGlobalRefusals(t *testing.T) {
	cases := []struct {
		name   string
		edit   func(*GlobalStartRequest)
		hello  *runnerwire.Info
		budget bool
		code   string
		field  string
		reason string
	}{
		{name: "empty task", edit: func(r *GlobalStartRequest) { r.Body.Task = " \n\t " }, code: StartInvalid, field: "task"},
		{name: "task over the limit", edit: func(r *GlobalStartRequest) {
			r.Body.Task = strings.Repeat("a", runnerwire.MaxGlobalTaskBytes+1)
		}, code: StartInvalid, field: "task"},
		{name: "NUL in the task", edit: func(r *GlobalStartRequest) { r.Body.Task = "fix\x00it" }, code: StartInvalid, field: "task"},
		{name: "invalid UTF-8", edit: func(r *GlobalStartRequest) { r.Body.Task = "fix \xff it" }, code: StartInvalid, field: "task"},
		{name: "bad slug", edit: func(r *GlobalStartRequest) { r.Body.Slug = "Fix_It" }, code: StartInvalid, field: "slug"},
		{name: "long slug", edit: func(r *GlobalStartRequest) { r.Body.Slug = strings.Repeat("a", 41) }, code: StartInvalid, field: "slug"},
		{name: "bad base", edit: func(r *GlobalStartRequest) { r.Body.Base = "-x" }, code: StartInvalid, field: "base"},
		{name: "bad mode", edit: func(r *GlobalStartRequest) { r.Body.Mode = "tty" }, code: StartInvalid, field: "mode"},
		{name: "someone else's runner", edit: func(r *GlobalStartRequest) { r.ActorID = "u-bob" }, code: StartNotFound},
		{name: "unknown runner", edit: func(r *GlobalStartRequest) { r.Body.RunnerID = "nope" }, code: StartNotFound},
		{name: "agent not installed", edit: func(r *GlobalStartRequest) { r.Body.Agent = "gemini" }, code: StartInvalid, field: "agent"},
		{name: "model not offered", edit: func(r *GlobalStartRequest) { r.Body.Model = "nope" }, code: StartInvalid, field: "model"},
		{name: "repo not registered", edit: func(r *GlobalStartRequest) { r.Body.RepoID = "/etc" }, code: StartInvalid, field: "repoId"},
		{name: "takes no runs", hello: ptr(globalHello(runnerwire.CapGlobalRuns, runnerwire.CapGlobalRunsOn)), code: StartPrecondition},
		{name: "no interactive", edit: func(r *GlobalStartRequest) { r.Body.Mode = runnerwire.ModeInteractive },
			hello: ptr(globalHello(runnerwire.CapHeadless, runnerwire.CapGlobalRuns, runnerwire.CapGlobalRunsOn)), code: StartPrecondition},
		{name: "outdated runner", hello: ptr(globalHello(runnerwire.CapHeadless)), code: StartOutdated},
		{name: "turned off", hello: ptr(globalHello(runnerwire.CapHeadless, runnerwire.CapGlobalRuns)),
			code: StartPrecondition, reason: CodeGlobalRunsOff},
		{name: "budget", budget: true, code: StartBudget},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hello := globalHello()
			if c.hello != nil {
				hello = *c.hello
			}
			f, log := newGlobalFixture(t, hello)
			ctx := context.Background()
			if c.budget {
				if err := f.s.SetBudget(ctx, "u-ann", runnerwire.Budget{DailyRuns: 1}); err != nil {
					t.Fatal(err)
				}
				if _, err := f.d.StartGlobal(ctx, f.globalRequest("first")); err != nil {
					t.Fatal(err)
				}
				log.audited = nil
			}
			req := f.globalRequest("Fix it")
			if c.edit != nil {
				c.edit(&req)
			}
			_, err := f.d.StartGlobal(ctx, req)
			var refused *StartError
			if !errors.As(err, &refused) || refused.Code != c.code || refused.Field != c.field || refused.Reason != c.reason {
				t.Fatalf("err = %#v, want code %q field %q reason %q", err, c.code, c.field, c.reason)
			}
			if c.code == StartOutdated && !errors.Is(err, ErrPrecondition) {
				t.Fatal("an outdated runner is not a precondition failure")
			}
			if len(log.audited) != 0 {
				t.Fatalf("a refused start was audited: %+v", log.audited)
			}
			var n int
			if err := f.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE is_global`).Scan(&n); err != nil || n != boolInt(c.budget) {
				t.Fatalf("%d global rows, %v", n, err)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// newGlobal is the New of a valid global run on runner r1.
func newGlobal(by, owner string) New {
	return New{
		Global: true, BranchSlug: "fix-it", NoPR: true, NodeTitle: "Fix it",
		RunnerID: "r1", RunnerName: "desk", OwnerID: owner, Agent: "claude", Model: "default",
		RepoID: "repo", RepoName: "app", JobPrompt: "# Fix it", CreatedBy: by, CreatedByName: by,
	}
}

func TestCreateHoldsAGlobalRunToItsShape(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	r, err := s.Create(ctx, newGlobal("u-ann", "u-ann"))
	if err != nil {
		t.Fatal(err)
	}
	job := r.Job()
	if !job.Global || job.Slug != "fix-it" || !job.NoPR || job.Project != "" || job.NodeID != "" || job.NodeTitle != "Fix it" {
		t.Fatalf("job = %+v", job)
	}
	for name, edit := range map[string]func(*New){
		"project":   func(n *New) { n.Project = "." },
		"node":      func(n *New) { n.NodeID = "n1" },
		"pipeline":  func(n *New) { n.PipelineID = "p1" },
		"retry":     func(n *New) { n.RetryOf = "x" },
		"kind":      func(n *New) { n.Kind = runnerwire.JobReview },
		"role":      func(n *New) { n.Role = "executor" },
		"fallbacks": func(n *New) { n.Fallbacks = []runnerwire.AgentModel{{Agent: "codex"}} },
		"handoff":   func(n *New) { n.JobHandoffBranch = runnerwire.HandoffBranchName("x") },
		"tools":     func(n *New) { n.Tools = []string{"get_node"} },
		"no slug":   func(n *New) { n.BranchSlug = "" },
		"bad slug":  func(n *New) { n.BranchSlug = "a--b" },
		"no runner": func(n *New) { n.RunnerID = "" },
	} {
		in := newGlobal("u-ann", "u-ann")
		edit(&in)
		if _, err := s.Create(ctx, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("global run with %s: %v", name, err)
		}
	}
	// A node's run takes no slug or NoPR.
	node := New{Project: ".", NodeID: "n1", NodeTitle: "T", RunnerID: "r1", OwnerID: "u-ann", Agent: "claude",
		RepoID: "repo", JobPrompt: "# T", CreatedBy: "u-ann", BranchSlug: "x"}
	if _, err := s.Create(ctx, node); !errors.Is(err, ErrInvalid) {
		t.Errorf("node run with a slug: %v", err)
	}
	// The database refuses a global row of the wrong shape, whoever writes it.
	if _, err := s.db.ExecContext(ctx, `UPDATE runs SET node_id = 'n1' WHERE id = ?`, r.ID); err == nil {
		t.Fatal("runs_global_shape let a global run take a node")
	}
	// A global run reads back with its title, and a node's run without one.
	got, err := s.Get(ctx, r.ID)
	if err != nil || !got.Global || got.Title != "Fix it" || got.BranchSlug != "fix-it" || !got.NoPR {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	plain := createRun(t, s, "n1")
	if plain.Global || plain.Title != "" || plain.BranchSlug != "" || plain.NoPR || plain.Job().Global {
		t.Fatalf("node run = %+v", plain)
	}
}

func TestListGlobalViewerPagesAndProjectListsExcludeIt(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	clock := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	s.now = func() time.Time { clock = clock.Add(time.Second); return clock }
	mk := func(by, owner string, end bool) Record {
		t.Helper()
		r, err := s.Create(ctx, newGlobal(by, owner))
		if err != nil {
			t.Fatal(err)
		}
		if end {
			if r, err = s.Transition(ctx, r.ID, runnerwire.RunCancelled, Change{}); err != nil {
				t.Fatal(err)
			}
		}
		return r
	}
	ended1 := mk("u-ann", "u-ann", true)
	byBob := mk("u-bob", "u-ann", true)  // on ann's runner
	ended3 := mk("u-ann", "u-cat", true) // ann started it on cat's
	active := mk("u-ann", "u-ann", false)
	stranger := mk("u-dan", "u-dan", false)
	nodeRun := createIn(t, s, ".", "u-ann")

	all, more, err := s.ListGlobal(ctx, GlobalFilter{Viewer: "u-ann"})
	if err != nil || more || !sameIDs(all, active, ended3, byBob, ended1) {
		t.Fatalf("ann's list = %v more %v, %v", ids(all), more, err)
	}
	page, more, err := s.ListGlobal(ctx, GlobalFilter{Viewer: "u-ann", Limit: 2})
	if err != nil || !more || !sameIDs(page, active, ended3, byBob) {
		t.Fatalf("first page = %v more %v, %v", ids(page), more, err)
	}
	page, more, err = s.ListGlobal(ctx, GlobalFilter{Viewer: "u-ann", Limit: 2, Before: byBob.CreatedAt})
	if err != nil || more || !sameIDs(page, ended1) {
		t.Fatalf("second page = %v more %v, %v", ids(page), more, err)
	}
	if list, _, _ := s.ListGlobal(ctx, GlobalFilter{Viewer: "u-bob"}); !sameIDs(list, byBob) {
		t.Fatalf("bob sees %v", ids(list))
	}
	if list, _, _ := s.ListGlobal(ctx, GlobalFilter{Viewer: "u-eve"}); len(list) != 0 {
		t.Fatalf("a stranger sees %v", ids(list))
	}
	if list, _, _ := s.ListGlobal(ctx, GlobalFilter{}); len(list) != 0 {
		t.Fatalf("no viewer sees %v", ids(list))
	}
	if list, err := s.ActiveGlobal(ctx, "u-ann"); err != nil || !sameIDs(list, active) {
		t.Fatalf("active = %v, %v", ids(list), err)
	}

	// Project-scoped reads never see a global run, even for an empty name.
	for _, project := range []string{"", "."} {
		list, err := s.List(ctx, Filter{Project: project, Limit: 500})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range list {
			if r.Global {
				t.Fatalf("List(%q) returned global run %s", project, r.ID)
			}
		}
	}
	building, err := s.ListBuilding(ctx, BuildingFilter{Projects: []string{"", "."}, Since: "2000"})
	if err != nil || !sameIDs(building, nodeRun) {
		t.Fatalf("building = %v, %v", ids(building), err)
	}
	others, err := s.CountOthersActive(ctx, []string{"", "."}, "u-zed")
	if err != nil || others[""] != 0 || others["."] != 1 {
		t.Fatalf("others = %v, %v", others, err)
	}
	_ = stranger
}

func TestGlobalRunsCountForTheRunnerAndBudget(t *testing.T) {
	f, _ := newGlobalFixture(t, globalHello())
	ctx := context.Background()
	r, err := f.d.StartGlobal(ctx, f.globalRequest("Fix it"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: r.ID, Base: "main", Branch: "agent/fix-it-" + r.ID[:8]}); err != nil {
		t.Fatal(err)
	}
	if busy, err := f.s.Busy(ctx, f.runner.ID); err != nil || busy != 1 {
		t.Fatalf("busy = %d, %v", busy, err)
	}
	if held, err := f.s.ForRunner(ctx, f.runner.ID, runnerwire.RunRunning); err != nil || !sameIDs(held, r) {
		t.Fatalf("ForRunner = %v, %v", ids(held), err)
	}
	usage := &runnerwire.Usage{InputTokens: 1000, OutputTokens: 500, CostUSD: 0.25}
	if err := f.d.Finish(ctx, f.runner.ID, r.ID, runnerwire.RunResult{Status: runnerwire.RunSucceeded, Usage: usage}); err != nil {
		t.Fatal(err)
	}
	status, err := f.s.BudgetStatus(ctx, "u-ann")
	if err != nil || status.Used.Runs != 1 || status.Used.Tokens != 1500 {
		t.Fatalf("budget = %+v, %v", status.Used, err)
	}
}

func TestRunnerLoads(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	s.now = func() time.Time { return time.Date(now.Year(), now.Month(), now.Day(), 1, 0, 0, 0, time.UTC) }
	on := func(runner string, global bool, status string, usage *runnerwire.Usage) {
		t.Helper()
		in := newGlobal("u-ann", "u-ann")
		if !global {
			in = New{Project: ".", NodeID: "n1", NodeTitle: "T", OwnerID: "u-ann", Agent: "claude", Model: "default",
				RepoID: "repo", JobPrompt: "# T", CreatedBy: "u-ann"}
		}
		in.RunnerID = runner
		r, err := s.Create(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		path := map[string][]string{
			runnerwire.RunQueued:    nil,
			runnerwire.RunOffered:   {runnerwire.RunOffered},
			runnerwire.RunRunning:   {runnerwire.RunOffered, runnerwire.RunRunning},
			runnerwire.RunSucceeded: {runnerwire.RunOffered, runnerwire.RunRunning, runnerwire.RunSucceeded},
		}[status]
		for _, step := range path {
			change := Change{}
			if step == runnerwire.RunSucceeded {
				change.Usage = usage
			}
			if _, err := s.Transition(ctx, r.ID, step, change); err != nil {
				t.Fatal(err)
			}
		}
	}
	on("r1", true, runnerwire.RunQueued, nil)
	on("r1", true, runnerwire.RunRunning, nil)
	on("r1", false, runnerwire.RunOffered, nil)
	on("r1", false, runnerwire.RunQueued, nil)
	on("r1", true, runnerwire.RunSucceeded, &runnerwire.Usage{InputTokens: 100, OutputTokens: 20, CostUSD: 1.5})
	on("r2", false, runnerwire.RunSucceeded, &runnerwire.Usage{InputTokens: 7, OutputTokens: 3})

	loads, err := s.RunnerLoads(ctx, []string{"r1", "r2", "r3"})
	if err != nil {
		t.Fatal(err)
	}
	r1 := loads["r1"]
	if r1.Busy != 2 || r1.Queued != 2 || r1.GlobalActive != 2 || r1.Today.Runs != 5 || r1.Today.Tokens != 120 || !approx(r1.Today.CostUSD, 1.5) {
		t.Fatalf("r1 = %+v", r1)
	}
	r2 := loads["r2"]
	if r2.Busy != 0 || r2.Queued != 0 || r2.GlobalActive != 0 || r2.Today.Runs != 1 || r2.Today.Tokens != 10 {
		t.Fatalf("r2 = %+v", r2)
	}
	if _, ok := loads["r3"]; ok {
		t.Fatal("a runner without runs has a load")
	}
	if empty, err := s.RunnerLoads(ctx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("no runners = %v, %v", empty, err)
	}
}

// A global agent that hits a usage limit just fails: no fallback, no wait
// for the reset, no handoff.
func TestGlobalFailureHasNoFollowUp(t *testing.T) {
	f, log := newGlobalFixture(t, globalHello())
	ctx := context.Background()
	r, err := f.d.StartGlobal(ctx, f.globalRequest("Fix it"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: r.ID, Base: "main", Branch: "agent/fix-it-x"}); err != nil {
		t.Fatal(err)
	}
	f.fail(t, r.ID, runnerwire.RunResult{FailureKind: runnerwire.FailureQuota, Error: "usage limit",
		HandoffBranch: runnerwire.HandoffBranchName(r.ID), HandoffNote: "half done", ResetAt: f.clock.Add(time.Hour).Format(time.RFC3339)})
	got, err := f.s.Get(ctx, r.ID)
	if err != nil || got.Status != runnerwire.RunFailed || got.RetryAt != "" || got.HandoffBranch != "" {
		t.Fatalf("failed global run = %+v, %v", got, err)
	}
	var n int
	if err := f.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d runs after the failure, %v", n, err)
	}
	if log.globals(runnerwire.GlobalRunNotifyEvent) != 1 || len(log.project) != 0 {
		t.Fatalf("hooks: global %v, project %v", log.global, log.project)
	}
}

// A runner that does not take global runs never gets one: it stays queued
// until a hello says the runner does.
func TestPumpHoldsAGlobalRunForARunnerThatTakesThem(t *testing.T) {
	f, _ := newGlobalFixture(t, globalHello(runnerwire.CapHeadless))
	ctx := context.Background()
	in := newGlobal("u-ann", "u-ann")
	in.RunnerID = f.runner.ID
	r, err := f.s.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.d.Submit(ctx, r); got.Status != runnerwire.RunQueued || f.conn.count(r.ID) != 0 {
		t.Fatalf("offered to an outdated runner: %+v", got)
	}
	// Turned off is no better.
	if err := f.d.reg.SetHello(ctx, f.runner.ID, globalHello(runnerwire.CapHeadless, runnerwire.CapGlobalRuns)); err != nil {
		t.Fatal(err)
	}
	f.d.Pump(ctx, f.runner.ID)
	if f.conn.count(r.ID) != 0 {
		t.Fatal("offered to a runner with global runs turned off")
	}
	// A node's run behind it is not held up.
	node, err := f.s.Create(ctx, New{Project: ".", NodeID: "n1", NodeTitle: "T", RunnerID: f.runner.ID, OwnerID: "u-ann",
		Agent: "claude", Model: "default", RepoID: "repo", JobPrompt: "# T", CreatedBy: "u-ann"})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.d.Submit(ctx, node); got.Status != runnerwire.RunOffered {
		t.Fatalf("node run = %s", got.Status)
	}
	if err := f.d.reg.SetHello(ctx, f.runner.ID, globalHello()); err != nil {
		t.Fatal(err)
	}
	f.d.Pump(ctx, f.runner.ID)
	if job := f.conn.last(t, r.ID); !job.Global || job.Slug != "fix-it" {
		t.Fatalf("offer = %+v", job)
	}
}

// A global run's realtime news goes to the global hook, never a project's;
// live usage is throttled like a node's run.
func TestGlobalNotificationsRouting(t *testing.T) {
	f, log := newGlobalFixture(t, globalHello())
	ctx := context.Background()
	r, err := f.d.StartGlobal(ctx, f.globalRequest("Fix it"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: r.ID, Base: "main", Branch: "agent/fix-it-x"}); err != nil {
		t.Fatal(err)
	}
	changed := log.globals(runnerwire.GlobalRunsChangedEvent)
	usage := func(seq int64) []runnerwire.RunEvent {
		return []runnerwire.RunEvent{{Seq: seq, Type: runnerwire.EventUsage, At: "t", Usage: &runnerwire.Usage{InputTokens: seq}}}
	}
	for seq := int64(1); seq <= 3; seq++ {
		if err := f.d.Events(ctx, f.runner.ID, r.ID, usage(seq)); err != nil {
			t.Fatal(err)
		}
	}
	if got := log.globals(runnerwire.GlobalRunEventsEvent); got != 3 {
		t.Fatalf("%d global-run-events, want 3", got)
	}
	if got := log.globals(runnerwire.GlobalRunsChangedEvent) - changed; got != 1 {
		t.Fatalf("usage announced %d times within the interval, want 1", got)
	}
	f.clock = f.clock.Add(UsageAnnounceInterval)
	if err := f.d.Events(ctx, f.runner.ID, r.ID, usage(4)); err != nil {
		t.Fatal(err)
	}
	if got := log.globals(runnerwire.GlobalRunsChangedEvent) - changed; got != 2 {
		t.Fatalf("usage after the interval: %d announcements, want 2", got)
	}
	if err := f.d.Finish(ctx, f.runner.ID, r.ID, runnerwire.RunResult{Status: runnerwire.RunSucceeded}); err != nil {
		t.Fatal(err)
	}
	if log.globals(runnerwire.GlobalRunNotifyEvent) != 1 {
		t.Fatalf("global hook: %v", log.global)
	}
	if len(log.project) != 0 {
		t.Fatalf("project hooks told about a global run: %v", log.project)
	}
	// A node's run still goes to its project.
	node := f.submit(t, "")
	if len(log.project) == 0 || strings.Contains(strings.Join(log.global, ","), node.ID) {
		t.Fatalf("node run: project %v, global %v", log.project, log.global)
	}
}

func TestCancelAllGlobal(t *testing.T) {
	f, _ := newGlobalFixture(t, globalHello())
	ctx := context.Background()
	// Room for one: the second waits queued.
	info := globalHello()
	info.MaxConcurrent = 1
	if err := f.d.reg.SetHello(ctx, f.runner.ID, info); err != nil {
		t.Fatal(err)
	}
	offered, err := f.d.StartGlobal(ctx, f.globalRequest("One"))
	if err != nil {
		t.Fatal(err)
	}
	queued, err := f.d.StartGlobal(ctx, f.globalRequest("Two"))
	if err != nil || offered.Status != runnerwire.RunOffered || queued.Status != runnerwire.RunQueued {
		t.Fatalf("statuses %s %s, %v", offered.Status, queued.Status, err)
	}
	// Someone else's runner and run are not ann's to stop.
	reg := f.d.reg
	bobRunner, _, err := reg.Create(ctx, identity.Actor{ID: "u-bob", Name: "bob"}, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	other := newGlobal("u-bob", "u-bob")
	other.RunnerID = bobRunner.ID
	bobs, err := f.s.Create(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := f.d.CancelAllGlobal(ctx, "u-ann", "ann")
	if err != nil || len(cancelled) != 2 {
		t.Fatalf("cancelled = %v, %v", ids(cancelled), err)
	}
	for _, r := range cancelled {
		if r.Status != runnerwire.RunCancelled {
			t.Fatalf("%s is %s", r.ID, r.Status)
		}
	}
	if got, _ := f.s.Get(ctx, bobs.ID); got.Status != runnerwire.RunQueued {
		t.Fatalf("bob's run is %s", got.Status)
	}
	if again, err := f.d.CancelAllGlobal(ctx, "u-ann", "ann"); err != nil || len(again) != 0 {
		t.Fatalf("second cancel-all = %v, %v", ids(again), err)
	}
}
