// Package autopilot is the Autopilot orchestrator: it works a goal node's tasks as a
// DAG, dispatching each ready task through the run dispatcher (implement →
// review → revise → merge) and recording every task instance's state and
// stages the way an Airflow DAG run does. The hub still executes nothing:
// every run is an ordinary run its runner has to accept. See
// docs/workstation.md, "Autopilot".
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
	// Changes is what a run committed, nil when its runner reported nothing.
	Changes(ctx context.Context, runID string) (*runnerwire.RunChanges, error)
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
	// added is the goal's members that joined after the autopilot started:
	// its task set is frozen at the start, so they are not worked.
	added []string
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
	fmt.Fprintf(&b, "%s|%s|%d|%s|%s|%s|%s|%s|%s|%s|%s|", t.State, t.Stage, t.Try, t.RunID, t.PipelineID, t.PRURL,
		t.CIStatus, t.HumanVerdict, t.Reason, t.Hold, t.ContentHash)
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

// syncMembers keeps the task set the autopilot started with (Create froze
// it): it refreshes the tasks' titles and edges from the board, and holds a
// pending task whose node changed or left the goal for a person. Nodes that
// joined the goal later are only noted (w.added): an edit after the start,
// by anyone who may change the board or by an agent following injected
// instructions, does not slip new work into a run its creator started.
func (o *Orchestrator) syncMembers(w *work) {
	statuses := engine.ComputeStatuses(w.graph, w.state)
	w.added = nil
	for _, id := range w.dag.order {
		t := w.tasks[id]
		if t == nil {
			w.added = append(w.added, id)
			continue
		}
		t.Title = w.dag.title(id)
		t.Upstream = slices.Clone(w.dag.upstream[id])
		if t.RunID != "" || t.PipelineID != "" || final1(t.State) || t.Hold != "" {
			continue
		}
		switch {
		case !w.dag.dispatchable(id):
			setState(t, runnerwire.TaskSkipped, runnerwire.StageDone, "not a task ("+w.dag.nodes[id].Kind+"): nothing to implement")
		case skippedOnBoard(statuses[id]):
			// Set aside on the board (a revision's replan skips what it made
			// obsolete): it settles what waits on it, and shows as skipped.
			setState(t, runnerwire.TaskSkipped, runnerwire.StageDone, string(statuses[id])+" on the board")
		case engine.Settled(statuses[id], boardStatuses(w.graph)):
			setState(t, runnerwire.TaskSuccess, runnerwire.StageDone, "already "+string(statuses[id])+" on the board")
		case w.dag.inCycle(id):
			setState(t, runnerwire.TaskFailed, "", "on a dependency cycle: it can never become ready")
		default:
			if body, err := o.opts.Board.Content(w.rec.Project, id); err == nil {
				o.checkContent(t, body)
			}
		}
	}
	for id, t := range w.tasks {
		if _, member := w.dag.nodes[id]; member || final1(t.State) || t.Hold != "" {
			continue
		}
		if t.RunID == "" && t.PipelineID == "" {
			// Held, not skipped: a skipped task counts as settled, so cutting
			// an edge would otherwise wave its downstream through.
			hold(t, runnerwire.HoldRemoved, "", "it left the goal after the autopilot started: skip it, or retry it once it is back")
		}
	}
}

// checkContent holds a pending task whose node changed since the task joined
// the run, and reports whether it may go on. A task recorded before content
// hashes were kept takes its current content as the one it started with.
func (o *Orchestrator) checkContent(t *runnerwire.TaskInstance, body string) bool {
	h := contentHash(t.Title, body)
	switch t.ContentHash {
	case "":
		t.ContentHash = h
		return true
	case h:
		return true
	}
	hold(t, runnerwire.HoldChanged, "", "the task changed after the autopilot started: a person must retry it to work the new content")
	return false
}

// hold sets a task aside for a person (a TaskInstance.Hold).
func hold(t *runnerwire.TaskInstance, kind, stage, reason string) {
	t.Hold = kind
	setState(t, runnerwire.TaskWaitingHuman, stage, reason)
	if stage == "" {
		t.Stage = ""
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
	// No person reviewed (allowAgentOnlyMerge): a PR that changes how code
	// is built, tested or run waits for one before it merges.
	if !humanReview(pol) {
		if t.Hold == runnerwire.HoldSensitive && t.HumanVerdict == runnerwire.VerdictApprove {
			add(runnerwire.StageHuman, runnerwire.StageOK, "merge approved by a person")
		} else if why := o.holdFor(ctx, w, t); why != "" {
			add(runnerwire.StageHuman, runnerwire.StageWaiting, why)
			hold(t, runnerwire.HoldSensitive, runnerwire.StageHuman, "not merging without a person: "+why)
			return
		}
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

// holdFor is why a task's approved PR must wait for a person, or "".
func (o *Orchestrator) holdFor(ctx context.Context, w *work, t *runnerwire.TaskInstance) string {
	obs, err := o.observe(ctx, w.rec.Project, t)
	if err != nil {
		return "its runs cannot be read: " + err.Error()
	}
	return o.mergeHold(ctx, obs)
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
		if t.Hold != "" && t.RunID == "" && t.PipelineID == "" {
			continue // held before it started: it holds no slot
		}
		switch t.State {
		case runnerwire.TaskQueued, runnerwire.TaskRunning, runnerwire.TaskWaitingReview, runnerwire.TaskWaitingHuman:
			active++
		}
	}
	room := w.rec.Policy.MaxParallel - active
	statuses := engine.ComputeStatuses(w.graph, w.state)
	for _, id := range w.dag.order {
		t := w.tasks[id]
		if t == nil || (t.State != runnerwire.TaskNone && t.State != runnerwire.TaskScheduled) {
			continue
		}
		ready := true
		for _, up := range w.dag.upstream[id] {
			if w.settled(up) {
				continue
			}
			if w.tasks[up] == nil && engine.Settled(statuses[up], boardStatuses(w.graph)) {
				continue // joined the goal later, but done on the board
			}
			ready = false
			if w.tasks[up] == nil {
				t.Reason = "waits on " + w.dag.title(up) + ", which joined the goal after the autopilot started: finish it, or start a new autopilot"
			}
			break
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
	content, err := o.opts.Board.Content(rec.Project, t.NodeID)
	if err != nil {
		setState(t, runnerwire.TaskScheduled, runnerwire.StageQueued, "cannot read the task: "+err.Error())
		return false
	}
	// The content the run gets is the content the task started with.
	if !o.checkContent(t, content) {
		return false
	}
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
	if len(w.tasks) == 0 {
		return // nothing to do
	}
	failed := 0
	for _, t := range w.tasks {
		if !final1(t.State) {
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
	if len(w.added) > 0 {
		// The goal is not done: what joined it later is still to do.
		w.rec.Status, w.rec.Reason = runnerwire.AutopilotSucceeded,
			fmt.Sprintf("every task is done; %d added to the goal after the start were not worked", len(w.added))
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
	todo := 0
	for _, id := range d.order {
		if d.dispatchable(id) {
			todo++
		}
	}
	if todo > runnerwire.MaxAutopilotTasks {
		return runnerwire.AutopilotView{}, invalidf("the goal has %d tasks; an autopilot works at most %d: split the goal", todo, runnerwire.MaxAutopilotTasks)
	}
	// The task set is frozen here, each task with the content it starts with.
	tasks := make([]runnerwire.TaskInstance, 0, len(d.order))
	for _, id := range d.order {
		body, err := o.opts.Board.Content(in.Project, id)
		if err != nil {
			return runnerwire.AutopilotView{}, fmt.Errorf("read task %s: %w", id, err)
		}
		title := d.title(id)
		tasks = append(tasks, runnerwire.TaskInstance{NodeID: id, Title: title, State: runnerwire.TaskNone,
			Upstream: slices.Clone(d.upstream[id]), ContentHash: contentHash(title, body)})
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
	}, tasks)
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
	var added []string
	if g, _, err := o.opts.Board.Graph(rec.Project); err == nil {
		have := map[string]bool{}
		for _, t := range tasks {
			have[t.NodeID] = true
		}
		for i, nodeID := range buildDAG(g, rec.GoalNodeID).order {
			order[nodeID] = i
			if !have[nodeID] {
				added = append(added, nodeID)
			}
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
	return runnerwire.AutopilotView{Run: rec.AutopilotRun, Tasks: tasks, Added: added}, nil
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

// List is a project's autopilots in the given statuses, oldest first, each as
// View shows it. One that ends between the listing and its view is still
// listed, as it now stands.
func (o *Orchestrator) List(ctx context.Context, project string, statuses ...string) ([]runnerwire.AutopilotView, error) {
	ids, err := o.opts.Store.InProject(ctx, project, statuses...)
	if err != nil {
		return nil, err
	}
	out := make([]runnerwire.AutopilotView, 0, len(ids))
	for _, id := range ids {
		view, err := o.View(ctx, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, view)
	}
	return out, nil
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

// skippedOnBoard reports a board status that sets a task aside rather than
// finishing it.
func skippedOnBoard(status engine.Status) bool {
	return status == engine.StatusSkipped || status == engine.StatusDeprecated
}

// Adopt resumes a paused autopilot with its goal as it stands now: after a
// revision of the goal's requirement (the voice copilot's revise_goal), whose
// replan added, changed and skipped tasks on the board. The task set is
// otherwise frozen at the start so an edit cannot slip new work in unseen;
// here the person who steers the autopilot asks for the edits, so they come
// in: tasks that joined the goal are added, pending tasks take their current
// content (a held change is accepted), and pending tasks that left the goal
// or were skipped on the board are skipped. A task with a try going, or one
// that ended, keeps its record as it is.
func (o *Orchestrator) Adopt(ctx context.Context, id string) error {
	unlock := o.lock(id)
	w, err := o.load(ctx, id)
	if err != nil {
		unlock()
		return err
	}
	if w.rec.Status != runnerwire.AutopilotPaused {
		unlock()
		return conflictf("the autopilot is %s; only a paused one takes the goal's revised tasks", w.rec.Status)
	}
	before := snapshot(w)
	statuses := engine.ComputeStatuses(w.graph, w.state)
	content := func(nodeID string) (string, error) {
		body, err := o.opts.Board.Content(w.rec.Project, nodeID)
		if err != nil {
			return "", fmt.Errorf("read task %s: %w", nodeID, err)
		}
		return contentHash(w.dag.title(nodeID), body), nil
	}
	for _, nodeID := range w.dag.order {
		if w.tasks[nodeID] != nil {
			continue
		}
		hash, err := content(nodeID)
		if err != nil {
			unlock()
			return err
		}
		w.tasks[nodeID] = &runnerwire.TaskInstance{NodeID: nodeID, Title: w.dag.title(nodeID),
			State: runnerwire.TaskNone, Upstream: slices.Clone(w.dag.upstream[nodeID]), ContentHash: hash}
	}
	todo := 0
	for _, nodeID := range sortedIDs(w.tasks) {
		t := w.tasks[nodeID]
		if _, member := w.dag.nodes[nodeID]; member && w.dag.dispatchable(nodeID) {
			todo++
		}
		if final1(t.State) || t.RunID != "" || t.PipelineID != "" {
			continue
		}
		_, member := w.dag.nodes[nodeID]
		switch {
		case !member:
			t.Hold = ""
			setState(t, runnerwire.TaskSkipped, "", "left the goal in a revision")
		case skippedOnBoard(statuses[nodeID]):
			t.Hold = ""
			setState(t, runnerwire.TaskSkipped, runnerwire.StageDone, string(statuses[nodeID])+" on the board")
		default:
			hash, err := content(nodeID)
			if err != nil {
				unlock()
				return err
			}
			t.ContentHash = hash
			if t.Hold == runnerwire.HoldChanged || t.Hold == runnerwire.HoldRemoved {
				t.Hold, t.Stage = "", ""
				setState(t, runnerwire.TaskNone, "", "")
			}
		}
	}
	if todo > runnerwire.MaxAutopilotTasks {
		unlock()
		return invalidf("the revised goal has %d tasks; an autopilot works at most %d: split the goal", todo, runnerwire.MaxAutopilotTasks)
	}
	w.rec.Status, w.rec.Reason, w.dirty = runnerwire.AutopilotRunning, "", true
	err = o.save(ctx, w, before)
	unlock()
	if err != nil {
		return err
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
		t.Hold = ""
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
		switch {
		case t.State == runnerwire.TaskFailed, t.State == runnerwire.TaskUpstreamFailed, t.State == runnerwire.TaskSkipped:
		case t.Hold == runnerwire.HoldChanged, t.Hold == runnerwire.HoldRemoved:
			if _, member := w.dag.nodes[nodeID]; !member {
				return conflictf("the task is no longer part of the goal; skip it instead")
			}
		default:
			return conflictf("only a failed, skipped or held task can be retried; this one is %s", t.State)
		}
		// Retrying a task held for its change accepts the task as it is now;
		// any other retry keeps the content it started with, so a change
		// made meanwhile is still held and shown.
		if t.Hold == runnerwire.HoldChanged {
			body, err := o.opts.Board.Content(w.rec.Project, nodeID)
			if err != nil {
				return err
			}
			t.ContentHash = contentHash(w.dag.title(nodeID), body)
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
		t.Hold = ""
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
// Review records a person's verdict on a task waiting for one. Who may give
// it is the pipeline review's rule: the autopilot's creator, the runner's
// owner or an administrator (admin), or anyone who may change the project when
// the policy lets any editor review (the caller has checked that much). Anyone
// else gets ErrNotFound, as for the pipeline route.
func (o *Orchestrator) Review(ctx context.Context, id, nodeID string, who runs.Reviewer, admin bool, verdict, comment string) error {
	verdict = strings.TrimSpace(verdict)
	if verdict != runnerwire.VerdictApprove && verdict != runnerwire.VerdictChanges {
		return invalidf("verdict must be %q or %q", runnerwire.VerdictApprove, runnerwire.VerdictChanges)
	}
	if len(comment) > 8<<10 {
		return invalidf("the comment is at most 8192 bytes")
	}
	rec, err := o.Get(ctx, id)
	if err != nil {
		return err
	}
	allowed := admin || rec.CreatedBy == who.ID || rec.Policy.Review.AnyEditor
	view, err := o.View(ctx, id)
	if err != nil {
		return err
	}
	if i := slices.IndexFunc(view.Tasks, func(t runnerwire.TaskInstance) bool { return t.NodeID == nodeID }); i >= 0 &&
		view.Tasks[i].Hold != "" {
		return o.reviewHold(ctx, rec, nodeID, who, admin, verdict, comment)
	}
	// A task whose pipeline waits for the person is decided by the pipeline
	// (POST /api/runs/pipelines/:id/review does the same): approve ends it
	// approved, changes starts its revise round. The pipeline's observer
	// update then moves the task on. Called outside the autopilot's lock,
	// since that update runs a pass.
	if pipelineHandlesHuman {
		for _, t := range view.Tasks {
			if t.NodeID != nodeID || t.PipelineID == "" {
				continue
			}
			if !allowed {
				p, err := o.opts.Runs.Pipeline(ctx, t.PipelineID)
				if err != nil || p.OwnerID != who.ID {
					return ErrNotFound
				}
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
	if !allowed {
		return ErrNotFound
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

// reviewHold decides a held task. Only a merge held for its sensitive paths
// is reviewed: approve lets it merge, changes start a new try. A task held
// because its node changed or left the goal is retried or skipped instead.
// The hold guards against whoever may edit the project, so the policy's
// AnyEditor does not reach it: the creator or an administrator decides.
func (o *Orchestrator) reviewHold(ctx context.Context, rec Record, nodeID string, who runs.Reviewer, admin bool, verdict, comment string) error {
	if !admin && rec.CreatedBy != who.ID {
		return ErrNotFound
	}
	return o.taskAction(ctx, rec.ID, nodeID, func(w *work, t *runnerwire.TaskInstance) error {
		if t.Hold != runnerwire.HoldSensitive || t.State != runnerwire.TaskWaitingHuman {
			if t.Hold == runnerwire.HoldChanged || t.Hold == runnerwire.HoldRemoved {
				return conflictf("the task is held because %s; retry or skip it", t.Reason)
			}
			return conflictf("the task is not waiting for a review; it is %s", t.State)
		}
		if verdict == runnerwire.VerdictApprove {
			t.HumanVerdict = verdict
			t.Reason = "merge approved by " + firstNonEmpty(who.Name, who.ID)
			return nil
		}
		o.clear(t, humanChangesPrefix+firstNonEmpty(strings.TrimSpace(comment), "(no comment)"))
		return nil
	})
}

// clear resets a task for a new try, keeping its try count.
func (o *Orchestrator) clear(t *runnerwire.TaskInstance, reason string) {
	t.RunID, t.PipelineID, t.PRURL, t.CIStatus, t.HumanVerdict, t.Advisor = "", "", "", "", "", ""
	t.Votes, t.Stages, t.Hold = nil, nil, ""
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
