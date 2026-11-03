package autopilot

import (
	"nodevas/internal/locale"
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
		return p, invalidf("maxParallel is between 1 and %d", "maxParallel 須介於 1 到 %d", runnerwire.MaxAutopilotParallel)
	}
	if p.MaxRounds == 0 {
		p.MaxRounds = 1
	}
	if p.MaxRounds < 1 || p.MaxRounds > runnerwire.MaxPipelineRounds {
		return p, invalidf("maxRounds is between 1 and %d", "maxRounds 須介於 1 到 %d", runnerwire.MaxPipelineRounds)
	}
	switch p.Advisor {
	case runnerwire.AdvisorRules, runnerwire.AdvisorLLM, runnerwire.AdvisorSystemOne:
	default:
		return p, invalidf("advisor must be \"\", %q or %q", "advisor 須為空、「%s」或「%s」", runnerwire.AdvisorLLM, runnerwire.AdvisorSystemOne)
	}
	r := &p.Review
	if r.Mode == "" {
		r.Mode = runnerwire.ReviewNone
	}
	switch r.Mode {
	case runnerwire.ReviewNone, runnerwire.ReviewHuman:
		if len(r.Reviewers) > 0 {
			return p, invalidf("review mode %q takes no reviewers", "審查模式「%s」不需要 reviewer", r.Mode)
		}
		r.Quorum = 0
	case runnerwire.ReviewAgents, runnerwire.ReviewAgentsHuman:
		if len(r.Reviewers) == 0 || len(r.Reviewers) > runnerwire.MaxReviewers {
			return p, invalidf("review mode %q needs 1 to %d reviewers", "審查模式「%s」需要 1 到 %d 位 reviewer", r.Mode, runnerwire.MaxReviewers)
		}
		for i, am := range r.Reviewers {
			if err := runs.ValidateAgentModel(am, locale.Say("reviewer %d", "第 %d 位 reviewer", i+1), false); err != nil {
				return p, err
			}
		}
		if r.Quorum < 0 || r.Quorum > len(r.Reviewers) {
			return p, invalidf("quorum is between 1 and the number of reviewers (0 means all)", "quorum 須介於 1 到 reviewer 人數（0 代表全部）")
		}
	default:
		return p, invalidf("review mode must be none, agents, human or agents+human", "審查模式須為 none、agents、human 或 agents+human")
	}
	if len(p.RunnerID) > 64 || len(p.RepoID) > 64 {
		return p, invalidf("runnerId and repoId are ids", "runnerId 與 repoId 須為代號")
	}
	// Agents read content anyone with edit rights (or a prompt injection in
	// the repo) controls; their approval alone does not merge unless the
	// owner opts in on purpose.
	if p.AutoMerge && !humanReview(p) && !p.AllowAgentOnlyMerge {
		return p, invalidf("autoMerge needs a person's review (review mode human or agents+human) unless allowAgentOnlyMerge is set", "autoMerge 需要人工審查（審查模式 human 或 agents+human），除非設定了 allowAgentOnlyMerge")
	}
	if !p.AutoMerge {
		p.AllowAgentOnlyMerge = false
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
