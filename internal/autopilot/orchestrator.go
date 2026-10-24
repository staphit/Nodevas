// Package autopilot is W7's orchestrator: it works a goal node's tasks as a
// DAG, dispatching each ready task through the run dispatcher (implement →
// review → revise → merge) and recording every task instance's state and
// stages the way an Airflow DAG run does. The hub still executes nothing:
// every run is an ordinary run its runner has to accept. See
// docs/nodevas-workstation-plan.md, W7.
package autopilot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// Runs is what the orchestrator needs of the run dispatcher.
type Runs interface {
	Start(ctx context.Context, req runs.StartRequest) (runs.Record, error)
	Get(ctx context.Context, id string) (runs.Record, error)
	NodeRuns(ctx context.Context, project, nodeID string) ([]runs.Record, error)
	Pipeline(ctx context.Context, id string) (runs.PipelineRecord, error)
	PipelineRuns(ctx context.Context, id string) ([]runs.Record, error)
	Merge(ctx context.Context, runID string, by runs.Approver, headSHA string) (runs.Record, error)
	Cancel(ctx context.Context, runID, reason string) (runs.Record, error)
	CancelPipeline(ctx context.Context, id, reason string) error
	// HumanReview records a person's verdict on a pipeline waiting for one.
	HumanReview(ctx context.Context, id string, who runs.Reviewer, verdict, comment string) (runs.PipelineRecord, error)
	Routing(ctx context.Context, ownerID string) ([]runnerwire.RouteRule, error)
	Busy(ctx context.Context, runnerID string) (int, error)
}

// Runners lists a person's runners.
type Runners interface {
	Owned(ctx context.Context, ownerID string) ([]runnerwire.Runner, error)
}

// Board is the project boards: their graphs, node content and statuses.
type Board interface {
	Graph(project string) (*engine.Graph, *engine.RunState, error)
	Content(project, nodeID string) (string, error)
	SetStatus(project, nodeID string, status engine.Status, by, note string) error
}

// Options are the orchestrator's collaborators.
type Options struct {
	Store   *Store
	Runs    Runs
	Runners Runners
	Board   Board
	// Changed is told when an autopilot changed (realtime "autopilot-changed").
	Changed func(project, autopilotID string)
	// BoardChanged is told when the orchestrator moved a node's status.
	BoardChanged func(project string)
	// Tick is how often every live autopilot is looked at regardless of
	// updates; zero means DefaultTick.
	Tick time.Duration
}

// ChangedEvent is the realtime event an autopilot change sends to its
// project's room; its id is the autopilot's.
const ChangedEvent = "autopilot-changed"

// DefaultTick is the periodic pass.
const DefaultTick = 15 * time.Second

// llmAdvisorTimeout bounds an LLM advisor's answer.
const llmAdvisorTimeout = 5 * time.Second

// systemOneAdvisorTimeout bounds the System One advisor: it answers in well
// under a second, but a local Laya loads its checkpoint on its first call.
const systemOneAdvisorTimeout = 10 * time.Second

// Orchestrator runs every live autopilot.
type Orchestrator struct {
	opts  Options
	rules rulesAdvisor

	kicks chan string
	locks sync.Map // autopilot ID → *sync.Mutex

	// index maps run IDs, pipeline IDs and "project\x00goal" to the live
	// autopilot they belong to, so a dispatcher update finds its autopilot
	// without a query on the dispatcher's goroutine.
	mu    sync.Mutex
	index map[string]string

	// lastStamp is the newest pass time handed out (stamp).
	stampMu   sync.Mutex
	lastStamp time.Time
}

// stageRank orders stages first seen at the same moment: a task's pipeline
// reaches them in this order (CI last before the merge, as it gates it).
var stageRank = map[string]int{
	runnerwire.StageQueued: 0, runnerwire.StageImplement: 1, runnerwire.StageCommit: 2,
	runnerwire.StagePush: 3, runnerwire.StagePR: 4, runnerwire.StageReview: 5,
	runnerwire.StageHuman: 6, runnerwire.StageRevise: 7, runnerwire.StageApproved: 8,
	runnerwire.StageCI: 9, runnerwire.StageMerge: 10, runnerwire.StageDone: 11,
}

// stamp is a pass's time: now, but strictly after the previous pass's, as the
// run store stamps runs. The clock can tick coarsely (half a millisecond on
// Windows), and two passes with one time would order their stages by
// derivation instead of by when they were seen.
func (o *Orchestrator) stamp() string {
	o.stampMu.Lock()
	defer o.stampMu.Unlock()
	t := time.Now().UTC().Truncate(time.Microsecond)
	if !t.After(o.lastStamp) {
		t = o.lastStamp.Add(time.Microsecond)
	}
	o.lastStamp = t
	return t.Format(stampLayout)
}

// New builds an orchestrator. Call Start to run it.
func New(opts Options) *Orchestrator {
	if opts.Tick <= 0 {
		opts.Tick = DefaultTick
	}
	o := &Orchestrator{opts: opts, kicks: make(chan string, 256), index: map[string]string{}}
	o.rules = rulesAdvisor{
		routing: opts.Runs.Routing,
		kind: func(project, nodeID string) (string, []string) {
			g, _, err := opts.Board.Graph(project)
			if err != nil || g == nil {
				return "", nil
			}
			if n := g.NodeByID(nodeID); n != nil {
				return n.Kind, n.Tags
			}
			return "", nil
		},
	}
	return o
}

// Observe is the dispatcher observer (runs.Dispatcher.Observe): it queues the
// autopilot a run or pipeline belongs to. It never blocks.
func (o *Orchestrator) Observe(u runs.Update) {
	keys := []string{u.Run.ID, u.Run.RetryOf, u.Run.PipelineID, goalKey(u.Run.Project, u.Run.NodeID)}
	if u.Pipeline != nil {
		keys = append(keys, u.Pipeline.ID)
	}
	o.mu.Lock()
	var ids []string
	for _, k := range keys {
		if k == "" || k == "\x00" {
			continue
		}
		if id, ok := o.index[k]; ok && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	o.mu.Unlock()
	for _, id := range ids {
		o.kick(id)
	}
}

func goalKey(project, nodeID string) string { return project + "\x00" + nodeID }

func (o *Orchestrator) kick(id string) {
	select {
	case o.kicks <- id:
	default:
		// Full: the periodic pass catches up.
	}
}

func (o *Orchestrator) remember(keys []string, id string) {
	o.mu.Lock()
	for _, k := range keys {
		if k != "" {
			o.index[k] = id
		}
	}
	o.mu.Unlock()
}

func (o *Orchestrator) forget(id string) {
	o.mu.Lock()
	for k, v := range o.index {
		if v == id {
			delete(o.index, k)
		}
	}
	o.mu.Unlock()
}

func (o *Orchestrator) lock(id string) func() {
	value, _ := o.locks.LoadOrStore(id, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// Start resumes every live autopilot and runs the loop until ctx ends.
func (o *Orchestrator) Start(ctx context.Context) {
	go o.loop(ctx)
	o.kickLive(ctx)
}

func (o *Orchestrator) kickLive(ctx context.Context) {
	ids, err := o.opts.Store.Live(ctx)
	if err != nil {
		slog.Warn("autopilot: list live", slog.Any("error", err))
		return
	}
	for _, id := range ids {
		o.kick(id)
	}
}

func (o *Orchestrator) loop(ctx context.Context) {
	ticker := time.NewTicker(o.opts.Tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-o.kicks:
			o.Reconcile(ctx, id)
		case <-ticker.C:
			o.kickLive(ctx)
		}
	}
}

// Reconcile brings one autopilot up to date: its tasks from the board and
// their runs, then scheduling, merging and its own end. Errors are logged;
// the next pass tries again.
func (o *Orchestrator) Reconcile(ctx context.Context, id string) {
	unlock := o.lock(id)
	defer unlock()
	if err := o.reconcileLocked(ctx, id); err != nil && !errors.Is(err, ErrNotFound) {
		slog.Warn("autopilot: reconcile", slog.String("autopilot", id), slog.Any("error", err))
	}
}

// work is one pass's view of an autopilot.
type work struct {
	rec   Record
	tasks map[string]*runnerwire.TaskInstance
	dag   dag
	graph *engine.Graph
	state *engine.RunState
	dirty bool
	now   string
}

func (w *work) settled(id string) bool {
	t := w.tasks[id]
	return t != nil && (t.State == runnerwire.TaskSuccess || t.State == runnerwire.TaskSkipped)
}

func (o *Orchestrator) load(ctx context.Context, id string) (*work, error) {
	rec, err := o.opts.Store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	list, err := o.opts.Store.Tasks(ctx, id)
	if err != nil {
		return nil, err
	}
	w := &work{rec: rec, tasks: map[string]*runnerwire.TaskInstance{}, now: o.stamp()}
	for i := range list {
		w.tasks[list[i].NodeID] = &list[i]
	}
	g, rs, err := o.opts.Board.Graph(rec.Project)
	if err != nil {
		return nil, err
	}
	w.graph, w.state = g, rs
	w.dag = buildDAG(g, rec.GoalNodeID)
	return w, nil
}

func (o *Orchestrator) reconcileLocked(ctx context.Context, id string) error {
	w, err := o.load(ctx, id)
	if err != nil {
		return err
	}
	if final(w.rec.Status) {
		o.forget(id)
		return nil
	}
	before := snapshot(w)
	o.syncMembers(w)
	o.refresh(ctx, w)
	o.propagateFailures(w)
	if w.rec.Status == runnerwire.AutopilotRunning {
		o.schedule(ctx, w)
	}
	o.finishIfDone(ctx, w)
	return o.save(ctx, w, before)
}

// snapshot is the tasks as loaded, to save only what changed.
func snapshot(w *work) map[string]string {
	out := map[string]string{}
	for id, t := range w.tasks {
		out[id] = fingerprint(*t)
	}
	return out
}

func fingerprint(t runnerwire.TaskInstance) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%s|%d|%s|%s|%s|%s|%s|%s|", t.State, t.Stage, t.Try, t.RunID, t.PipelineID, t.PRURL,
		t.CIStatus, t.HumanVerdict, t.Reason)
	for _, s := range t.Stages {
		fmt.Fprintf(&b, "%s:%s:%d:%s:%s;", s.Stage, s.Status, s.Round, s.RunID, s.Detail)
	}
	for _, v := range t.Votes {
		fmt.Fprintf(&b, "%s/%s/%s/%d;", v.Agent, v.Model, v.Verdict, v.Round)
	}
	return b.String()
}

func (o *Orchestrator) save(ctx context.Context, w *work, before map[string]string) error {
	var changed []runnerwire.TaskInstance
	for id, t := range w.tasks {
		if before[id] != fingerprint(*t) {
			changed = append(changed, *t)
		}
	}
	if len(changed) == 0 && !w.dirty {
		return nil
	}
	rec, err := o.opts.Store.Save(ctx, w.rec, changed)
	if err != nil {
		return err
	}
	w.rec = rec
	o.index1(w)
	if final(rec.Status) {
		o.forget(rec.ID)
	}
	if o.opts.Changed != nil {
		o.opts.Changed(rec.Project, rec.ID)
	}
	return nil
}

// index1 records the keys updates for this autopilot arrive under.
func (o *Orchestrator) index1(w *work) {
	keys := []string{goalKey(w.rec.Project, w.rec.GoalNodeID)}
	for _, t := range w.tasks {
		keys = append(keys, t.RunID, t.PipelineID)
	}
	o.remember(keys, w.rec.ID)
}

// syncMembers adds the DAG's new members as task instances and sets aside
// instances whose node left it.
func (o *Orchestrator) syncMembers(w *work) {
	statuses := engine.ComputeStatuses(w.graph, w.state)
	for _, id := range w.dag.order {
		t := w.tasks[id]
		if t == nil {
			t = &runnerwire.TaskInstance{NodeID: id, State: runnerwire.TaskNone}
			w.tasks[id] = t
		}
		t.Title = w.dag.title(id)
		t.Upstream = slices.Clone(w.dag.upstream[id])
		if t.RunID != "" || t.PipelineID != "" || final1(t.State) {
			continue
		}
		switch {
		case !w.dag.dispatchable(id):
			setState(t, runnerwire.TaskSkipped, runnerwire.StageDone, "not a task ("+w.dag.nodes[id].Kind+"): nothing to implement")
		case engine.Settled(statuses[id], boardStatuses(w.graph)):
			setState(t, runnerwire.TaskSuccess, runnerwire.StageDone, "already "+string(statuses[id])+" on the board")
		case w.dag.inCycle(id):
			setState(t, runnerwire.TaskFailed, "", "on a dependency cycle: it can never become ready")
		}
	}
	for id, t := range w.tasks {
		if _, member := w.dag.nodes[id]; member || final1(t.State) {
			continue
		}
		if t.RunID == "" && t.PipelineID == "" {
			setState(t, runnerwire.TaskSkipped, "", "no longer part of the goal")
		}
	}
}

func boardStatuses(g *engine.Graph) []engine.StatusDefinition {
	if g == nil || g.UI == nil {
		return nil
	}
	return g.UI.CustomStatuses
}

// final1 reports whether a task state is an end state.
func final1(state string) bool {
	switch state {
	case runnerwire.TaskSuccess, runnerwire.TaskFailed, runnerwire.TaskUpstreamFailed, runnerwire.TaskSkipped:
		return true
	}
	return false
}

func setState(t *runnerwire.TaskInstance, state, stage, reason string) {
	t.State, t.Reason = state, reason
	if stage != "" {
		t.Stage = stage
	}
}

// refresh reads every dispatched task's runs and acts on what they say:
// approval moves on to a person or the merge, a merge marks the node done.
func (o *Orchestrator) refresh(ctx context.Context, w *work) {
	for _, id := range sortedIDs(w.tasks) {
		t := w.tasks[id]
		if t.RunID == "" && t.PipelineID == "" {
			continue
		}
		if final1(t.State) {
			continue
		}
		obs, err := o.observe(ctx, w.rec.Project, t)
		if err != nil {
			slog.Warn("autopilot: read runs", slog.String("task", id), slog.Any("error", err))
			continue
		}
		d := derive(obs, w.now)
		t.RunID = firstNonEmpty(d.latestRun, t.RunID)
		t.PRURL, t.CIStatus, t.Votes = d.prURL, d.ci, d.votes
		stages := d.stages
		// act names the stage when it decides one (waiting for CI, the
		// merge, a person); otherwise the task is at its latest stage.
		t.Stage = ""
		o.act(ctx, w, t, d, &stages)
		t.Stages = keepTimes(t.Stages, stages)
		// In the order they happened; equal times (stages first seen in one
		// pass) in the pipeline's own order.
		sort.SliceStable(t.Stages, func(i, j int) bool {
			if t.Stages[i].At != t.Stages[j].At {
				return t.Stages[i].At < t.Stages[j].At
			}
			return stageRank[t.Stages[i].Stage] < stageRank[t.Stages[j].Stage]
		})
		if t.Stage == "" && len(t.Stages) > 0 {
			t.Stage = t.Stages[len(t.Stages)-1].Stage
		}
	}
}

// observe gathers a task's runs in its current try.
func (o *Orchestrator) observe(ctx context.Context, project string, t *runnerwire.TaskInstance) (observation, error) {
	var obs observation
	if t.PipelineID != "" {
		p, err := o.opts.Runs.Pipeline(ctx, t.PipelineID)
		if err != nil {
			return obs, err
		}
		obs.pipeline = &p
		list, err := o.opts.Runs.PipelineRuns(ctx, t.PipelineID)
		if err != nil {
			return obs, err
		}
		obs.runs = list
	} else {
		first, err := o.opts.Runs.Get(ctx, t.RunID)
		if err != nil {
			return obs, err
		}
		obs.runs = []runs.Record{first}
		// Follow the fallback chain: each retry names the run it replaced.
		all, err := o.opts.Runs.NodeRuns(ctx, project, t.NodeID)
		if err != nil {
			return obs, err
		}
		for cur := first.ID; ; {
			i := slices.IndexFunc(all, func(r runs.Record) bool { return r.RetryOf == cur })
			if i < 0 {
				break
			}
			obs.runs = append(obs.runs, all[i])
			cur = all[i].ID
		}
	}
	// The run carrying the PR, with its merge requests.
	for i := len(obs.runs) - 1; i >= 0; i-- {
		if obs.runs[i].PRURL != "" {
			r, err := o.opts.Runs.Get(ctx, obs.runs[i].ID)
			if err != nil {
				return obs, err
			}
			obs.prRun = &r
			break
		}
	}
	return obs, nil
}

// act turns a derived try into the task's state, and moves an approved task
// on: to a person, to the merge, or to done.
func (o *Orchestrator) act(ctx context.Context, w *work, t *runnerwire.TaskInstance, d derived, stages *[]runnerwire.StageRecord) {
	add := func(stage, status, detail string) {
		*stages = append(*stages, runnerwire.StageRecord{Stage: stage, Status: status, At: after(*stages, w.now), Detail: detail})
	}
	pol := w.rec.Policy
	switch d.phase {
	case phaseFailed:
		setState(t, runnerwire.TaskFailed, "", d.reason)
		return
	case phaseActive, phaseIdle:
		switch {
		case d.waitingHuman:
			setState(t, runnerwire.TaskWaitingHuman, runnerwire.StageHuman, "")
		case d.waitingReview:
			setState(t, runnerwire.TaskWaitingReview, "", "")
		case d.queued:
			setState(t, runnerwire.TaskQueued, "", "")
		default:
			setState(t, runnerwire.TaskRunning, "", "")
		}
		return
	case phaseMerged:
		add(runnerwire.StageApproved, runnerwire.StageOK, "")
		o.done(ctx, w, t, stages, "merged "+t.PRURL)
		return
	}
	// Approved by the agents (or without review). A person's review is the
	// pipeline's own when it can wait for one; otherwise it is asked here.
	if humanReview(pol) && !pipelineHandlesHuman {
		switch t.HumanVerdict {
		case runnerwire.VerdictApprove:
			add(runnerwire.StageHuman, runnerwire.StageOK, "approved")
		default:
			add(runnerwire.StageHuman, runnerwire.StageWaiting, "waiting for a person")
			setState(t, runnerwire.TaskWaitingHuman, runnerwire.StageHuman, "")
			return
		}
	}
	add(runnerwire.StageApproved, runnerwire.StageOK, "")
	if !pol.AutoMerge {
		setState(t, runnerwire.TaskSuccess, runnerwire.StageApproved, "approved; merging is left to a person")
		o.markBoard(w, t.NodeID, engine.StatusDone, "approved by the autopilot; PR "+t.PRURL)
		return
	}
	switch d.ci {
	case runnerwire.CIFailure:
		setState(t, runnerwire.TaskFailed, "", "the pull request's checks failed")
		return
	case runnerwire.CISuccess:
	default:
		setState(t, runnerwire.TaskRunning, runnerwire.StageCI, "waiting for the pull request's checks")
		return
	}
	o.merge(ctx, w, t, stages)
}

// merge asks the run's runner to merge the approved PR, once per head.
func (o *Orchestrator) merge(ctx context.Context, w *work, t *runnerwire.TaskInstance, stages *[]runnerwire.StageRecord) {
	setState(t, runnerwire.TaskRunning, runnerwire.StageMerge, "")
	obs, err := o.observe(ctx, w.rec.Project, t)
	if err != nil || obs.prRun == nil {
		return
	}
	pr := obs.prRun
	if m := pr.Merge; m != nil {
		switch m.Status {
		case runnerwire.MergePending, runnerwire.MergeDone:
			return
		case runnerwire.MergeFailed:
			setState(t, runnerwire.TaskFailed, runnerwire.StageMerge, "the merge failed: "+m.Reason)
			return
		}
		// Expired: the runner was away; ask again.
	}
	_, err = o.opts.Runs.Merge(ctx, pr.ID, runs.Approver{ID: w.rec.CreatedBy, Name: "autopilot (" + w.rec.CreatedByName + ")"}, pr.PRHeadSHA)
	if err != nil {
		if errors.Is(err, runs.ErrPrecondition) {
			t.Reason = "not merging yet: " + err.Error()
			return
		}
		slog.Warn("autopilot: merge", slog.String("run", pr.ID), slog.Any("error", err))
		return
	}
	*stages = append(*stages, runnerwire.StageRecord{Stage: runnerwire.StageMerge, Status: runnerwire.StageRunning,
		At: after(*stages, w.now), RunID: pr.ID, Detail: "merge requested"})
}

// done ends a task: its node is done on the board, so what waits on it is
// ready there too.
func (o *Orchestrator) done(ctx context.Context, w *work, t *runnerwire.TaskInstance, stages *[]runnerwire.StageRecord, note string) {
	_ = ctx
	o.markBoard(w, t.NodeID, engine.StatusDone, "autopilot: "+note)
	*stages = append(*stages, runnerwire.StageRecord{Stage: runnerwire.StageDone, Status: runnerwire.StageOK, At: after(*stages, w.now)})
	setState(t, runnerwire.TaskSuccess, runnerwire.StageDone, "")
}

// markBoard moves a node's status unless it is already settled there.
func (o *Orchestrator) markBoard(w *work, nodeID string, status engine.Status, note string) {
	statuses := engine.ComputeStatuses(w.graph, w.state)
	if statuses[nodeID] == status {
		return
	}
	by := "autopilot"
	if w.rec.CreatedByName != "" {
		by = "autopilot (" + w.rec.CreatedByName + ")"
	}
	if err := o.opts.Board.SetStatus(w.rec.Project, nodeID, status, by, note); err != nil {
		slog.Warn("autopilot: set status", slog.String("node", nodeID), slog.Any("error", err))
		return
	}
	if w.state != nil {
		_ = w.state.SetStatus(nodeID, status, by, time.Now())
	}
	if o.opts.BoardChanged != nil {
		o.opts.BoardChanged(w.rec.Project)
	}
}

// propagateFailures marks everything downstream of a failed task.
func (o *Orchestrator) propagateFailures(w *work) {
	for changed := true; changed; {
		changed = false
		for _, id := range w.dag.order {
			t := w.tasks[id]
			if t == nil || t.State != runnerwire.TaskNone && t.State != runnerwire.TaskScheduled {
				continue
			}
			for _, up := range w.dag.upstream[id] {
				u := w.tasks[up]
				if u != nil && (u.State == runnerwire.TaskFailed || u.State == runnerwire.TaskUpstreamFailed) {
					setState(t, runnerwire.TaskUpstreamFailed, "", "waits on "+u.Title+", which failed")
					changed = true
					break
				}
			}
		}
	}
}

// schedule starts ready tasks while there is room.
func (o *Orchestrator) schedule(ctx context.Context, w *work) {
	active := 0
	for _, t := range w.tasks {
		switch t.State {
		case runnerwire.TaskQueued, runnerwire.TaskRunning, runnerwire.TaskWaitingReview, runnerwire.TaskWaitingHuman:
			active++
		}
	}
	room := w.rec.Policy.MaxParallel - active
	for _, id := range w.dag.order {
		t := w.tasks[id]
		if t == nil || (t.State != runnerwire.TaskNone && t.State != runnerwire.TaskScheduled) {
			continue
		}
		ready := true
		for _, up := range w.dag.upstream[id] {
			if !w.settled(up) {
				ready = false
				break
			}
		}
		if ready {
			if blockers := externalBlockers(w.graph, w.state, w.dag, id); len(blockers) > 0 {
				ready = false
				t.Reason = "waits on " + strings.Join(blockers, ", ") + " outside this goal"
			}
		}
		if !ready {
			if t.State != runnerwire.TaskNone {
				setState(t, runnerwire.TaskNone, "", t.Reason)
			}
			continue
		}
		if room <= 0 {
			setState(t, runnerwire.TaskScheduled, runnerwire.StageQueued, "waiting for a free slot")
			continue
		}
		if o.dispatch(ctx, w, t) {
			room--
		}
	}
}

// dispatch starts a task's try: its pipeline (or a plain run when the policy
// reviews nothing by agent) on one of the creator's runners.
func (o *Orchestrator) dispatch(ctx context.Context, w *work, t *runnerwire.TaskInstance) bool {
	rec := w.rec
	pol := rec.Policy
	owned, err := o.opts.Runners.Owned(ctx, rec.CreatedBy)
	if err != nil {
		setState(t, runnerwire.TaskScheduled, runnerwire.StageQueued, "cannot list runners: "+err.Error())
		return false
	}
	var eligible []runnerwire.Runner
	for _, r := range owned {
		if !r.Online || !runs.Headless(r.Info) || (pol.RunnerID != "" && r.ID != pol.RunnerID) {
			continue
		}
		if !slices.ContainsFunc(r.Info.Repos, func(repo runnerwire.Repo) bool { return repo.ID == pol.RepoID }) {
			continue
		}
		eligible = append(eligible, r)
	}
	if len(eligible) == 0 {
		setState(t, runnerwire.TaskScheduled, runnerwire.StageQueued, "waiting for an online runner with the repository")
		return false
	}
	content, _ := o.opts.Board.Content(rec.Project, t.NodeID)
	choice, err := o.choose(ctx, ChooseInput{
		OwnerID: rec.CreatedBy, Project: rec.Project, NodeID: t.NodeID, Title: t.Title, Body: content,
		Policy: pol, Agents: mergeAgents(eligible),
	})
	if err != nil {
		setState(t, runnerwire.TaskFailed, "", err.Error())
		return false
	}
	head := choice.Implement[0]
	// The runner: one that has the head agent, least busy first.
	sort.SliceStable(eligible, func(i, j int) bool { return o.busy(ctx, eligible[i].ID) < o.busy(ctx, eligible[j].ID) })
	runnerID := ""
	for _, r := range eligible {
		if usable([]runnerwire.AgentModel{head}, mergeAgents([]runnerwire.Runner{r})) != nil {
			runnerID = r.ID
			break
		}
	}
	if runnerID == "" {
		setState(t, runnerwire.TaskScheduled, runnerwire.StageQueued, "no online runner has "+label(head))
		return false
	}
	extra := "This task is part of an autopilot run for goal " + rec.GoalNodeID + "."
	if t.Reason != "" && strings.HasPrefix(t.Reason, humanChangesPrefix) {
		extra += "\n\nA reviewer asked for changes on the previous attempt:\n" + strings.TrimPrefix(t.Reason, humanChangesPrefix)
	}
	body := runnerwire.CreateRunRequest{
		NodeID: t.NodeID, RunnerID: runnerID, Agent: head.Agent, Model: head.Model, RepoID: pol.RepoID,
		Prompt: runs.Truncate(extra, runs.MaxExtraPrompt), Fallbacks: choice.Implement[1:],
		Pipeline: pipelineFor(pol, head),
	}
	record, err := o.opts.Runs.Start(ctx, runs.StartRequest{
		Body: body, Project: rec.Project, ActorID: rec.CreatedBy, ActorName: rec.CreatedByName,
		SteppedUpAt: rec.SteppedUpAt,
		LoadNode: func() (string, string, bool, error) {
			return t.Title, content, true, nil
		},
	})
	if err != nil {
		var refused *runs.StartError
		if errors.As(err, &refused) && (refused.Code == runs.StartBudget || refused.Code == runs.StartPrecondition) {
			setState(t, runnerwire.TaskScheduled, runnerwire.StageQueued, refused.Msg)
			return false
		}
		if errors.As(err, &refused) {
			setState(t, runnerwire.TaskFailed, "", refused.Msg)
			return false
		}
		setState(t, runnerwire.TaskScheduled, runnerwire.StageQueued, "could not start: "+err.Error())
		return false
	}
	t.Try++
	t.RunID, t.PipelineID = record.ID, record.PipelineID
	t.Votes, t.HumanVerdict, t.PRURL, t.CIStatus = nil, "", "", ""
	t.Stages = []runnerwire.StageRecord{{Stage: runnerwire.StageQueued, Status: runnerwire.StageRunning,
		At: firstNonEmpty(record.CreatedAt, w.now), RunID: record.ID, Detail: choice.Reason}}
	t.Advisor = choice.Reason
	setState(t, runnerwire.TaskQueued, runnerwire.StageQueued, "")
	o.remember([]string{record.ID, record.PipelineID}, rec.ID)
	return true
}

func (o *Orchestrator) busy(ctx context.Context, runnerID string) int {
	n, err := o.opts.Runs.Busy(ctx, runnerID)
	if err != nil {
		return 1 << 20
	}
	return n
}

func label(am runnerwire.AgentModel) string {
	if am.Model == "" {
		return am.Agent
	}
	return am.Agent + "/" + am.Model
}

// choose asks the policy's advisor first (the LLM advisor, or the System One
// advisor), the rules otherwise and whenever it fails.
func (o *Orchestrator) choose(ctx context.Context, in ChooseInput) (Choice, error) {
	var advisor Advisor
	timeout := llmAdvisorTimeout
	switch in.Policy.Advisor {
	case runnerwire.AdvisorLLM:
		advisor = registeredLLMAdvisor()
	case runnerwire.AdvisorSystemOne:
		advisor, timeout = registeredSystemOneAdvisor(), systemOneAdvisorTimeout
	}
	if advisor != nil {
		cctx, cancel := context.WithTimeout(ctx, timeout)
		c, err := advisor.Choose(cctx, in)
		cancel()
		if err == nil {
			if chain := usable(c.Implement, in.Agents); len(chain) > 0 {
				c.Implement = chain
				return c, nil
			}
		}
	}
	return o.rules.Choose(ctx, in)
}

// finishIfDone ends the autopilot once no task can move: succeeded when
// every task succeeded or was skipped, failed otherwise.
func (o *Orchestrator) finishIfDone(ctx context.Context, w *work) {
	if len(w.dag.order) == 0 {
		return // nothing to do yet: a plan may still be adding tasks
	}
	failed := 0
	for _, id := range w.dag.order {
		t := w.tasks[id]
		if t == nil || !final1(t.State) {
			return
		}
		if t.State == runnerwire.TaskFailed || t.State == runnerwire.TaskUpstreamFailed {
			failed++
		}
	}
	_ = ctx
	w.dirty = true
	if failed > 0 {
		w.rec.Status, w.rec.Reason = runnerwire.AutopilotFailed, fmt.Sprintf("%d task(s) failed", failed)
		return
	}
	w.rec.Status, w.rec.Reason = runnerwire.AutopilotSucceeded, "every task is done"
	o.markBoard(w, w.rec.GoalNodeID, engine.StatusDone, "autopilot: every task is done")
}

// after is now, or the latest stage's time when that is later: a stage the
// orchestrator adds follows the ones the runs recorded, whatever the clocks'
// resolution made of their times.
func after(stages []runnerwire.StageRecord, now string) string {
	at := now
	for _, s := range stages {
		if s.At > at {
			at = s.At
		}
	}
	return at
}

func sortedIDs(m map[string]*runnerwire.TaskInstance) []string {
	out := make([]string, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// ---- API actions ----

// CreateInput is a new autopilot.
type CreateInput struct {
	Project, GoalNodeID string
	Policy              runnerwire.AutopilotPolicy
	Actor               identity.Actor
	SteppedUpAt         string
}

// Create starts an autopilot for a goal: validates the policy, resolves the
// repository, records it with its tasks and runs the first pass.
func (o *Orchestrator) Create(ctx context.Context, in CreateInput) (runnerwire.AutopilotView, error) {
	if !engine.ValidNodeID(in.GoalNodeID) {
		return runnerwire.AutopilotView{}, invalidf("goalNodeId is not a valid node id")
	}
	pol, err := ValidatePolicy(in.Policy)
	if err != nil {
		return runnerwire.AutopilotView{}, err
	}
	g, _, err := o.opts.Board.Graph(in.Project)
	if err != nil {
		return runnerwire.AutopilotView{}, err
	}
	if g == nil || g.NodeByID(in.GoalNodeID) == nil {
		return runnerwire.AutopilotView{}, fmt.Errorf("%w: goal node", ErrNotFound)
	}
	d := buildDAG(g, in.GoalNodeID)
	if len(d.order) == 0 {
		return runnerwire.AutopilotView{}, invalidf("the goal has no tasks yet: break it down first (a plan run, or tasks wired into it)")
	}
	if pol.RepoID == "" {
		owned, err := o.opts.Runners.Owned(ctx, in.Actor.ID)
		if err != nil {
			return runnerwire.AutopilotView{}, err
		}
		repos := map[string]bool{}
		for _, r := range owned {
			if r.Info == nil || (pol.RunnerID != "" && r.ID != pol.RunnerID) {
				continue
			}
			for _, repo := range r.Info.Repos {
				repos[repo.ID] = true
			}
		}
		if len(repos) != 1 {
			return runnerwire.AutopilotView{}, invalidf("choose the repository the tasks change (repoId): your runners offer %d", len(repos))
		}
		for id := range repos {
			pol.RepoID = id
		}
	}
	name := in.Actor.Name
	if name == "" {
		name = in.Actor.ID
	}
	rec, err := o.opts.Store.Create(ctx, Record{
		AutopilotRun: runnerwire.AutopilotRun{
			Project: in.Project, GoalNodeID: in.GoalNodeID, Policy: pol,
			CreatedBy: in.Actor.ID, CreatedByName: name,
		},
		SteppedUpAt: in.SteppedUpAt,
	}, nil)
	if err != nil {
		return runnerwire.AutopilotView{}, err
	}
	o.Reconcile(ctx, rec.ID)
	return o.View(ctx, rec.ID)
}

// View is an autopilot and its tasks in DAG order.
func (o *Orchestrator) View(ctx context.Context, id string) (runnerwire.AutopilotView, error) {
	rec, err := o.opts.Store.Get(ctx, id)
	if err != nil {
		return runnerwire.AutopilotView{}, err
	}
	tasks, err := o.opts.Store.Tasks(ctx, id)
	if err != nil {
		return runnerwire.AutopilotView{}, err
	}
	order := map[string]int{}
	if g, _, err := o.opts.Board.Graph(rec.Project); err == nil {
		for i, nodeID := range buildDAG(g, rec.GoalNodeID).order {
			order[nodeID] = i
		}
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		oi, iok := order[tasks[i].NodeID]
		oj, jok := order[tasks[j].NodeID]
		switch {
		case iok && jok:
			return oi < oj
		case iok != jok:
			return iok
		}
		return tasks[i].NodeID < tasks[j].NodeID
	})
	rec.Counts = count(tasks)
	return runnerwire.AutopilotView{Run: rec.AutopilotRun, Tasks: tasks}, nil
}

// count tallies tasks by state.
func count(tasks []runnerwire.TaskInstance) runnerwire.TaskCounts {
	var c runnerwire.TaskCounts
	for _, t := range tasks {
		c.Total++
		switch t.State {
		case runnerwire.TaskSuccess:
			c.Success++
		case runnerwire.TaskRunning, runnerwire.TaskQueued:
			c.Running++
		case runnerwire.TaskWaitingReview, runnerwire.TaskWaitingHuman:
			c.Waiting++
		case runnerwire.TaskFailed, runnerwire.TaskUpstreamFailed:
			c.Failed++
		case runnerwire.TaskSkipped:
			c.Skipped++
		default:
			c.Pending++
		}
	}
	return c
}

// Latest is a goal's newest autopilot.
func (o *Orchestrator) Latest(ctx context.Context, project, goal string) (runnerwire.AutopilotView, error) {
	rec, err := o.opts.Store.Latest(ctx, project, goal)
	if err != nil {
		return runnerwire.AutopilotView{}, err
	}
	return o.View(ctx, rec.ID)
}

// Get is one autopilot record, for access checks.
func (o *Orchestrator) Get(ctx context.Context, id string) (Record, error) {
	return o.opts.Store.Get(ctx, id)
}

// Pause stops scheduling new tasks; running ones carry on.
func (o *Orchestrator) Pause(ctx context.Context, id string) error {
	return o.setStatus(ctx, id, runnerwire.AutopilotRunning, runnerwire.AutopilotPaused, "paused")
}

// Resume schedules again.
func (o *Orchestrator) Resume(ctx context.Context, id string) error {
	return o.setStatus(ctx, id, runnerwire.AutopilotPaused, runnerwire.AutopilotRunning, "")
}

func (o *Orchestrator) setStatus(ctx context.Context, id, from, to, reason string) error {
	unlock := o.lock(id)
	rec, err := o.opts.Store.Get(ctx, id)
	if err != nil {
		unlock()
		return err
	}
	if rec.Status != from {
		unlock()
		return conflictf("the autopilot is %s", rec.Status)
	}
	rec.Status, rec.Reason = to, reason
	_, err = o.opts.Store.Save(ctx, rec, nil)
	unlock()
	if err != nil {
		return err
	}
	if o.opts.Changed != nil {
		o.opts.Changed(rec.Project, rec.ID)
	}
	o.Reconcile(ctx, id)
	return nil
}

// Cancel ends the autopilot and cancels its tasks' runs that are still going.
func (o *Orchestrator) Cancel(ctx context.Context, id, reason string) error {
	unlock := o.lock(id)
	defer unlock()
	w, err := o.load(ctx, id)
	if err != nil {
		return err
	}
	if final(w.rec.Status) {
		return conflictf("the autopilot already %s", w.rec.Status)
	}
	before := snapshot(w)
	for _, t := range w.tasks {
		if final1(t.State) {
			continue
		}
		o.stopTry(ctx, t, "autopilot cancelled")
		setState(t, runnerwire.TaskSkipped, "", "the autopilot was cancelled")
	}
	w.rec.Status, w.rec.Reason, w.dirty = runnerwire.AutopilotCancelled, firstNonEmpty(reason, "cancelled"), true
	return o.save(ctx, w, before)
}

// stopTry cancels a task's pipeline or run, if it is still going.
func (o *Orchestrator) stopTry(ctx context.Context, t *runnerwire.TaskInstance, reason string) {
	switch {
	case t.PipelineID != "":
		if err := o.opts.Runs.CancelPipeline(ctx, t.PipelineID, reason); err != nil {
			slog.Warn("autopilot: cancel pipeline", slog.String("pipeline", t.PipelineID), slog.Any("error", err))
		}
	case t.RunID != "":
		if _, err := o.opts.Runs.Cancel(ctx, t.RunID, reason); err != nil {
			slog.Warn("autopilot: cancel run", slog.String("run", t.RunID), slog.Any("error", err))
		}
	}
}

// Retry clears a failed, upstream-failed or skipped task (and what failed
// because of it) for a new try. On an autopilot that already ended, it runs
// again.
func (o *Orchestrator) Retry(ctx context.Context, id, nodeID string) error {
	return o.taskAction(ctx, id, nodeID, func(w *work, t *runnerwire.TaskInstance) error {
		switch t.State {
		case runnerwire.TaskFailed, runnerwire.TaskUpstreamFailed, runnerwire.TaskSkipped:
		default:
			return conflictf("only a failed or skipped task can be retried; this one is %s", t.State)
		}
		o.clear(t, "")
		for _, other := range w.tasks {
			if other.State == runnerwire.TaskUpstreamFailed {
				o.clear(other, "")
			}
		}
		return nil
	})
}

// Skip sets a task aside (cancelling its try if one is going); what waits on
// it goes ahead.
func (o *Orchestrator) Skip(ctx context.Context, id, nodeID string) error {
	return o.taskAction(ctx, id, nodeID, func(w *work, t *runnerwire.TaskInstance) error {
		if t.State == runnerwire.TaskSuccess || t.State == runnerwire.TaskSkipped {
			return conflictf("the task is already %s", t.State)
		}
		if !final1(t.State) {
			o.stopTry(ctx, t, "skipped in the autopilot")
		}
		setState(t, runnerwire.TaskSkipped, "", "skipped by a person")
		for _, other := range w.tasks {
			if other.State == runnerwire.TaskUpstreamFailed {
				o.clear(other, "")
			}
		}
		return nil
	})
}

// humanChangesPrefix marks a task's reason as a person's requested changes,
// which the next try's prompt carries.
const humanChangesPrefix = "changes requested: "

// Review records a person's verdict on a task waiting for one: approve lets
// it go on to the merge; changes start a new try with the comment.
func (o *Orchestrator) Review(ctx context.Context, id, nodeID string, who runs.Reviewer, verdict, comment string) error {
	verdict = strings.TrimSpace(verdict)
	if verdict != runnerwire.VerdictApprove && verdict != runnerwire.VerdictChanges {
		return invalidf("verdict must be %q or %q", runnerwire.VerdictApprove, runnerwire.VerdictChanges)
	}
	if len(comment) > 8<<10 {
		return invalidf("the comment is at most 8192 bytes")
	}
	// A task whose pipeline waits for the person is decided by the pipeline
	// (POST /api/runs/pipelines/:id/review does the same): approve ends it
	// approved, changes starts its revise round. The pipeline's observer
	// update then moves the task on. Called outside the autopilot's lock,
	// since that update runs a pass.
	if pipelineHandlesHuman {
		view, err := o.View(ctx, id)
		if err != nil {
			return err
		}
		for _, t := range view.Tasks {
			if t.NodeID != nodeID || t.PipelineID == "" {
				continue
			}
			if t.State != runnerwire.TaskWaitingHuman {
				return conflictf("the task is not waiting for a review; it is %s", t.State)
			}
			if _, err := o.opts.Runs.HumanReview(ctx, t.PipelineID, who, verdict, comment); err != nil {
				return err
			}
			o.Reconcile(ctx, id)
			return nil
		}
	}
	return o.taskAction(ctx, id, nodeID, func(w *work, t *runnerwire.TaskInstance) error {
		if t.State != runnerwire.TaskWaitingHuman {
			return conflictf("the task is not waiting for a review; it is %s", t.State)
		}
		if verdict == runnerwire.VerdictApprove {
			t.HumanVerdict = verdict
			return nil
		}
		o.clear(t, humanChangesPrefix+firstNonEmpty(strings.TrimSpace(comment), "(no comment)"))
		return nil
	})
}

// clear resets a task for a new try, keeping its try count.
func (o *Orchestrator) clear(t *runnerwire.TaskInstance, reason string) {
	t.RunID, t.PipelineID, t.PRURL, t.CIStatus, t.HumanVerdict, t.Advisor = "", "", "", "", "", ""
	t.Votes, t.Stages = nil, nil
	setState(t, runnerwire.TaskNone, "", reason)
	t.Stage = ""
}

// taskAction applies fn to one task under the autopilot's lock, reopens an
// autopilot that had ended, saves and runs a pass.
func (o *Orchestrator) taskAction(ctx context.Context, id, nodeID string, fn func(*work, *runnerwire.TaskInstance) error) error {
	unlock := o.lock(id)
	w, err := o.load(ctx, id)
	if err != nil {
		unlock()
		return err
	}
	t := w.tasks[nodeID]
	if t == nil {
		unlock()
		return fmt.Errorf("%w: task", ErrNotFound)
	}
	if w.rec.Status == runnerwire.AutopilotCancelled {
		unlock()
		return conflictf("the autopilot was cancelled; start a new one")
	}
	before := snapshot(w)
	if err := fn(w, t); err != nil {
		unlock()
		return err
	}
	if final(w.rec.Status) {
		w.rec.Status, w.rec.Reason, w.rec.FinishedAt = runnerwire.AutopilotRunning, "", ""
	}
	w.dirty = true
	err = o.save(ctx, w, before)
	unlock()
	if err != nil {
		return err
	}
	o.Reconcile(ctx, id)
	return nil
}
