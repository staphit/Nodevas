package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
)

// busyConn records all envelopes sent to a runner (offers, cancels, ends).
type busyConn struct {
	mu   sync.Mutex
	envs []runnerwire.Envelope
}

func (c *busyConn) Bye(string) {}

func (c *busyConn) Send(env runnerwire.Envelope) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.envs = append(c.envs, env)
	return nil
}

func (c *busyConn) offers() []runnerwire.Job {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []runnerwire.Job
	for _, e := range c.envs {
		if e.Type == runnerwire.TypeOffer && e.Job != nil {
			out = append(out, *e.Job)
		}
	}
	return out
}

func (c *busyConn) lastOffer(t *testing.T, runID string) runnerwire.Job {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.envs) - 1; i >= 0; i-- {
		if c.envs[i].Type == runnerwire.TypeOffer && c.envs[i].Job != nil && c.envs[i].Job.RunID == runID {
			return *c.envs[i].Job
		}
	}
	t.Fatalf("no offer for %s", runID)
	return runnerwire.Job{}
}

func (c *busyConn) ends() []runnerwire.Envelope {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []runnerwire.Envelope
	for _, e := range c.envs {
		if e.Type == runnerwire.TypeEnd {
			out = append(out, e)
		}
	}
	return out
}

func (c *busyConn) cancels() []runnerwire.Envelope {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []runnerwire.Envelope
	for _, e := range c.envs {
		if e.Type == runnerwire.TypeCancel {
			out = append(out, e)
		}
	}
	return out
}

type busyFixture struct {
	s      *Store
	d      *Dispatcher
	clock  time.Time
	ann    runnerwire.Runner
	annC   *busyConn
	bob    runnerwire.Runner
	bobC   *busyConn
	cal    runnerwire.Runner
	calC   *busyConn
	auditM sync.Mutex
	audits []AuditEntry
}

func busyHello(maxConcurrent int, caps ...string) runnerwire.Info {
	if caps == nil {
		caps = []string{
			runnerwire.CapHeadless,
			runnerwire.CapInteractive,
			runnerwire.CapPlan,
			runnerwire.CapRoles,
			runnerwire.CapVerify,
			runnerwire.CapJudge,
			runnerwire.CapGlobalRuns,
			runnerwire.CapGlobalRunsOn,
			runnerwire.CapContinue,
			runnerwire.CapCoord,
		}
	}
	return runnerwire.Info{
		Capabilities:  caps,
		MaxConcurrent: maxConcurrent,
		Agents: []runnerwire.Agent{
			{Kind: "claude", Installed: true, Models: []runnerwire.Model{{ID: "default", Default: true}, {ID: "opus"}}},
			{Kind: "codex", Installed: true, Models: []runnerwire.Model{{ID: "gpt-5.5", Default: true}}},
		},
		Repos: []runnerwire.Repo{
			{ID: "repo-1", Name: "api", Remote: "https://github.com/org/api.git", Verify: true},
			{ID: "repo-2", Name: "web", Remote: "https://github.com/org/web.git", Verify: true},
		},
	}
}

func newBusyFixture(t *testing.T, maxConcurrent int) *busyFixture {
	t.Helper()
	ctx := context.Background()
	f := newStepUpFixture(t)
	if err := f.d.reg.SetHello(ctx, f.runner.ID, busyHello(maxConcurrent)); err != nil {
		t.Fatal(err)
	}
	annC := &busyConn{}
	f.d.reg.Attach(f.runner.ID, annC)

	bobR, _, err := f.d.reg.Create(ctx, identity.Actor{ID: "u-bob", Name: "bob"}, "bob-pc")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.d.reg.SetHello(ctx, bobR.ID, busyHello(maxConcurrent)); err != nil {
		t.Fatal(err)
	}
	bobC := &busyConn{}
	f.d.reg.Attach(bobR.ID, bobC)

	calR, _, err := f.d.reg.Create(ctx, identity.Actor{ID: "u-cal", Name: "cal"}, "cal-pc")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.d.reg.SetHello(ctx, calR.ID, busyHello(maxConcurrent)); err != nil {
		t.Fatal(err)
	}
	calC := &busyConn{}
	f.d.reg.Attach(calR.ID, calC)

	annR, err := f.d.reg.Get(ctx, f.runner.ID)
	if err != nil {
		t.Fatal(err)
	}
	bobR, err = f.d.reg.Get(ctx, bobR.ID)
	if err != nil {
		t.Fatal(err)
	}
	calR, err = f.d.reg.Get(ctx, calR.ID)
	if err != nil {
		t.Fatal(err)
	}

	bf := &busyFixture{
		s: f.s, d: f.d, clock: f.clock,
		ann: annR, annC: annC,
		bob: bobR, bobC: bobC,
		cal: calR, calC: calC,
	}
	bf.d.SetAudit(func(e AuditEntry) {
		bf.auditM.Lock()
		bf.audits = append(bf.audits, e)
		bf.auditM.Unlock()
	})
	return bf
}

func (f *busyFixture) nodeReq(actorID, actorName, runnerID, nodeID string, edit func(*StartRequest)) StartRequest {
	req := StartRequest{
		Body: runnerwire.CreateRunRequest{
			NodeID:   nodeID,
			RunnerID: runnerID,
			Agent:    "claude",
			Model:    "opus",
			RepoID:   "repo-1",
			Base:     "main",
		},
		Project:   ".",
		ActorID:   actorID,
		ActorName: actorName,
		Origin:    runnerwire.OriginPerson,
		LoadNode: func() (NodeInfo, bool, error) {
			return NodeInfo{
				Title: "Fix " + nodeID, Content: "Do the work on " + nodeID,
				Type: "task", AgentMode: engine.AgentDispatch,
			}, true, nil
		},
	}
	if edit != nil {
		edit(&req)
	}
	return req
}

func (f *busyFixture) globalReq(actorID, actorName, runnerID, repoID, task string, edit func(*GlobalStartRequest)) GlobalStartRequest {
	req := GlobalStartRequest{
		Body: runnerwire.CreateGlobalRunRequest{
			RunnerID: runnerID,
			RepoID:   repoID,
			Agent:    "claude",
			Model:    "opus",
			Task:     task,
		},
		ActorID:   actorID,
		ActorName: actorName,
	}
	if edit != nil {
		edit(&req)
	}
	return req
}

func requireBusy(t *testing.T, err error) *StartError {
	t.Helper()
	var se *StartError
	if !errors.As(err, &se) || se.Code != StartBusy || se.Busy == nil {
		t.Fatalf("err = %#v, want StartBusy with Busy payload", err)
	}
	return se
}

func (f *busyFixture) acceptAndRun(t *testing.T, r Record) {
	t.Helper()
	if err := f.d.Accept(context.Background(), r.RunnerID, runnerwire.Accept{
		RunID: r.ID, Base: "main", Branch: "agent/test-" + r.ID[:6],
	}); err != nil {
		t.Fatalf("accept %s: %v", r.ID, err)
	}
}

func (f *busyFixture) finishRun(t *testing.T, r Record, status, summary string) {
	t.Helper()
	if err := f.d.Finish(context.Background(), r.RunnerID, r.ID, runnerwire.RunResult{
		Status: status, Summary: summary,
	}); err != nil {
		t.Fatalf("finish %s: %v", r.ID, err)
	}
}

func TestStartAsksWhenNodeBusy(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	// Ann starts a run on n1, Bob starts a second run on n1 (both).
	annRun, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, annRun)

	_, err = f.d.Start(ctx, f.nodeReq("u-bob", "bob", f.bob.ID, "n1", func(r *StartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyBoth
	}))
	if err != nil {
		t.Fatal(err)
	}

	// Ann starts again without onBusy -> 409 BUSY with Ann's visible holder and others == 1 (Bob's).
	_, err = f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil))
	se := requireBusy(t, err)
	if len(se.Busy.Holders) != 1 || se.Busy.Holders[0].RunID != annRun.ID {
		t.Fatalf("holders = %+v, want [%s]", se.Busy.Holders, annRun.ID)
	}
	h := se.Busy.Holders[0]
	if h.Title != "Fix n1" || h.Agent != "claude" || h.Model != "opus" || h.Status != runnerwire.RunRunning ||
		h.By != "ann" || h.NodeID != "n1" || h.RepoName != "api" || !h.MayStop || !h.SameNode || h.Since == "" {
		t.Fatalf("holder fields = %+v", h)
	}
	if se.Busy.Others != 1 {
		t.Fatalf("others = %d, want 1", se.Busy.Others)
	}
	// Hidden holder prevents takeOver.
	if slices.Contains(se.Busy.Choices, runnerwire.OnBusyTakeOver) {
		t.Fatalf("choices %v should not offer takeOver with hidden holder", se.Busy.Choices)
	}
	if !slices.Contains(se.Busy.Choices, runnerwire.OnBusyQueue) || !slices.Contains(se.Busy.Choices, runnerwire.OnBusyBoth) {
		t.Fatalf("choices = %v, want queue and both", se.Busy.Choices)
	}
}

func TestBusyHidesOtherMembersRun(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	bobRun, err := f.d.Start(ctx, f.nodeReq("u-bob", "bob", f.bob.ID, "n1", func(r *StartRequest) {
		r.LoadNode = func() (NodeInfo, bool, error) {
			return NodeInfo{Title: "Secret Bob Task", Content: "secret", Type: "task", AgentMode: engine.AgentDispatch}, true, nil
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, bobRun)

	// Ann tries to start on n1 -> BUSY, holders empty, others == 1, no leak of Bob's details.
	_, err = f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil))
	se := requireBusy(t, err)
	raw, _ := json.Marshal(se.Busy)
	body := string(raw)
	if len(se.Busy.Holders) != 0 || se.Busy.Others != 1 {
		t.Fatalf("busy = %s", body)
	}
	for _, forbidden := range []string{bobRun.ID, "Secret Bob Task", "bob", "claude", "opus", bobRun.StartedAt} {
		if forbidden != "" && strings.Contains(body, forbidden) {
			t.Errorf("busy payload leaked %q: %s", forbidden, body)
		}
	}
	// Compare against CountOthersActive: others never exceeds Station's OthersActive.
	counts, err := f.s.CountOthersActive(ctx, []string{"."}, "u-ann")
	if err != nil {
		t.Fatal(err)
	}
	if se.Busy.Others > counts["."] {
		t.Fatalf("others = %d exceeds CountOthersActive = %d", se.Busy.Others, counts["."])
	}
}

func TestBusyAdminSeesAndMayStop(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	bobRun, err := f.d.Start(ctx, f.nodeReq("u-bob", "bob", f.bob.ID, "n1", nil))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, bobRun)

	// Admin Ann sees Bob's holder with mayStop=true, others=0, and takeOver in choices.
	_, err = f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
		r.IsAdmin = true
	}))
	se := requireBusy(t, err)
	if len(se.Busy.Holders) != 1 || se.Busy.Holders[0].RunID != bobRun.ID || !se.Busy.Holders[0].MayStop || se.Busy.Others != 0 {
		t.Fatalf("admin busy = %+v", se.Busy)
	}
	if !slices.Contains(se.Busy.Choices, runnerwire.OnBusyTakeOver) {
		t.Fatalf("admin choices = %v, want takeOver", se.Busy.Choices)
	}

	// Admin takes over Bob's run.
	taken, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
		r.IsAdmin = true
		r.Body.OnBusy = runnerwire.OnBusyTakeOver
		r.Body.HolderIDs = []string{bobRun.ID}
	}))
	if err != nil {
		t.Fatalf("admin takeOver failed: %v", err)
	}
	if taken.AfterRun != bobRun.ID || taken.BusyChoice != runnerwire.OnBusyTakeOver {
		t.Fatalf("taken = %+v", taken.Run)
	}
	if len(f.bobC.cancels()) == 0 {
		t.Fatal("admin takeOver did not cancel Bob's running run")
	}
}

func TestBusyIgnoresOwnLineage(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	repoKey := runnerwire.RepoKey(f.ann.ID, f.ann.Info.Repos[0])

	// 1. Pipeline lineage.
	p, err := f.s.CreatePipeline(ctx, NewPipeline{
		OwnerID: "u-ann", CreatedBy: "u-ann", Project: ".", NodeID: "n1",
		Pipeline: runnerwire.Pipeline{
			Implement: runnerwire.AgentModel{Agent: "claude", Model: "opus"},
			Review:    runnerwire.AgentModel{Agent: "claude", Model: "opus"},
			MaxRounds: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Create(ctx, New{
		Project: ".", NodeID: "n1", NodeTitle: "T", RunnerID: f.ann.ID, OwnerID: "u-ann",
		Agent: "claude", Model: "opus", RepoID: "repo-1", CreatedBy: "u-ann",
		PipelineID: p.ID, RepoKey: repoKey,
	}); err != nil {
		t.Fatal(err)
	}
	hs, err := f.d.holders(ctx, f.s.db, BusyQuery{
		ActorID: "u-ann", Project: ".", NodeID: "n1", RepoKey: repoKey,
		Exclude: Lineage{PipelineID: p.ID},
	})
	if err != nil || len(hs) != 0 {
		t.Fatalf("pipeline lineage holders = %+v, %v", hs, err)
	}

	// 2. Contest lineage.
	if _, err := f.s.Create(ctx, New{
		Project: ".", NodeID: "n2", NodeTitle: "T", RunnerID: f.ann.ID, OwnerID: "u-ann",
		Agent: "claude", Model: "opus", RepoID: "repo-1", CreatedBy: "u-ann",
		NoPR: true, ContestID: "contest-1", ContestLabel: "A", RepoKey: repoKey,
	}); err != nil {
		t.Fatal(err)
	}
	hs, err = f.d.holders(ctx, f.s.db, BusyQuery{
		ActorID: "u-ann", Project: ".", NodeID: "n2", RepoKey: repoKey,
		Exclude: Lineage{ContestID: "contest-1"},
	})
	if err != nil || len(hs) != 0 {
		t.Fatalf("contest lineage holders = %+v, %v", hs, err)
	}

	// 3. WorkRoot lineage (global, isolated fixture).
	f3 := newBusyFixture(t, 4)
	rk3 := runnerwire.RepoKey(f3.ann.ID, f3.ann.Info.Repos[0])
	root, err := f3.s.Create(ctx, New{
		Global: true, BranchSlug: "root", NodeTitle: "Root", RunnerID: f3.ann.ID, OwnerID: "u-ann",
		Agent: "claude", Model: "opus", RepoID: "repo-1", CreatedBy: "u-ann", RepoKey: rk3,
	})
	if err != nil {
		t.Fatal(err)
	}
	hs, err = f3.d.holders(ctx, f3.s.db, BusyQuery{
		ActorID: "u-ann", RepoKey: rk3, Global: true,
		Exclude: Lineage{WorkRoot: root.ID},
	})
	if err != nil || len(hs) != 0 {
		t.Fatalf("work_root lineage holders = %+v, %v", hs, err)
	}

	// 4. Group lineage (global, isolated fixture).
	f4 := newBusyFixture(t, 4)
	rk4 := runnerwire.RepoKey(f4.ann.ID, f4.ann.Info.Repos[1])
	if _, err := f4.s.Create(ctx, New{
		Global: true, BranchSlug: "grp", NodeTitle: "Group", RunnerID: f4.ann.ID, OwnerID: "u-ann",
		Agent: "claude", Model: "opus", RepoID: "repo-2", CreatedBy: "u-ann",
		GroupID: "group-1", RepoKey: rk4,
	}); err != nil {
		t.Fatal(err)
	}
	hs, err = f4.d.holders(ctx, f4.s.db, BusyQuery{
		ActorID: "u-ann", RepoKey: rk4, Global: true,
		Exclude: Lineage{GroupID: "group-1"},
	})
	if err != nil || len(hs) != 0 {
		t.Fatalf("group lineage holders = %+v, %v", hs, err)
	}
}

func TestBusyCountsWaitingRunsAsHolders(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	r1, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil))
	if err != nil {
		t.Fatal(err)
	}
	r2, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyQueue
	}))
	if err != nil {
		t.Fatal(err)
	}
	if r2.Status != runnerwire.RunQueued || r2.AfterRun != r1.ID {
		t.Fatalf("r2 = %+v", r2.Run)
	}

	// Third start sees BOTH r1 (not waiting) and r2 (waiting: true).
	_, err = f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil))
	se := requireBusy(t, err)
	if len(se.Busy.Holders) != 2 {
		t.Fatalf("holders = %+v, want 2", se.Busy.Holders)
	}
	if se.Busy.Holders[0].RunID != r1.ID || se.Busy.Holders[0].Waiting {
		t.Fatalf("holder[0] = %+v", se.Busy.Holders[0])
	}
	if se.Busy.Holders[1].RunID != r2.ID || !se.Busy.Holders[1].Waiting {
		t.Fatalf("holder[1] = %+v, want waiting=true", se.Busy.Holders[1])
	}
}

func TestBusyReadOnlyKindsNeverHold(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)
	repoKey := runnerwire.RepoKey(f.ann.ID, f.ann.Info.Repos[0])

	// Active review, judge, verify, compare runs on n1 / repo-1.
	for _, kind := range []string{runnerwire.JobReview, runnerwire.JobJudge, runnerwire.JobVerify} {
		in := New{
			Project: ".", NodeID: "n1", NodeTitle: "T", RunnerID: f.ann.ID, OwnerID: "u-ann",
			Agent: "claude", Model: "opus", RepoID: "repo-1", CreatedBy: "u-ann",
			Kind: kind, RepoKey: repoKey,
		}
		if kind == runnerwire.JobVerify {
			in.Agent, in.Model = "", ""
		}
		if _, err := f.s.Create(ctx, in); err != nil {
			t.Fatalf("create %s: %v", kind, err)
		}
	}
	if _, err := f.s.Create(ctx, New{
		Project: ".", NodeID: "n1", NodeTitle: "T", RunnerID: f.ann.ID, OwnerID: "u-ann",
		Agent: "claude", Model: "opus", RepoID: "repo-1", CreatedBy: "u-ann",
		Kind: runnerwire.JobCompare, ContestID: "c1", ContestLabel: "J", RepoKey: repoKey,
	}); err != nil {
		t.Fatal(err)
	}

	// Node start on n1 proceeds without BUSY.
	if _, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil)); err != nil {
		t.Fatalf("node start blocked by read-only runs: %v", err)
	}
}

func TestTwoAutopilotTasksInOneRepoRunInParallel(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	r1, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
		r.Origin = runnerwire.OriginAutopilot
		r.Body.OnBusy = runnerwire.OnBusyQueue
	}))
	if err != nil {
		t.Fatal(err)
	}
	r2, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n2", func(r *StartRequest) {
		r.Origin = runnerwire.OriginAutopilot
		r.Body.OnBusy = runnerwire.OnBusyQueue
	}))
	if err != nil {
		t.Fatal(err)
	}
	if r1.Status != runnerwire.RunOffered || r2.Status != runnerwire.RunOffered {
		t.Fatalf("statuses = %s, %s; want both offered in parallel", r1.Status, r2.Status)
	}
	if r2.AfterRun != "" || r2.BusyChoice != "" {
		t.Fatalf("different nodes should not queue behind each other: %+v", r2.Run)
	}
}

func TestTwoNodeDispatchesInOneRepoNeverBusy(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	r1, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil))
	if err != nil {
		t.Fatal(err)
	}
	r2, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n2", nil))
	if err != nil {
		t.Fatalf("second node in same repo was busy: %v", err)
	}
	if r1.Status != runnerwire.RunOffered || r2.Status != runnerwire.RunOffered {
		t.Fatalf("statuses = %s, %s", r1.Status, r2.Status)
	}
}

func TestNodeStartHeldByOwnGlobalRepoRunOnly(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	// Bob's global run in repo-1 does NOT block Ann's node start in repo-1.
	bobG, err := f.d.StartGlobal(ctx, f.globalReq("u-bob", "bob", f.bob.ID, "repo-1", "Bob work", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil)); err != nil {
		t.Fatalf("Ann's node start blocked by Bob's global run %s: %v", bobG.ID, err)
	}

	// Ann's own global run in repo-1 DOES block Ann's node start on n2 in repo-1.
	annG, err := f.d.StartGlobal(ctx, f.globalReq("u-ann", "ann", f.ann.ID, "repo-1", "Ann global", func(r *GlobalStartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyBoth
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n2", nil))
	se := requireBusy(t, err)
	if len(se.Busy.Holders) != 1 || se.Busy.Holders[0].RunID != annG.ID || se.Busy.Holders[0].SameNode {
		t.Fatalf("node start repo holders = %+v, want [%s] with sameNode=false", se.Busy.Holders, annG.ID)
	}
}

func TestGlobalStartHeldByOwnNodeRunInRepo(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	// Bob's node run in repo-1 does NOT block Ann's global start in repo-1.
	if _, err := f.d.Start(ctx, f.nodeReq("u-bob", "bob", f.bob.ID, "n1", nil)); err != nil {
		t.Fatal(err)
	}
	// Ann's node run in repo-1 DOES block Ann's global start in repo-1.
	annNode, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n2", nil))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.d.StartGlobal(ctx, f.globalReq("u-ann", "ann", f.ann.ID, "repo-1", "Global task", nil))
	se := requireBusy(t, err)
	if len(se.Busy.Holders) != 1 || se.Busy.Holders[0].RunID != annNode.ID || se.Busy.Continue != nil {
		t.Fatalf("global start holders = %+v continue=%+v, want [%s] with nil continue", se.Busy.Holders, se.Busy.Continue, annNode.ID)
	}
}

func TestBusyRefusalLeavesNoPipelineRow(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	// Existing holder on n1.
	if _, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil)); err != nil {
		t.Fatal(err)
	}

	// Staged pipeline start on n1 gets BUSY and leaves zero run_pipelines rows.
	wf := &engine.Workflow{
		Preset: "standard",
		Stages: engine.Stages{Tester: true, Reviewer: true},
	}
	acc := &engine.Acceptance{Items: []engine.AcceptanceItem{{ID: "a1", Text: "it works"}}}
	_, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
		r.Roles = engine.BuiltinAgentRoles()
		r.LoadNode = func() (NodeInfo, bool, error) {
			return NodeInfo{
				Title: "Staged task", Content: "Build it", Type: "task", AgentMode: engine.AgentDispatch,
				Workflow: wf, Acceptance: acc,
			}, true, nil
		}
	}))
	requireBusy(t, err)

	var pipeCount int
	if err := f.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_pipelines`).Scan(&pipeCount); err != nil {
		t.Fatal(err)
	}
	if pipeCount != 0 {
		t.Fatalf("BUSY refusal left %d run_pipelines rows, want 0", pipeCount)
	}
}

func TestBusyTxNeverDeadlocksOnOneWriter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f := newBusyFixture(t, 4)

	// 1. Pipeline start on n1 (queued behind holder, then take-over).
	first, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil))
	if err != nil {
		t.Fatal(err)
	}
	pipeReq := f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
		r.Body.Pipeline = &runnerwire.Pipeline{
			Implement: runnerwire.AgentModel{Agent: "claude", Model: "opus"},
			Review:    runnerwire.AgentModel{Agent: "claude", Model: "opus"},
			MaxRounds: 1,
		}
		r.Body.OnBusy = runnerwire.OnBusyQueue
	})
	pipeRun, err := f.d.Start(ctx, pipeReq)
	if err != nil {
		t.Fatalf("pipeline queued start: %v", err)
	}
	if pipeRun.PipelineID == "" || pipeRun.AfterRun != first.ID {
		t.Fatalf("pipeRun = %+v", pipeRun.Run)
	}

	// 2. Take-over stops both without deadlocking on the single writer.
	if _, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyTakeOver
		r.Body.HolderIDs = []string{first.ID, pipeRun.ID}
	})); err != nil {
		t.Fatalf("takeOver: %v", err)
	}

	// 3. Continue on global run (outside any tx) completes promptly.
	g1, err := f.d.StartGlobal(ctx, f.globalReq("u-ann", "ann", f.ann.ID, "repo-2", "First global", nil))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, g1)
	g2, err := f.d.StartGlobal(ctx, f.globalReq("u-ann", "ann", f.ann.ID, "repo-2", "Follow up task", func(r *GlobalStartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyContinue
		r.Body.HolderIDs = []string{g1.ID}
	}))
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if g2.ContinueOf != g1.ID {
		t.Fatalf("g2.ContinueOf = %q, want %q", g2.ContinueOf, g1.ID)
	}
}

func TestQueueGoesToTheTail(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	r1, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil))
	if err != nil {
		t.Fatal(err)
	}
	r2, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyQueue
	}))
	if err != nil {
		t.Fatal(err)
	}
	r3, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyQueue
	}))
	if err != nil {
		t.Fatal(err)
	}
	if r2.AfterRun != r1.ID || r3.AfterRun != r2.ID {
		t.Fatalf("afterRuns: r2=%q (want %q), r3=%q (want %q)", r2.AfterRun, r1.ID, r3.AfterRun, r2.ID)
	}
}

func TestTwoQueuedBehindOneHolderGoInOrder(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	r1, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, r1)

	r2, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyQueue
	}))
	if err != nil {
		t.Fatal(err)
	}
	r3, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyQueue
	}))
	if err != nil {
		t.Fatal(err)
	}
	if r2.Status != runnerwire.RunQueued || r3.Status != runnerwire.RunQueued {
		t.Fatalf("initial statuses: r2=%s r3=%s", r2.Status, r3.Status)
	}

	// Finish r1 -> ONLY r2 is offered, r3 stays queued.
	f.finishRun(t, r1, runnerwire.RunSucceeded, "done 1")
	got2, _ := f.s.Get(ctx, r2.ID)
	got3, _ := f.s.Get(ctx, r3.ID)
	if got2.Status != runnerwire.RunOffered || got3.Status != runnerwire.RunQueued {
		t.Fatalf("after r1: r2=%s r3=%s, want offered/queued", got2.Status, got3.Status)
	}

	// Finish r2 -> r3 is offered.
	f.acceptAndRun(t, got2)
	f.finishRun(t, got2, runnerwire.RunSucceeded, "done 2")
	got3, _ = f.s.Get(ctx, r3.ID)
	if got3.Status != runnerwire.RunOffered {
		t.Fatalf("after r2: r3=%s, want offered", got3.Status)
	}
}

func TestThreeStartsOnOneNodeRunOneAfterAnother(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	// Three different members on three different runners queue on n1.
	rAnn, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, rAnn)

	rBob, err := f.d.Start(ctx, f.nodeReq("u-bob", "bob", f.bob.ID, "n1", func(r *StartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyQueue
	}))
	if err != nil {
		t.Fatal(err)
	}
	rCal, err := f.d.Start(ctx, f.nodeReq("u-cal", "cal", f.cal.ID, "n1", func(r *StartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyQueue
	}))
	if err != nil {
		t.Fatal(err)
	}
	if rBob.Status != runnerwire.RunQueued || rCal.Status != runnerwire.RunQueued {
		t.Fatalf("statuses: bob=%s cal=%s", rBob.Status, rCal.Status)
	}

	// Ann finishes -> Bob starts, Cal waits.
	f.finishRun(t, rAnn, runnerwire.RunSucceeded, "ann done")
	gotBob, _ := f.s.Get(ctx, rBob.ID)
	gotCal, _ := f.s.Get(ctx, rCal.ID)
	if gotBob.Status != runnerwire.RunOffered || gotCal.Status != runnerwire.RunQueued {
		t.Fatalf("after Ann: bob=%s cal=%s", gotBob.Status, gotCal.Status)
	}

	// Bob finishes -> Cal starts.
	f.acceptAndRun(t, gotBob)
	f.finishRun(t, gotBob, runnerwire.RunSucceeded, "bob done")
	gotCal, _ = f.s.Get(ctx, rCal.ID)
	if gotCal.Status != runnerwire.RunOffered {
		t.Fatalf("after Bob: cal=%s, want offered", gotCal.Status)
	}
}

func TestQueuedWaitsForEveryHolder(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	// Two holders for Ann's node start on n1 / repo-1:
	// (1) Bob's node run on n1, and (2) Ann's own global run in repo-1.
	bobNode, err := f.d.Start(ctx, f.nodeReq("u-bob", "bob", f.bob.ID, "n1", nil))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, bobNode)

	annGlobal, err := f.d.StartGlobal(ctx, f.globalReq("u-ann", "ann", f.ann.ID, "repo-1", "Global work", nil))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, annGlobal)

	queued, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyQueue
	}))
	if err != nil {
		t.Fatal(err)
	}
	if queued.Status != runnerwire.RunQueued {
		t.Fatalf("queued status = %s", queued.Status)
	}

	// Even when the newer holder (after_run) finishes, queued STILL waits for the other holder!
	f.finishRun(t, annGlobal, runnerwire.RunSucceeded, "global done")
	got, _ := f.s.Get(ctx, queued.ID)
	if got.Status != runnerwire.RunQueued {
		t.Fatalf("started while node holder still running: %s", got.Status)
	}

	// Once the remaining holder finishes, queued is offered.
	f.finishRun(t, bobNode, runnerwire.RunSucceeded, "bob done")
	got, _ = f.s.Get(ctx, queued.ID)
	if got.Status != runnerwire.RunOffered {
		t.Fatalf("after both holders ended: %s, want offered", got.Status)
	}
}

func TestQueuedWaitsForHolderLineage(t *testing.T) {
	ctx := context.Background()

	// Subcase 1: fallback successor keeps queued run waiting.
	t.Run("fallback", func(t *testing.T) {
		f := newBusyFixture(t, 4)
		r1, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
			r.Body.Fallbacks = []runnerwire.AgentModel{{Agent: "codex", Model: "gpt-5.5"}}
		}))
		if err != nil {
			t.Fatal(err)
		}
		f.acceptAndRun(t, r1)

		q, err := f.d.Start(ctx, f.nodeReq("u-bob", "bob", f.bob.ID, "n1", func(r *StartRequest) {
			r.Body.OnBusy = runnerwire.OnBusyQueue
		}))
		if err != nil {
			t.Fatal(err)
		}

		// r1 fails with rate_limit -> creates fallback successor on ann's runner.
		if err := f.d.Finish(ctx, r1.RunnerID, r1.ID, runnerwire.RunResult{
			Status: runnerwire.RunFailed, FailureKind: runnerwire.FailureRateLimit, Error: "rate limit",
		}); err != nil {
			t.Fatal(err)
		}
		gotQ, _ := f.s.Get(ctx, q.ID)
		if gotQ.Status != runnerwire.RunQueued {
			t.Fatalf("queued run cut in ahead of fallback successor: %s", gotQ.Status)
		}

		// Finish fallback -> now q is offered.
		all, _ := f.s.List(ctx, Filter{Project: ".", NodeID: "n1", Active: true})
		var fb Record
		for _, r := range all {
			if r.RetryOf == r1.ID {
				fb = r
			}
		}
		if fb.ID == "" {
			t.Fatal("expected fallback run")
		}
		f.acceptAndRun(t, fb)
		f.finishRun(t, fb, runnerwire.RunSucceeded, "fallback done")
		gotQ, _ = f.s.Get(ctx, q.ID)
		if gotQ.Status != runnerwire.RunOffered {
			t.Fatalf("after fallback finished: q=%s, want offered", gotQ.Status)
		}
	})

	// Subcase 2: usage-limit wait successor keeps queued run waiting.
	t.Run("usage-limit successor", func(t *testing.T) {
		f := newBusyFixture(t, 4)
		r1, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil))
		if err != nil {
			t.Fatal(err)
		}
		f.acceptAndRun(t, r1)

		q, err := f.d.Start(ctx, f.nodeReq("u-bob", "bob", f.bob.ID, "n1", func(r *StartRequest) {
			r.Body.OnBusy = runnerwire.OnBusyQueue
		}))
		if err != nil {
			t.Fatal(err)
		}

		// r1 fails with quota -> waitForReset creates queued retry_at successor.
		if err := f.d.Finish(ctx, r1.RunnerID, r1.ID, runnerwire.RunResult{
			Status: runnerwire.RunFailed, FailureKind: runnerwire.FailureQuota, Error: "quota exceeded",
		}); err != nil {
			t.Fatal(err)
		}
		gotQ, _ := f.s.Get(ctx, q.ID)
		if gotQ.Status != runnerwire.RunQueued {
			t.Fatalf("queued run cut in ahead of usage-limit successor: %s", gotQ.Status)
		}
	})

	// Subcase 3: running pipeline between steps keeps queued run waiting.
	t.Run("next pipeline step", func(t *testing.T) {
		f := newBusyFixture(t, 4)
		r1, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
			r.Body.Pipeline = &runnerwire.Pipeline{
				Implement: runnerwire.AgentModel{Agent: "claude", Model: "opus"},
				Review:    runnerwire.AgentModel{Agent: "claude", Model: "opus"},
				MaxRounds: 1,
			}
		}))
		if err != nil {
			t.Fatal(err)
		}
		f.acceptAndRun(t, r1)

		q, err := f.d.Start(ctx, f.nodeReq("u-bob", "bob", f.bob.ID, "n1", func(r *StartRequest) {
			r.Body.OnBusy = runnerwire.OnBusyQueue
		}))
		if err != nil {
			t.Fatal(err)
		}

		// r1 finishes with a commit, waiting for PR report -> pipeline is still running!
		if _, _, err := f.s.SetChanges(ctx, r1.ID, r1.RunnerID, runnerwire.RunChanges{
			Commit: strings.Repeat("a", 40),
			Files:  []runnerwire.FileChange{{Path: "a.go", Status: "modified", Additions: 1}},
		}); err != nil {
			t.Fatal(err)
		}
		f.finishRun(t, r1, runnerwire.RunSucceeded, "committed")

		gotQ, _ := f.s.Get(ctx, q.ID)
		if gotQ.Status != runnerwire.RunQueued {
			t.Fatalf("queued run cut in while pipeline still running between steps: %s", gotQ.Status)
		}

		// End the pipeline -> now waitsBehind is false and q can be offered.
		if _, err := f.s.EndPipeline(ctx, r1.PipelineID, runnerwire.PipelineApproved, "done"); err != nil {
			t.Fatal(err)
		}
		f.d.Pump(ctx, f.bob.ID)
		gotQ, _ = f.s.Get(ctx, q.ID)
		if gotQ.Status != runnerwire.RunOffered {
			t.Fatalf("after pipeline ended: q=%s, want offered", gotQ.Status)
		}
	})
}

func TestReadOnlyPipelineStepNeverHeld(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	// Bob has a running writing run on n1.
	bobRun, err := f.d.Start(ctx, f.nodeReq("u-bob", "bob", f.bob.ID, "n1", nil))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, bobRun)

	// Ann has a queued read-only pipeline step (review) on n1.
	for _, kind := range []string{runnerwire.JobReview, runnerwire.JobJudge, runnerwire.JobVerify} {
		in := New{
			Project: ".", NodeID: "n1", NodeTitle: "T", RunnerID: f.ann.ID, OwnerID: "u-ann",
			Agent: "claude", Model: "opus", RepoID: "repo-1", CreatedBy: "u-ann",
			Kind: kind, Origin: runnerwire.OriginPipeline,
		}
		if kind == runnerwire.JobVerify {
			in.Agent, in.Model = "", ""
		}
		r, err := f.s.Create(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		if f.d.heldByOther(ctx, r) {
			t.Errorf("read-only pipeline step %q was held by another lineage", kind)
		}
	}
}

func TestWaitsBehindHasNoCycle(t *testing.T) {
	ctx := context.Background()
	rng := rand.New(rand.NewSource(42))

	for trial := 0; trial < 20; trial++ {
		f := newBusyFixture(t, 4)

		repoKey := runnerwire.RepoKey(f.ann.ID, f.ann.Info.Repos[0])
		actors := []struct {
			id, runner string
		}{
			{"u-ann", f.ann.ID},
			{"u-bob", f.bob.ID},
			{"u-cal", f.cal.ID},
		}
		nodes := []string{"n1", "n2"}

		var queued []Record
		nRuns := 4 + rng.Intn(6)
		for i := 0; i < nRuns; i++ {
			a := actors[rng.Intn(len(actors))]
			isGlobal := rng.Intn(3) == 0
			after := ""
			choice := ""
			if len(queued) > 0 && rng.Intn(3) != 0 {
				after = queued[rng.Intn(len(queued))].ID
				choice = runnerwire.OnBusyQueue
				if rng.Intn(4) == 0 {
					choice = runnerwire.OnBusyTakeOver
				}
			}
			in := New{
				RunnerID: a.runner, OwnerID: a.id, CreatedBy: a.id,
				Agent: "claude", Model: "opus", RepoID: "repo-1", RepoKey: repoKey,
				AfterRun: after, BusyChoice: choice,
			}
			if isGlobal {
				in.Global = true
				in.BranchSlug = fmt.Sprintf("g-%d", i)
				in.NodeTitle = "Global"
			} else {
				in.Project = "."
				in.NodeID = nodes[rng.Intn(len(nodes))]
				in.NodeTitle = "Node"
			}
			r, err := f.s.Create(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			queued = append(queued, r)
		}

		// Every non-empty subset of queued runs must have at least one run whose waitsBehind is false
		// (i.e. the waits-on relation is a strict DAG with no cycle).
		remaining := append([]Record(nil), queued...)
		for len(remaining) > 0 {
			unblocked := -1
			for i, r := range remaining {
				if !f.d.waitsBehind(ctx, r) {
					unblocked = i
					break
				}
			}
			if unblocked < 0 {
				t.Fatalf("trial %d: deadlock / cycle detected among %d queued runs", trial, len(remaining))
			}
			// Cancel the unblocked run to remove it from the queue and repeat.
			if _, err := f.s.Transition(ctx, remaining[unblocked].ID, runnerwire.RunCancelled, Change{}); err != nil {
				t.Fatal(err)
			}
			remaining = append(remaining[:unblocked], remaining[unblocked+1:]...)
		}
	}
}

func TestQueueOnOwnGlobalIsAFreshRun(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	g1, err := f.d.StartGlobal(ctx, f.globalReq("u-ann", "ann", f.ann.ID, "repo-1", "First task", nil))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, g1)

	g2, err := f.d.StartGlobal(ctx, f.globalReq("u-ann", "ann", f.ann.ID, "repo-1", "Second task", func(r *GlobalStartRequest) {
		r.Body.Agent = "codex"
		r.Body.Model = "gpt-5.5"
		r.Body.OpenPR = true
		r.Body.OnBusy = runnerwire.OnBusyQueue
	}))
	if err != nil {
		t.Fatal(err)
	}
	if g2.Status != runnerwire.RunQueued || g2.AfterRun != g1.ID || g2.BusyChoice != runnerwire.OnBusyQueue ||
		g2.ContinueOf != "" || g2.WorkRoot != "" || g2.Agent != "codex" || g2.Model != "gpt-5.5" || g2.NoPR {
		t.Fatalf("queued global run = %+v", g2.Run)
	}
}

func TestContinueOnlyForSameRunnerAndRepoGlobal(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	// Create a second runner for Ann with the same remote URL (same repo_key, different runner_id).
	ann2, _, err := f.d.reg.Create(ctx, identity.Actor{ID: "u-ann", Name: "ann"}, "ann-laptop")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.d.reg.SetHello(ctx, ann2.ID, busyHello(4)); err != nil {
		t.Fatal(err)
	}
	ann2, err = f.d.reg.Get(ctx, ann2.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.d.reg.Attach(ann2.ID, &busyConn{})

	// Start global run on ann's first runner, repo-1.
	g1, err := f.d.StartGlobal(ctx, f.globalReq("u-ann", "ann", f.ann.ID, "repo-1", "Main work", nil))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, g1)

	repoKey := runnerwire.RepoKey(f.ann.ID, f.ann.Info.Repos[0])

	// Preflight on the SAME runner and repo offers continue.
	sameResp, err := f.d.PreflightBusy(ctx, BusyQuery{
		ActorID: "u-ann", RepoKey: repoKey, Global: true, RunnerID: f.ann.ID, RepoID: "repo-1",
	}, f.ann.Info.Capabilities, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(sameResp.Choices, runnerwire.OnBusyContinue) || sameResp.Continue == nil || sameResp.Continue.RunID != g1.ID {
		t.Fatalf("same runner+repo preflight = %+v, want continue", sameResp)
	}

	// Preflight on a DIFFERENT runner (same remote / repoKey) does NOT offer continue.
	diffRunnerResp, err := f.d.PreflightBusy(ctx, BusyQuery{
		ActorID: "u-ann", RepoKey: repoKey, Global: true, RunnerID: ann2.ID, RepoID: "repo-1",
	}, ann2.Info.Capabilities, false)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(diffRunnerResp.Choices, runnerwire.OnBusyContinue) || diffRunnerResp.Continue != nil {
		t.Fatalf("different runner should not offer continue: %+v", diffRunnerResp)
	}

	// Attempting onBusy="continue" from the different runner fails with 409 BUSY.
	_, err = f.d.StartGlobal(ctx, f.globalReq("u-ann", "ann", ann2.ID, "repo-1", "Try continue", func(r *GlobalStartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyContinue
		r.Body.HolderIDs = []string{g1.ID}
	}))
	requireBusy(t, err)

	// A node run holding the repo also does NOT offer continue for a global start.
	f2 := newBusyFixture(t, 4)
	nRun, err := f2.d.Start(ctx, f2.nodeReq("u-ann", "ann", f2.ann.ID, "n1", nil))
	if err != nil {
		t.Fatal(err)
	}
	rk2 := runnerwire.RepoKey(f2.ann.ID, f2.ann.Info.Repos[0])
	nodeHeldResp, err := f2.d.PreflightBusy(ctx, BusyQuery{
		ActorID: "u-ann", RepoKey: rk2, Global: true, RunnerID: f2.ann.ID, RepoID: "repo-1",
	}, f2.ann.Info.Capabilities, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodeHeldResp.Holders) != 1 || nodeHeldResp.Holders[0].RunID != nRun.ID ||
		slices.Contains(nodeHeldResp.Choices, runnerwire.OnBusyContinue) || nodeHeldResp.Continue != nil {
		t.Fatalf("node holder should not offer continue: %+v", nodeHeldResp)
	}
}

func TestContinueBecomesFollowUp(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	// Start an interactive running global run (ordinary FollowUp would refuse RUN_LIVE,
	// so QueuedBehind: true is required).
	g1, err := f.d.StartGlobal(ctx, f.globalReq("u-ann", "ann", f.ann.ID, "repo-1", "Interactive work", func(r *GlobalStartRequest) {
		r.Body.Mode = runnerwire.ModeInteractive
		r.Body.OpenPR = true
	}))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, g1)

	// Wrong holderIds -> 409 BUSY.
	_, err = f.d.StartGlobal(ctx, f.globalReq("u-ann", "ann", f.ann.ID, "repo-1", "Next turn", func(r *GlobalStartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyContinue
		r.Body.HolderIDs = []string{"stale-id"}
	}))
	requireBusy(t, err)

	// Matching holderIds -> delegates to FollowUp(QueuedBehind: true) outside any tx.
	g2, err := f.d.StartGlobal(ctx, f.globalReq("u-ann", "ann", f.ann.ID, "repo-1", "Also fix footer", func(r *GlobalStartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyContinue
		r.Body.HolderIDs = []string{g1.ID}
	}))
	if err != nil {
		t.Fatalf("continue failed: %v", err)
	}
	if g2.ContinueOf != g1.ID || g2.WorkRoot != g1.ID || g2.Status != runnerwire.RunQueued || g2.NoPR {
		t.Fatalf("continue follow-up = %+v", g2.Run)
	}
}

func TestTakeOverRefusesHiddenHolder(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	bobRun, err := f.d.Start(ctx, f.nodeReq("u-bob", "bob", f.bob.ID, "n1", nil))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, bobRun)

	// Non-admin Ann tries takeOver -> StartForbidden / NOT_YOURS_TO_STOP (403).
	_, err = f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyTakeOver
		r.Body.HolderIDs = []string{bobRun.ID}
	}))
	var se *StartError
	if !errors.As(err, &se) || se.Code != StartForbidden || se.Reason != CodeNotYoursToStop {
		t.Fatalf("err = %#v, want StartForbidden / %s", err, CodeNotYoursToStop)
	}
	if len(f.bobC.cancels()) != 0 || len(f.bobC.ends()) != 0 {
		t.Fatal("hidden holder was stopped")
	}
}

func TestTakeOverRefusesChangedHolderSet(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	r1, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil))
	if err != nil {
		t.Fatal(err)
	}
	r2, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyQueue
	}))
	if err != nil {
		t.Fatal(err)
	}

	// Only naming r1 when [r1, r2] hold n1 -> 409 BUSY with fresh holders.
	_, err = f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyTakeOver
		r.Body.HolderIDs = []string{r1.ID}
	}))
	se := requireBusy(t, err)
	if len(se.Busy.Holders) != 2 || se.Busy.Holders[1].RunID != r2.ID {
		t.Fatalf("fresh holders = %+v", se.Busy.Holders)
	}
	if len(f.annC.cancels()) != 0 {
		t.Fatal("stale takeOver stopped a holder")
	}
}

func TestTakeOverEndsInteractiveAndCancelsHeadless(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	// Start a running interactive global run and a headless global run ("both") in repo-1.
	inter, err := f.d.StartGlobal(ctx, f.globalReq("u-ann", "ann", f.ann.ID, "repo-1", "Interactive", func(r *GlobalStartRequest) {
		r.Body.Mode = runnerwire.ModeInteractive
	}))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, inter)

	head, err := f.d.StartGlobal(ctx, f.globalReq("u-ann", "ann", f.ann.ID, "repo-1", "Headless", func(r *GlobalStartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyBoth
	}))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, head)

	// Take over both: interactive gets TypeEnd (noPR=false on global), headless gets TypeCancel.
	next, err := f.d.StartGlobal(ctx, f.globalReq("u-ann", "ann", f.ann.ID, "repo-1", "Take over", func(r *GlobalStartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyTakeOver
		r.Body.HolderIDs = []string{inter.ID, head.ID}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if next.AfterRun != head.ID || next.BusyChoice != runnerwire.OnBusyTakeOver {
		t.Fatalf("next = %+v", next.Run)
	}
	ends := f.annC.ends()
	if len(ends) != 1 || ends[0].RunID != inter.ID || ends[0].NoPR {
		t.Fatalf("ends = %+v, want one TypeEnd for %s with noPR=false", ends, inter.ID)
	}
	cancels := f.annC.cancels()
	if len(cancels) != 1 || cancels[0].RunID != head.ID {
		t.Fatalf("cancels = %+v, want one TypeCancel for %s", cancels, head.ID)
	}
}

func TestNodeTakeOverEndsWithoutPR(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	inter, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
		r.Body.Mode = runnerwire.ModeInteractive
	}))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, inter)

	_, err = f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyTakeOver
		r.Body.HolderIDs = []string{inter.ID}
	}))
	if err != nil {
		t.Fatal(err)
	}
	ends := f.annC.ends()
	if len(ends) != 1 || ends[0].RunID != inter.ID || !ends[0].NoPR {
		t.Fatalf("node takeOver ends = %+v, want TypeEnd with NoPR=true", ends)
	}
}

func TestTakeOverCancelsPipeline(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	pRun, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
		r.Body.Pipeline = &runnerwire.Pipeline{
			Implement: runnerwire.AgentModel{Agent: "claude", Model: "opus"},
			Review:    runnerwire.AgentModel{Agent: "claude", Model: "opus"},
			MaxRounds: 2,
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if pRun.PipelineID == "" {
		t.Fatal("expected pipeline")
	}

	next, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyTakeOver
		r.Body.HolderIDs = []string{pRun.ID}
	}))
	if err != nil {
		t.Fatal(err)
	}
	pipe, err := f.s.Pipeline(ctx, pRun.PipelineID)
	if err != nil {
		t.Fatal(err)
	}
	if pipe.Status != runnerwire.PipelineCancelled {
		t.Fatalf("pipeline status = %q, want cancelled", pipe.Status)
	}
	gotNext, _ := f.s.Get(ctx, next.ID)
	if gotNext.Status != runnerwire.RunOffered {
		t.Fatalf("takeOver run should be offered once cancelled holder ended: %s", gotNext.Status)
	}
}

func TestTakeOverReleasesHoldersClaim(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	var mu sync.Mutex
	var released []string
	f.d.SetRunClaimRelease(func(_ context.Context, project, nodeID, runID string) {
		mu.Lock()
		released = append(released, fmt.Sprintf("%s/%s/%s", project, nodeID, runID))
		mu.Unlock()
	})

	holder, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, holder)

	_, err = f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyTakeOver
		r.Body.HolderIDs = []string{holder.ID}
	}))
	if err != nil {
		t.Fatal(err)
	}

	// Runner finishes the cancelled holder -> busyEnded releases holder's board claim.
	f.finishRun(t, holder, runnerwire.RunCancelled, "")
	mu.Lock()
	got := append([]string(nil), released...)
	mu.Unlock()
	want := fmt.Sprintf("./n1/%s", holder.ID)
	if !slices.Contains(got, want) {
		t.Fatalf("released claims = %v, want %q", got, want)
	}
}

func TestQueuedRunOnDoneNodeIsCoordinated(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	nodeDone := false
	f.d.SetNodeCoordCheck(func(_ context.Context, project, nodeID string) bool {
		return project == "." && nodeID == "n1" && nodeDone
	})

	r1, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, r1)

	r2, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyQueue
	}))
	if err != nil {
		t.Fatal(err)
	}

	// Holder marks the node done before finishing -> r2 is offered with Job.Coordinated = true.
	nodeDone = true
	f.finishRun(t, r1, runnerwire.RunSucceeded, "completed")
	job := f.annC.lastOffer(t, r2.ID)
	if !job.Coordinated {
		t.Fatal("queued run offered on done node did not set Job.Coordinated = true")
	}
}

func TestBothRunIsCoordinated(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	r1, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil))
	if err != nil {
		t.Fatal(err)
	}
	r2, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyBoth
	}))
	if err != nil {
		t.Fatal(err)
	}
	if f.annC.lastOffer(t, r1.ID).Coordinated {
		t.Fatal("first run should not be coordinated")
	}
	if !f.annC.lastOffer(t, r2.ID).Coordinated {
		t.Fatal("both run was not offered with Job.Coordinated = true")
	}
}

func TestOutdatedRunnerOffersOnlyTakeOver(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	// Remove CapCoord from Ann's runner.
	oldHello := busyHello(4, runnerwire.CapHeadless, runnerwire.CapInteractive)
	if err := f.d.reg.SetHello(ctx, f.ann.ID, oldHello); err != nil {
		t.Fatal(err)
	}

	if _, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil)); err != nil {
		t.Fatal(err)
	}
	_, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil))
	se := requireBusy(t, err)
	if !slices.Equal(se.Busy.Choices, []string{runnerwire.OnBusyTakeOver}) {
		t.Fatalf("outdated runner choices = %v, want [takeOver]", se.Busy.Choices)
	}

	// Refuses queue and both on the outdated runner.
	for _, choice := range []string{runnerwire.OnBusyQueue, runnerwire.OnBusyBoth} {
		_, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
			r.Body.OnBusy = choice
		}))
		var refused *StartError
		if !errors.As(err, &refused) || refused.Code != StartInvalid || refused.Field != "onBusy" {
			t.Fatalf("choice %q on outdated runner: err = %#v, want StartInvalid field=onBusy", choice, err)
		}
	}
}

func TestTwoConcurrentStartsOneSeesBusy(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			_, errs[idx] = f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil))
		}(i)
	}
	close(start)
	wg.Wait()

	var okCount, busyCount int
	for _, err := range errs {
		if err == nil {
			okCount++
		} else {
			var se *StartError
			if errors.As(err, &se) && se.Code == StartBusy {
				busyCount++
			} else {
				t.Fatalf("unexpected error: %v", err)
			}
		}
	}
	if okCount != 1 || busyCount != 1 {
		t.Fatalf("concurrent starts: ok=%d busy=%d (errs=%v), want 1 ok and 1 busy", okCount, busyCount, errs)
	}
}

func TestPipelineStepWaitsWhileOtherLineageRuns(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	// Bob is actively running on n1.
	bobRun, err := f.d.Start(ctx, f.nodeReq("u-bob", "bob", f.bob.ID, "n1", nil))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, bobRun)

	// Ann has a queued writing pipeline step (revise) on n1.
	step, err := f.s.Create(ctx, New{
		Project: ".", NodeID: "n1", NodeTitle: "T", RunnerID: f.ann.ID, OwnerID: "u-ann",
		Agent: "claude", Model: "opus", RepoID: "repo-1", CreatedBy: "u-ann",
		Kind: runnerwire.JobRevise, PipelineID: "pipe-ann", Origin: runnerwire.OriginPipeline,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := f.d.Submit(ctx, step)
	if got.Status != runnerwire.RunQueued {
		t.Fatalf("writing pipeline step offered while other lineage running: %s", got.Status)
	}

	// Bob finishes -> Ann's pipeline step is pumped and offered.
	f.finishRun(t, bobRun, runnerwire.RunSucceeded, "bob done")
	got, _ = f.s.Get(ctx, step.ID)
	if got.Status != runnerwire.RunOffered {
		t.Fatalf("after Bob finished: pipeline step status = %s, want offered", got.Status)
	}
}

func TestAfterRunReleasedAcrossRunners(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	// Ann runs on ann's runner; Bob queues on bob's runner.
	annRun, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, annRun)

	bobRun, err := f.d.Start(ctx, f.nodeReq("u-bob", "bob", f.bob.ID, "n1", func(r *StartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyQueue
	}))
	if err != nil {
		t.Fatal(err)
	}
	if bobRun.Status != runnerwire.RunQueued {
		t.Fatalf("bobRun = %s, want queued", bobRun.Status)
	}

	// Ann finishes on ann's runner -> busyEnded pumps bob's runner.
	f.finishRun(t, annRun, runnerwire.RunSucceeded, "ann done")
	gotBob, _ := f.s.Get(ctx, bobRun.ID)
	if gotBob.Status != runnerwire.RunOffered {
		t.Fatalf("bobRun after ann finished = %s, want offered", gotBob.Status)
	}
}

func TestAfterRunReleasedAfterRestart(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	// Ann runs on ann's runner; Bob queues on bob's runner.
	annRun, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, annRun)

	bobRun, err := f.d.Start(ctx, f.nodeReq("u-bob", "bob", f.bob.ID, "n1", func(r *StartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyQueue
	}))
	if err != nil {
		t.Fatal(err)
	}

	// Simulate hub restart gap: finish ann directly in store (skipping busyEnded pump).
	if _, err := f.s.Finish(ctx, annRun.ID, annRun.RunnerID, runnerwire.RunResult{Status: runnerwire.RunSucceeded}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.s.Get(ctx, bobRun.ID); got.Status != runnerwire.RunQueued {
		t.Fatalf("pre-tick status = %s, want queued", got.Status)
	}

	// releaseWaiting (called on RunRetries tick) picks up bobRun and pumps bob's runner.
	f.d.releaseWaiting(ctx)
	gotBob, _ := f.s.Get(ctx, bobRun.ID)
	if gotBob.Status != runnerwire.RunOffered {
		t.Fatalf("after releaseWaiting: bobRun = %s, want offered", gotBob.Status)
	}
}

func TestAfterSectionOwnVsHidden(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	// 1. Own prior: carries redacted summary (<= 2 KiB), branch, PR URL.
	annRun, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", nil))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, annRun)
	if err := f.d.PR(ctx, annRun.RunnerID, annRun.ID, runnerwire.PRInfo{
		URL: "https://github.com/org/api/pull/42", Number: 42, State: runnerwire.PROpen,
	}); err != nil {
		t.Fatal(err)
	}
	annNext, err := f.d.Start(ctx, f.nodeReq("u-ann", "ann", f.ann.ID, "n1", func(r *StartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyQueue
	}))
	if err != nil {
		t.Fatal(err)
	}
	// Secret token in summary must be redacted.
	f.finishRun(t, annRun, runnerwire.RunSucceeded, "Updated auth with token ghp_1234567890abcdef1234567890abcdef1234 and "+strings.Repeat("x", 3000))
	ownJob := f.annC.lastOffer(t, annNext.ID)
	if !strings.Contains(ownJob.Prompt, "https://github.com/org/api/pull/42") || !strings.Contains(ownJob.Prompt, "agent/test-") {
		t.Fatalf("own carry-over missing branch or PR:\n%s", ownJob.Prompt)
	}
	if strings.Contains(ownJob.Prompt, "ghp_1234567890abcdef1234567890abcdef1234") {
		t.Fatalf("own carry-over did not redact secret:\n%s", ownJob.Prompt)
	}
	if strings.Contains(ownJob.Prompt, strings.Repeat("x", 2200)) {
		t.Fatalf("own carry-over summary was not capped to 2 KiB")
	}

	// 2. Hidden prior (another member's run): NO summary, branch, or PR URL leaked.
	f.acceptAndRun(t, annNext)
	bobNext, err := f.d.Start(ctx, f.nodeReq("u-bob", "bob", f.bob.ID, "n1", func(r *StartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyQueue
	}))
	if err != nil {
		t.Fatal(err)
	}
	f.finishRun(t, annNext, runnerwire.RunSucceeded, "Top secret ann implementation details")
	hiddenJob := f.bobC.lastOffer(t, bobNext.ID)
	if !strings.Contains(hiddenJob.Prompt, "Another run on this task finished before yours; check the base branch and open pull requests for its work.") {
		t.Fatalf("hidden carry-over missing generic notice:\n%s", hiddenJob.Prompt)
	}
	if strings.Contains(hiddenJob.Prompt, "Top secret ann") || strings.Contains(hiddenJob.Prompt, "agent/test-") {
		t.Fatalf("hidden carry-over leaked another member's details:\n%s", hiddenJob.Prompt)
	}
}

func TestBusyAuditHasNoTaskText(t *testing.T) {
	ctx := context.Background()
	f := newBusyFixture(t, 4)

	secretTask := "ULTRA_SECRET_PAYROLL_PASSWORD_999"
	g1, err := f.d.StartGlobal(ctx, f.globalReq("u-ann", "ann", f.ann.ID, "repo-1", secretTask, nil))
	if err != nil {
		t.Fatal(err)
	}
	f.acceptAndRun(t, g1)

	_, err = f.d.StartGlobal(ctx, f.globalReq("u-ann", "ann", f.ann.ID, "repo-1", secretTask+" take over", func(r *GlobalStartRequest) {
		r.Body.OnBusy = runnerwire.OnBusyTakeOver
		r.Body.HolderIDs = []string{g1.ID}
	}))
	if err != nil {
		t.Fatal(err)
	}

	f.auditM.Lock()
	audits := append([]AuditEntry(nil), f.audits...)
	f.auditM.Unlock()

	var sawBusy, sawTakeOver bool
	for _, a := range audits {
		raw, _ := json.Marshal(a)
		if strings.Contains(string(raw), secretTask) {
			t.Fatalf("audit entry %q leaked task text: %s", a.Action, raw)
		}
		if a.Action == "run.busy" {
			sawBusy = true
		}
		if a.Action == "run.takeover" {
			sawTakeOver = true
		}
	}
	if !sawBusy || !sawTakeOver {
		t.Fatalf("expected run.busy and run.takeover audits, got %+v", audits)
	}
}
