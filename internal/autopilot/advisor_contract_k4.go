package autopilot

// TEMPORARY (worker K4): a copy of the advisor contract K1 defines in
// advisor.go, so llmadvisor.go compiles on its own branch. Delete this file
// when merging K1's internal/autopilot.

import (
	"context"

	"nodevas/internal/runnerwire"
)

// Advisor picks who implements a task.
type Advisor interface {
	Choose(ctx context.Context, in ChooseInput) (Choice, error)
}

// ChooseInput is what an Advisor decides on.
type ChooseInput struct {
	OwnerID, Project, NodeID, Title, Body string
	Policy                                runnerwire.AutopilotPolicy
	Agents                                []runnerwire.Agent
}

// Choice is an Advisor's answer: the implement chain and why.
type Choice struct {
	Implement []runnerwire.AgentModel
	Reason    string
}

var llmAdvisor Advisor

// RegisterLLMAdvisor sets the advisor used for AdvisorLLM policies.
func RegisterLLMAdvisor(a Advisor) { llmAdvisor = a }
