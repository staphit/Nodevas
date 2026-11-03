package analytics

import (
	"context"
	"math"
	"sort"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
)

// SuggestInput is what the suggestion rules read besides run history.
type SuggestInput struct {
	OwnerID, Project string
	Days             int
	Now              time.Time
	Rules            []runnerwire.RouteRule
	Settings         Settings
	// RoleDailyCost is each role's project-"" dailyCostUsd budget.
	RoleDailyCost map[string]float64
	// TesterStage: the project's workflow has a Tester stage.
	TesterStage bool
	// IncludeDismissed ignores dismissals (apply recomputes with it).
	IncludeDismissed bool
}

// Suggestion thresholds.
const (
	suggestMinSamples      = 10
	cheaperShare           = 0.70
	unreliableBelow        = 0.70
	reliableAlternative    = 0.90
	roleBudgetMinCost      = 5.00
	roleBudgetWasteShare   = 0.40
	roleBudgetHeadroom     = 1.2
	testerFirstMinUnits    = 10
	testerFirstPassBelow   = 0.50
	DismissDays            = 30
	enableAutoMinRoles     = 2
	enableAutoMinCombosPer = 2
)

// ruleRole is the role a routing rule's target works in.
func ruleRole(rule runnerwire.RouteRule) string {
	if rule.Match == runnerwire.RouteRole && rule.Value != "" {
		return rule.Value
	}
	return engine.RoleExecutor
}

func modelOrDefault(model string) string {
	if model == "" {
		return "default"
	}
	return model
}

func label(agent, model string) string { return agent + "/" + model }

// Suggestions computes the deterministic suggestions: by rule, then key, at
// most MaxSuggestions, dismissed keys hidden for 30 days.
func Suggestions(ctx context.Context, database *db.DB, in SuggestInput) ([]Suggestion, error) {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	cards, err := ScorecardsFor(ctx, database, Query{OwnerID: in.OwnerID, Days: in.Days, Track: TrackAll, Now: now})
	if err != nil {
		return nil, err
	}
	byRole := map[string][]ScorecardRow{}
	for _, r := range cards.Rows {
		byRole[r.Role] = append(byRole[r.Role], r)
	}
	find := func(role, agent, model string) (ScorecardRow, bool) {
		for _, r := range byRole[role] {
			if r.Agent == agent && r.Model == model {
				return r, true
			}
		}
		return ScorecardRow{}, false
	}
	cpd := func(r ScorecardRow) float64 {
		if r.CostPerDelivered == nil {
			return math.Inf(1)
		}
		return *r.CostPerDelivered
	}
	reliability := func(r ScorecardRow) float64 {
		if r.Reliability == nil {
			return 0
		}
		return *r.Reliability
	}
	groups := make([][]Suggestion, 5)

	rules := append([]runnerwire.RouteRule(nil), in.Rules...)
	sort.SliceStable(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	for _, rule := range rules {
		if rule.Project != "" && rule.Project != in.Project || rule.Target.Agent == "" {
			continue
		}
		role := ruleRole(rule)
		from := runnerwire.AgentModel{Agent: rule.Target.Agent, Model: rule.Target.Model}
		x, ok := find(role, rule.Target.Agent, modelOrDefault(rule.Target.Model))
		if !ok || x.Samples < suggestMinSamples {
			continue
		}
		// Rule 1: as good, and at most 70% of the cost.
		var cheapest *ScorecardRow
		for i := range byRole[role] {
			y := byRole[role][i]
			if y.Agent == x.Agent && y.Model == x.Model {
				continue
			}
			if TierRank(y.Tier) < TierRank(x.Tier) || y.Samples < suggestMinSamples || math.IsInf(cpd(x), 1) {
				continue
			}
			if cpd(y) > cheaperShare*cpd(x) {
				continue
			}
			if cheapest == nil || cpd(y) < cpd(*cheapest) {
				cheapest = &byRole[role][i]
			}
		}
		if cheapest != nil {
			to := runnerwire.AgentModel{Agent: cheapest.Agent, Model: cheapest.Model}
			groups[0] = append(groups[0], Suggestion{
				Key:  SuggestCheaperEqualTier + ":" + rule.ID + ":" + label(to.Agent, to.Model),
				Kind: SuggestCheaperEqualTier,
				Params: map[string]any{"ruleId": rule.ID, "role": role, "from": label(from.Agent, from.Model),
					"to": label(to.Agent, to.Model), "saving": int(math.Round((1 - cpd(*cheapest)/cpd(x)) * 100))},
				Change: &Change{Type: ChangeRoutingRuleTarget, RuleID: rule.ID, From: &from, To: &to},
			})
		}
		// Rule 2: the target fails for infrastructure reasons too often.
		if reliability(x) < unreliableBelow {
			var alt *ScorecardRow
			for i := range byRole[role] {
				y := byRole[role][i]
				if y.Agent == x.Agent && y.Model == x.Model {
					continue
				}
				if reliability(y) < reliableAlternative || TierRank(y.Tier) < TierRank(x.Tier) || y.Samples < suggestMinSamples {
					continue
				}
				if alt == nil || cpd(y) < cpd(*alt) {
					alt = &byRole[role][i]
				}
			}
			s := Suggestion{
				Key:  SuggestDemoteUnreliable + ":" + rule.ID,
				Kind: SuggestDemoteUnreliable,
				Params: map[string]any{"ruleId": rule.ID, "role": role, "from": label(from.Agent, from.Model),
					"rate": int(math.Round((1 - reliability(x)) * 100))},
			}
			if alt != nil {
				to := runnerwire.AgentModel{Agent: alt.Agent, Model: alt.Model}
				s.Params["to"] = label(to.Agent, to.Model)
				s.Change = &Change{Type: ChangeRoutingRuleTarget, RuleID: rule.ID, From: &from, To: &to}
			}
			groups[1] = append(groups[1], s)
		}
	}

	// Rule 3: auto routing would have a choice for two roles or more.
	if !in.Settings.AutoRouting {
		roles := 0
		for role, list := range byRole {
			need := TierRank(RequiredTier(in.Settings, role, slotOf(role)))
			combos := 0
			for _, r := range list {
				if r.Samples >= suggestMinSamples && TierRank(r.Tier) >= need {
					combos++
				}
			}
			if combos >= enableAutoMinCombosPer {
				roles++
			}
		}
		if roles >= enableAutoMinRoles {
			groups[2] = append(groups[2], Suggestion{
				Key: SuggestEnableAutoRouting + ":" + in.Project, Kind: SuggestEnableAutoRouting,
				Params: map[string]any{"count": roles, "project": in.Project},
				Change: &Change{Type: ChangeAutoRouting, Project: in.Project, On: true},
			})
		}
	}

	// Rules 4 and 5 read the project's own history.
	w := windowOf(in.Days, now)
	h, err := loadHistory(ctx, database, filter{project: in.Project, viewer: in.OwnerID, since: w.sinceStamp()})
	if err != nil {
		return nil, err
	}
	units := buildUnits(h)
	unitOf := map[string]*unit{}
	for _, u := range units {
		for _, r := range u.runs {
			unitOf[r.ID] = u
		}
	}
	roleCost := map[string]float64{}
	roleDays := map[string]map[string]float64{}
	for _, u := range units {
		if !u.ended {
			continue
		}
		for _, r := range u.runs {
			if r.isVerify() {
				continue
			}
			role := r.roleKey()
			roleCost[role] += r.Cost
			if roleDays[role] == nil {
				roleDays[role] = map[string]float64{}
			}
			if r.Cost > 0 && len(r.Created) >= 10 {
				roleDays[role][r.Created[:10]] += r.Cost
			}
		}
	}
	roleWaste := map[string]float64{}
	for _, wr := range wasteOf(h, unitOf) {
		roleWaste[wr.r.roleKey()] += wr.r.Cost
	}
	roleNames := make([]string, 0, len(roleCost))
	for role := range roleCost {
		roleNames = append(roleNames, role)
	}
	sort.Strings(roleNames)
	for _, role := range roleNames {
		cost := roleCost[role]
		if cost < roleBudgetMinCost || roleWaste[role]/cost < roleBudgetWasteShare || in.RoleDailyCost[role] > 0 {
			continue
		}
		days := roleDays[role]
		if len(days) == 0 {
			continue
		}
		var spend float64
		for _, v := range days {
			spend += v
		}
		avg := spend / float64(len(days))
		daily := math.Ceil(math.Round(avg*roleBudgetHeadroom*100*1e6)/1e6) / 100
		groups[3] = append(groups[3], Suggestion{
			Key: SuggestRoleBudget + ":" + role, Kind: SuggestRoleBudget,
			Params: map[string]any{"role": role, "share": int(math.Round(roleWaste[role] / cost * 100)), "cost": daily},
			Change: &Change{Type: ChangeRoleBudget, Role: role, DailyCostUSD: daily},
		})
	}

	if !in.TesterStage {
		execUnits, firstPass := 0, 0
		for _, u := range units {
			if !u.ended || u.execOwner == nil || slotOf(u.execOwner.roleKey()) != engine.RoleExecutor {
				continue
			}
			execUnits++
			if u.delivered && u.revise == 0 {
				firstPass++
			}
		}
		if execUnits >= testerFirstMinUnits && float64(firstPass)/float64(execUnits) < testerFirstPassBelow {
			groups[4] = append(groups[4], Suggestion{
				Key: SuggestTesterFirst + ":" + in.Project, Kind: SuggestTesterFirst,
				Params: map[string]any{"rate": int(math.Round(float64(firstPass) / float64(execUnits) * 100)), "project": in.Project},
				Change: &Change{Type: ChangeOpenSettings, Row: "stages"},
			})
		}
	}

	hidden := map[string]bool{}
	if !in.IncludeDismissed {
		hidden, err = dismissed(ctx, database, in.OwnerID, now)
		if err != nil {
			return nil, err
		}
	}
	out := []Suggestion{}
	for _, group := range groups {
		sort.SliceStable(group, func(i, j int) bool { return group[i].Key < group[j].Key })
		for _, s := range group {
			if hidden[s.Key] {
				continue
			}
			if len(out) < MaxSuggestions {
				out = append(out, s)
			}
		}
	}
	return out, nil
}

// dismissed is owner's keys dismissed within the last 30 days.
func dismissed(ctx context.Context, database *db.DB, ownerID string, now time.Time) (map[string]bool, error) {
	rows, err := database.QueryContext(ctx, `SELECT key FROM analytics_dismissed WHERE owner_id = ? AND dismissed_at > ?`,
		ownerID, formatStamp(now.AddDate(0, 0, -DismissDays)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		out[key] = true
	}
	return out, rows.Err()
}

// MaxSuggestionKey caps a dismissed key.
const MaxSuggestionKey = 300

// Dismiss hides a suggestion key for owner for 30 days from now.
func Dismiss(ctx context.Context, database *db.DB, ownerID, key string, now time.Time) error {
	if ownerID == "" || key == "" || len(key) > MaxSuggestionKey {
		return ErrInvalid
	}
	_, err := database.ExecContext(ctx, `
INSERT INTO analytics_dismissed (owner_id, key, dismissed_at) VALUES (?, ?, ?)
ON CONFLICT (owner_id, key) DO UPDATE SET dismissed_at = excluded.dismissed_at`, ownerID, key, formatStamp(now))
	return err
}
