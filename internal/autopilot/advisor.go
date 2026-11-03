package autopilot

import (
	"context"
	"slices"
	"sync"

	"nodevas/internal/engine"
	"nodevas/internal/locale"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// An Advisor picks who implements a task: an agent and model first, then
// fallbacks. The rules advisor is always there; an LLM advisor (K4: a fast
// model on the owner's runner, through the copilot's LLM relay) may be
// registered and is asked first when a policy says Advisor "llm".
type Advisor interface {
	Choose(ctx context.Context, in ChooseInput) (Choice, error)
}

// ChooseInput is what an advisor decides from.
type ChooseInput struct {
	OwnerID, Project, NodeID, Title, Body string
	Policy                                runnerwire.AutopilotPolicy
	// Agents are the installed agents and their models across the runners
	// the task may run on (merged by kind).
	Agents []runnerwire.Agent
}

// Choice is an advisor's answer: the implement chain, first is primary.
type Choice struct {
	Implement []runnerwire.AgentModel
	Reason    string
}

var (
	llmMu      sync.RWMutex
	llmAdvisor Advisor
)

// RegisterLLMAdvisor sets the advisor policies with Advisor "llm" ask first.
// The orchestrator falls back to the rules on its error or after
// llmAdvisorTimeout.
func RegisterLLMAdvisor(a Advisor) {
	llmMu.Lock()
	llmAdvisor = a
	llmMu.Unlock()
}

func registeredLLMAdvisor() Advisor {
	llmMu.RLock()
	defer llmMu.RUnlock()
	return llmAdvisor
}

var systemOneAdvisor Advisor // guarded by llmMu

// RegisterSystemOneAdvisor sets the advisor policies with Advisor
// "systemone" ask first (a System One model on the owner's runner). The
// orchestrator falls back to the rules on its error or after
// systemOneAdvisorTimeout.
func RegisterSystemOneAdvisor(a Advisor) {
	llmMu.Lock()
	systemOneAdvisor = a
	llmMu.Unlock()
}

func registeredSystemOneAdvisor() Advisor {
	llmMu.RLock()
	defer llmMu.RUnlock()
	return systemOneAdvisor
}

// errNoAgent is a task nothing can implement: no installed agent anywhere.
var errNoAgent = locale.Errorf("no installed agent on the owner's runners can take this task", "擁有者的 runner 上沒有已安裝的 agent 能接這個任務")

// rulesAdvisor is the default: the policy's own chain, else the owner's
// routing rules (as the dispatch dialog pre-fills them), else the first
// installed agent on its default model.
type rulesAdvisor struct {
	routing func(ctx context.Context, ownerID string) ([]runnerwire.RouteRule, error)
	// route is auto routing (Options.Route), asked when no rule matches.
	route RouteFunc
	kind  func(project, nodeID string) (kind string, tags []string)
}

func (r rulesAdvisor) Choose(ctx context.Context, in ChooseInput) (Choice, error) {
	if chain := usable(in.Policy.Implement, in.Agents); len(chain) > 0 {
		return Choice{Implement: chain, Reason: "the autopilot's implement chain"}, nil
	}
	if r.routing != nil {
		rules, err := r.routing(ctx, in.OwnerID)
		if err != nil {
			return Choice{}, err
		}
		kind, tags := "", []string(nil)
		if r.kind != nil {
			kind, tags = r.kind(in.Project, in.NodeID)
		}
		s := runs.Suggest(rules, runs.RouteNode{Project: in.Project, Kind: kind, Tags: tags})
		s = routeWith(ctx, r.route, RouteRequest{OwnerID: in.OwnerID, Project: in.Project, NodeID: in.NodeID,
			Role: engine.RoleExecutor, Manual: s})
		if s.RuleID != "" {
			if chain := usable(append([]runnerwire.AgentModel{s.Target}, s.Fallbacks...), in.Agents); len(chain) > 0 {
				return Choice{Implement: chain, Reason: "routing rule " + s.RuleID}, nil
			}
		}
	}
	for _, agent := range in.Agents {
		if agent.Installed {
			return Choice{Implement: []runnerwire.AgentModel{{Agent: agent.Kind}}, Reason: "the first installed agent"}, nil
		}
	}
	return Choice{}, errNoAgent
}

// usable keeps the entries of chain an installed agent can run, in order.
// An entry whose model is not offered is kept without its model only when it
// named none.
func usable(chain []runnerwire.AgentModel, agents []runnerwire.Agent) []runnerwire.AgentModel {
	var out []runnerwire.AgentModel
	for _, am := range chain {
		i := slices.IndexFunc(agents, func(a runnerwire.Agent) bool { return a.Kind == am.Agent && a.Installed })
		if i < 0 {
			continue
		}
		if am.Model != "" && am.Model != "default" &&
			!slices.ContainsFunc(agents[i].Models, func(m runnerwire.Model) bool { return m.ID == am.Model }) {
			continue
		}
		if !slices.Contains(out, am) {
			out = append(out, am)
		}
	}
	return out
}

// mergeAgents merges runners' agent lists by kind: installed if installed
// anywhere, with every model any of them offers.
func mergeAgents(runners []runnerwire.Runner) []runnerwire.Agent {
	var out []runnerwire.Agent
	for _, r := range runners {
		if r.Info == nil {
			continue
		}
		for _, a := range r.Info.Agents {
			i := slices.IndexFunc(out, func(o runnerwire.Agent) bool { return o.Kind == a.Kind })
			if i < 0 {
				copied := a
				copied.Models = slices.Clone(a.Models)
				out = append(out, copied)
				continue
			}
			out[i].Installed = out[i].Installed || a.Installed
			for _, m := range a.Models {
				if !slices.ContainsFunc(out[i].Models, func(x runnerwire.Model) bool { return x.ID == m.ID }) {
					out[i].Models = append(out[i].Models, m)
				}
			}
		}
	}
	return out
}
