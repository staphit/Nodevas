package autopilot

// The goal flow under workflow stages (agent roles, spec 3.3): QA writes the
// goal's acceptance criteria before the breakdown, the Planner breaks the
// goal down, the tasks run their staged pipelines, QA verifies the finished
// goal and, when it fails, the fix tasks its own run created join the
// autopilot, bounded per round and in rounds. A person may always finish
// the goal on the board; agents never can (CompletionPeople).

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"

	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// maxFixTasksPerRound caps the fix tasks one QA round may add.
const maxFixTasksPerRound = 5

// QAFixTag marks a fix task QA created.
const QAFixTag = "qa-fix"

// initialGoalPhase is where a staged autopilot starts.
func initialGoalPhase(wf *engine.Workflow, goal *engine.Node, members int) string {
	switch {
	case wf.Stages.GoalQA && goal.Acceptance.Empty():
		return runnerwire.GoalPhaseAcceptance
	case wf.Stages.Planner && members == 0:
		return runnerwire.GoalPhasePlan
	}
	return runnerwire.GoalPhaseTasks
}

// goalFlow runs one pass of a staged autopilot. A pass may move through more
// than one phase (QA's criteria in, the planner started).
func (o *Orchestrator) goalFlow(ctx context.Context, w *work) {
	for i := 0; i < 4; i++ {
		before := w.rec.GoalPhase
		switch w.rec.GoalPhase {
		case runnerwire.GoalPhaseAcceptance:
			o.acceptancePhase(ctx, w)
		case runnerwire.GoalPhasePlan:
			o.planPhase(ctx, w)
		case runnerwire.GoalPhaseQA:
			o.qaPhase(ctx, w)
		case runnerwire.GoalPhaseHuman:
			o.humanPhase(w)
		case runnerwire.GoalPhaseDone:
		default:
			if w.rec.Status == runnerwire.AutopilotRunning {
				o.schedule(ctx, w)
			}
			o.tasksDone(ctx, w)
		}
		if w.rec.GoalPhase == before || w.rec.Status != runnerwire.AutopilotRunning {
			return
		}
	}
}

func (o *Orchestrator) setPhase(w *work, phase string) {
	w.rec.GoalPhase = phase
	w.dirty = true
}

func (o *Orchestrator) goalNode(w *work) *engine.Node {
	if w.graph == nil {
		return nil
	}
	return w.graph.NodeByID(w.rec.GoalNodeID)
}

// afterAcceptance is the phase after the goal's criteria are in.
func (o *Orchestrator) afterAcceptance(w *work) string {
	if w.rec.Workflow.Stages.Planner && len(w.tasks) == 0 {
		return runnerwire.GoalPhasePlan
	}
	return runnerwire.GoalPhaseTasks
}

// acceptancePhase has QA write the goal's acceptance criteria (a plan run in
// the QA role: criteria only, no tasks).
func (o *Orchestrator) acceptancePhase(ctx context.Context, w *work) {
	goal := o.goalNode(w)
	if goal == nil {
		return
	}
	if w.rec.QARunID == "" {
		if !goal.Acceptance.Empty() {
			o.setPhase(w, o.afterAcceptance(w))
			return
		}
		if w.rec.Status != runnerwire.AutopilotRunning {
			return
		}
		o.startGoalRun(ctx, w, runnerwire.JobPlan, engine.RoleQA, func(id string) { w.rec.QARunID = id },
			"Goal QA: write this goal's acceptance criteria with set_acceptance (checkable one-line items). Create no tasks.")
		return
	}
	r, err := o.opts.Runs.Get(ctx, w.rec.QARunID)
	if err != nil || !runnerwire.RunFinal(r.Status) {
		return
	}
	if r.Status == runnerwire.RunSucceeded && !goal.Acceptance.Empty() {
		w.rec.QARunID = ""
		o.setPhase(w, o.afterAcceptance(w))
		return
	}
	w.rec.Status = runnerwire.AutopilotFailed
	w.rec.Reason = fmt.Sprintf("QA wrote no acceptance criteria for the goal (run %s %s); write them and start a new autopilot", r.ID, r.Status)
	w.dirty = true
}

// planPhase has the Planner break the goal down, then adopts the tasks its
// run created.
func (o *Orchestrator) planPhase(ctx context.Context, w *work) {
	if w.rec.PlanRunID == "" {
		if w.rec.Status != runnerwire.AutopilotRunning {
			return
		}
		o.startGoalRun(ctx, w, runnerwire.JobPlan, engine.RolePlanner, func(id string) { w.rec.PlanRunID = id },
			"Break this goal down into tasks with create_subgraph; give every task 2-6 checkable acceptance criteria.")
		return
	}
	r, err := o.opts.Runs.Get(ctx, w.rec.PlanRunID)
	if err != nil || !runnerwire.RunFinal(r.Status) {
		return
	}
	if r.Status != runnerwire.RunSucceeded {
		w.rec.Status, w.rec.Reason = runnerwire.AutopilotFailed, fmt.Sprintf("the planner run %s %s", r.ID, r.Status)
		w.dirty = true
		return
	}
	adopted := o.adopt(w, func(id string) bool {
		return o.opts.Board.CreatedByRun(w.rec.Project, id) == w.rec.PlanRunID
	}, 0)
	if adopted == 0 {
		w.rec.Status, w.rec.Reason = runnerwire.AutopilotFailed, "the planner created no tasks for the goal"
		w.dirty = true
		return
	}
	o.setPhase(w, runnerwire.GoalPhaseTasks)
}

// adopt adds the goal's members that are not tasks yet and that keep says
// to, in DAG order, at most limit (0 for no limit). It reports how many.
func (o *Orchestrator) adopt(w *work, keep func(id string) bool, limit int) int {
	adopted := 0
	for _, id := range w.dag.order {
		if w.tasks[id] != nil || !keep(id) {
			continue
		}
		if limit > 0 && adopted >= limit {
			break
		}
		body, err := o.opts.Board.Content(w.rec.Project, id)
		if err != nil {
			continue
		}
		title := w.dag.title(id)
		w.tasks[id] = &runnerwire.TaskInstance{NodeID: id, Title: title, State: runnerwire.TaskNone,
			Upstream: slices.Clone(w.dag.upstream[id]), ContentHash: contentHash(title, body)}
		adopted++
	}
	if adopted > 0 {
		w.dirty = true
		o.syncMembers(w)
	}
	return adopted
}

// tasksDone moves a staged autopilot on once every task ended: to QA's
// verification, a person's review of the goal, or done.
func (o *Orchestrator) tasksDone(ctx context.Context, w *work) {
	if len(w.tasks) == 0 {
		return
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
	if failed > 0 {
		w.rec.Status, w.rec.Reason = runnerwire.AutopilotFailed, fmt.Sprintf("%d task(s) failed", failed)
		w.dirty = true
		return
	}
	wf := w.rec.Workflow.Normalized()
	switch {
	case wf.Stages.GoalQA:
		w.rec.QARunID = ""
		o.setPhase(w, runnerwire.GoalPhaseQA)
	case wf.Stages.GoalReview:
		w.rec.Reason = "every task is done; waiting for a person to review the goal"
		o.setPhase(w, runnerwire.GoalPhaseHuman)
	default:
		o.goalDone(w, "every task is done")
	}
}

// goalDone ends the autopilot succeeded and the goal done on the board.
func (o *Orchestrator) goalDone(w *work, reason string) {
	w.rec.Status, w.rec.Reason = runnerwire.AutopilotSucceeded, reason
	if len(w.added) > 0 {
		w.rec.Reason += fmt.Sprintf("; %d added to the goal after the start were not worked", len(w.added))
	}
	o.setPhase(w, runnerwire.GoalPhaseDone)
	o.markBoard(w, w.rec.GoalNodeID, engine.StatusDone, "autopilot: "+reason)
}

// qaPassed reads a QA verification run: every criterion passes, the verdict
// is pass and the repository's tests passed (when it ran them).
func qaPassed(r runs.Record) bool {
	if r.Status != runnerwire.RunSucceeded || r.Verdict != runnerwire.VerdictPass {
		return false
	}
	for _, check := range r.Checks {
		if !check.Pass {
			return false
		}
	}
	if v := r.Verify; v != nil && !v.NoCommand && v.ExitCode != 0 {
		return false
	}
	return true
}

// qaPhase runs QA's verification of the finished goal; a failure adopts the
// fix tasks that QA run created, or hands the goal to a person.
func (o *Orchestrator) qaPhase(ctx context.Context, w *work) {
	if w.rec.QARunID == "" {
		if w.rec.Status != runnerwire.AutopilotRunning {
			return
		}
		o.startGoalRun(ctx, w, runnerwire.JobJudge, engine.RoleQA, func(id string) { w.rec.QARunID = id },
			fmt.Sprintf("Goal QA, round %d: verify the finished goal against its acceptance criteria. On failure create at most %d fix tasks with create_subgraph, tagged %s and %s, each with acceptance criteria.",
				w.rec.QARound+1, maxFixTasksPerRound, engine.GoalTag(w.rec.GoalNodeID), QAFixTag))
		return
	}
	r, err := o.opts.Runs.Get(ctx, w.rec.QARunID)
	if err != nil || !runnerwire.RunFinal(r.Status) {
		return
	}
	wf := w.rec.Workflow.Normalized()
	round := w.rec.QARound + 1
	if qaPassed(r) {
		o.recordQARound(ctx, w.rec.ID, round, r.ID, QAOutcomePass, 0, "QA verified the goal")
		if wf.Stages.GoalReview {
			w.rec.Reason = "QA verified the goal; waiting for a person to review it"
			o.setPhase(w, runnerwire.GoalPhaseHuman)
			return
		}
		o.goalDone(w, "QA verified the goal")
		return
	}
	w.rec.QARound++
	w.dirty = true
	if w.rec.QARound >= wf.MaxQARounds {
		w.rec.Reason = fmt.Sprintf("QA still failing after %d round(s); a person decides", w.rec.QARound)
		o.recordQARound(ctx, w.rec.ID, round, r.ID, QAOutcomeFail, 0, w.rec.Reason)
		o.setPhase(w, runnerwire.GoalPhaseHuman)
		return
	}
	qaRun := w.rec.QARunID
	goalTag := engine.GoalTag(w.rec.GoalNodeID)
	adopted := o.adopt(w, func(id string) bool {
		n := w.graph.NodeByID(id)
		return n != nil && slices.Contains(n.Tags, QAFixTag) && slices.Contains(n.Tags, goalTag) &&
			o.opts.Board.CreatedByRun(w.rec.Project, id) == qaRun
	}, maxFixTasksPerRound)
	if adopted == 0 {
		w.rec.Reason = fmt.Sprintf("QA failed in round %d and created no fix tasks; a person decides", w.rec.QARound)
		o.recordQARound(ctx, w.rec.ID, round, r.ID, QAOutcomeNone, 0, w.rec.Reason)
		o.setPhase(w, runnerwire.GoalPhaseHuman)
		return
	}
	w.rec.Reason = fmt.Sprintf("QA round %d failed: %d fix task(s) added", w.rec.QARound, adopted)
	o.recordQARound(ctx, w.rec.ID, round, r.ID, QAOutcomeFail, adopted, w.rec.Reason)
	o.setPhase(w, runnerwire.GoalPhaseTasks)
}

// humanPhase waits for a person to finish the goal on the board.
func (o *Orchestrator) humanPhase(w *work) {
	statuses := engine.ComputeStatuses(w.graph, w.state)
	status := statuses[w.rec.GoalNodeID]
	if engine.Settled(status, boardStatuses(w.graph)) {
		w.rec.Status, w.rec.Reason = runnerwire.AutopilotSucceeded, "a person finished the goal ("+string(status)+")"
		o.setPhase(w, runnerwire.GoalPhaseDone)
	}
}

// goalRunner is an online runner of the creator's with the policy's
// repository and an agent for role: the role's routing rule's target, else
// what the advisor picks for the goal.
func (o *Orchestrator) goalRunner(ctx context.Context, w *work, role, content string) (string, runnerwire.AgentModel, string) {
	rec := w.rec
	pol := rec.Policy
	owned, err := o.opts.Runners.Owned(ctx, rec.CreatedBy)
	if err != nil {
		return "", runnerwire.AgentModel{}, "cannot list runners: " + err.Error()
	}
	var eligible []runnerwire.Runner
	for _, r := range owned {
		if !r.Online || !runs.Headless(r.Info) || (pol.RunnerID != "" && r.ID != pol.RunnerID) {
			continue
		}
		if slices.ContainsFunc(r.Info.Repos, func(repo runnerwire.Repo) bool { return repo.ID == pol.RepoID }) {
			eligible = append(eligible, r)
		}
	}
	if len(eligible) == 0 {
		return "", runnerwire.AgentModel{}, "waiting for an online runner with the repository"
	}
	var chain []runnerwire.AgentModel
	if rules, err := o.opts.Runs.Routing(ctx, rec.CreatedBy); err == nil {
		s := runs.Suggest(rules, runs.RouteNode{Project: rec.Project, Role: role})
		s = routeWith(ctx, o.opts.Route, RouteRequest{OwnerID: rec.CreatedBy, Project: rec.Project,
			NodeID: rec.GoalNodeID, Role: role, Manual: s})
		if s.RuleID != "" {
			chain = append(chain, s.Target)
		}
	}
	if choice, err := o.choose(ctx, ChooseInput{OwnerID: rec.CreatedBy, Project: rec.Project, NodeID: rec.GoalNodeID,
		Title: w.dag.title(rec.GoalNodeID), Body: content, Policy: pol, Agents: mergeAgents(eligible)}); err == nil {
		chain = append(chain, choice.Implement...)
	}
	sort.SliceStable(eligible, func(i, j int) bool { return o.busy(ctx, eligible[i].ID) < o.busy(ctx, eligible[j].ID) })
	for _, am := range chain {
		for _, r := range eligible {
			if usable([]runnerwire.AgentModel{am}, mergeAgents([]runnerwire.Runner{r})) != nil {
				return r.ID, am, ""
			}
		}
	}
	return "", runnerwire.AgentModel{}, "no online runner has an agent for the " + role + " role"
}

// startGoalRun starts a run on the goal node itself (QA's criteria, the
// breakdown, QA's verification) and records it with set. A refusal is noted
// on the autopilot and tried again next pass.
func (o *Orchestrator) startGoalRun(ctx context.Context, w *work, kind, role string, set func(id string), extra string) {
	rec := w.rec
	goal := o.goalNode(w)
	if goal == nil {
		return
	}
	content, err := o.opts.Board.Content(rec.Project, rec.GoalNodeID)
	if err != nil {
		w.rec.Reason, w.dirty = "cannot read the goal: "+err.Error(), true
		return
	}
	runnerID, am, why := o.goalRunner(ctx, w, role, content)
	if runnerID == "" {
		w.rec.Reason, w.dirty = why, true
		return
	}
	nodeType := w.graph.TypeOf(goal)
	acceptance := goal.Acceptance
	record, err := o.opts.Runs.Start(ctx, runs.StartRequest{
		Body: runnerwire.CreateRunRequest{
			NodeID: rec.GoalNodeID, RunnerID: runnerID, Agent: am.Agent, Model: am.Model, RepoID: rec.Policy.RepoID,
			Kind: kind, Role: role, Prompt: runs.Truncate(extra, runs.MaxExtraPrompt),
		},
		Project: rec.Project, ActorID: rec.CreatedBy, ActorName: rec.CreatedByName, SteppedUpAt: rec.SteppedUpAt,
		Roles: w.graph.ResolvedAgentRoles(),
		LoadNode: func() (runs.NodeInfo, bool, error) {
			refs, truncated, err := o.opts.Board.References(rec.Project, rec.GoalNodeID)
			if err != nil {
				return runs.NodeInfo{}, false, err
			}
			return runs.NodeInfo{
				Title: goal.Title, Content: content, Type: nodeType.ID, AgentMode: nodeType.AgentMode,
				References: refs, ReferencesTruncated: truncated,
				Acceptance: acceptance, Workflow: rec.Workflow,
			}, true, nil
		},
	})
	if err != nil {
		var refused *runs.StartError
		if errors.As(err, &refused) {
			w.rec.Reason, w.dirty = refused.Msg, true
			return
		}
		slog.Warn("autopilot: start goal run", slog.String("autopilot", rec.ID), slog.Any("error", err))
		return
	}
	set(record.ID)
	w.dirty = true
	o.remember([]string{record.ID}, rec.ID)
}
