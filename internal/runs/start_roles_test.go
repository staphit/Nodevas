package runs

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
)

var allRoleCaps = []string{runnerwire.CapHeadless, runnerwire.CapPlan, runnerwire.CapRoles, runnerwire.CapVerify, runnerwire.CapJudge}

// newRoleFixture is the step-up fixture's dispatcher with a runner that has
// the given capabilities, Claude and Codex, and one repo whose test command
// is configured (verify) or not.
func newRoleFixture(t *testing.T, verify bool, caps ...string) *stepUpFixture {
	t.Helper()
	f := newStepUpFixture(t)
	f.d.SetStepUp(false, 0)
	if len(caps) == 0 {
		caps = allRoleCaps
	}
	if err := f.d.reg.SetHello(context.Background(), f.runner.ID, runnerwire.Info{
		Capabilities: caps, MaxConcurrent: 8,
		Agents: []runnerwire.Agent{
			{Kind: "claude", Installed: true, Models: []runnerwire.Model{{ID: "opus", Default: true}, {ID: "sonnet"}}},
			{Kind: "codex", Installed: true, Models: []runnerwire.Model{{ID: "gpt-5", Default: true}}},
		},
		Repos: []runnerwire.Repo{{ID: "repo", Name: "repo", Verify: verify}},
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func standardStages() *engine.Workflow {
	return &engine.Workflow{Preset: engine.PresetStandard, Stages: engine.Stages{Planner: true, Tester: true, Reviewer: true},
		MaxTesterRounds: 2, MaxGreenTries: 3}
}

func lightStages() *engine.Workflow {
	return &engine.Workflow{Preset: engine.PresetLight, Stages: engine.Stages{Planner: true}}
}

func criteria(texts ...string) *engine.Acceptance {
	acc := &engine.Acceptance{}
	for i, text := range texts {
		acc.Items = append(acc.Items, engine.AcceptanceItem{ID: "a" + string(rune('1'+i)), Text: text})
	}
	return acc
}

// roleStart dispatches n1 as ann with the node's workflow and acceptance.
func (f *stepUpFixture) roleStart(t *testing.T, wf *engine.Workflow, acc *engine.Acceptance, edit func(*StartRequest)) (Record, error) {
	t.Helper()
	req := StartRequest{
		Body:    runnerwire.CreateRunRequest{NodeID: "n1", RunnerID: f.runner.ID, Agent: "claude", RepoID: "repo", Base: "main"},
		Project: ".", ActorID: "u-ann", ActorName: "ann",
		Roles: engine.BuiltinAgentRoles(),
		LoadNode: func() (NodeInfo, bool, error) {
			return NodeInfo{Title: "Title", Content: "Do the thing.", Type: "task", AgentMode: engine.AgentDispatch,
				Workflow: wf, Acceptance: acc}, true, nil
		},
	}
	if edit != nil {
		edit(&req)
	}
	return f.d.Start(context.Background(), req)
}

func wantStartRefusal(t *testing.T, err error, code, contains string) *StartError {
	t.Helper()
	var se *StartError
	if !errors.As(err, &se) || se.Code != code || !strings.Contains(se.Msg, contains) {
		t.Fatalf("err = %v, want %s containing %q", err, code, contains)
	}
	return se
}

func TestStartRecordsRole(t *testing.T) {
	f := newRoleFixture(t, true)
	r, err := f.roleStart(t, lightStages(), criteria("it works"), func(req *StartRequest) { req.Body.Role = engine.RoleExecutor })
	if err != nil {
		t.Fatal(err)
	}
	if r.Role != engine.RoleExecutor {
		t.Fatalf("run role = %q", r.Role)
	}
	got, _ := f.s.Get(context.Background(), r.ID)
	if got.Role != engine.RoleExecutor {
		t.Fatalf("stored role = %q", got.Role)
	}
	job := f.conn.last(t, r.ID)
	if job.Role != engine.RoleExecutor || !slices.Contains(job.Tools, "claim_task") || slices.Contains(job.Tools, "create_subgraph") {
		t.Fatalf("offer = role %q tools %v", job.Role, job.Tools)
	}
	if len(job.Acceptance) != 1 || job.Acceptance[0].Text != "it works" {
		t.Fatalf("offer acceptance = %+v", job.Acceptance)
	}
	// An unknown role is refused.
	_, err = f.roleStart(t, lightStages(), criteria("x"), func(req *StartRequest) { req.Body.Role = "boss" })
	wantStartRefusal(t, err, StartInvalid, "role")
}

func TestStartRefusesExecutorWithoutAcceptance(t *testing.T) {
	f := newRoleFixture(t, true)
	for _, role := range []string{engine.RoleExecutor, engine.RoleTester} {
		_, err := f.roleStart(t, standardStages(), nil, func(req *StartRequest) { req.Body.Role = role })
		se := wantStartRefusal(t, err, StartPrecondition, "acceptance")
		if se.Reason != "ACCEPTANCE_REQUIRED" {
			t.Fatalf("%s: reason = %q", role, se.Reason)
		}
	}
	// A staged dispatch (no role, no override) is refused the same way.
	_, err := f.roleStart(t, standardStages(), nil, nil)
	if se := wantStartRefusal(t, err, StartPrecondition, "acceptance"); se.Reason != "ACCEPTANCE_REQUIRED" {
		t.Fatalf("staged: reason = %q", se.Reason)
	}
	// Light needs none.
	if _, err := f.roleStart(t, lightStages(), nil, func(req *StartRequest) { req.Body.Role = engine.RoleExecutor }); err != nil {
		t.Fatalf("light without acceptance: %v", err)
	}
}

func TestStartRefusesStagedOnRunnerWithoutVerify(t *testing.T) {
	f := newRoleFixture(t, false)
	_, err := f.roleStart(t, standardStages(), criteria("x"), nil)
	wantStartRefusal(t, err, StartPrecondition, "nodevas runner repo set")
}

func TestStartRefusesStagedOnOldRunner(t *testing.T) {
	f := newRoleFixture(t, true, runnerwire.CapHeadless, runnerwire.CapPlan)
	_, err := f.roleStart(t, standardStages(), criteria("x"), nil)
	wantStartRefusal(t, err, StartPrecondition, "update nodevas")
	// Light and legacy still dispatch on it.
	if _, err := f.roleStart(t, lightStages(), criteria("x"), nil); err != nil {
		t.Fatalf("light on an old runner: %v", err)
	}
	if _, err := f.roleStart(t, nil, nil, nil); err != nil {
		t.Fatalf("legacy on an old runner: %v", err)
	}
}

func TestHumanOverrideRecorded(t *testing.T) {
	f := newRoleFixture(t, true)
	r, err := f.roleStart(t, standardStages(), criteria("x"), func(req *StartRequest) {
		req.Body.Override = "prod is down, hotfix now"
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != runnerwire.JobImplement || r.PipelineID != "" || r.Override != "prod is down, hotfix now" {
		t.Fatalf("override run = kind %q pipeline %q override %q", r.Kind, r.PipelineID, r.Override)
	}
	events, _, _ := f.s.Events(context.Background(), r.ID, 0, 50)
	noted := false
	for _, e := range events {
		if strings.Contains(e.Text, "override") && strings.Contains(e.Text, "hotfix") {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("override not noted on the run: %+v", events)
	}
	_, err = f.roleStart(t, standardStages(), criteria("x"), func(req *StartRequest) { req.Body.Override = strings.Repeat("x", 201) })
	wantStartRefusal(t, err, StartInvalid, "override")
	// Without stages there is nothing to override.
	_, err = f.roleStart(t, nil, nil, func(req *StartRequest) { req.Body.Override = "because" })
	wantStartRefusal(t, err, StartInvalid, "override")
}

func TestExecutorDispatchLocksAcceptance(t *testing.T) {
	f := newRoleFixture(t, true)
	locks := 0
	lock := func(req *StartRequest) {
		req.LockAcceptance = func() error { locks++; return nil }
	}
	if _, err := f.roleStart(t, lightStages(), criteria("x"), func(req *StartRequest) { lock(req); req.Body.Role = engine.RoleExecutor }); err != nil {
		t.Fatal(err)
	}
	if locks != 1 {
		t.Fatalf("executor dispatch locked %d times", locks)
	}
	// A planner run does not lock anything.
	if _, err := f.roleStart(t, lightStages(), criteria("x"), func(req *StartRequest) {
		lock(req)
		req.Body.Role = engine.RolePlanner
		req.Body.Kind = runnerwire.JobPlan
	}); err != nil {
		t.Fatal(err)
	}
	if locks != 1 {
		t.Fatalf("planner dispatch locked: %d", locks)
	}
	// A lock that fails stops the dispatch.
	_, err := f.roleStart(t, lightStages(), criteria("x"), func(req *StartRequest) {
		req.Body.Role = engine.RoleExecutor
		req.LockAcceptance = func() error { return errors.New("disk full") }
	})
	if err == nil {
		t.Fatal("dispatch went ahead without the lock")
	}
}
