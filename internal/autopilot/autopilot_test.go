package autopilot

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
	"nodevas/internal/testpg"
)

// ---- fakes ----

type fakeRuns struct {
	mu        sync.Mutex
	last      time.Time
	runs      map[string]*runs.Record
	pipelines map[string]*runs.PipelineRecord
	starts    []runs.StartRequest
	merges    []string
	cancelled []string
	reviews   []string // "<pipeline> <verdict> <comment>"
	startErr  error
	rules     []runnerwire.RouteRule
	changes   map[string]*runnerwire.RunChanges
}

func newFakeRuns() *fakeRuns {
	return &fakeRuns{runs: map[string]*runs.Record{}, pipelines: map[string]*runs.PipelineRecord{}}
}

// stamp is the time now, strictly after the last one, as the run store's.
func (f *fakeRuns) stamp() string {
	t := time.Now().UTC().Truncate(time.Microsecond)
	if !t.After(f.last) {
		t = f.last.Add(time.Microsecond)
	}
	f.last = t
	return t.Format("2006-01-02T15:04:05.000000Z07:00")
}

func (f *fakeRuns) Start(_ context.Context, req runs.StartRequest) (runs.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return runs.Record{}, f.startErr
	}
	f.starts = append(f.starts, req)
	r := &runs.Record{}
	r.ID = fmt.Sprintf("run-%d", len(f.runs)+1)
	r.Project, r.NodeID, r.RunnerID, r.RunnerName = req.Project, req.Body.NodeID, req.Body.RunnerID, "pc"
	r.Agent, r.Model, r.Status, r.CreatedAt = req.Body.Agent, req.Body.Model, runnerwire.RunOffered, f.stamp()
	r.CreatedBy = req.ActorID
	r.Kind, r.Role = req.Body.Kind, req.Body.Role
	if req.Body.Pipeline != nil {
		p := &runs.PipelineRecord{}
		p.ID = "pipe-" + r.ID
		p.NodeID, p.Pipeline, p.Round, p.Status = r.NodeID, *req.Body.Pipeline, 1, runnerwire.PipelineRunning
		f.pipelines[p.ID] = p
		r.PipelineID = p.ID
	}
	f.runs[r.ID] = r
	return *r, nil
}

func (f *fakeRuns) Get(_ context.Context, id string) (runs.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.runs[id]
	if !ok {
		return runs.Record{}, runs.ErrNotFound
	}
	return *r, nil
}

func (f *fakeRuns) NodeRuns(_ context.Context, project, nodeID string) ([]runs.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []runs.Record
	for _, r := range f.runs {
		if r.NodeID == nodeID {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (f *fakeRuns) Pipeline(_ context.Context, id string) (runs.PipelineRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.pipelines[id]
	if !ok {
		return runs.PipelineRecord{}, runs.ErrNotFound
	}
	return *p, nil
}

func (f *fakeRuns) PipelineRuns(_ context.Context, id string) ([]runs.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []runs.Record
	for _, r := range f.runs {
		if r.PipelineID == id {
			out = append(out, *r)
		}
	}
	slices.SortFunc(out, func(a, b runs.Record) int { return strings.Compare(a.CreatedAt, b.CreatedAt) })
	return out, nil
}

func (f *fakeRuns) Merge(_ context.Context, runID string, by runs.Approver, headSHA string) (runs.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.runs[runID]
	if err := runs.CheckMergeable(*r, headSHA); err != nil {
		return *r, err
	}
	f.merges = append(f.merges, runID)
	r.Merge = &runnerwire.MergeRequest{ID: "m-" + runID, RunID: runID, HeadSHA: headSHA,
		Status: runnerwire.MergePending, RequestedBy: by.Name, CreatedAt: f.stamp()}
	return *r, nil
}

func (f *fakeRuns) Cancel(_ context.Context, runID, reason string) (runs.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, runID)
	r := f.runs[runID]
	r.Status = runnerwire.RunCancelled
	return *r, nil
}

func (f *fakeRuns) HumanReview(_ context.Context, id string, _ runs.Reviewer, verdict, comment string) (runs.PipelineRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.pipelines[id]
	f.reviews = append(f.reviews, id+" "+verdict+" "+comment)
	if verdict == runnerwire.VerdictApprove {
		p.Status = runnerwire.PipelineApproved
	} else {
		p.Status = runnerwire.PipelineRunning
	}
	return *p, nil
}

func (f *fakeRuns) CancelPipeline(_ context.Context, id, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, id)
	f.pipelines[id].Status = runnerwire.PipelineCancelled
	return nil
}

func (f *fakeRuns) Routing(context.Context, string) ([]runnerwire.RouteRule, error) {
	return f.rules, nil
}
func (f *fakeRuns) Busy(context.Context, string) (int, error) { return 0, nil }

func (f *fakeRuns) Changes(_ context.Context, runID string) (*runnerwire.RunChanges, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.changes[runID], nil
}

// commit records what a run committed: its head and changed paths.
func (f *fakeRuns) commit(runID, sha string, paths ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.changes == nil {
		f.changes = map[string]*runnerwire.RunChanges{}
	}
	ch := &runnerwire.RunChanges{Commit: sha, Files: []runnerwire.FileChange{}}
	for _, p := range paths {
		ch.Files = append(ch.Files, runnerwire.FileChange{Path: p, Status: runnerwire.FileModified})
	}
	f.changes[runID] = ch
}

// update changes a run under the lock.
func (f *fakeRuns) update(id string, fn func(*runs.Record)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f.runs[id])
}

// add records a run the dispatcher made by itself (a pipeline step, a
// fallback).
func (f *fakeRuns) add(r runs.Record) runs.Record {
	f.mu.Lock()
	defer f.mu.Unlock()
	r.ID = fmt.Sprintf("run-%d", len(f.runs)+1)
	r.CreatedAt = f.stamp()
	f.runs[r.ID] = &r
	return r
}

func (f *fakeRuns) startsFor(node string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, s := range f.starts {
		if s.Body.NodeID == node {
			n++
		}
	}
	return n
}

func (f *fakeRuns) lastStart(node string) runs.StartRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.starts) - 1; i >= 0; i-- {
		if f.starts[i].Body.NodeID == node {
			return f.starts[i]
		}
	}
	return runs.StartRequest{}
}

type fakeRunners struct{ list []runnerwire.Runner }

func (f fakeRunners) Owned(_ context.Context, owner string) ([]runnerwire.Runner, error) {
	var out []runnerwire.Runner
	for _, r := range f.list {
		if r.OwnerID == owner {
			out = append(out, r)
		}
	}
	return out, nil
}

func onlineRunner(owner string) runnerwire.Runner {
	return runnerwire.Runner{ID: "runner-1", Name: "pc", OwnerID: owner, Online: true, Info: &runnerwire.Info{
		Capabilities: []string{runnerwire.CapHeadless},
		Agents: []runnerwire.Agent{
			{Kind: "claude", Installed: true, Models: []runnerwire.Model{{ID: "default", Default: true}, {ID: "opus"}}},
			{Kind: "codex", Installed: true, Models: []runnerwire.Model{{ID: "gpt-5.5", Default: true}}},
			{Kind: "gemini", Installed: false},
		},
		Repos: []runnerwire.Repo{{ID: "repo-1", Name: "app"}},
	}}
}

type fakeBoard struct {
	mu    sync.Mutex
	graph *engine.Graph
	state *engine.RunState
	sets  []string
	// bodies overrides a node's content ("Do <id>." otherwise).
	bodies map[string]string
	// refs are the reference notes a node's run is given.
	refs map[string][]runs.Reference
	// createdBy is the run that created a node (roles_test.go).
	createdBy map[string]string
}

// newBoard builds a graph: nodes by id (kind after a colon: "x:gate"), and
// edges "a>b" (a before b).
func newBoard(nodes []string, edges []string) *fakeBoard {
	g := &engine.Graph{Version: 1}
	for _, spec := range nodes {
		id, kind, _ := strings.Cut(spec, ":")
		g.Nodes = append(g.Nodes, &engine.Node{ID: id, Title: strings.ToUpper(id), Kind: kind})
	}
	for _, spec := range edges {
		from, to, _ := strings.Cut(spec, ">")
		relation := ""
		if strings.HasSuffix(to, "~") {
			to, relation = strings.TrimSuffix(to, "~"), engine.RelationDeprecated
		}
		g.Edges = append(g.Edges, &engine.Edge{From: from, To: to, Relation: relation})
		target := g.NodeByID(to)
		if relation == "" {
			if target.Requires == "" {
				target.Requires = from
			} else {
				target.Requires += " and " + from
			}
		}
	}
	return &fakeBoard{graph: g, state: engine.NewRunState(time.Now())}
}

func (b *fakeBoard) Graph(string) (*engine.Graph, *engine.RunState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, err := engine.MarshalRunState(b.state)
	if err != nil {
		return nil, nil, err
	}
	rs, err := engine.ParseRunState(data)
	return b.graph, rs, err
}

func (b *fakeBoard) Content(_, id string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if body, ok := b.bodies[id]; ok {
		return body, nil
	}
	return "Do " + id + ".", nil
}

func (b *fakeBoard) References(_, id string) ([]runs.Reference, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.refs[id], false, nil
}

// edit changes a node's content.
func (b *fakeBoard) edit(id, body string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.bodies == nil {
		b.bodies = map[string]string{}
	}
	b.bodies[id] = body
}

func (b *fakeBoard) SetStatus(_, id string, status engine.Status, by, note string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sets = append(b.sets, id+"="+string(status))
	return b.state.SetStatus(id, status, by, time.Now())
}

func (b *fakeBoard) status(id string) engine.Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	return engine.ComputeStatuses(b.graph, b.state)[id]
}

// ---- harness ----

type harness struct {
	t     *testing.T
	o     *Orchestrator
	runs  *fakeRuns
	board *fakeBoard
	store *Store
	ctx   context.Context
	bea   identity.Actor
}

func newHarness(t *testing.T, board *fakeBoard) *harness {
	t.Helper()
	database := testpg.New(t)
	t.Cleanup(func() { _ = database.Close() })
	h := &harness{t: t, runs: newFakeRuns(), board: board, store: NewStore(database), ctx: context.Background(),
		bea: identity.Actor{ID: "bea", Name: "Bea"}}
	h.o = New(Options{Store: h.store, Runs: h.runs, Runners: fakeRunners{list: []runnerwire.Runner{onlineRunner("bea")}},
		Board: board})
	return h
}

func (h *harness) create(policy runnerwire.AutopilotPolicy) runnerwire.AutopilotView {
	h.t.Helper()
	view, err := h.o.Create(h.ctx, CreateInput{Project: "main", GoalNodeID: "goal", Policy: policy, Actor: h.bea})
	if err != nil {
		h.t.Fatalf("create: %v", err)
	}
	return view
}

func (h *harness) view(id string) runnerwire.AutopilotView {
	h.t.Helper()
	h.o.Reconcile(h.ctx, id)
	v, err := h.o.View(h.ctx, id)
	if err != nil {
		h.t.Fatal(err)
	}
	return v
}

func task(v runnerwire.AutopilotView, node string) runnerwire.TaskInstance {
	for _, t := range v.Tasks {
		if t.NodeID == node {
			return t
		}
	}
	return runnerwire.TaskInstance{}
}

func states(v runnerwire.AutopilotView) string {
	var parts []string
	for _, t := range v.Tasks {
		parts = append(parts, t.NodeID+"="+t.State)
	}
	return strings.Join(parts, " ")
}

func stageList(t runnerwire.TaskInstance) string {
	var parts []string
	for _, s := range t.Stages {
		parts = append(parts, s.Stage+":"+s.Status)
	}
	return strings.Join(parts, " ")
}

// finishWithPR ends a plain run successfully with a commit and a PR.
func (h *harness) finishWithPR(runID string) {
	h.runs.update(runID, func(r *runs.Record) {
		r.Status, r.StartedAt, r.FinishedAt = runnerwire.RunSucceeded, h.runs.stampLocked(), h.runs.stampLocked()
		r.Commit, r.Branch, r.PRURL, r.PRState = "abcdef1234", "agent/x", "https://github.com/o/r/pull/"+runID, runnerwire.PROpen
		r.PRHeadSHA = "abcdef1234"
	})
}

func (f *fakeRuns) stampLocked() string { return f.stamp() }

// ---- tests ----

func TestRulesAdvisor(t *testing.T) {
	agents := onlineRunner("bea").Info.Agents
	routing := []runnerwire.RouteRule{{ID: "r1", Match: runnerwire.RouteDefault,
		Target:    runnerwire.AgentModel{Agent: "codex", Model: "gpt-5.5"},
		Fallbacks: []runnerwire.AgentModel{{Agent: "gemini"}, {Agent: "claude"}}}}
	a := rulesAdvisor{routing: func(context.Context, string) ([]runnerwire.RouteRule, error) { return routing, nil }}

	c, err := a.Choose(context.Background(), ChooseInput{Agents: agents, Policy: runnerwire.AutopilotPolicy{
		Implement: []runnerwire.AgentModel{{Agent: "gemini"}, {Agent: "claude", Model: "opus"}, {Agent: "claude", Model: "nope"}}}})
	if err != nil || len(c.Implement) != 1 || c.Implement[0] != (runnerwire.AgentModel{Agent: "claude", Model: "opus"}) {
		t.Fatalf("policy chain: %+v %v", c, err)
	}
	c, err = a.Choose(context.Background(), ChooseInput{Agents: agents})
	if err != nil || len(c.Implement) != 2 || c.Implement[0].Agent != "codex" || c.Implement[1].Agent != "claude" ||
		!strings.Contains(c.Reason, "r1") {
		t.Fatalf("routing: %+v %v", c, err)
	}
	routing = nil
	c, err = a.Choose(context.Background(), ChooseInput{Agents: agents})
	if err != nil || len(c.Implement) != 1 || c.Implement[0].Agent != "claude" {
		t.Fatalf("first installed: %+v %v", c, err)
	}
	if _, err := a.Choose(context.Background(), ChooseInput{Agents: []runnerwire.Agent{{Kind: "gemini"}}}); !errors.Is(err, errNoAgent) {
		t.Fatalf("no agent: %v", err)
	}
}

func TestBuildDAG(t *testing.T) {
	b := newBoard([]string{"goal", "a", "b", "c", "g:gate", "x", "old"},
		[]string{"a>b", "b>goal", "c>goal", "g>c", "x>a", "old>goal~", "a>x"})
	g, _, _ := b.Graph("")
	d := buildDAG(g, "goal")
	for _, id := range []string{"a", "b", "c", "g", "x"} {
		if _, ok := d.nodes[id]; !ok {
			t.Fatalf("%s missing from %v", id, d.order)
		}
	}
	if _, ok := d.nodes["old"]; ok {
		t.Fatal("a deprecated edge brought a node in")
	}
	if !d.inCycle("a") || !d.inCycle("x") || d.inCycle("c") {
		t.Fatal("cycle detection")
	}
	if d.dispatchable("g") || !d.dispatchable("a") {
		t.Fatal("dispatchable")
	}
	if i, j := slices.Index(d.order, "g"), slices.Index(d.order, "c"); i < 0 || i > j {
		t.Fatalf("order %v: g must come before c", d.order)
	}
}

func TestScheduleParallelUpstreamAndRetry(t *testing.T) {
	// a → b → goal, c → goal, d → goal; two at a time, no review.
	h := newHarness(t, newBoard([]string{"goal", "a", "b", "c", "d"}, []string{"a>b", "b>goal", "c>goal", "d>goal"}))
	v := h.create(runnerwire.AutopilotPolicy{MaxParallel: 2})
	if v.Run.Status != runnerwire.AutopilotRunning || v.Run.Policy.RepoID != "repo-1" {
		t.Fatalf("run = %+v", v.Run)
	}
	if got := states(v); got != "a=queued b=none c=queued d=scheduled" {
		t.Fatalf("after create: %s", got)
	}
	if st := h.runs.lastStart("a"); st.Body.Pipeline != nil || st.Body.RepoID != "repo-1" || st.Body.Agent != "claude" ||
		st.ActorID != "bea" || !strings.Contains(st.Body.Prompt, "goal") {
		t.Fatalf("start a = %+v", st)
	}

	// a merges nothing (no review, no auto-merge): its PR is the end.
	h.finishWithPR(task(v, "a").RunID)
	v = h.view(v.Run.ID)
	if a := task(v, "a"); a.State != runnerwire.TaskSuccess || a.Stage != runnerwire.StageApproved ||
		!strings.Contains(stageList(a), "queued:success implement:success commit:success push:success pr:success approved:success") {
		t.Fatalf("a = %+v (%s)", a, stageList(a))
	}
	if h.board.status("a") != engine.StatusDone {
		t.Fatal("a is not done on the board")
	}
	if got := states(v); got != "a=success c=queued d=queued b=queued" {
		// c still holds a slot; b is ready now (a done) and d takes the
		// other free slot first by order... both got one: two slots, c held
		// one, a's freed one went to d, and b waits? No: MaxParallel 2 and c
		// active leave one slot. Order is topological: b comes after a.
		t.Logf("states: %s", got)
	}
	active := 0
	for _, tk := range v.Tasks {
		if tk.State == runnerwire.TaskQueued {
			active++
		}
	}
	if active != 2 {
		t.Fatalf("active = %d, want 2: %s", active, states(v))
	}

	// c fails; the others finish: the autopilot fails, then a retry of c
	// reopens it.
	h.runs.update(task(v, "c").RunID, func(r *runs.Record) { r.Status, r.Error = runnerwire.RunFailed, "boom" })
	for _, id := range []string{"b", "d"} {
		v = h.view(v.Run.ID)
		if run := task(v, id).RunID; run != "" {
			h.finishWithPR(run)
		}
	}
	v = h.view(v.Run.ID)
	for _, id := range []string{"b", "d"} {
		if run := task(v, id).RunID; run != "" && task(v, id).State != runnerwire.TaskSuccess {
			h.finishWithPR(run)
		}
	}
	v = h.view(v.Run.ID)
	if v.Run.Status != runnerwire.AutopilotFailed || task(v, "c").State != runnerwire.TaskFailed ||
		!strings.Contains(task(v, "c").Reason, "boom") {
		t.Fatalf("after c failed: %s %+v", states(v), v.Run)
	}
	if h.board.status("goal") == engine.StatusDone {
		t.Fatal("a failed autopilot marked the goal done")
	}
	if err := h.o.Retry(h.ctx, v.Run.ID, "c"); err != nil {
		t.Fatal(err)
	}
	v = h.view(v.Run.ID)
	if v.Run.Status != runnerwire.AutopilotRunning || task(v, "c").State != runnerwire.TaskQueued || task(v, "c").Try != 2 {
		t.Fatalf("after retry: %+v %s", v.Run, states(v))
	}
	h.finishWithPR(task(v, "c").RunID)
	v = h.view(v.Run.ID)
	if v.Run.Status != runnerwire.AutopilotSucceeded || h.board.status("goal") != engine.StatusDone {
		t.Fatalf("after c: %+v %s", v.Run, states(v))
	}
	if v.Run.Counts.Success != 4 || v.Run.Counts.Total != 4 {
		t.Fatalf("counts = %+v", v.Run.Counts)
	}
}

func TestUpstreamFailedAndSkip(t *testing.T) {
	h := newHarness(t, newBoard([]string{"goal", "a", "b", "c"}, []string{"a>b", "b>c", "c>goal"}))
	v := h.create(runnerwire.AutopilotPolicy{})
	h.runs.update(task(v, "a").RunID, func(r *runs.Record) { r.Status = runnerwire.RunDeclined; r.Error = "no" })
	v = h.view(v.Run.ID)
	if got := states(v); got != "a=failed b=upstream_failed c=upstream_failed" {
		t.Fatalf("states = %s", got)
	}
	if !strings.Contains(stageList(task(v, "a")), "queued:failed") {
		t.Fatalf("a stages = %s", stageList(task(v, "a")))
	}
	if err := h.o.Skip(h.ctx, v.Run.ID, "a"); err != nil {
		t.Fatal(err)
	}
	v = h.view(v.Run.ID)
	if got := states(v); got != "a=skipped b=queued c=none" {
		t.Fatalf("after skip = %s", got)
	}
}

func TestPipelineStagesAndAutoMerge(t *testing.T) {
	h := newHarness(t, newBoard([]string{"goal", "a"}, []string{"a>goal"}))
	v := h.create(runnerwire.AutopilotPolicy{AutoMerge: true, AllowAgentOnlyMerge: true, MaxRounds: 2, Review: runnerwire.ReviewPolicy{
		Mode: runnerwire.ReviewAgents, Reviewers: []runnerwire.AgentModel{{Agent: "codex"}}}})
	a := task(v, "a")
	st := h.runs.lastStart("a")
	if st.Body.Pipeline == nil || len(st.Body.Pipeline.Reviewers) != 1 || st.Body.Pipeline.Reviewers[0].Agent != "codex" || st.Body.Pipeline.MaxRounds != 2 || a.PipelineID == "" {
		t.Fatalf("start = %+v, task = %+v", st, a)
	}
	impl := a.RunID
	h.runs.update(impl, func(r *runs.Record) { r.Status, r.StartedAt = runnerwire.RunRunning, h.runs.stampLocked() })
	if got := task(h.view(v.Run.ID), "a"); got.State != runnerwire.TaskRunning || got.Stage != runnerwire.StageImplement {
		t.Fatalf("running: %+v", got)
	}
	h.finishWithPR(impl)
	h.runs.commit(impl, "abcdef1234", "internal/a.go")
	h.runs.update(impl, func(r *runs.Record) { r.CIStatus = runnerwire.CIPending })
	review := h.runs.add(runs.Record{Run: runnerwire.Run{NodeID: "a", Agent: "codex", Kind: runnerwire.JobReview,
		PipelineID: a.PipelineID, Status: runnerwire.RunRunning}})
	if got := task(h.view(v.Run.ID), "a"); got.State != runnerwire.TaskWaitingReview {
		t.Fatalf("reviewing: %+v", got)
	}
	h.runs.update(review.ID, func(r *runs.Record) { r.Status, r.Verdict = runnerwire.RunSucceeded, runnerwire.VerdictApprove })
	h.runs.mu.Lock()
	h.runs.pipelines[a.PipelineID].Status = runnerwire.PipelineApproved
	h.runs.mu.Unlock()
	got := task(h.view(v.Run.ID), "a")
	if got.State != runnerwire.TaskRunning || got.Stage != runnerwire.StageCI || len(h.runs.merges) != 0 {
		t.Fatalf("approved, CI pending: %+v merges=%v", got, h.runs.merges)
	}
	if len(got.Votes) != 1 || got.Votes[0].Verdict != runnerwire.VerdictApprove || got.Votes[0].Round != 1 {
		t.Fatalf("votes = %+v", got.Votes)
	}
	h.runs.update(impl, func(r *runs.Record) { r.CIStatus = runnerwire.CISuccess })
	got = task(h.view(v.Run.ID), "a")
	if len(h.runs.merges) != 1 || got.Stage != runnerwire.StageMerge || got.State != runnerwire.TaskRunning {
		t.Fatalf("merge requested: %+v merges=%v", got, h.runs.merges)
	}
	// Another pass does not ask again while the merge is pending.
	h.view(v.Run.ID)
	if len(h.runs.merges) != 1 {
		t.Fatalf("merges = %v", h.runs.merges)
	}
	h.runs.update(impl, func(r *runs.Record) { r.Merge.Status, r.PRState = runnerwire.MergeDone, runnerwire.PRMerged })
	v = h.view(v.Run.ID)
	got = task(v, "a")
	// In the order things happened: the checks went green after the approval.
	want := "queued:success implement:success commit:success push:success pr:success review:success approved:success ci:success merge:success done:success"
	if got.State != runnerwire.TaskSuccess || stageList(got) != want {
		t.Fatalf("merged: %s\n stages %s\n want   %s", got.State, stageList(got), want)
	}
	if v.Run.Status != runnerwire.AutopilotSucceeded || h.board.status("a") != engine.StatusDone ||
		h.board.status("goal") != engine.StatusDone {
		t.Fatalf("run %+v a=%s goal=%s", v.Run, h.board.status("a"), h.board.status("goal"))
	}
}

func TestHumanReview(t *testing.T) {
	h := newHarness(t, newBoard([]string{"goal", "a"}, []string{"a>goal"}))
	v := h.create(runnerwire.AutopilotPolicy{MaxRounds: 2, Review: runnerwire.ReviewPolicy{Mode: runnerwire.ReviewHuman}})
	st := h.runs.lastStart("a")
	if st.Body.Pipeline == nil || !st.Body.Pipeline.Human || len(st.Body.Pipeline.Reviewers) != 0 {
		t.Fatalf("a human-only review is a pipeline that waits for a person: %+v", st.Body.Pipeline)
	}
	a := task(v, "a")
	h.finishWithPR(a.RunID)
	h.runs.mu.Lock()
	h.runs.pipelines[a.PipelineID].Status = runnerwire.PipelineWaitingHuman
	h.runs.mu.Unlock()
	v = h.view(v.Run.ID)
	if got := task(v, "a"); got.State != runnerwire.TaskWaitingHuman || got.Stage != runnerwire.StageHuman {
		t.Fatalf("waiting: %+v", got)
	}
	// Someone who is neither the creator, the runner's owner nor an
	// administrator may not review unless the policy lets any editor; an
	// administrator may.
	cal := runs.Reviewer{ID: "cal", Name: "Cal"}
	if err := h.o.Review(h.ctx, v.Run.ID, "a", cal, false, runnerwire.VerdictApprove, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a stranger's review: %v", err)
	}
	if len(h.runs.reviews) != 0 {
		t.Fatalf("a refused review reached the pipeline: %q", h.runs.reviews)
	}
	ann := runs.Reviewer{ID: "bea", Name: "Bea"} // the autopilot's creator
	if err := h.o.Review(h.ctx, v.Run.ID, "a", ann, false, "maybe", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad verdict: %v", err)
	}
	// The pipeline decides: changes go to it as the person's review.
	if err := h.o.Review(h.ctx, v.Run.ID, "a", ann, false, runnerwire.VerdictChanges, "Handle the empty list."); err != nil {
		t.Fatal(err)
	}
	if len(h.runs.reviews) != 1 || h.runs.reviews[0] != a.PipelineID+" changes Handle the empty list." {
		t.Fatalf("reviews = %q", h.runs.reviews)
	}
	h.runs.mu.Lock()
	h.runs.pipelines[a.PipelineID].Status = runnerwire.PipelineWaitingHuman
	h.runs.mu.Unlock()
	h.view(v.Run.ID)
	if err := h.o.Review(h.ctx, v.Run.ID, "a", ann, false, runnerwire.VerdictApprove, ""); err != nil {
		t.Fatal(err)
	}
	v = h.view(v.Run.ID)
	if got := task(v, "a"); got.State != runnerwire.TaskSuccess {
		t.Fatalf("approved: %+v %s", got, stageList(got))
	}
	if err := h.o.Review(h.ctx, v.Run.ID, "a", ann, false, runnerwire.VerdictApprove, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("second review: %v", err)
	}
}

func TestPauseResumeCancel(t *testing.T) {
	h := newHarness(t, newBoard([]string{"goal", "a", "b"}, []string{"a>goal", "b>goal"}))
	v := h.create(runnerwire.AutopilotPolicy{Review: runnerwire.ReviewPolicy{Mode: runnerwire.ReviewAgents,
		Reviewers: []runnerwire.AgentModel{{Agent: "claude"}}}})
	if err := h.o.Pause(h.ctx, v.Run.ID); err != nil {
		t.Fatal(err)
	}
	h.runs.update(task(v, "a").RunID, func(r *runs.Record) { r.Status = runnerwire.RunFailed })
	h.runs.mu.Lock()
	h.runs.pipelines[task(v, "a").PipelineID].Status = runnerwire.PipelineFailed
	h.runs.mu.Unlock()
	v = h.view(v.Run.ID)
	if v.Run.Status != runnerwire.AutopilotPaused || h.runs.startsFor("b") != 0 {
		t.Fatalf("paused: %+v starts(b)=%d", v.Run, h.runs.startsFor("b"))
	}
	if err := h.o.Pause(h.ctx, v.Run.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("pause twice: %v", err)
	}
	if err := h.o.Resume(h.ctx, v.Run.ID); err != nil {
		t.Fatal(err)
	}
	v = h.view(v.Run.ID)
	if h.runs.startsFor("b") != 1 || task(v, "b").State != runnerwire.TaskQueued {
		t.Fatalf("resumed: %s", states(v))
	}
	if err := h.o.Cancel(h.ctx, v.Run.ID, ""); err != nil {
		t.Fatal(err)
	}
	v = h.view(v.Run.ID)
	if v.Run.Status != runnerwire.AutopilotCancelled || task(v, "b").State != runnerwire.TaskSkipped ||
		!slices.Contains(h.runs.cancelled, task(v, "b").PipelineID) {
		t.Fatalf("cancelled: %+v %s cancelled=%v", v.Run, states(v), h.runs.cancelled)
	}
	if err := h.o.Retry(h.ctx, v.Run.ID, "a"); !errors.Is(err, ErrConflict) {
		t.Fatalf("retry after cancel: %v", err)
	}
	// The goal can have a new autopilot now.
	h.create(runnerwire.AutopilotPolicy{})
}

func TestOneLiveAutopilotPerGoalAndValidation(t *testing.T) {
	h := newHarness(t, newBoard([]string{"goal", "a", "empty"}, []string{"a>goal"}))
	h.create(runnerwire.AutopilotPolicy{})
	if _, err := h.o.Create(h.ctx, CreateInput{Project: "main", GoalNodeID: "goal", Actor: h.bea}); !errors.Is(err, ErrConflict) {
		t.Fatalf("second live: %v", err)
	}
	if _, err := h.o.Create(h.ctx, CreateInput{Project: "main", GoalNodeID: "empty", Actor: h.bea}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no tasks: %v", err)
	}
	if _, err := h.o.Create(h.ctx, CreateInput{Project: "main", GoalNodeID: "nope", Actor: h.bea}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no goal: %v", err)
	}
	for _, p := range []runnerwire.AutopilotPolicy{
		{MaxParallel: 99},
		{MaxRounds: 9},
		{Advisor: "magic"},
		{Review: runnerwire.ReviewPolicy{Mode: runnerwire.ReviewAgents}},
		{Review: runnerwire.ReviewPolicy{Mode: runnerwire.ReviewAgents, Reviewers: []runnerwire.AgentModel{{Agent: "codex"}}, Quorum: 2}},
		{Review: runnerwire.ReviewPolicy{Mode: runnerwire.ReviewHuman, Reviewers: []runnerwire.AgentModel{{Agent: "codex"}}}},
		{Review: runnerwire.ReviewPolicy{Mode: "vote"}},
	} {
		if _, err := ValidatePolicy(p); !errors.Is(err, ErrInvalid) && !errors.Is(err, runs.ErrInvalid) {
			t.Errorf("policy %+v: %v", p, err)
		}
	}
}

func TestBoardAndNonTasks(t *testing.T) {
	b := newBoard([]string{"goal", "done", "g:gate", "a", "x", "y"}, []string{"done>a", "g>a", "a>goal", "x>y", "y>x", "y>goal"})
	_ = b.SetStatus("", "done", engine.StatusDone, "me", "")
	h := newHarness(t, b)
	v := h.create(runnerwire.AutopilotPolicy{MaxParallel: 3})
	// A review waits for a person, and what depends on it waits with it.
	if task(v, "done").State != runnerwire.TaskSuccess || task(v, "g").State != runnerwire.TaskWaitingHuman ||
		task(v, "g").Hold != runnerwire.HoldPerson ||
		task(v, "a").State != runnerwire.TaskNone || task(v, "x").State != runnerwire.TaskFailed ||
		!strings.Contains(task(v, "x").Reason, "cycle") {
		t.Fatalf("states = %s", states(v))
	}
	if h.runs.startsFor("done") != 0 || h.runs.startsFor("g") != 0 {
		t.Fatal("dispatched a settled node or a gate")
	}
	// The person approves the review on the board: it succeeds and releases a.
	_ = b.SetStatus("", "g", engine.StatusDone, "me", "")
	v = h.view(v.Run.ID)
	if task(v, "g").State != runnerwire.TaskSuccess || task(v, "g").Hold != "" || task(v, "a").State != runnerwire.TaskQueued {
		t.Fatalf("after the review: %s", states(v))
	}
}

func TestStoryWritingUnitsAreDispatchedAndNotesAreNotMembers(t *testing.T) {
	b := newBoard([]string{"goal:goal", "s:scene", "e:event", "sub:goal", "n:note", "open:start"},
		[]string{"open>s", "s>e", "e>goal", "sub>goal", "n>goal"})
	h := newHarness(t, b)
	v := h.create(runnerwire.AutopilotPolicy{MaxParallel: 3})
	if task(v, "n").NodeID != "" {
		t.Fatalf("a note linked into the goal became a task: %s", states(v))
	}
	if task(v, "open").State != runnerwire.TaskSuccess {
		t.Fatalf("a start settles itself: %s", states(v))
	}
	if task(v, "s").State != runnerwire.TaskQueued || h.runs.startsFor("s") != 1 {
		t.Fatalf("a scene was not dispatched: %s", states(v))
	}
	if task(v, "sub").State != runnerwire.TaskWaitingHuman || h.runs.startsFor("sub") != 0 {
		t.Fatalf("a goal inside the goal was dispatched: %s", states(v))
	}
	h.finishWithPR(task(v, "s").RunID)
	v = h.view(v.Run.ID)
	if h.runs.startsFor("e") != 1 {
		t.Fatalf("an event was not dispatched once its scene finished: %s", states(v))
	}
}

func TestAGoalOfANonAgentTypeIsRefused(t *testing.T) {
	for _, kind := range []string{"gate", "note", "choice"} {
		b := newBoard([]string{"goal:" + kind, "a"}, []string{"a>goal"})
		h := newHarness(t, b)
		_, err := h.o.Create(h.ctx, CreateInput{Project: "main", GoalNodeID: "goal",
			Policy: runnerwire.AutopilotPolicy{}, Actor: h.bea})
		if !errors.Is(err, ErrNodeType) {
			t.Fatalf("%s goal: err = %v, want ErrNodeType", kind, err)
		}
	}
}

func TestRestartRecovery(t *testing.T) {
	h := newHarness(t, newBoard([]string{"goal", "a", "b"}, []string{"a>b", "b>goal"}))
	v := h.create(runnerwire.AutopilotPolicy{})
	h.finishWithPR(task(v, "a").RunID)

	// A new orchestrator on the same store: the restart.
	again := New(Options{Store: h.store, Runs: h.runs, Runners: fakeRunners{list: []runnerwire.Runner{onlineRunner("bea")}},
		Board: h.board, Tick: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	again.Start(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for h.runs.startsFor("b") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the restarted orchestrator did not pick the autopilot up")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Updates find the autopilot through the index the pass rebuilt.
	view, _ := again.View(ctx, v.Run.ID)
	runB := task(view, "b").RunID
	for runB == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		view, _ = again.View(ctx, v.Run.ID)
		runB = task(view, "b").RunID
	}
	h.finishWithPR(runB)
	r, _ := h.runs.Get(ctx, runB)
	again.Observe(runs.Update{Run: r})
	for {
		view, _ = again.View(ctx, v.Run.ID)
		if view.Run.Status == runnerwire.AutopilotSucceeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after b: %+v %s", view.Run, states(view))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLLMAdvisorFallsBack(t *testing.T) {
	h := newHarness(t, newBoard([]string{"goal", "a", "b"}, []string{"a>goal", "b>goal"}))
	calls := 0
	RegisterLLMAdvisor(advisorFunc(func(ctx context.Context, in ChooseInput) (Choice, error) {
		calls++
		if in.NodeID == "a" {
			return Choice{Implement: []runnerwire.AgentModel{{Agent: "codex"}}, Reason: "llm"}, nil
		}
		return Choice{}, errors.New("model unavailable")
	}))
	t.Cleanup(func() { RegisterLLMAdvisor(nil) })
	h.create(runnerwire.AutopilotPolicy{MaxParallel: 2, Advisor: runnerwire.AdvisorLLM})
	if calls != 2 || h.runs.lastStart("a").Body.Agent != "codex" || h.runs.lastStart("b").Body.Agent != "claude" {
		t.Fatalf("calls=%d a=%s b=%s", calls, h.runs.lastStart("a").Body.Agent, h.runs.lastStart("b").Body.Agent)
	}
}

type advisorFunc func(context.Context, ChooseInput) (Choice, error)

func (f advisorFunc) Choose(ctx context.Context, in ChooseInput) (Choice, error) { return f(ctx, in) }

func TestStartRefusalsKeepTheTaskScheduled(t *testing.T) {
	h := newHarness(t, newBoard([]string{"goal", "a"}, []string{"a>goal"}))
	h.runs.startErr = &runs.StartError{Code: runs.StartBudget, Msg: "the daily budget is spent"}
	v := h.create(runnerwire.AutopilotPolicy{})
	if got := task(v, "a"); got.State != runnerwire.TaskScheduled || !strings.Contains(got.Reason, "budget") {
		t.Fatalf("budget: %+v", got)
	}
	h.runs.startErr = nil
	if got := task(h.view(v.Run.ID), "a"); got.State != runnerwire.TaskQueued {
		t.Fatalf("after the budget: %+v", got)
	}
}

// A stage that moves on keeps what it said: the queued stage's detail (why
// the advisor picked the implementer) survives the stage succeeding.
func TestKeepTimesCarriesDetail(t *testing.T) {
	prev := []runnerwire.StageRecord{{Stage: runnerwire.StageQueued, Status: runnerwire.StageRunning, At: "t1",
		RunID: "run-1", Detail: "System One (laya): opencode/x p=0.80"}}
	next := []runnerwire.StageRecord{
		{Stage: runnerwire.StageQueued, Status: runnerwire.StageOK, At: "t2", RunID: "run-1"},
		{Stage: runnerwire.StageImplement, Status: runnerwire.StageRunning, At: "t2", RunID: "run-1", Detail: "opencode/x"},
	}
	got := keepTimes(prev, next)
	if got[0].Detail != "System One (laya): opencode/x p=0.80" || got[0].At != "t2" || got[1].Detail != "opencode/x" {
		t.Fatalf("stages = %+v", got)
	}
}

// The advisor's reason stays on the task for the whole try, whatever the
// stages say later, and a new try starts without it.
func TestTaskKeepsAdvisorReason(t *testing.T) {
	h := newHarness(t, newBoard([]string{"goal", "a"}, []string{"a>goal"}))
	v := h.create(runnerwire.AutopilotPolicy{})
	a := task(v, "a")
	if a.Advisor == "" {
		t.Fatalf("no advisor reason: %+v", a)
	}
	h.runs.update(a.RunID, func(r *runs.Record) { r.Status, r.StartedAt = runnerwire.RunRunning, h.runs.stampLocked() })
	if got := task(h.view(v.Run.ID), "a"); got.Advisor != a.Advisor {
		t.Fatalf("advisor after start = %q, want %q", got.Advisor, a.Advisor)
	}
}

// A policy that lets any editor review lets a teammate give the person's
// review; the pipeline it starts says so too (humanAnyEditor).
func TestAnyEditorMayReview(t *testing.T) {
	h := newHarness(t, newBoard([]string{"goal", "a"}, []string{"a>goal"}))
	v := h.create(runnerwire.AutopilotPolicy{MaxRounds: 1,
		Review: runnerwire.ReviewPolicy{Mode: runnerwire.ReviewHuman, AnyEditor: true}})
	if st := h.runs.lastStart("a"); st.Body.Pipeline == nil || !st.Body.Pipeline.HumanAnyEditor {
		t.Fatalf("pipeline = %+v", st.Body.Pipeline)
	}
	a := task(v, "a")
	h.finishWithPR(a.RunID)
	h.runs.mu.Lock()
	h.runs.pipelines[a.PipelineID].Status = runnerwire.PipelineWaitingHuman
	h.runs.mu.Unlock()
	h.view(v.Run.ID)
	cal := runs.Reviewer{ID: "cal", Name: "Cal"}
	if err := h.o.Review(h.ctx, v.Run.ID, "a", cal, false, runnerwire.VerdictApprove, ""); err != nil {
		t.Fatal(err)
	}
	if len(h.runs.reviews) != 1 {
		t.Fatalf("reviews = %q", h.runs.reviews)
	}
}

func TestAGoalTypedGoalIsStillMarkedDoneByTheAutopilot(t *testing.T) {
	b := newBoard([]string{"goal:goal", "r:gate"}, []string{"r>goal"})
	h := newHarness(t, b)
	v := h.create(runnerwire.AutopilotPolicy{})
	if task(v, "r").Hold != runnerwire.HoldPerson {
		t.Fatalf("review not held for a person: %s", states(v))
	}
	_ = b.SetStatus("", "r", engine.StatusDone, "me", "")
	v = h.view(v.Run.ID)
	if v.Run.Status != runnerwire.AutopilotSucceeded || h.board.status("goal") != engine.StatusDone {
		t.Fatalf("run %s goal=%s states=%s", v.Run.Status, h.board.status("goal"), states(v))
	}
}

func TestAScenesReferenceNotesReachItsRun(t *testing.T) {
	b := newBoard([]string{"goal:goal", "s:scene", "n:note"}, []string{"s>goal"})
	b.refs = map[string][]runs.Reference{"s": {{ID: "n", Title: "林曉", TypeLabel: "Note", Summary: "怕水"}}}
	h := newHarness(t, b)
	v := h.create(runnerwire.AutopilotPolicy{})
	if task(v, "s").State != runnerwire.TaskQueued {
		t.Fatalf("scene not dispatched: %s", states(v))
	}
	info, found, err := h.runs.lastStart("s").LoadNode()
	if err != nil || !found {
		t.Fatalf("LoadNode: %v %v", found, err)
	}
	if len(info.References) != 1 || info.References[0].Summary != "怕水" {
		t.Fatalf("references = %+v", info.References)
	}
	prompt := runs.BuildPrompt(info.Title, info.Content, runs.ReferencesBlock(info.References, info.ReferencesTruncated), "")
	if !strings.Contains(prompt, "## Reference notes") || !strings.Contains(prompt, "### 林曉 (n, Note)") {
		t.Fatalf("prompt = %q", prompt)
	}
}
