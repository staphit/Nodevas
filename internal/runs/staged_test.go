package runs

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
)

// The staged task pipeline (spec 3.2): test → verify_red → implement →
// verify_green → guard → ci → review → done, each stage optional per the
// frozen workflow. These drive the dispatcher the way a runner would.

// PR heads, as the runner reports them.
var (
	head1 = strings.Repeat("1", 40)
	head2 = strings.Repeat("2", 40)
	head3 = strings.Repeat("3", 40)
)

func stagedPipeline() *runnerwire.Pipeline {
	return &runnerwire.Pipeline{
		Tester:    am("claude", "sonnet"),
		Implement: am("claude", "opus"),
		Reviewers: []runnerwire.AgentModel{am("codex", "gpt-5")},
		MaxRounds: 2,
	}
}

// stagedStart dispatches n1 through the staged pipeline under wf.
func (f *stepUpFixture) stagedStart(t *testing.T, wf *engine.Workflow, edit func(*StartRequest)) Record {
	t.Helper()
	r, err := f.roleStart(t, wf, criteria("GET /x returns 200", "unknown ids 404"), func(req *StartRequest) {
		req.Body.Pipeline = stagedPipeline()
		if edit != nil {
			edit(req)
		}
	})
	if err != nil {
		t.Fatalf("staged start: %v", err)
	}
	if r.PipelineID == "" {
		t.Fatalf("staged start made no pipeline: %+v", r.Run)
	}
	return r
}

func (f *stepUpFixture) pipelineOf(t *testing.T, r Record) PipelineRecord {
	t.Helper()
	p, err := f.s.Pipeline(context.Background(), r.PipelineID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// latest is the newest run of r's pipeline.
func (f *stepUpFixture) latest(t *testing.T, r Record) Record {
	t.Helper()
	all, err := f.s.List(context.Background(), Filter{Project: ".", Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	var newest Record
	for _, run := range all {
		if run.PipelineID == r.PipelineID && run.CreatedAt >= newest.CreatedAt {
			newest = run
		}
	}
	return newest
}

type stepOutcome struct {
	files  []runnerwire.FileChange
	head   string
	result runnerwire.RunResult
}

// complete accepts r, reports its changes and PR head, finishes it and
// returns the pipeline's next run (r itself when nothing followed).
func (f *stepUpFixture) complete(t *testing.T, r Record, out stepOutcome) Record {
	t.Helper()
	ctx := context.Background()
	if err := f.d.Accept(ctx, f.runner.ID, runnerwire.Accept{RunID: r.ID, Base: "main", Branch: "agent/n1"}); err != nil {
		t.Fatalf("accept %s (%s): %v", r.ID, r.Kind, err)
	}
	if out.files != nil {
		adds := 0
		for _, file := range out.files {
			adds += file.Additions
		}
		if err := f.d.Changes(ctx, f.runner.ID, r.ID, runnerwire.RunChanges{Commit: r.ID + "0000000000000000", Base: "main", Files: out.files, Additions: adds}); err != nil {
			t.Fatal(err)
		}
	}
	if out.head != "" {
		if err := f.d.PR(ctx, f.runner.ID, r.ID, runnerwire.PRInfo{URL: "https://github.com/o/r/pull/7", Number: 7, State: "open", CI: runnerwire.CIPending, HeadSHA: out.head}); err != nil {
			t.Fatal(err)
		}
	}
	if out.result.Status == "" {
		out.result.Status = runnerwire.RunSucceeded
	}
	if err := f.d.Finish(ctx, f.runner.ID, r.ID, out.result); err != nil {
		t.Fatalf("finish %s: %v", r.ID, err)
	}
	return f.latest(t, r)
}

func testFile(path string) runnerwire.FileChange {
	return runnerwire.FileChange{Path: path, Status: "added", Additions: 20}
}

func codeFile(path string, additions int) runnerwire.FileChange {
	return runnerwire.FileChange{Path: path, Status: "modified", Additions: additions}
}

func red() runnerwire.RunResult {
	return runnerwire.RunResult{Verify: &runnerwire.VerifyResult{Phase: runnerwire.VerifyRed, ExitCode: 1, Passed: true, Output: "--- FAIL: TestX"}}
}

func green(passed bool) runnerwire.RunResult {
	exit := 0
	if !passed {
		exit = 1
	}
	return runnerwire.RunResult{Verify: &runnerwire.VerifyResult{Phase: runnerwire.VerifyGreen, ExitCode: exit, Passed: passed, Output: "--- FAIL: TestX: want 404"}}
}

// throughRed runs the Tester and a failing verify, returning the first
// executor run.
func (f *stepUpFixture) throughRed(t *testing.T, first Record) Record {
	t.Helper()
	verify := f.complete(t, first, stepOutcome{files: []runnerwire.FileChange{testFile("pkg/x_test.go")}, head: head1})
	if verify.Kind != runnerwire.JobVerify {
		t.Fatalf("after the tester: %q run, want verify", verify.Kind)
	}
	exec := f.complete(t, verify, stepOutcome{result: red()})
	if exec.Kind != runnerwire.JobRevise || exec.Role != engine.RoleExecutor {
		t.Fatalf("after red: %q/%q run, want revise/executor", exec.Kind, exec.Role)
	}
	return exec
}

func TestStagedPipelineStartsWithTester(t *testing.T) {
	f := newRoleFixture(t, true)
	first := f.stagedStart(t, standardStages(), nil)
	if first.Kind != runnerwire.JobTest || first.Role != engine.RoleTester || first.Agent != "claude" || first.Model != "sonnet" {
		t.Fatalf("first run = kind %q role %q %s/%s", first.Kind, first.Role, first.Agent, first.Model)
	}
	job := f.conn.last(t, first.ID)
	if job.Kind != runnerwire.JobTest || job.Role != engine.RoleTester || len(job.Acceptance) != 2 || slices.Contains(job.Tools, "claim_task") {
		t.Fatalf("tester offer = %+v", job)
	}
	p := f.pipelineOf(t, first)
	if p.Phase != runnerwire.PhaseTest || p.Stages == nil || !p.Stages.Stages["tester"] {
		t.Fatalf("pipeline = phase %q stages %+v", p.Phase, p.Stages)
	}
}

func TestVerifyRedFailingTestsAdvanceToExecutor(t *testing.T) {
	f := newRoleFixture(t, true)
	first := f.stagedStart(t, standardStages(), nil)
	verify := f.complete(t, first, stepOutcome{files: []runnerwire.FileChange{testFile("pkg/x_test.go")}, head: head1})
	if verify.Kind != runnerwire.JobVerify || verify.Role != "" {
		t.Fatalf("after the tester: kind %q role %q", verify.Kind, verify.Role)
	}
	if job := f.conn.last(t, verify.ID); job.Verify == nil || job.Verify.Phase != runnerwire.VerifyRed || job.Verify.Head != head1 || job.Prompt != "" {
		t.Fatalf("verify offer = %+v", job)
	}
	exec := f.complete(t, verify, stepOutcome{result: red()})
	if exec.Kind != runnerwire.JobRevise || exec.Role != engine.RoleExecutor || exec.Model != "opus" {
		t.Fatalf("executor run = %+v", exec.Run)
	}
	job := f.conn.last(t, exec.ID)
	if !reflect.DeepEqual(job.TestFiles, []string{"pkg/x_test.go"}) || job.Branch == "" || job.PRURL == "" {
		t.Fatalf("executor offer = %+v", job)
	}
	p := f.pipelineOf(t, exec)
	if p.Phase != runnerwire.PhaseImplement || !reflect.DeepEqual(p.TestFiles, []string{"pkg/x_test.go"}) {
		t.Fatalf("pipeline = phase %q test files %v", p.Phase, p.TestFiles)
	}
}

func TestVerifyRedPassingTestsRetryTester(t *testing.T) {
	f := newRoleFixture(t, true)
	first := f.stagedStart(t, standardStages(), nil)
	verify := f.complete(t, first, stepOutcome{files: []runnerwire.FileChange{testFile("pkg/x_test.go")}, head: head1})
	again := f.complete(t, verify, stepOutcome{result: runnerwire.RunResult{Verify: &runnerwire.VerifyResult{Phase: runnerwire.VerifyRed, ExitCode: 0, Passed: false}}})
	if again.Kind != runnerwire.JobTest || again.Role != engine.RoleTester || again.ID == first.ID {
		t.Fatalf("after passing tests: %+v", again.Run)
	}
	if !strings.Contains(f.conn.last(t, again.ID).Review, "already pass") {
		t.Fatalf("retry review = %q", f.conn.last(t, again.ID).Review)
	}
}

func TestTesterRoundsExhaustedWaitsHuman(t *testing.T) {
	f := newRoleFixture(t, true)
	wf := standardStages()
	wf.MaxTesterRounds = 1
	first := f.stagedStart(t, wf, nil)
	verify := f.complete(t, first, stepOutcome{files: []runnerwire.FileChange{testFile("pkg/x_test.go")}, head: head1})
	f.complete(t, verify, stepOutcome{result: runnerwire.RunResult{Verify: &runnerwire.VerifyResult{Phase: runnerwire.VerifyRed, ExitCode: 0}}})
	p := f.pipelineOf(t, first)
	if p.Status != runnerwire.PipelineWaitingHuman || !strings.Contains(p.Reason, "already pass") {
		t.Fatalf("pipeline = %s (%s)", p.Status, p.Reason)
	}
}

func TestTesterNonTestFilesFailRound(t *testing.T) {
	f := newRoleFixture(t, true)
	first := f.stagedStart(t, standardStages(), nil)
	next := f.complete(t, first, stepOutcome{files: []runnerwire.FileChange{testFile("pkg/x_test.go"), codeFile("pkg/x.go", 5)}, head: head1})
	if next.Kind != runnerwire.JobTest || !strings.Contains(f.conn.last(t, next.ID).Review, "non-test files pkg/x.go") {
		t.Fatalf("after a tester that implemented: %+v", next.Run)
	}
}

func TestNoTestCommandWaitsHuman(t *testing.T) {
	f := newRoleFixture(t, true)
	first := f.stagedStart(t, standardStages(), nil)
	verify := f.complete(t, first, stepOutcome{files: []runnerwire.FileChange{testFile("pkg/x_test.go")}, head: head1})
	f.complete(t, verify, stepOutcome{result: runnerwire.RunResult{Verify: &runnerwire.VerifyResult{Phase: runnerwire.VerifyRed, NoCommand: true}}})
	p := f.pipelineOf(t, first)
	if p.Status != runnerwire.PipelineWaitingHuman || !strings.Contains(p.Reason, "test command") {
		t.Fatalf("pipeline = %s (%s)", p.Status, p.Reason)
	}
}

func TestVerifyGreenFailureReturnsToExecutor(t *testing.T) {
	f := newRoleFixture(t, true)
	exec := f.throughRed(t, f.stagedStart(t, standardStages(), nil))
	verify := f.complete(t, exec, stepOutcome{files: []runnerwire.FileChange{codeFile("pkg/x.go", 30)}, head: head2})
	if job := f.conn.last(t, verify.ID); verify.Kind != runnerwire.JobVerify || job.Verify == nil || job.Verify.Phase != runnerwire.VerifyGreen || job.Verify.Head != head2 {
		t.Fatalf("green verify = %+v", job)
	}
	again := f.complete(t, verify, stepOutcome{result: green(false)})
	if again.Kind != runnerwire.JobRevise || again.Role != engine.RoleExecutor || !strings.Contains(f.conn.last(t, again.ID).Review, "want 404") {
		t.Fatalf("after red green: %+v", again.Run)
	}
}

func TestGreenTriesExhaustedFails(t *testing.T) {
	f := newRoleFixture(t, true)
	wf := standardStages()
	wf.MaxGreenTries = 1
	exec := f.throughRed(t, f.stagedStart(t, wf, nil))
	verify := f.complete(t, exec, stepOutcome{files: []runnerwire.FileChange{codeFile("pkg/x.go", 30)}, head: head2})
	f.complete(t, verify, stepOutcome{result: green(false)})
	p := f.pipelineOf(t, exec)
	if p.Status != runnerwire.PipelineFailed && p.Status != runnerwire.PipelineChangesLeft {
		t.Fatalf("pipeline = %s (%s)", p.Status, p.Reason)
	}
}

func TestGuardFlagsTesterFilesAndRenames(t *testing.T) {
	tests := []string{"pkg/x_test.go", "web/a.test.ts"}
	clean := Guard(tests, []runnerwire.FileChange{codeFile("pkg/x.go", 3)})
	if len(clean.Touched) != 0 || len(clean.Sensitive) != 0 || !reflect.DeepEqual(clean.TestFiles, tests) {
		t.Fatalf("clean = %+v", clean)
	}
	got := Guard(tests, []runnerwire.FileChange{
		{Path: "pkg/x_test.go", Status: "modified"},
		{Path: "web/b.test.ts", OldPath: "web/a.test.ts", Status: "renamed"},
		codeFile("pkg/x.go", 3),
	})
	if !reflect.DeepEqual(got.Touched, []string{"pkg/x_test.go", "web/a.test.ts"}) || !got.Violation() {
		t.Fatalf("touched = %+v", got)
	}
	deleted := Guard(tests, []runnerwire.FileChange{{Path: "web/a.test.ts", Status: "deleted"}})
	if !reflect.DeepEqual(deleted.Touched, []string{"web/a.test.ts"}) {
		t.Fatalf("deleted = %+v", deleted)
	}
}

func TestGuardFlagsSensitivePaths(t *testing.T) {
	for _, p := range []string{"package.json", "web/package.json", "go.mod", "go.sum", "Makefile", ".github/workflows/ci.yml", "vitest.config.ts"} {
		if !SensitivePath(p) {
			t.Errorf("%s is not sensitive", p)
		}
	}
	for _, p := range []string{"pkg/x.go", "web/src/App.tsx", "docs/a.md"} {
		if SensitivePath(p) {
			t.Errorf("%s is sensitive", p)
		}
	}
	got := Guard(nil, []runnerwire.FileChange{codeFile("go.mod", 1), codeFile("pkg/x.go", 3)})
	if !reflect.DeepEqual(got.Sensitive, []string{"go.mod"}) || !got.Violation() {
		t.Fatalf("guard = %+v", got)
	}
	if bad := NonTestFiles(engine.DefaultTestGlobs, []runnerwire.FileChange{testFile("pkg/x_test.go"), codeFile("pkg/x.go", 1)}); !reflect.DeepEqual(bad, []string{"pkg/x.go"}) {
		t.Fatalf("NonTestFiles = %v", bad)
	}
}

func TestGuardViolationWithoutReviewerWaitsHuman(t *testing.T) {
	f := newRoleFixture(t, true)
	wf := &engine.Workflow{Preset: engine.PresetCustom, Stages: engine.Stages{Tester: true}, MaxGreenTries: 3, MaxTesterRounds: 2}
	exec := f.throughRed(t, f.stagedStart(t, wf, nil))
	verify := f.complete(t, exec, stepOutcome{files: []runnerwire.FileChange{codeFile("pkg/x.go", 3), {Path: "pkg/x_test.go", Status: "modified", Additions: 1}}, head: head2})
	f.complete(t, verify, stepOutcome{result: green(true)})
	p := f.pipelineOf(t, exec)
	if p.Status != runnerwire.PipelineWaitingHuman || !strings.Contains(p.Reason, "pkg/x_test.go") || p.Guard == nil || !p.Guard.Violation() {
		t.Fatalf("pipeline = %s (%s) guard %+v", p.Status, p.Reason, p.Guard)
	}
}

func TestGuardViolationNeedsExplicitAck(t *testing.T) {
	f := newRoleFixture(t, true)
	exec := f.throughRed(t, f.stagedStart(t, standardStages(), nil))
	touched := []runnerwire.FileChange{codeFile("pkg/x.go", 3), {Path: "pkg/x_test.go", Status: "modified", Additions: 1}}
	verify := f.complete(t, exec, stepOutcome{files: touched, head: head2})
	review := f.complete(t, verify, stepOutcome{result: green(true)})
	if review.Kind != runnerwire.JobReview || review.Role != engine.RoleReviewer {
		t.Fatalf("after green: %+v", review.Run)
	}
	if !strings.Contains(f.conn.last(t, review.ID).Prompt+f.conn.last(t, review.ID).Review, "pkg/x_test.go") {
		t.Fatal("the reviewer is not told which tester files changed")
	}
	// An approve without the acknowledgement counts as changes.
	next := f.complete(t, review, stepOutcome{result: runnerwire.RunResult{Verdict: runnerwire.VerdictApprove}})
	if next.Kind != runnerwire.JobRevise {
		t.Fatalf("an unacknowledged approve moved on: %+v", next.Run)
	}
	verify = f.complete(t, next, stepOutcome{files: touched, head: head3})
	review = f.complete(t, verify, stepOutcome{result: green(true)})
	f.complete(t, review, stepOutcome{result: runnerwire.RunResult{Verdict: runnerwire.VerdictApprove, GuardAck: true}})
	if p := f.pipelineOf(t, review); p.Status != runnerwire.PipelineApproved || p.Guard == nil || !p.Guard.Approved {
		t.Fatalf("acknowledged approve: %s guard %+v", p.Status, p.Guard)
	}
}

func TestCIGateWaitsForGreenHead(t *testing.T) {
	f := newRoleFixture(t, true)
	wf := &engine.Workflow{Preset: engine.PresetCustom, Stages: engine.Stages{Tester: true, CI: true}, MaxGreenTries: 3, MaxTesterRounds: 2}
	exec := f.throughRed(t, f.stagedStart(t, wf, nil))
	verify := f.complete(t, exec, stepOutcome{files: []runnerwire.FileChange{codeFile("pkg/x.go", 3)}, head: head2})
	f.complete(t, verify, stepOutcome{result: green(true)})
	ctx := context.Background()
	if p := f.pipelineOf(t, exec); p.Phase != runnerwire.PhaseCI || p.Status != runnerwire.PipelineRunning {
		t.Fatalf("after green: phase %q status %s", p.Phase, p.Status)
	}
	// CI green on an older head does not count.
	_ = f.d.PR(ctx, f.runner.ID, exec.ID, runnerwire.PRInfo{URL: "https://github.com/o/r/pull/7", Number: 7, State: "open", CI: runnerwire.CISuccess, HeadSHA: head1})
	if p := f.pipelineOf(t, exec); p.Status != runnerwire.PipelineRunning {
		t.Fatalf("CI on an old head moved the pipeline: %s", p.Status)
	}
	_ = f.d.PR(ctx, f.runner.ID, exec.ID, runnerwire.PRInfo{URL: "https://github.com/o/r/pull/7", Number: 7, State: "open", CI: runnerwire.CISuccess, HeadSHA: head2})
	if p := f.pipelineOf(t, exec); p.Status != runnerwire.PipelineApproved {
		t.Fatalf("CI green on the green head: %s (%s)", p.Status, p.Reason)
	}
}

func TestCIFailureReturnsToExecutor(t *testing.T) {
	f := newRoleFixture(t, true)
	wf := &engine.Workflow{Preset: engine.PresetCustom, Stages: engine.Stages{Tester: true, CI: true}, MaxGreenTries: 3, MaxTesterRounds: 2}
	exec := f.throughRed(t, f.stagedStart(t, wf, nil))
	verify := f.complete(t, exec, stepOutcome{files: []runnerwire.FileChange{codeFile("pkg/x.go", 3)}, head: head2})
	f.complete(t, verify, stepOutcome{result: green(true)})
	_ = f.d.PR(context.Background(), f.runner.ID, exec.ID, runnerwire.PRInfo{URL: "https://github.com/o/r/pull/7", Number: 7, State: "open", CI: runnerwire.CIFailure, HeadSHA: head2})
	next := f.latest(t, exec)
	if next.Kind != runnerwire.JobRevise || next.Role != engine.RoleExecutor || !strings.Contains(f.conn.last(t, next.ID).Review, "CI failed") {
		t.Fatalf("after CI failure: %+v", next.Run)
	}
}

func TestReviewerSkippedBelowThreshold(t *testing.T) {
	f := newRoleFixture(t, true)
	wf := standardStages()
	wf.ReviewerSkipBelowLines = 50
	exec := f.throughRed(t, f.stagedStart(t, wf, nil))
	verify := f.complete(t, exec, stepOutcome{files: []runnerwire.FileChange{codeFile("pkg/x.go", 10)}, head: head2})
	after := f.complete(t, verify, stepOutcome{result: green(true)})
	if after.Kind == runnerwire.JobReview {
		t.Fatal("a 10-line change was reviewed under a 50-line threshold")
	}
	if p := f.pipelineOf(t, exec); p.Status != runnerwire.PipelineApproved {
		t.Fatalf("pipeline = %s (%s)", p.Status, p.Reason)
	}
}

func TestReviewerNotSkippedOnViolationOrSensitive(t *testing.T) {
	for name, files := range map[string][]runnerwire.FileChange{
		"sensitive": {codeFile("pkg/x.go", 3), codeFile("go.mod", 1)},
		"violation": {codeFile("pkg/x.go", 3), {Path: "pkg/x_test.go", Status: "modified", Additions: 1}},
	} {
		f := newRoleFixture(t, true)
		wf := standardStages()
		wf.ReviewerSkipBelowLines = 50
		exec := f.throughRed(t, f.stagedStart(t, wf, nil))
		verify := f.complete(t, exec, stepOutcome{files: files, head: head2})
		if after := f.complete(t, verify, stepOutcome{result: green(true)}); after.Kind != runnerwire.JobReview {
			t.Errorf("%s: small change skipped review: %+v", name, after.Run)
		}
	}
}

func TestApprovedStagedPipelineMarksNodeDone(t *testing.T) {
	f := newRoleFixture(t, true)
	var done []string
	f.d.SetOnApproved(func(project, nodeID, evidence string) {
		done = append(done, project+"/"+nodeID+": "+evidence)
	})
	exec := f.throughRed(t, f.stagedStart(t, standardStages(), nil))
	verify := f.complete(t, exec, stepOutcome{files: []runnerwire.FileChange{codeFile("pkg/x.go", 30)}, head: head2})
	review := f.complete(t, verify, stepOutcome{result: green(true)})
	f.complete(t, review, stepOutcome{result: runnerwire.RunResult{Verdict: runnerwire.VerdictApprove}})
	if len(done) != 1 || !strings.HasPrefix(done[0], "./n1: ") || !strings.Contains(done[0], "green") {
		t.Fatalf("approved hook = %v", done)
	}
}

func TestLightRunsImplementOnly(t *testing.T) {
	f := newRoleFixture(t, true)
	r, err := f.roleStart(t, lightStages(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != runnerwire.JobImplement || r.Role != engine.RoleExecutor {
		t.Fatalf("light run = kind %q role %q", r.Kind, r.Role)
	}
	if job := f.conn.last(t, r.ID); job.Role != engine.RoleExecutor || job.TestFiles != nil {
		t.Fatalf("light offer = %+v", job)
	}
}

func TestEvidencePersisted(t *testing.T) {
	f := newRoleFixture(t, true)
	var evidence []string
	f.d.SetEvidence(func(project, nodeID, kind, summary string) { evidence = append(evidence, kind+": "+summary) })
	first := f.stagedStart(t, standardStages(), nil)
	verify := f.complete(t, first, stepOutcome{files: []runnerwire.FileChange{testFile("pkg/x_test.go")}, head: head1})
	f.complete(t, verify, stepOutcome{result: red()})
	got, _ := f.s.Get(context.Background(), verify.ID)
	if got.Verify == nil || got.Verify.ExitCode != 1 || got.Verify.Phase != runnerwire.VerifyRed {
		t.Fatalf("stored verify = %+v", got.Verify)
	}
	if len(evidence) == 0 || !strings.HasPrefix(evidence[0], runnerwire.PhaseVerifyRed+": ") || !strings.Contains(evidence[0], head1[:7]) {
		t.Fatalf("evidence = %v", evidence)
	}
}

func TestStoryJudgeLoop(t *testing.T) {
	f := newRoleFixture(t, true)
	wf := standardStages()
	wf.Judge = engine.JudgeChecklist
	r, err := f.roleStart(t, wf, criteria("plant the key foreshadowing", "keep Lin's POV"), func(req *StartRequest) {
		req.Body.Pipeline = stagedPipeline()
		load := req.LoadNode
		req.LoadNode = func() (NodeInfo, bool, error) {
			n, ok, err := load()
			n.Type, n.Content = "scene", ""
			return n, ok, err
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	// An empty scene is red by definition: the writer goes first.
	if r.Kind != runnerwire.JobImplement || r.Role != engine.RoleExecutor {
		t.Fatalf("first story run = kind %q role %q", r.Kind, r.Role)
	}
	judge := f.complete(t, r, stepOutcome{})
	if judge.Kind != runnerwire.JobJudge || judge.Role != engine.RoleReviewer || len(f.conn.last(t, judge.ID).Acceptance) != 2 {
		t.Fatalf("judge = %+v", judge.Run)
	}
	rewrite := f.complete(t, judge, stepOutcome{result: runnerwire.RunResult{Verdict: runnerwire.VerdictFail,
		Checks: []runnerwire.CheckResult{{ID: "a1", Pass: false, Note: "no foreshadowing"}, {ID: "a2", Pass: true}}}})
	if rewrite.Kind != runnerwire.JobImplement || rewrite.Role != engine.RoleExecutor || !strings.Contains(f.conn.last(t, rewrite.ID).Review, "a1") {
		t.Fatalf("rewrite = %+v", rewrite.Run)
	}
	judge = f.complete(t, rewrite, stepOutcome{})
	f.complete(t, judge, stepOutcome{result: runnerwire.RunResult{Verdict: runnerwire.VerdictPass,
		Checks: []runnerwire.CheckResult{{ID: "a1", Pass: true}, {ID: "a2", Pass: true}}}})
	if p := f.pipelineOf(t, r); p.Status != runnerwire.PipelineApproved || p.PRURL != "" {
		t.Fatalf("story pipeline = %s pr %q", p.Status, p.PRURL)
	}
}

func TestRoleFallbackChainOnLimit(t *testing.T) {
	f := newRoleFixture(t, true)
	first := f.start(t, func(in *New) {
		in.Role = engine.RoleReviewer
		in.Kind = runnerwire.JobReview
		in.JobBranch, in.JobPRURL = "agent/n1", "https://github.com/o/r/pull/7"
		in.Fallbacks = []runnerwire.AgentModel{{Agent: "codex"}}
	})
	f.fail(t, first.ID, runnerwire.RunResult{FailureKind: runnerwire.FailureQuota, Error: "usage limit"})
	next, ok := f.follower(t, first.ID)
	if !ok || next.Role != engine.RoleReviewer || next.Agent != "codex" || f.conn.last(t, next.ID).Role != engine.RoleReviewer {
		t.Fatalf("fallback = %+v", next.Run)
	}
}

func TestReviseWaitsForLimitReset(t *testing.T) {
	f := newRoleFixture(t, true)
	first := f.start(t, func(in *New) {
		in.Role = engine.RoleExecutor
		in.Kind = runnerwire.JobRevise
		in.JobBranch, in.JobPRURL = "agent/n1", "https://github.com/o/r/pull/7"
	})
	reset := f.clock.Add(time.Hour).UTC().Truncate(time.Second)
	f.fail(t, first.ID, runnerwire.RunResult{FailureKind: runnerwire.FailureQuota, ResetAt: reset.Format(time.RFC3339)})
	wait, ok := f.follower(t, first.ID)
	if !ok || wait.RetryAt == "" || wait.Kind != runnerwire.JobRevise || wait.JobBranch != "agent/n1" || wait.Role != engine.RoleExecutor {
		t.Fatalf("revise wait = %+v ok=%v", wait.Run, ok)
	}
}

// A test file the Executor adds beside the Tester's (a TestMain that exits 0
// would make the test command pass with nothing done) needs a sign-off too.
func TestGuardFlagsExecutorAddedTestFiles(t *testing.T) {
	f := newRoleFixture(t, true)
	wf := &engine.Workflow{Preset: engine.PresetCustom, Stages: engine.Stages{Tester: true}, MaxGreenTries: 3, MaxTesterRounds: 2}
	exec := f.throughRed(t, f.stagedStart(t, wf, nil))
	verify := f.complete(t, exec, stepOutcome{files: []runnerwire.FileChange{codeFile("pkg/x.go", 3), testFile("pkg/main_test.go")}, head: head2})
	f.complete(t, verify, stepOutcome{result: green(true)})
	p := f.pipelineOf(t, exec)
	if p.Status != runnerwire.PipelineWaitingHuman || p.Guard == nil || !slices.Contains(p.Guard.Sensitive, "pkg/main_test.go") {
		t.Fatalf("pipeline = %s (%s) guard %+v", p.Status, p.Reason, p.Guard)
	}
}
