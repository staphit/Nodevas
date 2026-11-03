package runs

import (
	"context"
	"errors"
	"slices"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
)

// runningJob records and starts a run of kind in role.
func runningJob(t *testing.T, s *Store, kind, role string) Record {
	t.Helper()
	ctx := context.Background()
	r, err := s.Create(ctx, New{
		Project: ".", NodeID: "n1", NodeTitle: "Title", RunnerID: "r1", RunnerName: "desk",
		OwnerID: "u-ann", Agent: "codex", Model: "gpt-5.5", RepoID: "repo", RepoName: "repo",
		JobPrompt: "# Title", CreatedBy: "u-ann", CreatedByName: "ann", Kind: kind, Role: role,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, r.ID, runnerwire.RunOffered, Change{}); err != nil {
		t.Fatal(err)
	}
	r, err = s.Transition(ctx, r.ID, runnerwire.RunRunning, Change{Runner: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// B-14: a run's verdict is one row, replaced on a second call, and it
// decides runs.verdict at finish over the textual VERDICT line.
func TestSubmitVerdictReplacesAndDecidesFinish(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := runningJob(t, s, runnerwire.JobReview, engine.RoleReviewer)
	if role := VerdictRole(r); role != engine.RoleReviewer {
		t.Fatalf("VerdictRole(review run) = %q", role)
	}
	first, replaced, err := s.SubmitVerdict(ctx, r, VerdictInput{Outcome: VerdictOutcomePass, Severity: SeverityNone})
	if err != nil || replaced || first.Round != 1 || first.Role != engine.RoleReviewer || first.Source != VerdictSourceMCP {
		t.Fatalf("first verdict = %+v replaced %v err %v", first, replaced, err)
	}
	if _, replaced, err = s.SubmitVerdict(ctx, r, VerdictInput{Outcome: VerdictOutcomeFail, Severity: SeverityMedium, Reasons: []string{"tests_weak"}}); err != nil || !replaced {
		t.Fatalf("second verdict replaced %v err %v", replaced, err)
	}
	got, found, err := s.VerdictFor(ctx, r.ID)
	if err != nil || !found || got.Outcome != VerdictOutcomeFail || got.Severity != SeverityMedium || !slices.Equal(got.Reasons, []string{"tests_weak"}) {
		t.Fatalf("stored verdict = %+v found %v err %v", got, found, err)
	}

	// The agent's text says approve; its structured verdict says fail.
	done, err := s.Finish(ctx, r.ID, "r1", runnerwire.RunResult{Status: runnerwire.RunSucceeded, Verdict: runnerwire.VerdictApprove})
	if err != nil {
		t.Fatal(err)
	}
	if done.Verdict != runnerwire.VerdictChanges {
		t.Fatalf("runs.verdict = %q, want changes from the MCP verdict", done.Verdict)
	}
}

// B-15: a review or judge run that gave no structured verdict gets one
// from its finish (source "finish").
func TestFinishSynthesizesAVerdict(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	review := runningJob(t, s, runnerwire.JobReview, engine.RoleReviewer)
	if _, err := s.Finish(ctx, review.ID, "r1", runnerwire.RunResult{Status: runnerwire.RunSucceeded, Verdict: runnerwire.VerdictApprove}); err != nil {
		t.Fatal(err)
	}
	v, found, err := s.VerdictFor(ctx, review.ID)
	if err != nil || !found || v.Outcome != VerdictOutcomePass || v.Severity != SeverityNone || len(v.Reasons) != 0 || v.Source != VerdictSourceFinish {
		t.Fatalf("review verdict = %+v found %v err %v", v, found, err)
	}

	judge := runningJob(t, s, runnerwire.JobJudge, engine.RoleQA)
	if _, err := s.Finish(ctx, judge.ID, "r1", runnerwire.RunResult{Status: runnerwire.RunSucceeded, Verdict: runnerwire.VerdictFail,
		Checks: []runnerwire.CheckResult{{ID: "a1", Pass: false}, {ID: "a2", Pass: true}}}); err != nil {
		t.Fatal(err)
	}
	v, found, err = s.VerdictFor(ctx, judge.ID)
	if err != nil || !found || v.Outcome != VerdictOutcomeFail || v.Severity != SeverityMedium ||
		!slices.Equal(v.Reasons, []string{"criteria_unmet"}) || v.Role != engine.RoleQA || v.Source != VerdictSourceFinish {
		t.Fatalf("judge verdict = %+v found %v err %v", v, found, err)
	}

	// An implement run gives no verdict at all.
	impl := runningJob(t, s, runnerwire.JobImplement, engine.RoleExecutor)
	if _, err := s.Finish(ctx, impl.ID, "r1", runnerwire.RunResult{Status: runnerwire.RunSucceeded}); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.VerdictFor(ctx, impl.ID); found {
		t.Fatal("an implement run got a verdict")
	}
	if _, _, err := s.SubmitVerdict(ctx, impl, VerdictInput{Outcome: VerdictOutcomePass, Severity: SeverityNone}); !errors.Is(err, ErrVerdictRole) {
		t.Fatalf("executor submitting a verdict: %v, want ErrVerdictRole", err)
	}
}

func TestValidateVerdict(t *testing.T) {
	for name, in := range map[string]VerdictInput{
		"unknown reason":   {Outcome: "fail", Severity: "medium", Reasons: []string{"vibes"}},
		"pass high":        {Outcome: "pass", Severity: "high"},
		"pass weak tests":  {Outcome: "pass", Severity: "low", Reasons: []string{"tests_weak"}},
		"fail no reasons":  {Outcome: "fail", Severity: "medium"},
		"fail severity no": {Outcome: "fail", Severity: "none", Reasons: []string{"other"}},
		"duplicate":        {Outcome: "fail", Severity: "low", Reasons: []string{"style", "style"}},
		"six reasons":      {Outcome: "fail", Severity: "low", Reasons: []string{"tests_missing", "tests_weak", "criteria_unmet", "regression", "style", "security"}},
		"bad outcome":      {Outcome: "maybe", Severity: "none"},
	} {
		in := in
		if err := ValidateVerdict(&in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := VerdictInput{Outcome: "pass", Severity: "low", Reasons: []string{"style"}, Note: "fine\x00 overall\x07"}
	if err := ValidateVerdict(&ok); err != nil || ok.Note != "fine overall" {
		t.Fatalf("valid pass: %v, note %q", err, ok.Note)
	}
}
