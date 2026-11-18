package adapter

import (
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

// A draft plan run (P6) sends one create_subgraph the hub holds for the
// person: provisional ids, and no link to the goal.
func TestPlanDraftPrompt(t *testing.T) {
	job := runnerwire.Job{NodeID: "n-1", NodeTitle: "Login page", Prompt: "Build the login page.",
		Kind: runnerwire.JobPlan, Draft: true}
	prompt := Prompt(job, "")
	for _, want := range []string{"create_subgraph", "draft:<key>", "goal:n-1", "refused in this run",
		"Do not link anything to the goal", "get_graph_outline", "Build the login page."} {
		if !strings.Contains(prompt, want) {
			t.Errorf("draft prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "link_nodes from: that node") {
		t.Errorf("draft prompt still links to the goal:\n%s", prompt)
	}
	if strings.Contains(prompt, plannerAcceptanceRule) {
		t.Error("a role-less draft prompt carries the planner's acceptance rule")
	}

	job.Role = rolePlanner
	if prompt := Prompt(job, ""); !strings.Contains(prompt, plannerAcceptanceRule) || !strings.Contains(prompt, untrustedRule) {
		t.Errorf("planner draft prompt lacks the role rules:\n%s", prompt)
	}

	// A revision keeps the replan prompt; the hub never makes that a draft.
	job.Review = "Also handle SSO."
	if got, want := Prompt(job, ""), replanPrompt(job); got != want {
		t.Errorf("a draft replan got another prompt:\n%s", got)
	}

	// Without Draft the plan prompt is today's.
	job.Review, job.Draft = "", false
	if prompt := Prompt(job, ""); !strings.Contains(prompt, "link_nodes from: that node") || strings.Contains(prompt, "draft:<key>") {
		t.Errorf("plain plan prompt changed:\n%s", prompt)
	}
}
