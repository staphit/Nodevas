package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
	"nodevas/internal/usage"
)

// history seeds n delivered executor units of agent/model at cost each,
// all first time, so the combination scores 1.0 on n samples.
func (f *fixture) history(agent, model string, n int, cost float64) {
	f.t.Helper()
	for i := 0; i < n; i++ {
		f.run(seedRun{id: fmt.Sprintf("h-%s-%d", agent, i), node: fmt.Sprintf("h-%s-%d", agent, i), role: "executor",
			agent: agent, model: model, cost: cost, created: at(8, i), seconds: 60})
	}
}

func desk(online bool, agents ...runnerwire.Agent) RunnerCandidate {
	return RunnerCandidate{ID: "rn1", Name: "desk", Online: online, Info: runnerwire.Info{
		Capabilities: []string{runnerwire.CapHeadless}, Agents: agents,
	}}
}

func agentWith(kind string, models ...string) runnerwire.Agent {
	a := runnerwire.Agent{Kind: kind, Installed: true}
	for _, m := range models {
		a.Models = append(a.Models, runnerwire.Model{ID: m})
	}
	return a
}

var claudeCodex = []runnerwire.Agent{agentWith("claude", "sonnet"), agentWith("codex", "gpt-5.5")}

func (f *fixture) autoRouting(on bool, pct int) {
	f.t.Helper()
	if err := SaveSettings(context.Background(), f.db, "u1", Settings{Project: "p", AutoRouting: on, ExplorationPct: pct}); err != nil {
		f.t.Fatalf("save settings: %v", err)
	}
}

func routeIn(role string, runners ...RunnerCandidate) RouteInput {
	return RouteInput{OwnerID: "u1", Project: "p", NodeID: "n1", Role: role, Slot: role,
		Runners: runners, Prices: usage.NewTable(nil), Now: testNow}
}

func route(t *testing.T, f *fixture, in RouteInput) runnerwire.RouteSuggestion {
	t.Helper()
	got, err := Route(context.Background(), f.db, in)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func picks(t *testing.T, f *fixture) []RoutePick {
	t.Helper()
	got, err := ListPicks(context.Background(), f.db, "u1", "p", 50)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func reasonOf(t *testing.T, p RoutePick) PickReason {
	t.Helper()
	var r PickReason
	if err := json.Unmarshal(p.Reason, &r); err != nil {
		t.Fatalf("pick reason %s: %v", p.Reason, err)
	}
	return r
}

// B-33: manual rules win, auto off is silent, and auto picks the cheapest
// combination meeting the role's tier.
func TestRouteManualWinsAutoOffCheapest(t *testing.T) {
	f := newFixture(t)
	f.history("claude", "sonnet", 10, 1.50)
	f.history("codex", "gpt-5.5", 10, 0.90)

	// Auto routing off (no settings row): nothing, and no log.
	if got := route(t, f, routeIn("executor", desk(true, claudeCodex...))); got.RuleID != "" {
		t.Fatalf("auto off picked %+v", got)
	}
	if len(picks(t, f)) != 0 {
		t.Fatal("a pick was logged with auto routing off")
	}

	f.autoRouting(true, 0)
	in := routeIn("executor", desk(true, claudeCodex...))
	in.Manual = runnerwire.RouteSuggestion{RuleID: "rule1", Match: runnerwire.RouteTag, Target: runnerwire.AgentModel{Agent: "claude", Model: "sonnet"}}
	if got := route(t, f, in); got.RuleID != "rule1" || got.Target.Agent != "claude" {
		t.Fatalf("manual rule lost: %+v", got)
	}
	logged := picks(t, f)
	if len(logged) != 1 || logged[0].Source != PickManual || reasonOf(t, logged[0]).Code != ReasonManual {
		t.Fatalf("manual pick log = %+v", logged)
	}

	got := route(t, f, routeIn("executor", desk(true, claudeCodex...)))
	if !strings.HasPrefix(got.RuleID, "auto:") || got.Match != runnerwire.RouteAuto || got.RunnerID != "rn1" ||
		got.Target != (runnerwire.AgentModel{Agent: "codex", Model: "gpt-5.5"}) {
		t.Fatalf("auto pick = %+v, want codex/gpt-5.5 on rn1", got)
	}
	logged = picks(t, f)
	if len(logged) != 2 || logged[0].Source != PickAuto || logged[0].ID != strings.TrimPrefix(got.RuleID, "auto:") ||
		logged[0].Tier != TierMid || logged[0].RequiredTier != TierMid || logged[0].Explore {
		t.Fatalf("auto pick log = %+v", logged)
	}
	if r := reasonOf(t, logged[0]); r.Code != ReasonCheapest || len(r.Candidates) != 2 {
		t.Fatalf("auto pick reason = %+v", r)
	}
}

// B-34: exploration gives probation combinations samples, deterministically.
func TestRouteExploration(t *testing.T) {
	h := fnv.New32a()
	h.Write([]byte("p\x00n1\x00executor"))
	if want := int(h.Sum32() % 100); ExploreBucket("p", "n1", "executor") != want || ExploreBucket("p", "n1", "executor") != want {
		t.Fatalf("ExploreBucket = %d, want fnv32a %% 100 = %d", ExploreBucket("p", "n1", "executor"), want)
	}

	f := newFixture(t)
	f.history("claude", "sonnet", 10, 1.50)
	f.history("codex", "gpt-5.5", 10, 0.90)
	f.history("opencode", "big", 3, 0.10)
	agents := append(append([]runnerwire.Agent{}, claudeCodex...), agentWith("opencode", "big"), agentWith("gemini", "gemini-2.5-pro"))

	f.autoRouting(true, 100)
	got := route(t, f, routeIn("executor", desk(true, agents...)))
	if got.Target != (runnerwire.AgentModel{Agent: "gemini", Model: "gemini-2.5-pro"}) {
		t.Fatalf("exploring picked %+v, want the probation combo with fewest samples (gemini, 0)", got.Target)
	}
	if p := picks(t, f)[0]; !p.Explore || reasonOf(t, p).Code != ReasonExplore || p.Tier != TierProbation {
		t.Fatalf("explore pick = %+v", p)
	}

	f.autoRouting(true, 0)
	if got := route(t, f, routeIn("executor", desk(true, agents...))); got.Target.Agent != "codex" {
		t.Fatalf("exploration 0%% picked %+v, want codex", got.Target)
	}

	// A role that needs senior never explores.
	f.autoRouting(true, 100)
	if got := route(t, f, routeIn("reviewer", desk(true, agents...))); got.RuleID != "" {
		t.Fatalf("a senior-required role explored: %+v", got)
	}
	if p := picks(t, f)[0]; p.Explore || p.Source != PickNone || reasonOf(t, p).Code != ReasonNoneMeetsTier {
		t.Fatalf("senior-required pick = %+v", p)
	}
}

// B-35: budgets, no qualifying combination, no runner, unpriced last.
func TestRouteBudgetsAndRefusals(t *testing.T) {
	f := newFixture(t)
	f.history("claude", "sonnet", 10, 1.50)
	f.history("codex", "gpt-5.5", 10, 0.90)
	f.autoRouting(true, 0)

	in := routeIn("executor", desk(true, claudeCodex...))
	in.Budget = runnerwire.BudgetStatus{Exceeded: "cost"}
	if got := route(t, f, in); got.RuleID != "" {
		t.Fatalf("over budget picked %+v", got)
	}
	if p := picks(t, f)[0]; p.Source != PickNone || reasonOf(t, p).Code != ReasonBudget {
		t.Fatalf("budget pick = %+v", p)
	}
	in = routeIn("executor", desk(true, claudeCodex...))
	in.RoleBudget = runnerwire.BudgetStatus{Exceeded: "runs"}
	if got := route(t, f, in); got.RuleID != "" || reasonOf(t, picks(t, f)[0]).Code != ReasonBudget {
		t.Fatalf("role over budget picked %+v", got)
	}

	// $1 left today: claude's 1.50 is dropped, codex's 0.90 fits.
	in = routeIn("executor", desk(true, claudeCodex...))
	in.Budget = runnerwire.BudgetStatus{Budget: runnerwire.Budget{DailyCostUSD: 2}}
	in.Budget.Used.CostUSD = 1
	if got := route(t, f, in); got.Target.Agent != "codex" {
		t.Fatalf("with $1 left picked %+v", got.Target)
	}
	dropped := false
	for _, c := range reasonOf(t, picks(t, f)[0]).Candidates {
		if c.Agent == "claude" && c.Dropped == ReasonOverBudget {
			dropped = true
		}
	}
	if !dropped {
		t.Fatalf("claude not logged as over budget: %+v", reasonOf(t, picks(t, f)[0]))
	}

	if got := route(t, f, routeIn("reviewer", desk(true, claudeCodex...))); got.RuleID != "" {
		t.Fatalf("no senior reviewer, yet picked %+v", got)
	}
	if p := picks(t, f)[0]; p.Source != PickNone || reasonOf(t, p).Code != ReasonNoneMeetsTier || p.RequiredTier != TierSenior {
		t.Fatalf("none-meets-tier pick = %+v", p)
	}

	if got := route(t, f, routeIn("executor", desk(false, claudeCodex...))); got.RuleID != "" {
		t.Fatalf("offline runner picked %+v", got)
	}
	if p := picks(t, f)[0]; reasonOf(t, p).Code != ReasonNoRunner {
		t.Fatalf("no-runner pick = %+v", p)
	}

	// No history for researchers: costs come from the price table, and an
	// unpriced combination sorts last.
	got := route(t, f, routeIn("researcher", desk(true, agentWith("mystery", "m1"), agentWith("claude", "sonnet"), agentWith("codex", "gpt-5.5"))))
	if got.Target.Agent != "codex" {
		t.Fatalf("researcher pick = %+v, want codex (cheapest estimate)", got.Target)
	}
	candidates := reasonOf(t, picks(t, f)[0]).Candidates
	if len(candidates) != 3 || candidates[len(candidates)-1].Agent != "mystery" {
		t.Fatalf("candidates = %+v, want the unpriced mystery last", candidates)
	}
}
