package runs

import (
	"context"
	"errors"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

func am(agent, model string) runnerwire.AgentModel {
	return runnerwire.AgentModel{Agent: agent, Model: model}
}

func TestValidateRoutingFillsIDsAndRefusesJunk(t *testing.T) {
	rules, err := ValidateRouting([]runnerwire.RouteRule{
		{Match: runnerwire.RouteTag, Value: " ui ", Target: am("claude", "opus")},
		{ID: "mine", Match: runnerwire.RouteDefault, Value: "ignored", Target: am("codex", "gpt-5"),
			Fallbacks: []runnerwire.AgentModel{am("claude", "sonnet")},
			Pipeline:  &runnerwire.Pipeline{Implement: am("claude", "sonnet"), Review: am("codex", "gpt-5"), MaxRounds: 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rules[0].ID == "" || rules[0].Value != "ui" || rules[1].ID != "mine" || rules[1].Value != "" {
		t.Fatalf("rules = %+v", rules)
	}

	long := strings.Repeat("x", MaxRouteName+1)
	ok := runnerwire.RouteRule{Match: runnerwire.RouteDefault, Target: am("claude", "opus")}
	with := func(edit func(*runnerwire.RouteRule)) []runnerwire.RouteRule {
		rule := ok
		edit(&rule)
		return []runnerwire.RouteRule{rule}
	}
	tooMany := make([]runnerwire.RouteRule, runnerwire.MaxRouteRules+1)
	for i := range tooMany {
		tooMany[i] = ok
	}
	chain := make([]runnerwire.AgentModel, runnerwire.MaxFallbacks+1)
	for i := range chain {
		chain[i] = am("claude", "opus")
	}
	for name, rules := range map[string][]runnerwire.RouteRule{
		"too many rules":     tooMany,
		"unknown match":      with(func(r *runnerwire.RouteRule) { r.Match = "weekday" }),
		"tag without value":  with(func(r *runnerwire.RouteRule) { r.Match = runnerwire.RouteTag }),
		"project no project": with(func(r *runnerwire.RouteRule) { r.Match = runnerwire.RouteProject }),
		"empty agent":        with(func(r *runnerwire.RouteRule) { r.Target.Agent = "" }),
		"empty model":        with(func(r *runnerwire.RouteRule) { r.Target.Model = " " }),
		"long agent":         with(func(r *runnerwire.RouteRule) { r.Target.Agent = long }),
		"long id":            with(func(r *runnerwire.RouteRule) { r.ID = long }),
		"long chain":         with(func(r *runnerwire.RouteRule) { r.Fallbacks = chain }),
		"bad fallback":       with(func(r *runnerwire.RouteRule) { r.Fallbacks = []runnerwire.AgentModel{am("", "x")} }),
		"zero rounds": with(func(r *runnerwire.RouteRule) {
			r.Pipeline = &runnerwire.Pipeline{Implement: am("a", "b"), Review: am("c", "d")}
		}),
		"four rounds": with(func(r *runnerwire.RouteRule) {
			r.Pipeline = &runnerwire.Pipeline{Implement: am("a", "b"), Review: am("c", "d"), MaxRounds: 4}
		}),
		"review without model": with(func(r *runnerwire.RouteRule) {
			r.Pipeline = &runnerwire.Pipeline{Implement: am("a", "b"), Review: am("c", ""), MaxRounds: 1}
		}),
		"duplicate ids": {{ID: "a", Match: "default", Target: am("x", "y")}, {ID: "a", Match: "default", Target: am("x", "y")}},
	} {
		if _, err := ValidateRouting(rules); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestSuggestOrder(t *testing.T) {
	rule := func(id, project, match, value string) runnerwire.RouteRule {
		return runnerwire.RouteRule{ID: id, Project: project, Match: match, Value: value, Target: am("claude", id)}
	}
	rules := []runnerwire.RouteRule{
		rule("default", "", runnerwire.RouteDefault, ""),
		rule("proj", "web", runnerwire.RouteProject, ""),
		rule("kind-global", "", runnerwire.RouteKind, "bug"),
		rule("kind-web", "web", runnerwire.RouteKind, "bug"),
		rule("tag-other", "api", runnerwire.RouteTag, "ui"),
		rule("tag", "", runnerwire.RouteTag, "UI"),
		rule("task", "", runnerwire.RouteKind, "task"),
		rule("review", "", runnerwire.RouteKind, "review"),
		rule("custom", "", runnerwire.RouteKind, "custom-type-3"),
	}
	cases := []struct {
		name string
		node RouteNode
		want string
	}{
		{"tag beats everything", RouteNode{Project: "web", Kind: "bug", Tags: []string{"x", "ui"}}, "tag"},
		{"a rule for another project never matches", RouteNode{Project: "api", Kind: "bug", Tags: []string{"ui"}}, "tag-other"},
		{"project-scoped kind beats global kind", RouteNode{Project: "web", Kind: "bug"}, "kind-web"},
		{"global kind elsewhere", RouteNode{Project: "docs", Kind: "Bug"}, "kind-global"},
		{"no kind is a task", RouteNode{Project: "web"}, "task"},
		{"project beats default", RouteNode{Project: "web", Kind: "scene"}, "proj"},
		{"default last", RouteNode{Project: "docs", Kind: "scene"}, "default"},
		{"an alias rule matches its built-in", RouteNode{Project: "docs", Kind: "gate"}, "review"},
		{"a custom type id matches", RouteNode{Project: "docs", Kind: "custom-type-3"}, "custom"},
	}
	for _, tc := range cases {
		got := Suggest(rules, tc.node)
		if got.RuleID != tc.want || got.Target.Model != tc.want {
			t.Errorf("%s: got %+v, want %s", tc.name, got, tc.want)
		}
	}
	if got := Suggest(rules[1:2], RouteNode{Project: "docs"}); got.RuleID != "" || got.Match != "" {
		t.Fatalf("no match = %+v", got)
	}
	// First match in list order within one kind.
	two := []runnerwire.RouteRule{rule("first", "", runnerwire.RouteDefault, ""), rule("second", "", runnerwire.RouteDefault, "")}
	if got := Suggest(two, RouteNode{Project: "x"}); got.RuleID != "first" {
		t.Fatalf("list order = %+v", got)
	}
}

func TestRoutingStoreReplacesTheList(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if rules, err := s.Routing(ctx, "u-ann"); err != nil || len(rules) != 0 {
		t.Fatalf("fresh = %+v %v", rules, err)
	}
	stored, err := s.SetRouting(ctx, "u-ann", []runnerwire.RouteRule{
		{Match: runnerwire.RouteDefault, Target: am("claude", "opus")},
		{ID: "b", Match: runnerwire.RouteTag, Value: "ui", Target: am("codex", "gpt-5")},
	})
	if err != nil || len(stored) != 2 || stored[0].ID == "" {
		t.Fatalf("set = %+v %v", stored, err)
	}
	if _, err := s.SetRouting(ctx, "u-bob", []runnerwire.RouteRule{{Match: runnerwire.RouteDefault, Target: am("x", "y")}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Routing(ctx, "u-ann")
	if err != nil || len(got) != 2 || got[0].ID != stored[0].ID || got[1].ID != "b" {
		t.Fatalf("read back = %+v %v", got, err)
	}
	if _, err := s.SetRouting(ctx, "u-ann", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Routing(ctx, "u-ann"); len(got) != 0 {
		t.Fatalf("after clearing = %+v", got)
	}
	if got, _ := s.Routing(ctx, "u-bob"); len(got) != 1 {
		t.Fatalf("bob's rules = %+v", got)
	}
}

func TestFinishStoresFailureKindAndVerdict(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	start := func() Record {
		r := createRun(t, s, "n1")
		if _, err := s.Transition(ctx, r.ID, runnerwire.RunOffered, Change{}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Transition(ctx, r.ID, runnerwire.RunRunning, Change{}); err != nil {
			t.Fatal(err)
		}
		return r
	}
	failed, err := s.Finish(ctx, start().ID, "r1", runnerwire.RunResult{Status: runnerwire.RunFailed, FailureKind: runnerwire.FailureQuota})
	if err != nil || failed.FailureKind != runnerwire.FailureQuota {
		t.Fatalf("failed = %+v %v", failed, err)
	}
	odd, _ := s.Finish(ctx, start().ID, "r1", runnerwire.RunResult{Status: runnerwire.RunFailed, FailureKind: "cosmic_rays"})
	if odd.FailureKind != runnerwire.FailureTask {
		t.Fatalf("unknown failure kind stored as %q", odd.FailureKind)
	}
	ok, _ := s.Finish(ctx, start().ID, "r1", runnerwire.RunResult{
		Status: runnerwire.RunSucceeded, FailureKind: runnerwire.FailureQuota, Verdict: runnerwire.VerdictChanges,
	})
	if ok.FailureKind != "" || ok.Verdict != runnerwire.VerdictChanges {
		t.Fatalf("succeeded = %+v", ok)
	}
	junk, _ := s.Finish(ctx, start().ID, "r1", runnerwire.RunResult{Status: runnerwire.RunSucceeded, Verdict: "lgtm"})
	if junk.Verdict != "" {
		t.Fatalf("unknown verdict stored as %q", junk.Verdict)
	}
}

func TestCreateKeepsRoutingFields(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r, err := s.Create(ctx, New{
		Project: ".", NodeID: "n1", NodeTitle: "T", RunnerID: "r1", OwnerID: "u", Agent: "claude",
		RepoID: "repo", CreatedBy: "u", JobPrompt: "# T", Kind: runnerwire.JobRevise,
		JobBranch: "agent/n1", JobPRURL: "https://github.com/o/r/pull/1", Review: "fix it",
		RetryOf: "older", PipelineID: "p1", Fallbacks: []runnerwire.AgentModel{am("codex", "gpt-5")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != runnerwire.JobRevise || r.RetryOf != "older" || r.PipelineID != "p1" ||
		len(r.Fallbacks) != 1 || r.Fallbacks[0].Agent != "codex" {
		t.Fatalf("record = %+v", r)
	}
	job := r.Job()
	if job.Kind != runnerwire.JobRevise || job.Branch != "agent/n1" || job.PRURL == "" || job.Review != "fix it" {
		t.Fatalf("job = %+v", job)
	}
	if _, err := s.Create(ctx, New{Project: ".", NodeID: "n", RunnerID: "r", OwnerID: "u", Agent: "a",
		RepoID: "x", CreatedBy: "u", Kind: "deploy"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown kind err = %v", err)
	}
}

func TestNoteOnlyOnEndedRuns(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := createRun(t, s, "n1")
	if err := s.Note(ctx, r.ID, "too early"); err != nil {
		t.Fatal(err)
	}
	if events, _, _ := s.Events(ctx, r.ID, 0, 10); len(events) != 0 {
		t.Fatalf("noted a queued run: %+v", events)
	}
	if _, err := s.Transition(ctx, r.ID, runnerwire.RunOffered, Change{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, r.ID, runnerwire.RunRunning, Change{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendEvents(ctx, r.ID, []runnerwire.RunEvent{{Seq: 1, Type: runnerwire.EventText, Text: "hi"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Finish(ctx, r.ID, "r1", runnerwire.RunResult{Status: runnerwire.RunFailed}); err != nil {
		t.Fatal(err)
	}
	if err := s.Note(ctx, r.ID, "falling back"); err != nil {
		t.Fatal(err)
	}
	events, _, _ := s.Events(ctx, r.ID, 0, 10)
	if len(events) != 2 || events[1].Seq != 2 || events[1].Type != runnerwire.EventLog || events[1].Text != "hub: falling back" {
		t.Fatalf("events = %+v", events)
	}
}

func TestRevisePromptKeepsTheReview(t *testing.T) {
	got := RevisePrompt(strings.Repeat("x", runnerwire.MaxPromptBytes), "Rename the helper.\nVERDICT: changes")
	if len(got) > runnerwire.MaxPromptBytes || !strings.HasSuffix(got, "Review to address:\nRename the helper.\nVERDICT: changes") {
		t.Fatalf("revise prompt (%d bytes) ends %q", len(got), got[len(got)-80:])
	}
}

func TestPipelineStore(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	p, err := s.CreatePipeline(ctx, NewPipeline{
		OwnerID: "u", CreatedBy: "u", Project: ".", NodeID: "n1",
		Pipeline: runnerwire.Pipeline{Implement: am("claude", "opus"), Review: am("codex", "gpt-5"), MaxRounds: 2},
	})
	if err != nil || p.Status != runnerwire.PipelineRunning || p.Round != 1 || len(p.RunIDs) != 0 {
		t.Fatalf("created = %+v %v", p, err)
	}
	if claimed, _ := s.ClaimPR(ctx, p.ID, "agent/n1", "https://github.com/o/r/pull/1"); !claimed {
		t.Fatal("first claim refused")
	}
	if claimed, _ := s.ClaimPR(ctx, p.ID, "agent/n1", "https://github.com/o/r/pull/1"); claimed {
		t.Fatal("second claim accepted")
	}
	if moved, _ := s.NextRound(ctx, p.ID, 1); !moved {
		t.Fatal("next round refused")
	}
	if moved, _ := s.NextRound(ctx, p.ID, 1); moved {
		t.Fatal("stale round moved")
	}
	if ended, _ := s.EndPipeline(ctx, p.ID, runnerwire.PipelineApproved, "ok"); !ended {
		t.Fatal("end refused")
	}
	if ended, _ := s.EndPipeline(ctx, p.ID, runnerwire.PipelineFailed, "late"); ended {
		t.Fatal("ended twice")
	}
	got, err := s.Pipeline(ctx, p.ID)
	if err != nil || got.Status != runnerwire.PipelineApproved || got.Round != 2 || got.PRURL == "" || got.Reason != "ok" {
		t.Fatalf("read back = %+v %v", got, err)
	}
	if _, err := s.Pipeline(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing err = %v", err)
	}
}
