package runner

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

// A plan job (W7) runs the agent read-only in a throwaway worktree: no
// changes, no PR, no branch left, whatever the agent wrote to the files.
func TestPlanJob(t *testing.T) {
	env := startJobRunner(t, nil)
	hello := env.hub.hello()
	if !slices.Contains(hello.Hello.Capabilities, runnerwire.CapPlan) {
		t.Fatalf("capabilities %q", hello.Hello.Capabilities)
	}

	job := testJob("run-plan", "edit: Build the export feature")
	job.Kind = runnerwire.JobPlan
	env.hub.offer(job)
	result := env.hub.wait("run-plan", runnerwire.TypeFinish).Result
	if result.Status != runnerwire.RunSucceeded {
		t.Fatalf("result = %+v", result)
	}
	msgs := env.hub.messages("run-plan")
	if got := typesOf(msgs); !slices.Equal(got, []string{runnerwire.TypeAccept, runnerwire.TypeEvents, runnerwire.TypeFinish}) {
		t.Fatalf("message order %v: a plan sends no changes and no PR", got)
	}
	if a := msgs[0].Accept; a.Branch != "" || a.Base != "main" {
		t.Errorf("accept = %+v", a)
	}
	events := eventsOf(msgs)
	if !hasEvent(events, "the planner changed files; discarded") || !hasEvent(events, "plan: worktree") {
		t.Errorf("events %+v", events)
	}

	stdin := env.recorded(t, "run-plan", "stdin")
	for _, want := range []string{"as a planner for Nodevas goal node n-1", `"goal:n-1"`, "create_node", "link_nodes",
		"Do not modify, create or delete any file", "edit: Build the export feature"} {
		if !strings.Contains(stdin, want) {
			t.Errorf("prompt missing %q:\n%s", want, stdin)
		}
	}
	if _, err := os.Stat(filepath.Join(env.client.Config.DataDir, "worktrees", "run-plan")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("plan worktree still there: %v", err)
	}
	if branches := gitT(t, env.repo, "branch", "--list", "agent/*"); strings.TrimSpace(branches) != "" {
		t.Errorf("a plan left branches: %q", branches)
	}
	if status := gitT(t, env.repo, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Errorf("the main checkout changed: %q", status)
	}
}

// A plan reads with the same read-only setting a review gets: Codex's
// read-only sandbox, whatever the owner set for implement runs.
func TestPlanJobReadOnly(t *testing.T) {
	env := startJobRunner(t, func(c *Client) { c.Config.CodexSandbox = "workspace-write" })
	env.hub.hello()
	job := testJob("run-plan-codex", "codex-ok: plan it")
	job.Kind, job.Agent, job.Model = runnerwire.JobPlan, "codex", ""
	env.hub.offer(job)
	if r := env.hub.wait("run-plan-codex", runnerwire.TypeFinish).Result; r.Status != runnerwire.RunSucceeded {
		t.Fatalf("result = %+v", r)
	}
	var args []string
	if err := json.Unmarshal([]byte(env.recorded(t, "run-plan-codex", "args")), &args); err != nil {
		t.Fatal(err)
	}
	if i := slices.Index(args, "-s"); i < 0 || i+1 >= len(args) || args[i+1] != "read-only" {
		t.Fatalf("args = %q", args)
	}
	if !hasEvent(eventsOf(env.hub.messages("run-plan-codex")), "sandbox read-only for a plan") {
		t.Error("no start line naming the plan's read-only sandbox")
	}
}

func TestCheckKindPlan(t *testing.T) {
	cfg := &Config{}
	if got := checkKind(&runnerwire.Job{Kind: runnerwire.JobPlan}, cfg); got != "" {
		t.Fatalf("plan: %q", got)
	}
	if got := checkKind(&runnerwire.Job{Kind: runnerwire.JobPlan, PRURL: "https://github.com/o/r/pull/1"}, cfg); got == "" {
		t.Fatal("a plan naming a PR passed")
	}
}

// A replan (a plan job with a revision) gets the reconciliation rules and
// the revision in its prompt, and its summary keeps the reconciliation block
// however long the final message is: the hub applies it from there.
func TestReplanJob(t *testing.T) {
	env := startJobRunner(t, nil)
	env.hub.hello()
	job := testJob("run-replan", "replan: Build the export feature\n\n## Revisions\n\nDrop the PDF export.")
	job.Kind = runnerwire.JobPlan
	job.Review = "Change to the requirement: drop the PDF export.\n- t1 [done] \"Add the table\"\n- t3 [pending] \"Add PDF export\""
	env.hub.offer(job)
	result := env.hub.wait("run-replan", runnerwire.TypeFinish).Result
	if result.Status != runnerwire.RunSucceeded {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Summary) > runnerwire.MaxSummaryBytes ||
		!strings.HasSuffix(result.Summary, "RECONCILE:\n- keep t1\n- skip t3: the PDF export was dropped\n- add t9: Add the SSO button\nEND RECONCILE") {
		t.Fatalf("summary (%d bytes) lost its block: …%s", len(result.Summary), result.Summary[max(len(result.Summary)-200, 0):])
	}
	stdin := env.recorded(t, "run-replan", "stdin")
	for _, want := range []string{"reconcile the goal's tasks with the revised requirement", "keep (still needed",
		"flag (a task marked running", `"goal:n-1"`, "RECONCILE:\n- keep <id>", "END RECONCILE",
		"do not dispatch or merge anything", `t3 [pending] "Add PDF export"`, "drop the PDF export", "## Revisions"} {
		if !strings.Contains(stdin, want) {
			t.Errorf("prompt missing %q:\n%s", want, stdin)
		}
	}
	if items, ok := runnerwire.ParseReconcile(result.Summary); !ok || len(items) != 3 {
		t.Fatalf("items = %+v", items)
	}
}
