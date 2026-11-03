package runs

import (
	"errors"
	"testing"

	"nodevas/internal/runnerwire"
)

func roleRule(id, project, match, value string) runnerwire.RouteRule {
	return runnerwire.RouteRule{ID: id, Project: project, Match: match, Value: value, Target: am("claude", id)}
}

func TestValidateAcceptsRoleMatch(t *testing.T) {
	rules, err := ValidateRouting([]runnerwire.RouteRule{
		{Match: runnerwire.RouteRole, Value: " reviewer ", Target: am("codex", "gpt-5")},
		{Match: runnerwire.RouteRole, Value: "custom-role-lore", Project: "novel", Target: am("claude", "opus")},
	})
	if err != nil {
		t.Fatalf("role rules refused: %v", err)
	}
	if rules[0].Value != "reviewer" {
		t.Fatalf("value not trimmed: %+v", rules[0])
	}
	for name, rule := range map[string]runnerwire.RouteRule{
		"no value":     {Match: runnerwire.RouteRole, Target: am("codex", "gpt-5")},
		"unknown role": {Match: runnerwire.RouteRole, Value: "boss", Target: am("codex", "gpt-5")},
		"bad custom":   {Match: runnerwire.RouteRole, Value: "custom-role-", Target: am("codex", "gpt-5")},
	} {
		if _, err := ValidateRouting([]runnerwire.RouteRule{rule}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestRoleRuleMatchesJobRole(t *testing.T) {
	rules := []runnerwire.RouteRule{
		roleRule("default", "", runnerwire.RouteDefault, ""),
		roleRule("reviewer", "", runnerwire.RouteRole, "reviewer"),
		roleRule("qa", "", runnerwire.RouteRole, "qa"),
	}
	if got := Suggest(rules, RouteNode{Project: "web", Role: "reviewer"}); got.RuleID != "reviewer" || got.Match != runnerwire.RouteRole {
		t.Fatalf("reviewer job routed by %+v", got)
	}
	if got := Suggest(rules, RouteNode{Project: "web", Role: "qa"}); got.RuleID != "qa" {
		t.Fatalf("qa job routed by %+v", got)
	}
	// A legacy job (no role) never matches a role rule.
	if got := Suggest(rules, RouteNode{Project: "web"}); got.RuleID != "default" {
		t.Fatalf("legacy job routed by %+v", got)
	}
}

func TestExecutorPrefersTagKindOverRole(t *testing.T) {
	rules := []runnerwire.RouteRule{
		roleRule("default", "", runnerwire.RouteDefault, ""),
		roleRule("proj", "web", runnerwire.RouteProject, ""),
		roleRule("executor", "", runnerwire.RouteRole, "executor"),
		roleRule("tag", "", runnerwire.RouteTag, "ui"),
		roleRule("kind", "", runnerwire.RouteKind, "bug"),
	}
	cases := []struct {
		node RouteNode
		want string
	}{
		{RouteNode{Project: "web", Role: "executor", Tags: []string{"ui"}, Kind: "bug"}, "tag"},
		{RouteNode{Project: "web", Role: "executor", Kind: "bug"}, "kind"},
		{RouteNode{Project: "web", Role: "executor", Kind: "scene"}, "executor"},
	}
	for _, c := range cases {
		if got := Suggest(rules, c.node); got.RuleID != c.want {
			t.Errorf("%+v routed by %q, want %q", c.node, got.RuleID, c.want)
		}
	}
}

func TestNonExecutorSkipsTagKindRules(t *testing.T) {
	rules := []runnerwire.RouteRule{
		roleRule("default", "", runnerwire.RouteDefault, ""),
		roleRule("proj", "web", runnerwire.RouteProject, ""),
		roleRule("tag", "", runnerwire.RouteTag, "ui"),
		roleRule("kind", "", runnerwire.RouteKind, "bug"),
		roleRule("reviewer", "", runnerwire.RouteRole, "reviewer"),
	}
	// Tag and kind describe the work, not who reviews it.
	if got := Suggest(rules, RouteNode{Project: "web", Role: "reviewer", Tags: []string{"ui"}, Kind: "bug"}); got.RuleID != "reviewer" {
		t.Fatalf("reviewer routed by %q", got.RuleID)
	}
	if got := Suggest(rules, RouteNode{Project: "web", Role: "tester", Tags: []string{"ui"}, Kind: "bug"}); got.RuleID != "proj" {
		t.Fatalf("tester without a rule routed by %q, want the project rule", got.RuleID)
	}
	if got := Suggest(rules, RouteNode{Project: "docs", Role: "planner", Tags: []string{"ui"}}); got.RuleID != "default" {
		t.Fatalf("planner routed by %q, want default", got.RuleID)
	}
}

func TestProjectRoleRuleBeatsGlobal(t *testing.T) {
	rules := []runnerwire.RouteRule{
		roleRule("global", "", runnerwire.RouteRole, "reviewer"),
		roleRule("web", "web", runnerwire.RouteRole, "reviewer"),
	}
	if got := Suggest(rules, RouteNode{Project: "web", Role: "reviewer"}); got.RuleID != "web" {
		t.Fatalf("web reviewer routed by %q", got.RuleID)
	}
	if got := Suggest(rules, RouteNode{Project: "api", Role: "reviewer"}); got.RuleID != "global" {
		t.Fatalf("api reviewer routed by %q", got.RuleID)
	}
}
