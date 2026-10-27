package autopilot

import (
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// Agent roles on the autopilot: stages frozen at start, the goal's QA
// acceptance before breakdown, QA verification after the tasks, fix tasks and
// rounds, and the human goal review (spec 3.3).

func withWorkflow(b *fakeBoard, wf *engine.Workflow) *fakeBoard {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.graph.Workflow = wf
	return b
}

func goalCriteria(b *fakeBoard, texts ...string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	acc := &engine.Acceptance{}
	for i, text := range texts {
		acc.Items = append(acc.Items, engine.AcceptanceItem{ID: "a" + string(rune('1'+i)), Text: text})
	}
	b.graph.NodeByID("goal").Acceptance = acc
}

// addMember puts a node on the board tagged for the goal and wired into it,
// as a plan or QA run would, recording which run created it.
func (b *fakeBoard) addMember(id, createdBy string, tags ...string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.graph.Nodes = append(b.graph.Nodes, &engine.Node{ID: id, Title: strings.ToUpper(id), Kind: "task",
		Tags: append([]string{"goal:goal"}, tags...)})
	b.graph.Edges = append(b.graph.Edges, &engine.Edge{From: id, To: "goal"})
	goal := b.graph.NodeByID("goal")
	if goal.Requires == "" {
		goal.Requires = id
	} else {
		goal.Requires += " and " + id
	}
	if b.createdBy == nil {
		b.createdBy = map[string]string{}
	}
	b.createdBy[id] = createdBy
}

// CreatedByRun is the run that created a node (store.CreatedByRun).
func (b *fakeBoard) CreatedByRun(_, id string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.createdBy[id]
}

func qaOnly(extra func(*engine.Workflow)) *engine.Workflow {
	wf := &engine.Workflow{Preset: engine.PresetCustom, Stages: engine.Stages{GoalQA: true}, MaxQARounds: 2}
	if extra != nil {
		extra(wf)
	}
	return wf
}

// throughTasks creates the autopilot over goal ← a and finishes a.
func throughTasks(t *testing.T, wf *engine.Workflow) (*harness, runnerwire.AutopilotView) {
	t.Helper()
	board := withWorkflow(newBoard([]string{"goal:goal", "a"}, []string{"a>goal"}), wf)
	goalCriteria(board, "checkout works end to end", "totals include tax")
	h := newHarness(t, board)
	v := h.create(runnerwire.AutopilotPolicy{MaxParallel: 1})
	h.finishWithPR(task(v, "a").RunID)
	return h, h.view(v.Run.ID)
}

func (h *harness) qaRun(t *testing.T) runs.Record {
	t.Helper()
	st := h.runs.lastStart("goal")
	if st.Body.NodeID != "goal" || st.Body.Kind != runnerwire.JobJudge || st.Body.Role != engine.RoleQA {
		t.Fatalf("QA start = kind %q role %q node %q", st.Body.Kind, st.Body.Role, st.Body.NodeID)
	}
	var last runs.Record
	h.runs.mu.Lock()
	for _, r := range h.runs.runs {
		if r.NodeID == "goal" && r.CreatedAt >= last.CreatedAt {
			last = *r
		}
	}
	h.runs.mu.Unlock()
	return last
}

func (h *harness) finishQA(runID, verdict string, pass bool) {
	h.runs.update(runID, func(r *runs.Record) {
		r.Status, r.Verdict = runnerwire.RunSucceeded, verdict
		r.Checks = []runnerwire.CheckResult{{ID: "a1", Pass: pass}, {ID: "a2", Pass: true}}
		r.Verify = &runnerwire.VerifyResult{Phase: runnerwire.VerifyGoal, Passed: pass, ExitCode: map[bool]int{true: 0, false: 1}[pass]}
	})
}

func TestStagedTaskStagesDerived(t *testing.T) {
	std := &engine.Workflow{Preset: engine.PresetStandard, Stages: engine.Stages{Planner: true, Tester: true, Reviewer: true}}
	board := withWorkflow(newBoard([]string{"goal:goal", "a"}, []string{"a>goal"}), std)
	h := newHarness(t, board)
	v := h.create(runnerwire.AutopilotPolicy{MaxParallel: 1})
	st := h.runs.lastStart("a")
	node, _, err := st.LoadNode()
	if err != nil || node.Workflow == nil || node.Workflow.Preset != engine.PresetStandard {
		t.Fatalf("task start carries workflow %+v (%v)", node.Workflow, err)
	}
	if st.Body.Pipeline == nil || st.Body.Pipeline.Tester.Agent == "" {
		t.Fatalf("task start has no tester: %+v", st.Body.Pipeline)
	}
	a := task(v, "a")
	h.runs.update(a.RunID, func(r *runs.Record) {
		r.Kind, r.Role, r.Status = runnerwire.JobTest, engine.RoleTester, runnerwire.RunRunning
	})
	if got := task(h.view(v.Run.ID), "a"); got.Stage != runnerwire.StageTest {
		t.Fatalf("tester running: stage %q", got.Stage)
	}
	h.runs.update(a.RunID, func(r *runs.Record) { r.Status = runnerwire.RunSucceeded })
	h.runs.add(runs.Record{Run: runnerwire.Run{NodeID: "a", Kind: runnerwire.JobVerify, PipelineID: a.PipelineID, Status: runnerwire.RunRunning}})
	if got := task(h.view(v.Run.ID), "a"); got.Stage != runnerwire.StageVerifyRed {
		t.Fatalf("red verify running: stage %q (%s)", got.Stage, stageList(got))
	}
}

func TestWorkflowFrozenAtStart(t *testing.T) {
	std := &engine.Workflow{Preset: engine.PresetStandard, Stages: engine.Stages{Planner: true, Tester: true, Reviewer: true}}
	board := withWorkflow(newBoard([]string{"goal:goal", "a", "b"}, []string{"a>b", "b>goal"}), std)
	h := newHarness(t, board)
	v := h.create(runnerwire.AutopilotPolicy{MaxParallel: 1})
	if v.Run.Stages == nil || v.Run.Stages.Preset != engine.PresetStandard {
		t.Fatalf("stages at start = %+v", v.Run.Stages)
	}
	withWorkflow(board, &engine.Workflow{Preset: engine.PresetLight, Stages: engine.Stages{Planner: true}})
	h.finishWithPR(task(v, "a").RunID)
	v = h.view(v.Run.ID)
	if v.Run.Stages == nil || v.Run.Stages.Preset != engine.PresetStandard {
		t.Fatalf("stages after the project changed = %+v", v.Run.Stages)
	}
	node, _, _ := h.runs.lastStart("b").LoadNode()
	if node.Workflow == nil || node.Workflow.Preset != engine.PresetStandard {
		t.Fatalf("b started under %+v, want the frozen standard", node.Workflow)
	}
}

func TestQAAcceptanceBeforeBreakdown(t *testing.T) {
	full := &engine.Workflow{Preset: engine.PresetFull, Stages: engine.Stages{Planner: true, GoalQA: true, Tester: true, Reviewer: true, CI: true, GoalReview: true}}
	board := withWorkflow(newBoard([]string{"goal:goal"}, nil), full)
	h := newHarness(t, board)
	v := h.create(runnerwire.AutopilotPolicy{MaxParallel: 1})
	if v.Run.GoalPhase != runnerwire.GoalPhaseAcceptance {
		t.Fatalf("phase = %q", v.Run.GoalPhase)
	}
	qa := h.runs.lastStart("goal")
	if qa.Body.Kind != runnerwire.JobPlan || qa.Body.Role != engine.RoleQA {
		t.Fatalf("first start = kind %q role %q", qa.Body.Kind, qa.Body.Role)
	}
	// QA wrote the criteria and ended: the planner breaks the goal down.
	goalCriteria(board, "checkout works end to end")
	h.runs.update(h.qaRunID(t), func(r *runs.Record) { r.Status = runnerwire.RunSucceeded })
	v = h.view(v.Run.ID)
	plan := h.runs.lastStart("goal")
	if v.Run.GoalPhase != runnerwire.GoalPhasePlan || plan.Body.Kind != runnerwire.JobPlan || plan.Body.Role != engine.RolePlanner {
		t.Fatalf("after acceptance: phase %q start %q/%q", v.Run.GoalPhase, plan.Body.Kind, plan.Body.Role)
	}
	// The planner's tasks become the autopilot's.
	planRun := h.qaRunID(t)
	board.addMember("a", planRun)
	h.runs.update(planRun, func(r *runs.Record) { r.Status = runnerwire.RunSucceeded })
	v = h.view(v.Run.ID)
	if v.Run.GoalPhase != runnerwire.GoalPhaseTasks || task(v, "a").RunID == "" {
		t.Fatalf("after the plan: phase %q tasks %s", v.Run.GoalPhase, states(v))
	}
}

// qaRunID is the newest run on the goal node.
func (h *harness) qaRunID(t *testing.T) string {
	t.Helper()
	var last runs.Record
	h.runs.mu.Lock()
	for _, r := range h.runs.runs {
		if r.NodeID == "goal" && r.CreatedAt >= last.CreatedAt {
			last = *r
		}
	}
	h.runs.mu.Unlock()
	if last.ID == "" {
		t.Fatal("no run on the goal")
	}
	return last.ID
}

func TestGoalQARunsAfterAllTasks(t *testing.T) {
	h, v := throughTasks(t, qaOnly(nil))
	if v.Run.GoalPhase != runnerwire.GoalPhaseQA || v.Run.Status != runnerwire.AutopilotRunning {
		t.Fatalf("after the tasks: phase %q status %s", v.Run.GoalPhase, v.Run.Status)
	}
	if h.board.status("goal") == engine.StatusDone {
		t.Fatal("the goal was marked done before QA verified it")
	}
	qa := h.qaRun(t)
	st := h.runs.lastStart("goal")
	if node, _, _ := st.LoadNode(); node.Acceptance == nil || len(node.Acceptance.Items) != 2 {
		t.Fatalf("QA run not given the goal's criteria: %+v", node.Acceptance)
	}
	if v.Run.QARunID != qa.ID || v.Run.QARound != 0 {
		t.Fatalf("qa run %q round %d", v.Run.QARunID, v.Run.QARound)
	}
}

func TestGoalQAPassMarksGoalDone(t *testing.T) {
	h, v := throughTasks(t, qaOnly(nil))
	h.finishQA(h.qaRun(t).ID, runnerwire.VerdictPass, true)
	v = h.view(v.Run.ID)
	if v.Run.Status != runnerwire.AutopilotSucceeded || v.Run.GoalPhase != runnerwire.GoalPhaseDone || h.board.status("goal") != engine.StatusDone {
		t.Fatalf("after QA pass: %s phase %q goal %s", v.Run.Status, v.Run.GoalPhase, h.board.status("goal"))
	}
}

func TestGoalQAPassWaitsForHumanGoalReview(t *testing.T) {
	h, v := throughTasks(t, qaOnly(func(wf *engine.Workflow) { wf.Stages.GoalReview = true }))
	h.finishQA(h.qaRun(t).ID, runnerwire.VerdictPass, true)
	v = h.view(v.Run.ID)
	if v.Run.GoalPhase != runnerwire.GoalPhaseHuman || v.Run.Status != runnerwire.AutopilotRunning || h.board.status("goal") == engine.StatusDone {
		t.Fatalf("after QA pass with a goal review: %s phase %q goal %s", v.Run.Status, v.Run.GoalPhase, h.board.status("goal"))
	}
	// The person marks the goal done: the autopilot is done.
	if err := h.board.SetStatus("main", "goal", engine.StatusDone, "Bea", ""); err != nil {
		t.Fatal(err)
	}
	if v = h.view(v.Run.ID); v.Run.Status != runnerwire.AutopilotSucceeded {
		t.Fatalf("after the person's review: %s", v.Run.Status)
	}
}

func TestGoalQAFailAdoptsOnlyItsFixTasks(t *testing.T) {
	h, v := throughTasks(t, qaOnly(nil))
	qa := h.qaRun(t)
	h.board.addMember("fix1", qa.ID, "qa-fix")
	h.board.addMember("rogue", "run-elsewhere", "qa-fix")
	h.board.addMember("person", "", "qa-fix")
	h.finishQA(qa.ID, runnerwire.VerdictFail, false)
	v = h.view(v.Run.ID)
	if task(v, "fix1").NodeID == "" || task(v, "rogue").NodeID != "" || task(v, "person").NodeID != "" {
		t.Fatalf("adopted: %s", states(v))
	}
	if v.Run.QARound != 1 || v.Run.GoalPhase != runnerwire.GoalPhaseTasks || task(v, "fix1").RunID == "" {
		t.Fatalf("after QA fail: round %d phase %q fix1 %+v", v.Run.QARound, v.Run.GoalPhase, task(v, "fix1"))
	}
	added := strings.Join(v.Added, ",")
	if !strings.Contains(added, "rogue") || !strings.Contains(added, "person") {
		t.Fatalf("unadopted nodes not reported: %v", v.Added)
	}
	// The fix done, QA verifies again.
	h.finishWithPR(task(v, "fix1").RunID)
	v = h.view(v.Run.ID)
	if v.Run.GoalPhase != runnerwire.GoalPhaseQA || h.runs.startsFor("goal") != 2 {
		t.Fatalf("after the fix: phase %q goal starts %d", v.Run.GoalPhase, h.runs.startsFor("goal"))
	}
}

func TestGoalQAFixTasksCapped(t *testing.T) {
	h, v := throughTasks(t, qaOnly(nil))
	qa := h.qaRun(t)
	for _, id := range []string{"f1", "f2", "f3", "f4", "f5", "f6", "f7"} {
		h.board.addMember(id, qa.ID, "qa-fix")
	}
	h.finishQA(qa.ID, runnerwire.VerdictFail, false)
	v = h.view(v.Run.ID)
	adopted := 0
	for _, tk := range v.Tasks {
		if strings.HasPrefix(tk.NodeID, "f") {
			adopted++
		}
	}
	if adopted != 5 {
		t.Fatalf("adopted %d fix tasks, want at most 5: %s", adopted, states(v))
	}
}

func TestGoalQAMaxRoundsHandsToHuman(t *testing.T) {
	h, v := throughTasks(t, qaOnly(func(wf *engine.Workflow) { wf.MaxQARounds = 1 }))
	qa := h.qaRun(t)
	h.board.addMember("fix1", qa.ID, "qa-fix")
	h.finishQA(qa.ID, runnerwire.VerdictFail, false)
	v = h.view(v.Run.ID)
	if v.Run.GoalPhase != runnerwire.GoalPhaseHuman || !strings.Contains(v.Run.Reason, "QA still failing after 1 round") {
		t.Fatalf("after the last round: phase %q reason %q", v.Run.GoalPhase, v.Run.Reason)
	}
	if task(v, "fix1").NodeID != "" {
		t.Fatal("fix tasks adopted past the round limit")
	}
}

func TestLegacyPolicyReviewModeStillHonored(t *testing.T) {
	h := newHarness(t, newBoard([]string{"goal", "a"}, []string{"a>goal"}))
	v := h.create(runnerwire.AutopilotPolicy{MaxRounds: 1, Review: runnerwire.ReviewPolicy{
		Mode: runnerwire.ReviewAgents, Reviewers: []runnerwire.AgentModel{{Agent: "codex"}}}})
	st := h.runs.lastStart("a")
	if st.Body.Pipeline == nil || len(st.Body.Pipeline.Reviewers) != 1 || st.Body.Role != "" || st.Body.Pipeline.Tester.Agent != "" {
		t.Fatalf("legacy start = %+v", st.Body)
	}
	if v.Run.Stages != nil || v.Run.GoalPhase != "" {
		t.Fatalf("legacy autopilot has stages %+v phase %q", v.Run.Stages, v.Run.GoalPhase)
	}
}
