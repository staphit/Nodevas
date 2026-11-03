package analytics

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"nodevas/internal/runnerwire"
)

func am(agent, model string) *runnerwire.AgentModel {
	return &runnerwire.AgentModel{Agent: agent, Model: model}
}

func ruleTo(id, agent, model string) runnerwire.RouteRule {
	return runnerwire.RouteRule{ID: id, Match: runnerwire.RouteDefault, Target: runnerwire.AgentModel{Agent: agent, Model: model}}
}

func suggest(t *testing.T, f *fixture, in SuggestInput) []Suggestion {
	t.Helper()
	if in.OwnerID == "" {
		in.OwnerID, in.Project = "u1", "p"
	}
	if in.Days == 0 {
		in.Days = 30
	}
	if in.Now.IsZero() {
		in.Now = testNow
	}
	got, err := Suggestions(context.Background(), f.db, in)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func byKind(list []Suggestion, kind string) *Suggestion {
	for i := range list {
		if list[i].Kind == kind {
			return &list[i]
		}
	}
	return nil
}

// flaky seeds n delivered executor units of agent/model, the first k of
// which needed a retry after a rate limit (the failed run costs nothing).
func (f *fixture) flaky(agent, model string, n, k int, cost float64) {
	f.t.Helper()
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("f-%s-%d", agent, i)
		retryOf := ""
		if i < k {
			f.run(seedRun{id: id + "-0", node: id, role: "executor", agent: agent, model: model, status: "failed",
				failureKind: "rate_limit", created: at(7, i), seconds: 10})
			retryOf = id + "-0"
		}
		f.run(seedRun{id: id, node: id, role: "executor", agent: agent, model: model, retryOf: retryOf, cost: cost,
			created: at(8, i), seconds: 60})
	}
}

// testers seeds n delivered tester units of agent/model.
func (f *fixture) testers(agent, model string, n int) {
	f.t.Helper()
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("t-%s-%d", agent, i)
		f.run(seedRun{id: id, node: id, kind: "test", role: "tester", agent: agent, model: model, cost: 0.5, created: at(9, i), seconds: 60})
	}
}

// B-36, rule 1: a cheaper combination at the same tier.
func TestSuggestCheaperEqualTier(t *testing.T) {
	f := newFixture(t)
	f.history("claude", "sonnet", 10, 1.50)
	f.history("codex", "gpt-5.5", 10, 0.90)
	got := byKind(suggest(t, f, SuggestInput{Rules: []runnerwire.RouteRule{ruleTo("rule1", "claude", "sonnet")}}), SuggestCheaperEqualTier)
	want := &Change{Type: ChangeRoutingRuleTarget, RuleID: "rule1", From: am("claude", "sonnet"), To: am("codex", "gpt-5.5")}
	if got == nil || got.Key != "cheaper_equal_tier:rule1:codex/gpt-5.5" || !reflect.DeepEqual(got.Change, want) || fmt.Sprint(got.Params["saving"]) != "40" {
		t.Fatalf("cheaper suggestion = %+v", got)
	}

	// Just over 70% of the cost: quiet.
	g := newFixture(t)
	g.history("claude", "sonnet", 10, 1.50)
	g.history("codex", "gpt-5.5", 10, 1.06)
	if s := byKind(suggest(t, g, SuggestInput{Rules: []runnerwire.RouteRule{ruleTo("rule1", "claude", "sonnet")}}), SuggestCheaperEqualTier); s != nil {
		t.Fatalf("1.06 against 1.50 suggested %+v", s)
	}
}

// Rule 2: a rule's target that fails for infrastructure reasons.
func TestSuggestDemoteUnreliable(t *testing.T) {
	f := newFixture(t)
	f.flaky("claude", "sonnet", 10, 5, 1)
	f.history("codex", "gpt-5.5", 10, 1)
	got := byKind(suggest(t, f, SuggestInput{Rules: []runnerwire.RouteRule{ruleTo("rule1", "claude", "sonnet")}}), SuggestDemoteUnreliable)
	want := &Change{Type: ChangeRoutingRuleTarget, RuleID: "rule1", From: am("claude", "sonnet"), To: am("codex", "gpt-5.5")}
	if got == nil || got.Key != "demote_unreliable:rule1" || !reflect.DeepEqual(got.Change, want) {
		t.Fatalf("demote suggestion = %+v", got)
	}

	g := newFixture(t)
	g.flaky("claude", "sonnet", 10, 4, 1) // reliability 1 - 4/14 = 0.714
	g.history("codex", "gpt-5.5", 10, 1)
	if s := byKind(suggest(t, g, SuggestInput{Rules: []runnerwire.RouteRule{ruleTo("rule1", "claude", "sonnet")}}), SuggestDemoteUnreliable); s != nil {
		t.Fatalf("reliability 0.714 suggested %+v", s)
	}
}

// Rule 3: auto routing would have choices for two roles.
func TestSuggestEnableAutoRouting(t *testing.T) {
	f := newFixture(t)
	f.history("claude", "sonnet", 10, 1)
	f.history("codex", "gpt-5.5", 10, 1)
	f.testers("claude", "sonnet", 10)
	f.testers("codex", "gpt-5.5", 10)
	got := byKind(suggest(t, f, SuggestInput{}), SuggestEnableAutoRouting)
	if got == nil || got.Key != "enable_auto_routing:p" || !reflect.DeepEqual(got.Change, &Change{Type: ChangeAutoRouting, Project: "p", On: true}) {
		t.Fatalf("auto routing suggestion = %+v", got)
	}
	if s := byKind(suggest(t, f, SuggestInput{Settings: Settings{Project: "p", AutoRouting: true}}), SuggestEnableAutoRouting); s != nil {
		t.Fatalf("suggested while already on: %+v", s)
	}

	g := newFixture(t)
	g.history("claude", "sonnet", 10, 1)
	g.history("codex", "gpt-5.5", 10, 1)
	g.testers("claude", "sonnet", 10)
	if s := byKind(suggest(t, g, SuggestInput{}), SuggestEnableAutoRouting); s != nil {
		t.Fatalf("one tester combination suggested %+v", s)
	}
}

// Rule 4: a role that wastes much of a large spend gets a daily cap.
func TestSuggestRoleBudget(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 3; i++ {
		f.run(seedRun{id: fmt.Sprintf("ok%d", i), node: fmt.Sprintf("ok%d", i), role: "executor", cost: 1, created: at(9, i), seconds: 60})
		f.run(seedRun{id: fmt.Sprintf("bad%d", i), node: fmt.Sprintf("bad%d", i), role: "executor", status: "failed", failureKind: "task",
			cost: 1, created: at(10, i), seconds: 60})
	}
	got := byKind(suggest(t, f, SuggestInput{}), SuggestRoleBudget)
	want := &Change{Type: ChangeRoleBudget, Role: "executor", DailyCostUSD: 7.2}
	if got == nil || got.Key != "role_budget:executor" || !reflect.DeepEqual(got.Change, want) {
		t.Fatalf("role budget suggestion = %+v", got)
	}
	if s := byKind(suggest(t, f, SuggestInput{RoleDailyCost: map[string]float64{"executor": 10}}), SuggestRoleBudget); s != nil {
		t.Fatalf("suggested a cap for a role that has one: %+v", s)
	}

	g := newFixture(t)
	for i := 0; i < 3; i++ {
		g.run(seedRun{id: fmt.Sprintf("ok%d", i), node: fmt.Sprintf("ok%d", i), role: "executor", cost: 1, created: at(9, i), seconds: 60})
		g.run(seedRun{id: fmt.Sprintf("bad%d", i), node: fmt.Sprintf("bad%d", i), role: "executor", status: "failed", failureKind: "task",
			cost: 0.66, created: at(10, i), seconds: 60})
	}
	if s := byKind(suggest(t, g, SuggestInput{}), SuggestRoleBudget); s != nil {
		t.Fatalf("$4.98 of spend suggested %+v", s)
	}
}

// Rule 5: executors rarely pass first time and there is no Tester stage.
func TestSuggestTesterFirst(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 10; i++ {
		p := fmt.Sprintf("pp%d", i)
		f.pipeline(p, "p", p, "approved", 2, at(9, i))
		f.run(seedRun{id: p + "-1", node: p, pipeline: p, role: "executor", cost: 0.1, created: at(9, i), seconds: 60})
		f.run(seedRun{id: p + "-2", node: p, pipeline: p, kind: "revise", role: "executor", cost: 0.1, created: at(10, i), seconds: 60})
	}
	got := byKind(suggest(t, f, SuggestInput{}), SuggestTesterFirst)
	if got == nil || got.Key != "tester_first:p" || !reflect.DeepEqual(got.Change, &Change{Type: ChangeOpenSettings, Row: "stages"}) {
		t.Fatalf("tester-first suggestion = %+v", got)
	}
	if s := byKind(suggest(t, f, SuggestInput{TesterStage: true}), SuggestTesterFirst); s != nil {
		t.Fatalf("suggested with a Tester stage on: %+v", s)
	}
}

// Ordering, the cap of five, and a dismissal's 30 days.
func TestSuggestionsOrderCapAndDismissal(t *testing.T) {
	f := newFixture(t)
	f.history("claude", "sonnet", 10, 1.50)
	f.history("codex", "gpt-5.5", 10, 0.90)
	var rules []runnerwire.RouteRule
	for i := 6; i >= 1; i-- {
		rules = append(rules, ruleTo(fmt.Sprintf("rule%d", i), "claude", "sonnet"))
	}
	got := suggest(t, f, SuggestInput{Rules: rules})
	if len(got) != MaxSuggestions {
		t.Fatalf("%d suggestions, want the cap of %d", len(got), MaxSuggestions)
	}
	for i, s := range got {
		if want := fmt.Sprintf("cheaper_equal_tier:rule%d:codex/gpt-5.5", i+1); s.Key != want {
			t.Errorf("suggestion %d = %q, want %q", i, s.Key, want)
		}
	}

	if err := Dismiss(context.Background(), f.db, "u1", "cheaper_equal_tier:rule1:codex/gpt-5.5", testNow); err != nil {
		t.Fatal(err)
	}
	got = suggest(t, f, SuggestInput{Rules: rules})
	if len(got) == 0 || got[0].Key != "cheaper_equal_tier:rule2:codex/gpt-5.5" {
		t.Fatalf("after dismissing rule1: %+v", got)
	}
	if all := suggest(t, f, SuggestInput{Rules: rules, IncludeDismissed: true}); len(all) == 0 || all[0].Key != "cheaper_equal_tier:rule1:codex/gpt-5.5" {
		t.Fatalf("apply's recompute still hides the dismissed key: %+v", all)
	}
	later := suggest(t, f, SuggestInput{Rules: rules, Days: 60, Now: testNow.AddDate(0, 0, 31)})
	if len(later) == 0 || later[0].Key != "cheaper_equal_tier:rule1:codex/gpt-5.5" {
		t.Fatalf("31 days later the dismissal still hides it: %+v", later)
	}
}
