package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
)

// The staged pipeline end to end over a fake runner's socket: the project
// runs Standard stages and the node has acceptance criteria.

func stagedHello() runnerwire.Envelope {
	hello := runsHello(4)
	hello.Hello.Capabilities = []string{runnerwire.CapHeadless, runnerwire.CapPlan, runnerwire.CapRoles, runnerwire.CapVerify, runnerwire.CapJudge}
	hello.Hello.Agents = append(hello.Hello.Agents[:1], runnerwire.Agent{Kind: "codex", Installed: true, Models: []runnerwire.Model{{ID: "gpt-5", Default: true}}})
	hello.Hello.Repos[0].Verify = true
	return hello
}

// stagedProject puts the test node under Standard stages with criteria.
func (f *runsFixture) stagedProject(t *testing.T) {
	t.Helper()
	st := f.server.pm.Store()
	g, rev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	g.Workflow = &engine.Workflow{Preset: engine.PresetStandard, Stages: engine.Stages{Planner: true, Tester: true, Reviewer: true}}
	g.NodeByID(runsTestNode).Acceptance = &engine.Acceptance{Items: []engine.AcceptanceItem{
		{ID: "a1", Text: "the login button works on Safari"}, {ID: "a2", Text: "an error is shown when the request fails"},
	}}
	if _, err := st.SaveGraph(identity.Local, g, rev); err != nil {
		t.Fatal(err)
	}
}

func (f *runsFixture) stagedDispatch(t *testing.T) {
	t.Helper()
	body, _ := json.Marshal(runnerwire.CreateRunRequest{
		NodeID: runsTestNode, RunnerID: f.runner.Runner.ID, Agent: "claude", RepoID: "repo-1", Base: "main",
		Pipeline: &runnerwire.Pipeline{Tester: runnerwire.AgentModel{Agent: "claude"}, Implement: runnerwire.AgentModel{Agent: "claude"},
			Reviewers: []runnerwire.AgentModel{{Agent: "codex"}}, MaxRounds: 2},
	})
	f.dispatch(t, f.bea, string(body))
}

// offer reads the next offer and checks its kind and role.
func stagedOffer(t *testing.T, conn *websocket.Conn, kind, role string) runnerwire.Job {
	t.Helper()
	env := readEnvelopeForTest(t, conn)
	if env.Type != runnerwire.TypeOffer || env.Job == nil {
		t.Fatalf("got %+v, want an offer", env)
	}
	if env.Job.Kind != kind || env.Job.Role != role {
		t.Fatalf("offer = kind %q role %q, want %q/%q", env.Job.Kind, env.Job.Role, kind, role)
	}
	return *env.Job
}

func (f *runsFixture) runJob(t *testing.T, conn *websocket.Conn, job runnerwire.Job, changes *runnerwire.RunChanges, head string, result runnerwire.RunResult) {
	t.Helper()
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeAccept, Accept: &runnerwire.Accept{RunID: job.RunID, Base: "main", Branch: "agent/" + runsTestNode}})
	f.waitStatus(t, job.RunID, runnerwire.RunRunning)
	if changes != nil {
		sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeChanges, RunID: job.RunID, Changes: changes})
	}
	if head != "" {
		sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypePR, RunID: job.RunID, PR: &runnerwire.PRInfo{
			URL: "https://github.com/bea/visual-editor/pull/7", Number: 7, State: runnerwire.PROpen, HeadSHA: head}})
	}
	if result.Status == "" {
		result.Status = runnerwire.RunSucceeded
	}
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: job.RunID, Result: &result})
	f.waitStatus(t, job.RunID, result.Status)
}

func filesChange(files ...runnerwire.FileChange) *runnerwire.RunChanges {
	adds := 0
	for _, file := range files {
		adds += file.Additions
	}
	return &runnerwire.RunChanges{Commit: testCommit, Base: "main", Files: files, Additions: adds}
}

var (
	stagedHead1 = strings.Repeat("a", 40)
	stagedHead2 = strings.Repeat("b", 40)
)

func TestStagedFlowEndToEnd(t *testing.T) {
	f := newRunsFixture(t)
	f.stagedProject(t)
	conn := f.connect(t, stagedHello())
	f.stagedDispatch(t)

	tester := stagedOffer(t, conn, runnerwire.JobTest, engine.RoleTester)
	if len(tester.Acceptance) != 2 || len(tester.Tools) == 0 {
		t.Fatalf("tester job = %+v", tester)
	}
	f.runJob(t, conn, tester, filesChange(runnerwire.FileChange{Path: "web/login.test.ts", Status: runnerwire.FileAdded, Additions: 12}), stagedHead1, runnerwire.RunResult{})

	red := stagedOffer(t, conn, runnerwire.JobVerify, "")
	if red.Verify == nil || red.Verify.Phase != runnerwire.VerifyRed || red.Verify.Head != stagedHead1 {
		t.Fatalf("red verify = %+v", red.Verify)
	}
	f.runJob(t, conn, red, nil, "", runnerwire.RunResult{Verify: &runnerwire.VerifyResult{Phase: runnerwire.VerifyRed, Head: stagedHead1, ExitCode: 1, Passed: true, DurationMs: 4200}})

	exec := stagedOffer(t, conn, runnerwire.JobRevise, engine.RoleExecutor)
	if len(exec.TestFiles) != 1 || exec.TestFiles[0] != "web/login.test.ts" {
		t.Fatalf("executor test files = %v", exec.TestFiles)
	}
	f.runJob(t, conn, exec, filesChange(runnerwire.FileChange{Path: "web/login.ts", Status: runnerwire.FileModified, Additions: 30}), stagedHead2, runnerwire.RunResult{})

	green := stagedOffer(t, conn, runnerwire.JobVerify, "")
	if green.Verify == nil || green.Verify.Phase != runnerwire.VerifyGreen || green.Verify.Head != stagedHead2 {
		t.Fatalf("green verify = %+v", green.Verify)
	}
	f.runJob(t, conn, green, nil, "", runnerwire.RunResult{Verify: &runnerwire.VerifyResult{Phase: runnerwire.VerifyGreen, Head: stagedHead2, ExitCode: 0, Passed: true}})

	review := stagedOffer(t, conn, runnerwire.JobReview, engine.RoleReviewer)
	if review.Agent != "codex" {
		t.Fatalf("reviewer = %s", review.Agent)
	}
	f.runJob(t, conn, review, nil, "", runnerwire.RunResult{Verdict: runnerwire.VerdictApprove, Summary: "Tests cover a1 and a2.\nVERDICT: approve"})

	// The server marks the node done, with the evidence on its history.
	deadline := time.Now().Add(5 * time.Second)
	for {
		rs, err := f.server.pm.Store().LoadState()
		if err != nil {
			t.Fatal(err)
		}
		if rs.Nodes[runsTestNode] != nil && rs.Nodes[runsTestNode].Status == engine.StatusDone {
			kinds := map[string]bool{}
			for _, event := range rs.History {
				if event.Event == "evidence" && event.Node == runsTestNode {
					kinds[strings.SplitN(event.Note, ":", 2)[0]] = true
				}
			}
			if len(kinds) < 2 {
				t.Fatalf("evidence on the node = %+v", rs.History)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("node not done: %+v", rs.Nodes[runsTestNode])
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestStagedFlowRejectsPassingTests(t *testing.T) {
	f := newRunsFixture(t)
	f.stagedProject(t)
	conn := f.connect(t, stagedHello())
	f.stagedDispatch(t)

	tester := stagedOffer(t, conn, runnerwire.JobTest, engine.RoleTester)
	f.runJob(t, conn, tester, filesChange(runnerwire.FileChange{Path: "web/login.test.ts", Status: runnerwire.FileAdded, Additions: 3}), stagedHead1, runnerwire.RunResult{})
	red := stagedOffer(t, conn, runnerwire.JobVerify, "")
	f.runJob(t, conn, red, nil, "", runnerwire.RunResult{Verify: &runnerwire.VerifyResult{Phase: runnerwire.VerifyRed, Head: stagedHead1, ExitCode: 0, Passed: false}})

	again := stagedOffer(t, conn, runnerwire.JobTest, engine.RoleTester)
	if !strings.Contains(again.Review, "already pass") {
		t.Fatalf("tester retry review = %q", again.Review)
	}
	// The run history shows the rejected red run.
	response := f.do(t, &f.bea, http.MethodGet, "/api/runs/"+red.RunID, "", nil)
	var run runnerwire.Run
	_ = json.Unmarshal(response.Body.Bytes(), &run)
	if run.Verify == nil || run.Verify.Passed || run.Verify.ExitCode != 0 {
		t.Fatalf("red run evidence = %+v", run.Verify)
	}
}
