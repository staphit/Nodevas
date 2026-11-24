package adapter

import (
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

func TestCoordinatedPromptHasNoClaimStep(t *testing.T) {
	forbidden := []string{"claim_task", "set_node_status", "release_task"}

	t.Run("headless implement", func(t *testing.T) {
		job := runnerwire.Job{
			RunID:       "run-coord-1",
			Project:     "demo",
			NodeID:      "n-1",
			NodeTitle:   "Fix login",
			Prompt:      "Fix the login flow.",
			Coordinated: true,
			Role:        "executor",
			Acceptance:  []runnerwire.AcceptanceItem{{ID: "a1", Text: "login succeeds"}},
		}
		prompt := Prompt(job, "agent/n-1-run-coor")
		for _, bad := range forbidden {
			if strings.Contains(prompt, bad) {
				t.Errorf("coordinated headless prompt contains %q:\n%s", bad, prompt)
			}
		}
		for _, want := range []string{
			"Another run worked on, or is working on, this task",
			"Do not claim it or change its status",
			"get_node",
			"the person updates the board",
			"Fix the login flow.",
		} {
			if !strings.Contains(prompt, want) {
				t.Errorf("coordinated headless prompt missing %q:\n%s", want, prompt)
			}
		}
	})

	t.Run("interactive", func(t *testing.T) {
		job := runnerwire.Job{
			RunID:       "run-coord-2",
			Project:     "demo",
			NodeID:      "n-1",
			NodeTitle:   "Fix login",
			Prompt:      "Fix the login flow interactively.",
			Mode:        runnerwire.ModeInteractive,
			Coordinated: true,
		}
		prompt := InteractivePrompt(job, "agent/n-1-run-coor")
		for _, bad := range forbidden {
			if strings.Contains(prompt, bad) {
				t.Errorf("coordinated interactive prompt contains %q:\n%s", bad, prompt)
			}
		}
		for _, want := range []string{
			"Another run worked on, or is working on, this task",
			"Do not claim it or change its status",
			"get_node",
			"the person updates the board",
			"Fix the login flow interactively.",
		} {
			if !strings.Contains(prompt, want) {
				t.Errorf("coordinated interactive prompt missing %q:\n%s", want, prompt)
			}
		}
	})

	t.Run("role staged executor", func(t *testing.T) {
		job := roleJob(runnerwire.JobRevise, "executor")
		job.TestFiles = []string{"internal/auth/login_test.go"}
		job.Coordinated = true
		prompt := Prompt(job, job.Branch)
		for _, bad := range forbidden {
			if strings.Contains(prompt, bad) {
				t.Errorf("coordinated role prompt contains %q:\n%s", bad, prompt)
			}
		}
		for _, want := range []string{
			"Another run worked on, or is working on, this task",
			"Do not claim it or change its status",
			"get_node",
			"internal/auth/login_test.go",
			"the person updates the board",
		} {
			if !strings.Contains(prompt, want) {
				t.Errorf("coordinated role prompt missing %q:\n%s", want, prompt)
			}
		}
	})
}

func TestContestPromptSection(t *testing.T) {
	t.Run("node contest entry", func(t *testing.T) {
		job := runnerwire.Job{
			RunID:       "run-c1",
			Project:     "demo",
			NodeID:      "n-1",
			NodeTitle:   "Build cache",
			Prompt:      "Implement an LRU cache.",
			NoPR:        true,
			Coordinated: true,
			Contest:     &runnerwire.JobContest{ID: "contest-1", Label: "B", Of: 2},
		}
		prompt := Prompt(job, "agent/n-1-run-c1")
		for _, want := range []string{
			"You are entry B of 2 attempts at this task",
			"another agent works on the same task in a separate folder",
			"Work only in your folder",
			"Do not claim the task or change its status",
			"Finish with a short summary of your approach",
		} {
			if !strings.Contains(prompt, want) {
				t.Errorf("node contest prompt missing %q:\n%s", want, prompt)
			}
		}
		for _, bad := range []string{"claim_task", "set_node_status", "release_task"} {
			if strings.Contains(prompt, bad) {
				t.Errorf("node contest prompt contains %q:\n%s", bad, prompt)
			}
		}
	})

	t.Run("global contest entry", func(t *testing.T) {
		job := runnerwire.Job{
			RunID:     "run-gc1",
			Global:    true,
			Slug:      "build-cache",
			NodeTitle: "Build cache",
			Prompt:    "Implement an LRU cache.",
			NoPR:      true,
			Contest:   &runnerwire.JobContest{ID: "contest-2", Label: "A", Of: 3},
		}
		prompt := Prompt(job, "agent/build-cache-run-gc1")
		for _, want := range []string{
			"You are entry A of 3 attempts at this task",
			"another agent works on the same task in a separate folder",
			"Finish with a short summary of your approach",
		} {
			if !strings.Contains(prompt, want) {
				t.Errorf("global contest prompt missing %q:\n%s", want, prompt)
			}
		}
	})

	t.Run("compare job is read-only and gets compare prompt", func(t *testing.T) {
		job := runnerwire.Job{
			RunID:   "run-cmp",
			Global:  true,
			Slug:    "build-cache",
			Kind:    runnerwire.JobCompare,
			Prompt:  "# Contest review\n\n## Entry A\n...",
			NoPR:    true,
			Contest: &runnerwire.JobContest{ID: "contest-2", Label: "J", Of: 2},
		}
		if !ReadOnly(job) {
			t.Fatal("ReadOnly(JobCompare) = false, want true")
		}
		prompt := Prompt(job, "main")
		for _, want := range []string{"contest reviewer", "read-only", "Do not modify, create or delete any file", "# Contest review"} {
			if !strings.Contains(prompt, want) {
				t.Errorf("compare prompt missing %q:\n%s", want, prompt)
			}
		}
		if strings.Contains(prompt, "You are entry J") {
			t.Errorf("compare reviewer should not receive entry contest section:\n%s", prompt)
		}
	})
}
