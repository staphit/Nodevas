package adapter

import (
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

func TestPromptKinds(t *testing.T) {
	job := runnerwire.Job{NodeID: "n-1", NodeTitle: "Fix it", Prompt: "Do the thing.",
		Branch: "agent/n-1-run-a", PRURL: "https://github.com/o/r/pull/7", Review: "Handle the empty case."}

	implement := Prompt(job, "agent/n-1-run-b")
	if !strings.Contains(implement, "claim_task") || strings.Contains(implement, "Review to address") ||
		strings.Contains(implement, "pull/7") {
		t.Errorf("implement prompt:\n%s", implement)
	}

	job.Kind = runnerwire.JobRevise
	revise := Prompt(job, job.Branch)
	for _, want := range []string{"on branch agent/n-1-run-a", "open pull request, https://github.com/o/r/pull/7",
		"claim_task", "pushes your work to the pull request's branch", "The task:\n\nDo the thing.\n",
		"\nReview to address:\n\nHandle the empty case.\n"} {
		if !strings.Contains(revise, want) {
			t.Errorf("revise prompt missing %q:\n%s", want, revise)
		}
	}
	if strings.Index(revise, "Do the thing.") > strings.Index(revise, "Handle the empty case.") {
		t.Error("the review comes before the task")
	}

	job.Kind = runnerwire.JobReview
	review := Prompt(job, job.Branch)
	for _, want := range []string{"code review of pull request https://github.com/o/r/pull/7 (branch agent/n-1-run-a)",
		"Do not modify, create or delete any file", "Do not call claim_task", `exactly "VERDICT: approve"`,
		`"VERDICT: changes"`, "Do the thing."} {
		if !strings.Contains(review, want) {
			t.Errorf("review prompt missing %q:\n%s", want, review)
		}
	}
	if strings.Contains(review, "Handle the empty case.") {
		t.Error("a review prompt carries the previous review")
	}
}
