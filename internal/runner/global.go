package runner

import (
	"fmt"

	"nodevas/internal/runnerwire"
)

// Global agents (agent workspace, phase 2; runnerwire/global.go): a run the
// runner's owner starts from the hub in one of the repositories here, for no
// project and no node. It gets no board: the runner starts no `nodevas mcp`
// for it (adapter.RunEnv.NoBoard), so it claims nothing, asks nobody and
// relays no permission prompt; interactive mode is how a person talks to one.
// Its branch is agent/<slug>-<run8>. Unless a pull request was asked for
// (and autoPR is on), its work stays on that branch in its worktree, for the
// owner to merge locally or discard from the hub's agent workspace
// (workspace_actions.go). A usage limit never turns it into a handoff: its
// partial work is committed on its own branch like any other.
//
// Every other run names a project and a node. One that names neither and is
// not global is declined: its `nodevas mcp --project ""` would work on
// whatever project the hub has open.

// checkGlobal vets an offer's global shape, and the owner's globalRuns for
// a global one. "" means fine.
func checkGlobal(job *runnerwire.Job, cfg *Config) string {
	if !job.Global {
		if job.Project == "" || job.NodeID == "" {
			return "a run for no project or node must be a global agent's, which this one is not"
		}
		if job.ContinueOf != "" || job.WorkRoot != "" {
			return "only a global agent run may continue an earlier run"
		}
		return ""
	}
	kindOK := job.Kind == runnerwire.JobImplement ||
		(job.Kind == runnerwire.JobCompare && job.Contest != nil && job.Contest.ID != "")
	if job.Project != "" || job.NodeID != "" || !kindOK || job.Role != "" ||
		len(job.Tools) > 0 || len(job.TestFiles) > 0 || len(job.Acceptance) > 0 || job.Verify != nil ||
		job.HandoffBranch != "" || job.Branch != "" || job.PRURL != "" || job.Review != "" ||
		!runnerwire.ValidSlug(job.Slug) {
		return "malformed global job"
	}
	if job.Contest != nil {
		if job.Contest.ID == "" || !validContestLabel(job.Contest.Label) {
			return "malformed global job"
		}
		if job.Kind == runnerwire.JobCompare && job.Contest.Label != "J" {
			return "malformed global job"
		}
		if job.Kind != runnerwire.JobCompare && job.Contest.Label == "J" {
			return "malformed global job"
		}
	}
	if job.ContinueOf != "" && !validRunID.MatchString(job.ContinueOf) {
		return "malformed global job"
	}
	if job.WorkRoot != "" && (!validRunID.MatchString(job.WorkRoot) || job.ContinueOf == "") {
		return "malformed global job"
	}
	if !cfg.GlobalRunsOn() {
		return "global agents are turned off on this runner (nodevas runner set globalRuns on)"
	}
	return ""
}

// branchKey is what names a run's branch (BranchName): its node, else a
// global run's slug, else runnerwire.DefaultGlobalSlug.
func branchKey(nodeID, slug string) string {
	switch {
	case nodeID != "":
		return nodeID
	case slug != "":
		return slug
	}
	return runnerwire.DefaultGlobalSlug
}

// offerText is how the runner's log names what an offer is for.
func offerText(job *runnerwire.Job) string {
	if job.Global {
		return fmt.Sprintf("global agent %q", job.NodeTitle)
	}
	return fmt.Sprintf("node %s %q", job.NodeID, job.NodeTitle)
}
