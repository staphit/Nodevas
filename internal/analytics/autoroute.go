package analytics

import (
	"context"
	"hash/fnv"
	"math"
	"slices"
	"sort"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/runnerwire"
	"nodevas/internal/usage"
)

// RunnerCandidate is one of the owner's runners as auto routing sees it.
type RunnerCandidate struct {
	ID     string
	Name   string
	Online bool
	Info   runnerwire.Info
}

// RouteInput is one routing decision's inputs. The caller resolves the
// manual rules (runs.Suggest) and the budgets, so this package needs
// neither internal/runs nor internal/runners.
type RouteInput struct {
	OwnerID, Project, NodeID string
	// Role is the run's role id; Slot its built-in base role.
	Role, Slot string
	// Manual is what the owner's routing rules picked (RuleID "" none).
	Manual     runnerwire.RouteSuggestion
	Runners    []RunnerCandidate
	Budget     runnerwire.BudgetStatus
	RoleBudget runnerwire.BudgetStatus
	Prices     usage.Table
	Now        time.Time
}

// estimateTokens is the job size an unscored combination is priced at.
var estimateTokens = runnerwire.Usage{InputTokens: 200_000, OutputTokens: 20_000}

// AutoRuleID is the RouteSuggestion.RuleID prefix of an auto pick.
const AutoRuleID = "auto:"

// candidate is one combination with its ranking fields.
type candidate struct {
	Candidate
	rank int
	cost float64 // +Inf unpriced
}

// Route picks an agent and model for a node: the owner's manual rule when
// one matches, else (auto routing on) the cheapest combination meeting the
// role's required tier. Every decision while auto routing is on is logged.
func Route(ctx context.Context, database *db.DB, in RouteInput) (runnerwire.RouteSuggestion, error) {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	settings, err := LoadSettings(ctx, database, in.OwnerID, in.Project)
	if err != nil {
		return in.Manual, err
	}
	slot := in.Slot
	if slot == "" {
		slot = in.Role
	}
	required := RequiredTier(settings, in.Role, slot)
	base := RoutePick{OwnerID: in.OwnerID, Project: in.Project, NodeID: in.NodeID, Role: in.Role, RequiredTier: required}

	// Manual rules always win.
	if in.Manual.RuleID != "" {
		if settings.AutoRouting {
			p := base
			p.Source, p.RuleID, p.RunnerID = PickManual, in.Manual.RuleID, in.Manual.RunnerID
			p.Agent, p.Model, p.RequiredTier = in.Manual.Target.Agent, in.Manual.Target.Model, ""
			if _, err := logPick(ctx, database, p, PickReason{Code: ReasonManual}, now); err != nil {
				return in.Manual, err
			}
		}
		return in.Manual, nil
	}
	if !settings.AutoRouting {
		return runnerwire.RouteSuggestion{}, nil
	}
	none := func(code string, candidates []Candidate) (runnerwire.RouteSuggestion, error) {
		p := base
		p.Source = PickNone
		_, err := logPick(ctx, database, p, PickReason{Code: code, Candidates: candidates}, now)
		return runnerwire.RouteSuggestion{}, err
	}
	if in.Budget.Exceeded != "" || in.RoleBudget.Exceeded != "" {
		return none(ReasonBudget, nil)
	}

	runners := slices.Clone(in.Runners)
	sort.Slice(runners, func(i, j int) bool { return runners[i].ID < runners[j].ID })
	var online []RunnerCandidate
	for _, r := range runners {
		if r.Online && slices.Contains(r.Info.Capabilities, runnerwire.CapHeadless) {
			online = append(online, r)
		}
	}
	if len(online) == 0 {
		return none(ReasonNoRunner, nil)
	}

	cards, err := ScorecardsFor(ctx, database, Query{OwnerID: in.OwnerID, Days: WindowDays, Track: TrackAll, Now: now})
	if err != nil {
		return runnerwire.RouteSuggestion{}, err
	}
	rows := map[[2]string]ScorecardRow{}
	for _, r := range cards.Rows {
		if r.Role == in.Role {
			rows[[2]string{r.Agent, r.Model}] = r
		}
	}
	remaining := math.Inf(1)
	if in.Budget.Budget.DailyCostUSD > 0 {
		remaining = in.Budget.Budget.DailyCostUSD - in.Budget.Used.CostUSD
	}
	if in.RoleBudget.Budget.DailyCostUSD > 0 {
		remaining = math.Min(remaining, in.RoleBudget.Budget.DailyCostUSD-in.RoleBudget.Used.CostUSD)
	}

	var all []candidate
	for _, r := range online {
		for _, agent := range r.Info.Agents {
			if !agent.Installed || agent.Kind == "" {
				continue
			}
			models := []string{}
			for _, m := range agent.Models {
				models = append(models, m.ID)
			}
			if len(models) == 0 {
				models = []string{""}
			}
			seen := map[string]bool{}
			for _, model := range models {
				if model == "" {
					model = "default"
				}
				if seen[model] {
					continue
				}
				seen[model] = true
				c := candidate{Candidate: Candidate{RunnerID: r.ID, Agent: agent.Kind, Model: model, Tier: TierProbation}}
				row, scored := rows[[2]string{agent.Kind, model}]
				if scored {
					c.Samples, c.Score, c.Tier = row.Samples, row.Score, row.Tier
				}
				switch {
				case scored && c.Samples > 0 && row.CostPerDelivered != nil:
					c.cost = *row.CostPerDelivered
				case scored && c.Samples > 0:
					c.cost = math.Inf(1)
				default:
					cost, _, priced := usage.RunCost(in.Prices, agent.Kind, model, estimateTokens)
					c.cost = cost
					if !priced {
						c.cost = math.Inf(1)
					}
				}
				c.rank = TierRank(c.Tier)
				if !math.IsInf(remaining, 1) && c.cost > remaining {
					c.Dropped = ReasonOverBudget
				}
				c.Cost = round6(c.cost)
				if math.IsInf(c.cost, 1) {
					c.Cost = -1
				}
				all = append(all, c)
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return better(all[i], all[j]) })
	logged := make([]Candidate, 0, len(all))
	for _, c := range all {
		logged = append(logged, c.Candidate)
	}

	pick := func(c candidate, code string, explore bool) (runnerwire.RouteSuggestion, error) {
		p := base
		p.Source, p.RunnerID, p.Agent, p.Model, p.Tier, p.Explore = PickAuto, c.RunnerID, c.Agent, c.Model, c.Tier, explore
		id, err := logPick(ctx, database, p, PickReason{Code: code, Candidates: logged}, now)
		if err != nil {
			return runnerwire.RouteSuggestion{}, err
		}
		return runnerwire.RouteSuggestion{RuleID: AutoRuleID + id, Match: runnerwire.RouteAuto, RunnerID: c.RunnerID,
			Target: runnerwire.AgentModel{Agent: c.Agent, Model: c.Model}}, nil
	}

	pct := settings.ExplorationPct
	if required != TierSenior && pct > 0 && ExploreBucket(in.Project, in.NodeID, in.Role) < pct {
		var best *candidate
		for i := range all {
			c := &all[i]
			if c.Dropped != "" || c.Tier != TierProbation || c.Samples >= MinSamples {
				continue
			}
			if best == nil || exploreBefore(*c, *best) {
				best = c
			}
		}
		if best != nil {
			return pick(*best, ReasonExplore, true)
		}
	}
	need := TierRank(required)
	for _, c := range all {
		if c.Dropped == "" && c.rank >= need {
			return pick(c, ReasonCheapest, false)
		}
	}
	return none(ReasonNoneMeetsTier, logged)
}

// better ranks combinations: cheapest, then higher tier, then higher score,
// then by name.
func better(a, b candidate) bool {
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	if a.rank != b.rank {
		return a.rank > b.rank
	}
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if a.Agent != b.Agent {
		return a.Agent < b.Agent
	}
	if a.Model != b.Model {
		return a.Model < b.Model
	}
	return a.RunnerID < b.RunnerID
}

// exploreBefore ranks probation combinations to explore: fewest samples,
// then cheapest, then by name.
func exploreBefore(a, b candidate) bool {
	if a.Samples != b.Samples {
		return a.Samples < b.Samples
	}
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	if a.Agent != b.Agent {
		return a.Agent < b.Agent
	}
	if a.Model != b.Model {
		return a.Model < b.Model
	}
	return a.RunnerID < b.RunnerID
}

// ExploreBucket is the deterministic 0..99 bucket exploration compares
// with the exploration share: fnv32a(project NUL node NUL role) % 100.
func ExploreBucket(project, nodeID, role string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(project + "\x00" + nodeID + "\x00" + role))
	return int(h.Sum32() % 100)
}
