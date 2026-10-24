package autopilot

import (
	"fmt"

	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// ValidatePolicy checks a policy's bounds and fills its defaults: one task
// at a time, one review round.
func ValidatePolicy(p runnerwire.AutopilotPolicy) (runnerwire.AutopilotPolicy, error) {
	if err := runs.ValidateChain(p.Implement, false); err != nil {
		return p, err
	}
	if p.MaxParallel == 0 {
		p.MaxParallel = 1
	}
	if p.MaxParallel < 1 || p.MaxParallel > runnerwire.MaxAutopilotParallel {
		return p, invalidf("maxParallel is between 1 and %d", runnerwire.MaxAutopilotParallel)
	}
	if p.MaxRounds == 0 {
		p.MaxRounds = 1
	}
	if p.MaxRounds < 1 || p.MaxRounds > runnerwire.MaxPipelineRounds {
		return p, invalidf("maxRounds is between 1 and %d", runnerwire.MaxPipelineRounds)
	}
	switch p.Advisor {
	case runnerwire.AdvisorRules, runnerwire.AdvisorLLM, runnerwire.AdvisorSystemOne:
	default:
		return p, invalidf("advisor must be \"\", %q or %q", runnerwire.AdvisorLLM, runnerwire.AdvisorSystemOne)
	}
	r := &p.Review
	if r.Mode == "" {
		r.Mode = runnerwire.ReviewNone
	}
	switch r.Mode {
	case runnerwire.ReviewNone, runnerwire.ReviewHuman:
		if len(r.Reviewers) > 0 {
			return p, invalidf("review mode %q takes no reviewers", r.Mode)
		}
		r.Quorum = 0
	case runnerwire.ReviewAgents, runnerwire.ReviewAgentsHuman:
		if len(r.Reviewers) == 0 || len(r.Reviewers) > runnerwire.MaxReviewers {
			return p, invalidf("review mode %q needs 1 to %d reviewers", r.Mode, runnerwire.MaxReviewers)
		}
		for i, am := range r.Reviewers {
			if err := runs.ValidateAgentModel(am, fmt.Sprintf("reviewer %d", i+1), false); err != nil {
				return p, err
			}
		}
		if r.Quorum < 0 || r.Quorum > len(r.Reviewers) {
			return p, invalidf("quorum is between 1 and the number of reviewers (0 means all)")
		}
	default:
		return p, invalidf("review mode must be none, agents, human or agents+human")
	}
	if len(p.RunnerID) > 64 || len(p.RepoID) > 64 {
		return p, invalidf("runnerId and repoId are ids")
	}
	return p, nil
}

// reviewed reports whether a policy's tasks go through a pipeline at all.
func reviewed(p runnerwire.AutopilotPolicy) bool {
	return p.Review.Mode != runnerwire.ReviewNone && p.Review.Mode != ""
}

// pipelineFor is the pipeline a task runs, implement being its chain's head:
// the policy's reviewers with their quorum, and Human when a person decides
// (K2's consensus pipeline). A policy without review runs no pipeline: the
// task is one implement run, approved once its PR is open.
func pipelineFor(p runnerwire.AutopilotPolicy, implement runnerwire.AgentModel) *runnerwire.Pipeline {
	if !reviewed(p) {
		return nil
	}
	pl := &runnerwire.Pipeline{Implement: implement, MaxRounds: p.MaxRounds, Human: humanReview(p)}
	pl.HumanAnyEditor = pl.Human && p.Review.AnyEditor
	if p.Review.Mode == runnerwire.ReviewAgents || p.Review.Mode == runnerwire.ReviewAgentsHuman {
		pl.Reviewers, pl.Quorum = p.Review.Reviewers, p.Review.Quorum
	}
	return pl
}

// pipelineHandlesHuman: the pipelines pipelineFor builds wait for a person
// themselves (Pipeline.Human, decided through
// POST /api/runs/pipelines/:id/review), so the orchestrator only mirrors
// their waiting_human state.
const pipelineHandlesHuman = true

// humanReview reports whether a person must approve after the agents.
func humanReview(p runnerwire.AutopilotPolicy) bool {
	return p.Review.Mode == runnerwire.ReviewHuman || p.Review.Mode == runnerwire.ReviewAgentsHuman
}
