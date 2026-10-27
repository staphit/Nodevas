package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"nodevas/internal/auth"
	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// runningRoleRun records a run of bea's runner on the test node with kind,
// role and pipeline, and starts it.
func (f *runsFixture) runningRoleRun(t *testing.T, kind, role, pipelineID string) runs.Record {
	t.Helper()
	ctx := context.Background()
	st := f.server.runs.Store()
	rec, err := st.Create(ctx, runs.New{
		Project: ".", NodeID: runsTestNode, NodeTitle: "Fix the login bug",
		RunnerID: f.runner.Runner.ID, RunnerName: "bea-pc", OwnerID: f.runner.Runner.OwnerID,
		Agent: "codex", Model: "gpt-5.5", RepoID: "repo-1", RepoName: "visual-editor",
		JobPrompt: "# Fix", CreatedBy: f.runner.Runner.OwnerID, CreatedByName: "bea",
		Kind: kind, Role: role, PipelineID: pipelineID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Transition(ctx, rec.ID, runnerwire.RunOffered, runs.Change{}); err != nil {
		t.Fatal(err)
	}
	rec, err = st.Transition(ctx, rec.ID, runnerwire.RunRunning, runs.Change{Runner: f.runner.Runner.ID})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func (f *runsFixture) reviewPipeline(t *testing.T) runs.PipelineRecord {
	t.Helper()
	am := runnerwire.AgentModel{Agent: "codex", Model: "gpt-5.5"}
	p, err := f.server.runs.Store().CreatePipeline(context.Background(), runs.NewPipeline{
		OwnerID: f.runner.Runner.OwnerID, CreatedBy: f.runner.Runner.OwnerID, Project: ".", NodeID: runsTestNode,
		Pipeline: runnerwire.Pipeline{Implement: am, Review: am, MaxRounds: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type verdictAnswer struct {
	Round    int  `json:"round"`
	Replaced bool `json:"replaced"`
}

func (f *runsFixture) postVerdict(t *testing.T, runID, body string, extra map[string]string) (int, string) {
	t.Helper()
	return f.proxied(t, runID, http.MethodPost, "verdicts", body, extra)
}

func (f *runsFixture) verdictsOf(t *testing.T, b browser, runID string) ([]runs.Verdict, int) {
	t.Helper()
	response := f.do(t, &b, http.MethodGet, "/api/runs/"+runID+"/verdicts", "", nil)
	var out struct {
		Verdicts []runs.Verdict `json:"verdicts"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &out)
	return out.Verdicts, response.Code
}

const failBody = `{"outcome":"fail","severity":"medium","reasons":["tests_weak"],"note":"no 404 test"}`

// B-22: the verdict route is open to runs, as the reviewer and QA's
// capability.
func TestVerdictRouteThroughTheProxy(t *testing.T) {
	if !runnerProxyAllows(http.MethodPost, "/api/verdicts") {
		t.Fatal("the runner proxy refuses POST /api/verdicts")
	}
	if capability, ok := roleRouteCapability(http.MethodPost, "/api/verdicts"); !ok || capability != "verdict" {
		t.Fatalf("POST /api/verdicts capability = %q (%v), want verdict", capability, ok)
	}
	if runnerProxyAllows(http.MethodGet, "/api/runs/x/verdicts") {
		t.Fatal("the runner proxy lets an agent read verdicts")
	}
}

// B-16: a proxied reviewer run records its verdict at its pipeline's round,
// against the implement run it reviews.
func TestReviewerRunPostsVerdict(t *testing.T) {
	f := newRunsFixture(t)
	p := f.reviewPipeline(t)
	impl := f.runningRoleRun(t, runnerwire.JobImplement, engine.RoleExecutor, p.ID)
	review := f.runningRoleRun(t, runnerwire.JobReview, engine.RoleReviewer, p.ID)
	code, body := f.postVerdict(t, review.ID, failBody, nil)
	var answer verdictAnswer
	_ = json.Unmarshal([]byte(body), &answer)
	if code != http.StatusOK || answer.Round != p.Round || answer.Replaced {
		t.Fatalf("reviewer verdict: %d %s", code, body)
	}
	code, body = f.postVerdict(t, review.ID, failBody, nil)
	if code != http.StatusOK || !strings.Contains(body, `"replaced":true`) {
		t.Fatalf("second verdict: %d %s", code, body)
	}
	list, code := f.verdictsOf(t, f.bea, review.ID)
	if code != http.StatusOK || len(list) != 1 {
		t.Fatalf("GET verdicts = %d %+v", code, list)
	}
	v := list[0]
	if v.Role != engine.RoleReviewer || v.SubjectRunID != impl.ID || v.Outcome != "fail" || v.Severity != "medium" ||
		len(v.Reasons) != 1 || v.Reasons[0] != "tests_weak" || v.Note != "no 404 test" || v.Source != "mcp" || v.Agent != "codex" {
		t.Fatalf("verdict = %+v", v)
	}
}

// B-17 and B-18: only a reviewer or QA run, by its record, may post one.
func TestVerdictRefusals(t *testing.T) {
	f := newRunsFixture(t)
	exec := f.runningRoleRun(t, runnerwire.JobImplement, engine.RoleExecutor, "")
	code, body := f.postVerdict(t, exec.ID, failBody, nil)
	if code != http.StatusForbidden || !strings.Contains(body, "ROLE_DENIED") {
		t.Fatalf("executor run: %d %s", code, body)
	}
	// What the request claims about its role changes nothing.
	code, body = f.postVerdict(t, exec.ID, failBody, map[string]string{auth.RoleHeaderName: "reviewer"})
	if code != http.StatusForbidden || !strings.Contains(body, "ROLE_DENIED") {
		t.Fatalf("executor run claiming reviewer: %d %s", code, body)
	}
	// A legacy review run (no role) is a reviewer by its kind.
	legacy := f.runningRoleRun(t, runnerwire.JobReview, "", "")
	if code, body := f.postVerdict(t, legacy.ID, failBody, nil); code != http.StatusOK {
		t.Fatalf("legacy review run: %d %s", code, body)
	}
	// A person is not a run.
	response := f.do(t, &f.bea, http.MethodPost, "/api/verdicts", failBody, nil)
	if response.Code != http.StatusForbidden || errorCode(t, response) != "VERDICT_NOT_A_RUN" {
		t.Fatalf("person: %d %s", response.Code, response.Body)
	}
	// A queued run is not running yet.
	queued := f.roleRun(t, engine.RoleReviewer)
	if code, body := f.postVerdict(t, queued.ID, failBody, nil); code != http.StatusConflict || !strings.Contains(body, "RUN_NOT_RUNNING") {
		t.Fatalf("queued run: %d %s", code, body)
	}
	// An ended run can no longer act.
	review := f.runningRoleRun(t, runnerwire.JobReview, engine.RoleReviewer, "")
	if _, err := f.server.runs.Store().Finish(context.Background(), review.ID, f.runner.Runner.ID, runnerwire.RunResult{Status: runnerwire.RunSucceeded}); err != nil {
		t.Fatal(err)
	}
	if code, body := f.postVerdict(t, review.ID, failBody, nil); code != http.StatusUnauthorized || !strings.Contains(body, "RUN_ENDED") {
		t.Fatalf("ended run: %d %s", code, body)
	}
}

// B-19: the body is held to the fixed reasons and the outcome's rules.
func TestVerdictValidation(t *testing.T) {
	f := newRunsFixture(t)
	review := f.runningRoleRun(t, runnerwire.JobReview, engine.RoleReviewer, "")
	for name, body := range map[string]string{
		"unknown reason": `{"outcome":"fail","severity":"medium","reasons":["vibes"]}`,
		"pass high":      `{"outcome":"pass","severity":"high","reasons":[]}`,
		"fail no reason": `{"outcome":"fail","severity":"medium","reasons":[]}`,
		"duplicate":      `{"outcome":"fail","severity":"low","reasons":["style","style"]}`,
		"long note":      `{"outcome":"fail","severity":"low","reasons":["other"],"note":"` + strings.Repeat("n", 1001) + `"}`,
	} {
		if code, answer := f.postVerdict(t, review.ID, body, nil); code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", name, code, answer)
		}
	}
	if code, body := f.postVerdict(t, review.ID, `{"outcome":"pass","severity":"low","reasons":["style"]}`, nil); code != http.StatusOK {
		t.Fatalf("a valid pass: %d %s", code, body)
	}
}

// B-20: QA verdicts on one node count rounds; reading them needs canSee.
func TestQAVerdictRounds(t *testing.T) {
	f := newRunsFixture(t)
	var last runs.Record
	for want := 1; want <= 3; want++ {
		last = f.runningRoleRun(t, runnerwire.JobJudge, engine.RoleQA, "")
		code, body := f.postVerdict(t, last.ID, `{"outcome":"fail","severity":"high","reasons":["criteria_unmet"]}`, nil)
		var answer verdictAnswer
		_ = json.Unmarshal([]byte(body), &answer)
		if code != http.StatusOK || answer.Round != want {
			t.Fatalf("qa verdict %d: %d %s", want, code, body)
		}
	}
	list, code := f.verdictsOf(t, f.bea, last.ID)
	if code != http.StatusOK || len(list) != 1 || list[0].Role != engine.RoleQA || list[0].Round != 3 || list[0].SubjectRunID != "" {
		t.Fatalf("bea reading = %d %+v", code, list)
	}
	if _, code := f.verdictsOf(t, f.cal, last.ID); code == http.StatusOK {
		t.Fatal("cal, who neither made the run nor owns its runner, read its verdicts")
	}
}
