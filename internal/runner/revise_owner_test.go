package runner

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

// A revise pushes the agent's commit to the job's branch with the owner's
// git credentials. The agent/ prefix alone let a hub point a revise at any
// agent/* branch with an open PR, another member's included, and at a node
// other than the one the branch was made for. A revise now pushes only to a
// branch one of this runner's own runs opened its PR from, for the same
// node, in the same repository; anything else is declined before anyone is
// asked, and nothing is pushed.
func TestReviseOnlyPushesToBranchesThisRunnerOpened(t *testing.T) {
	asked := 0
	env := startReviewRunner(t, func(c *Client) {
		c.Accepter = acceptFunc(func(context.Context, Offer) (bool, string) { asked++; return true, "" })
	})
	env.hub.hello()

	// No run of this runner opened the PR.
	env.hub.offer(prJob("run-foreign", runnerwire.JobRevise, "edit: Fix it"))
	if reason := env.hub.wait("run-foreign", runnerwire.TypeDecline).Reason; !strings.Contains(reason,
		"only to branches its own runs opened for this node") {
		t.Errorf("foreign branch: decline reason %q", reason)
	}

	// This runner opened it, but for another node.
	env.client.jobs.watch(watchedPR{RunID: "run-impl-other", NodeID: "n-other", URL: reviewPRURL, Repo: env.repo,
		Branch: reviewBranch, PR: runnerwire.PRInfo{URL: reviewPRURL, Number: 7, State: runnerwire.PROpen}})
	env.hub.offer(prJob("run-othernode", runnerwire.JobRevise, "edit: Fix it"))
	if reason := env.hub.wait("run-othernode", runnerwire.TypeDecline).Reason; !strings.Contains(reason,
		"only to branches its own runs opened for this node") {
		t.Errorf("another node's branch: decline reason %q", reason)
	}

	if asked != 0 {
		t.Errorf("asked %d times about revising a branch this runner did not open", asked)
	}
	if head := gitT(t, env.bare, "rev-parse", "refs/heads/"+reviewBranch); head != env.head {
		t.Fatalf("origin's %s moved to %s (was %s)", reviewBranch, head, env.head)
	}

	// Its own run for this node opened it: the revise goes ahead.
	ownPR(env)
	env.hub.offer(prJob("run-own", runnerwire.JobRevise, "edit: Fix it"))
	if result := env.hub.wait("run-own", runnerwire.TypeFinish).Result; result.Status != runnerwire.RunSucceeded {
		t.Fatalf("own branch: result %+v", result)
	}
}

// A handoff is continued only on the node its run was for.
func TestHandoffIsContinuedOnlyForItsNode(t *testing.T) {
	asked := 0
	env := startPRRunner(t, nil, func(c *Client) {
		c.Accepter = acceptFunc(func(context.Context, Offer) (bool, string) { asked++; return true, "" })
	})
	env.hub.hello()
	gitT(t, env.repo, "branch", "nodevas/handoff/elsewhere", "main")
	// The record as the runner writes it: this runner made the branch, for
	// node n-other.
	record, err := json.Marshal([]map[string]string{{
		"branch": "nodevas/handoff/elsewhere", "repoId": "r1", "repoPath": env.repo, "nodeId": "n-other",
		"commit": gitT(t, env.repo, "rev-parse", "main"), "runId": "elsewhere",
		"createdAt": time.Now().UTC().Format(time.RFC3339),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(env.client.Config.DataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(handoffPath(env.client.Config.DataDir), record, 0o600); err != nil {
		t.Fatal(err)
	}
	job := testJob("run-elsewhere", "ok:")
	job.HandoffBranch = "nodevas/handoff/elsewhere"
	env.hub.offer(job)
	if reason := env.hub.wait("run-elsewhere", runnerwire.TypeDecline).Reason; !strings.Contains(reason,
		"was made for another node") {
		t.Errorf("decline reason %q", reason)
	}
	if asked != 0 {
		t.Errorf("asked %d times about continuing another node's handoff", asked)
	}
}

// ownPR records the review PR as one this runner's implement run opened for
// node n-1, as publish does.
func ownPR(env *reviewEnv) {
	env.client.jobs.watch(watchedPR{RunID: "run-impl", NodeID: "n-1", URL: reviewPRURL, Repo: env.repo,
		Branch: reviewBranch, PR: runnerwire.PRInfo{URL: reviewPRURL, Number: 7, State: runnerwire.PROpen}})
}
