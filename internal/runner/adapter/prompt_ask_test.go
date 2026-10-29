package adapter

import (
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

// Every headless preamble says nobody watches the terminal but a person can
// be reached through ask_user, with the kinds to use; none says nobody can
// answer.
func TestHeadlessPreamblesPointAtAskUser(t *testing.T) {
	base := runnerwire.Job{RunID: "run-1", NodeID: "n-42", NodeTitle: "Fix it", Prompt: "do it",
		PRURL: "https://example.com/pr/1", Branch: "agent/n-42"}
	with := func(edit func(*runnerwire.Job)) runnerwire.Job {
		job := base
		edit(&job)
		return job
	}
	for name, job := range map[string]runnerwire.Job{
		"implement": base,
		"revise":    with(func(j *runnerwire.Job) { j.Kind = runnerwire.JobRevise; j.Review = "fix" }),
		"staged executor": with(func(j *runnerwire.Job) {
			j.Kind, j.Role, j.TestFiles = runnerwire.JobRevise, roleExecutor, []string{"a_test.go"}
		}),
		"review":        with(func(j *runnerwire.Job) { j.Kind = runnerwire.JobReview }),
		"plan":          with(func(j *runnerwire.Job) { j.Kind = runnerwire.JobPlan }),
		"replan":        with(func(j *runnerwire.Job) { j.Kind, j.Review = runnerwire.JobPlan, "changed" }),
		"qa acceptance": with(func(j *runnerwire.Job) { j.Kind, j.Role = runnerwire.JobPlan, roleQA }),
		"tester":        with(func(j *runnerwire.Job) { j.Kind = runnerwire.JobTest }),
		"judge":         with(func(j *runnerwire.Job) { j.Kind = runnerwire.JobJudge }),
		"qa verify":     with(func(j *runnerwire.Job) { j.Kind, j.Role = runnerwire.JobJudge, roleQA }),
	} {
		prompt := Prompt(job, "agent/n-42")
		if strings.Contains(prompt, "no one to answer") {
			t.Errorf("%s: still says nobody can answer:\n%s", name, prompt)
		}
		for _, want := range []string{"Nobody is watching this terminal", "ask_user", `"question"`, `"choice"`, `"approval"`} {
			if !strings.Contains(prompt, want) {
				t.Errorf("%s: preamble missing %q:\n%s", name, want, prompt)
			}
		}
	}
}
