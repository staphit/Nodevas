package store

import (
	"errors"
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

// roleStore is a project with a goal, two of its tasks, a scene, a note and an
// orchestrator-only task, under the given workflow (nil = legacy).
func roleStore(t *testing.T, wf *engine.Workflow) *Store {
	t.Helper()
	st := NewStore(t.TempDir())
	g := &engine.Graph{
		Version:  1,
		Workflow: wf,
		Nodes: []*engine.Node{
			{ID: "g", Title: "Goal", Kind: "goal"},
			{ID: "work", Title: "Work", Kind: "task", Tags: []string{"goal:g"}},
			{ID: "other", Title: "Other", Kind: "task", Tags: []string{"goal:g"}},
			{ID: "s1", Title: "Scene", Kind: "scene"},
			{ID: "lore", Title: "Lore", Kind: "note"},
			{ID: "boss", Title: "Boss", Kind: "task", WriteAccess: engine.WriteAccessOrchestrator},
		},
	}
	if _, err := st.SaveGraph(identity.Local, g, ""); err != nil {
		t.Fatal(err)
	}
	return st
}

func standardWorkflow() *engine.Workflow {
	return &engine.Workflow{Preset: engine.PresetStandard, Stages: engine.Stages{Planner: true, Tester: true, Reviewer: true}}
}

func lightWorkflow() *engine.Workflow {
	return &engine.Workflow{Preset: engine.PresetLight, Stages: engine.Stages{Planner: true}}
}

// roleActor is a hub-dispatched run's actor: the owner, as an agent of class,
// in role, working runNode.
func roleActor(role string, class identity.AgentClass, runNode string) identity.Actor {
	actor := identity.Local
	actor.Agent = class
	actor.AgentRole = role
	actor.RunID = "run-" + role
	actor.RunNodeID = runNode
	return actor
}

func wantRoleCode(t *testing.T, err error, code string) {
	t.Helper()
	var denied *ErrRoleDenied
	if !errors.As(err, &denied) || denied.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
}

func items(texts ...string) []engine.AcceptanceItem {
	out := make([]engine.AcceptanceItem, len(texts))
	for i, text := range texts {
		out[i] = engine.AcceptanceItem{Text: text}
	}
	return out
}

func acceptanceOf(t *testing.T, st *Store, id string) *engine.Acceptance {
	t.Helper()
	g, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	n := g.NodeByID(id)
	if n == nil {
		t.Fatalf("node %s gone", id)
	}
	return n.Acceptance
}

func setAcceptance(t *testing.T, st *Store, actor identity.Actor, id string, texts ...string) error {
	t.Helper()
	_, rev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.SetAcceptance(actor, id, items(texts...), rev)
	return err
}

func TestAgentCannotChangeAgentRoles(t *testing.T) {
	st := roleStore(t, standardWorkflow())
	change := func(g *engine.Graph) {
		g.AgentRoles = []engine.AgentRoleDef{{ID: engine.RoleExecutor, Tools: []string{"get_node", "claim_task", "update_node_body"}}}
	}
	for _, actor := range []identity.Actor{agentActor(identity.AgentOrchestrator), roleActor(engine.RolePlanner, identity.AgentOrchestrator, "g")} {
		wantRoleCode(t, saveWith(t, st, actor, change), CodeRolesAgentEdit)
	}
	if err := saveWith(t, st, identity.Local, change); err != nil {
		t.Fatalf("a person changing roles: %v", err)
	}
	g, _, _ := st.LoadGraph()
	if len(g.AgentRoles) != 1 || g.AgentRoles[0].ID != engine.RoleExecutor {
		t.Fatalf("roles after a person's save = %+v", g.AgentRoles)
	}
	// An agent's unrelated save keeps the roles as they are.
	if err := saveWith(t, st, agentActor(identity.AgentWorker), func(g *engine.Graph) { g.Nodes[1].Title = "Work 2" }); err != nil {
		t.Fatalf("unrelated agent save: %v", err)
	}
}

func TestAgentCannotChangeWorkflow(t *testing.T) {
	st := roleStore(t, standardWorkflow())
	for name, mutate := range map[string]func(*engine.Graph){
		"downgrade": func(g *engine.Graph) { g.Workflow = lightWorkflow() },
		"remove":    func(g *engine.Graph) { g.Workflow = nil },
		"lever":     func(g *engine.Graph) { w := *g.Workflow; w.MaxGreenTries = 5; g.Workflow = &w },
	} {
		g, _, _ := st.LoadGraph()
		if g.Workflow == nil {
			t.Fatalf("%s: the project's workflow was not stored", name)
		}
		wantRoleCode(t, saveWith(t, st, agentActor(identity.AgentOrchestrator), mutate), CodeWorkflowAgentEdit)
	}
	if err := saveWith(t, st, identity.Local, func(g *engine.Graph) { g.Workflow = lightWorkflow() }); err != nil {
		t.Fatalf("a person choosing Light: %v", err)
	}
}

func TestAgentCannotChangeGoalOverride(t *testing.T) {
	st := roleStore(t, standardWorkflow())
	full := &engine.Workflow{Preset: engine.PresetFull, Stages: engine.Stages{Planner: true, GoalQA: true, Tester: true, Reviewer: true, CI: true, GoalReview: true}}
	wantRoleCode(t, saveWith(t, st, agentActor(identity.AgentOrchestrator), func(g *engine.Graph) {
		g.NodeByID("g").Workflow = full
	}), CodeWorkflowAgentEdit)
	if err := saveWith(t, st, identity.Local, func(g *engine.Graph) { g.NodeByID("g").Workflow = full }); err != nil {
		t.Fatalf("a person overriding the goal: %v", err)
	}
	wantRoleCode(t, saveWith(t, st, roleActor(engine.RoleQA, identity.AgentOrchestrator, "g"), func(g *engine.Graph) {
		g.NodeByID("g").Workflow = nil
	}), CodeWorkflowAgentEdit)
}

func TestLegacyAgentCannotWriteAcceptance(t *testing.T) {
	st := roleStore(t, standardWorkflow())
	wantRoleCode(t, setAcceptance(t, st, agentActor(identity.AgentOrchestrator), "work", "tests pass"), CodeRoleDenied)
	// Nor by a graph op or a whole-graph save.
	_, _, err := st.ApplyGraphOps(agentActor(identity.AgentOrchestrator), []GraphOp{{
		Kind: "acceptance", NodeID: "work", Acceptance: &engine.Acceptance{Items: []engine.AcceptanceItem{{ID: "a1", Text: "x"}}},
	}})
	wantRoleCode(t, err, CodeRoleDenied)
	wantRoleCode(t, saveWith(t, st, agentActor(identity.AgentWorker), func(g *engine.Graph) {
		g.NodeByID("work").Acceptance = &engine.Acceptance{Items: []engine.AcceptanceItem{{ID: "a1", Text: "x"}}}
	}), CodeRoleDenied)
}

func TestExecutorCannotWriteAcceptance(t *testing.T) {
	st := roleStore(t, standardWorkflow())
	wantRoleCode(t, setAcceptance(t, st, roleActor(engine.RoleExecutor, identity.AgentWorker, "work"), "work", "trivially true"), CodeRoleDenied)
}

func TestTesterCannotWriteAcceptance(t *testing.T) {
	st := roleStore(t, standardWorkflow())
	wantRoleCode(t, setAcceptance(t, st, roleActor(engine.RoleTester, identity.AgentWorker, "work"), "work", "trivially true"), CodeRoleDenied)
}

func TestPlannerWritesTaskAcceptance(t *testing.T) {
	st := roleStore(t, standardWorkflow())
	planner := roleActor(engine.RolePlanner, identity.AgentOrchestrator, "g")
	if err := setAcceptance(t, st, planner, "work", "GET /x returns 200", "errors are logged"); err != nil {
		t.Fatalf("planner writing task acceptance: %v", err)
	}
	acc := acceptanceOf(t, st, "work")
	if acc == nil || len(acc.Items) != 2 || acc.Items[0].ID != "a1" || acc.Items[1].ID != "a2" || acc.Items[0].Text != "GET /x returns 200" {
		t.Fatalf("stored acceptance = %+v", acc)
	}
	if !strings.Contains(acc.By, "planner") {
		t.Fatalf("attribution = %q", acc.By)
	}
	// A note takes no acceptance, from anyone.
	if err := setAcceptance(t, st, planner, "lore", "x"); err == nil {
		t.Fatal("acceptance stored on a note")
	}
}

func TestPlannerRefusedWhenLocked(t *testing.T) {
	st := roleStore(t, standardWorkflow())
	planner := roleActor(engine.RolePlanner, identity.AgentOrchestrator, "g")
	if err := setAcceptance(t, st, planner, "work", "a"); err != nil {
		t.Fatal(err)
	}
	if err := st.LockAcceptance("work"); err != nil {
		t.Fatal(err)
	}
	wantRoleCode(t, setAcceptance(t, st, planner, "work", "weaker"), CodeAcceptanceLocked)
	if acc := acceptanceOf(t, st, "work"); acc == nil || acc.Items[0].Text != "a" || !acc.Locked {
		t.Fatalf("locked acceptance changed: %+v", acc)
	}
}

func TestQAWritesGoalAcceptance(t *testing.T) {
	st := roleStore(t, standardWorkflow())
	qa := roleActor(engine.RoleQA, identity.AgentOrchestrator, "g")
	if err := setAcceptance(t, st, qa, "g", "checkout works end to end"); err != nil {
		t.Fatalf("QA writing goal acceptance: %v", err)
	}
	if acc := acceptanceOf(t, st, "g"); acc == nil || len(acc.Items) != 1 {
		t.Fatalf("goal acceptance = %+v", acc)
	}
	// QA does not rewrite a task's criteria it did not create.
	wantRoleCode(t, setAcceptance(t, st, qa, "work", "x"), CodeRoleDenied)
}

func TestHumanEditsLockedAcceptance(t *testing.T) {
	st := roleStore(t, standardWorkflow())
	if err := setAcceptance(t, st, identity.Local, "work", "a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := st.LockAcceptance("work"); err != nil {
		t.Fatal(err)
	}
	if err := setAcceptance(t, st, identity.Local, "work", "a", "b", "c"); err != nil {
		t.Fatalf("a person editing locked criteria: %v", err)
	}
	acc := acceptanceOf(t, st, "work")
	if acc == nil || len(acc.Items) != 3 || !acc.Locked {
		t.Fatalf("after the person's edit: %+v", acc)
	}
}

func TestOnlySystemActorSetsLocked(t *testing.T) {
	st := roleStore(t, standardWorkflow())
	if err := setAcceptance(t, st, identity.Local, "work", "a"); err != nil {
		t.Fatal(err)
	}
	// A person's op that tries to set the lock does not lock.
	_, _, err := st.ApplyGraphOps(identity.Local, []GraphOp{{
		Kind: "acceptance", NodeID: "work", Acceptance: &engine.Acceptance{Items: []engine.AcceptanceItem{{ID: "a1", Text: "a"}}, Locked: true, By: "hub"},
	}})
	if err == nil {
		if acc := acceptanceOf(t, st, "work"); acc != nil && acc.Locked {
			t.Fatal("a person's op locked the acceptance")
		}
	}
	// A planner's whole-graph save cannot flip it either.
	wantRoleCode(t, saveWith(t, st, roleActor(engine.RolePlanner, identity.AgentOrchestrator, "g"), func(g *engine.Graph) {
		g.NodeByID("work").Acceptance.Locked = true
	}), CodeRoleDenied)
	// The hub can.
	if err := st.LockAcceptance("work"); err != nil {
		t.Fatalf("LockAcceptance: %v", err)
	}
	if acc := acceptanceOf(t, st, "work"); acc == nil || !acc.Locked {
		t.Fatalf("after LockAcceptance: %+v", acc)
	}
}

func TestExecutorCannotSettleWhileGatesPending(t *testing.T) {
	st := roleStore(t, standardWorkflow())
	exec := roleActor(engine.RoleExecutor, identity.AgentWorker, "work")
	if _, err := st.ClaimNode(exec, "work", "run-executor", 0, ""); err != nil {
		t.Fatalf("executor claiming its node: %v", err)
	}
	_, err := st.ReportStatus(exec, "work", engine.StatusDone, "run", "", "run-executor", "")
	wantRoleCode(t, err, CodeStageGatePending)
	if !strings.Contains(err.Error(), "server marks this done") {
		t.Fatalf("message = %q", err)
	}
	// Doing is fine: only settling waits for the gates.
	if _, err := st.ReportStatus(exec, "work", engine.StatusInProgress, "run", "", "run-executor", ""); err != nil {
		t.Fatalf("doing: %v", err)
	}
}

func TestExecutorMaySettleUnderLight(t *testing.T) {
	st := roleStore(t, lightWorkflow())
	if g, _, _ := st.LoadGraph(); g.Workflow == nil || g.Workflow.Preset != engine.PresetLight {
		t.Fatalf("project workflow not stored: %+v", g.Workflow)
	}
	exec := roleActor(engine.RoleExecutor, identity.AgentWorker, "work")
	if _, err := st.ReportStatus(exec, "work", engine.StatusDone, "run", "", "", ""); err != nil {
		t.Fatalf("executor finishing under Light: %v", err)
	}
}

func TestLegacyWorkflowAllowsExecutorDone(t *testing.T) {
	st := roleStore(t, nil)
	// A legacy worker (no role) and an executor-role run on a legacy project.
	if _, err := st.ReportStatus(agentActor(identity.AgentWorker), "work", engine.StatusDone, "run", "", "", ""); err != nil {
		t.Fatalf("legacy worker: %v", err)
	}
	if _, err := st.ReportStatus(roleActor(engine.RoleExecutor, identity.AgentWorker, "other"), "other", engine.StatusDone, "run", "", "", ""); err != nil {
		t.Fatalf("executor on a legacy project: %v", err)
	}
}

func TestExecutorWritesOnlyItsRunNode(t *testing.T) {
	st := roleStore(t, lightWorkflow())
	exec := roleActor(engine.RoleExecutor, identity.AgentWorker, "work")
	_, err := st.ClaimNode(exec, "other", "run-executor", 0, "")
	wantRoleCode(t, err, CodeRoleDenied)
	_, err = st.ReportStatus(exec, "other", engine.StatusInProgress, "run", "", "", "")
	wantRoleCode(t, err, CodeRoleDenied)
	// The executor of a scene writes that scene's body, and no other.
	scene := roleActor(engine.RoleExecutor, identity.AgentWorker, "s1")
	_, rev, _ := st.LoadNodeContent("s1")
	if _, _, err := st.SaveNodeContent(scene, "s1", "# Scene\n\nLin waits.\n", rev); err != nil {
		t.Fatalf("scene executor writing its scene: %v", err)
	}
	_, rev, _ = st.LoadNodeContent("work")
	_, _, err = st.SaveNodeContent(scene, "work", "# hijack\n", rev)
	wantRoleCode(t, err, CodeRoleDenied)
}

func TestReviewerCannotWriteBodyOrStatusOrClaim(t *testing.T) {
	st := roleStore(t, standardWorkflow())
	reviewer := roleActor(engine.RoleReviewer, identity.AgentWorker, "work")
	_, err := st.ClaimNode(reviewer, "work", "run-reviewer", 0, "")
	wantRoleCode(t, err, CodeRoleDenied)
	_, err = st.ReportStatus(reviewer, "work", engine.StatusDone, "run", "", "", "")
	wantRoleCode(t, err, CodeRoleDenied)
	_, rev, _ := st.LoadNodeContent("work")
	_, _, err = st.SaveNodeContent(reviewer, "work", "# approved by me\n", rev)
	wantRoleCode(t, err, CodeRoleDenied)
	_, _, err = st.ApplyGraphOps(reviewer, []GraphOp{{Kind: "node-metadata", NodeID: "work", Title: stringPtr("LGTM")}})
	wantRoleCode(t, err, CodeRoleDenied)
}

func TestResearcherWritesOnlyNotes(t *testing.T) {
	st := roleStore(t, standardWorkflow())
	researcher := roleActor(engine.RoleResearcher, identity.AgentWorker, "")
	if _, err := st.CreateNode(researcher, &engine.Node{Title: "Lin's backstory", Kind: "note"}, ""); err != nil {
		t.Fatalf("researcher creating a note: %v", err)
	}
	_, err := st.CreateNode(researcher, &engine.Node{Title: "Sneaky task", Kind: "task"}, "")
	wantRoleCode(t, err, CodeRoleDenied)
	_, rev, _ := st.LoadNodeContent("lore")
	if _, _, err := st.SaveNodeContent(researcher, "lore", "# Lore\n\nupdated\n", rev); err != nil {
		t.Fatalf("researcher updating a note: %v", err)
	}
	_, rev, _ = st.LoadNodeContent("s1")
	_, _, err = st.SaveNodeContent(researcher, "s1", "# rewritten prose\n", rev)
	wantRoleCode(t, err, CodeRoleDenied)
	_, err = st.ClaimNode(researcher, "work", "r", 0, "")
	wantRoleCode(t, err, CodeRoleDenied)
}

func TestResearcherLinksRefsNotRequires(t *testing.T) {
	st := roleStore(t, standardWorkflow())
	researcher := roleActor(engine.RoleResearcher, identity.AgentWorker, "")
	if _, _, err := st.ApplyGraphOps(researcher, []GraphOp{{Kind: "add-ref", NodeID: "s1", Ref: "lore"}}); err != nil {
		t.Fatalf("researcher adding a reference: %v", err)
	}
	_, _, err := st.ApplyGraphOps(researcher, []GraphOp{{Kind: "add-edge", From: "work", To: "other"}})
	wantRoleCode(t, err, CodeRoleDenied)
}

func TestQACreatesOnlyUnderItsGoal(t *testing.T) {
	st := roleStore(t, standardWorkflow())
	qa := roleActor(engine.RoleQA, identity.AgentOrchestrator, "g")
	result, err := st.CreateSubgraph(qa, SubgraphSpec{Nodes: []SubgraphNode{{
		Key: "fix1", Title: "Fix the checkout total", Kind: "task", Tags: []string{"goal:g", "qa-fix"},
		Acceptance: []string{"total includes tax"},
	}}})
	if err != nil {
		t.Fatalf("QA creating a fix task under its goal: %v", err)
	}
	if id := result.IDs["fix1"]; id == "" || acceptanceOf(t, st, id) == nil {
		t.Fatalf("fix task acceptance not stored: %+v", result)
	}
	_, err = st.CreateSubgraph(qa, SubgraphSpec{Nodes: []SubgraphNode{{Key: "x", Title: "Elsewhere", Kind: "task"}}})
	wantRoleCode(t, err, CodeRoleDenied)
	_, err = st.CreateSubgraph(qa, SubgraphSpec{Nodes: []SubgraphNode{{Key: "x", Title: "Other goal", Kind: "task", Tags: []string{"goal:h"}}}})
	wantRoleCode(t, err, CodeRoleDenied)
}

func TestRoledAgentCannotDeleteOrRestore(t *testing.T) {
	st := roleStore(t, standardWorkflow())
	planner := roleActor(engine.RolePlanner, identity.AgentOrchestrator, "g")
	_, err := st.DeleteNodes(planner, []string{"work"})
	wantRoleCode(t, err, CodeRoleDenied)
	// A legacy orchestrator still can (unchanged).
	if _, err := st.DeleteNodes(agentActor(identity.AgentOrchestrator), []string{"other"}); err != nil {
		t.Fatalf("legacy delete: %v", err)
	}
	// Restoring a node file is refused for a roled agent.
	_, rev, _ := st.LoadNodeContent("work")
	if _, _, err := st.SaveNodeContent(identity.Local, "work", "# v2\n", rev); err != nil {
		t.Fatal(err)
	}
	versions, err := st.ListHistory("nodes/work.md")
	if err != nil || len(versions) == 0 {
		t.Skipf("no history to restore: %v", err)
	}
	err = st.RestoreHistory(roleActor(engine.RoleExecutor, identity.AgentWorker, "work"), "nodes/work.md", versions[0].Name)
	var denied *ErrAgentRestoreDenied
	if !errors.As(err, &denied) {
		t.Fatalf("roled restore: err = %v, want ErrAgentRestoreDenied", err)
	}
}

func TestRoleNeverRaisesClass(t *testing.T) {
	st := roleStore(t, standardWorkflow())
	// Declared orchestrator, role executor: the role's worker rank wins.
	lowered := roleActor(engine.RoleExecutor, identity.AgentOrchestrator, "boss")
	_, err := st.ClaimNode(lowered, "boss", "run-executor", 0, "")
	var writeDenied *ErrNodeWriteDenied
	if !errors.As(err, &writeDenied) {
		t.Fatalf("an executor-role orchestrator wrote an orchestrator node: err = %v", err)
	}
	// Declared worker, role planner: still a worker.
	raised := roleActor(engine.RolePlanner, identity.AgentWorker, "g")
	_, _, err = st.ApplyGraphOps(raised, []GraphOp{{Kind: "add-ref", NodeID: "boss", Ref: "lore"}})
	if !errors.As(err, &writeDenied) {
		t.Fatalf("a planner-role worker wrote an orchestrator node: err = %v", err)
	}
}

func TestCreateSubgraphCarriesAcceptance(t *testing.T) {
	st := roleStore(t, standardWorkflow())
	planner := roleActor(engine.RolePlanner, identity.AgentOrchestrator, "g")
	result, err := st.CreateSubgraph(planner, SubgraphSpec{Nodes: []SubgraphNode{
		{Key: "api", Title: "API", Kind: "task", Tags: []string{"goal:g"}, Acceptance: []string{"GET /x returns 200", "unknown ids 404"}},
		{Key: "ui", Title: "UI", Kind: "task", Tags: []string{"goal:g"}, DependsOn: []string{"api"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	acc := acceptanceOf(t, st, result.IDs["api"])
	if acc == nil || len(acc.Items) != 2 || acc.Items[1].ID != "a2" || acc.Items[1].Text != "unknown ids 404" || acc.Locked {
		t.Fatalf("api acceptance = %+v", acc)
	}
	if acceptanceOf(t, st, result.IDs["ui"]) != nil {
		t.Fatal("ui got acceptance it was not given")
	}
	// A legacy agent's create_subgraph cannot carry acceptance.
	_, err = st.CreateSubgraph(agentActor(identity.AgentOrchestrator), SubgraphSpec{Nodes: []SubgraphNode{
		{Key: "z", Title: "Z", Kind: "task", Acceptance: []string{"x"}},
	}})
	wantRoleCode(t, err, CodeRoleDenied)
}

func TestEvidenceJournalEventIgnoredByStatus(t *testing.T) {
	st := roleStore(t, standardWorkflow())
	if _, err := st.SetStatus("work", engine.StatusInProgress, "run", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendEvidence("work", "verify_red", "exit 1 in 4.2s at abc123"); err != nil {
		t.Fatalf("AppendEvidence: %v", err)
	}
	rs, err := st.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if rs.Nodes["work"] == nil || rs.Nodes["work"].Status != engine.StatusInProgress {
		t.Fatalf("evidence changed the status: %+v", rs.Nodes["work"])
	}
	found := false
	for _, event := range rs.History {
		if event.Event == "evidence" && event.Node == "work" && strings.Contains(event.Note, "exit 1") {
			found = true
		}
	}
	if !found {
		t.Fatalf("evidence not in the history: %+v", rs.History)
	}
	if err := st.AppendEvidence("missing", "verify_red", "x"); err == nil {
		t.Fatal("evidence recorded on a node that does not exist")
	}
}

func TestCreatedByRunRecordsTheRun(t *testing.T) {
	st := roleStore(t, standardWorkflow())
	qa := roleActor(engine.RoleQA, identity.AgentOrchestrator, "g")
	result, err := st.CreateSubgraph(qa, SubgraphSpec{Nodes: []SubgraphNode{{Key: "f", Title: "Fix", Kind: "task", Tags: []string{"goal:g", "qa-fix"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := st.CreatedByRun(result.IDs["f"]); got != qa.RunID {
		t.Fatalf("CreatedByRun = %q, want %q", got, qa.RunID)
	}
	if got := st.CreatedByRun("work"); got != "" {
		t.Fatalf("a person's node CreatedByRun = %q", got)
	}
}
