package runs

import (
	"context"
	"errors"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

func TestValidatePipelineReviewers(t *testing.T) {
	base := func(edit func(*runnerwire.Pipeline)) runnerwire.Pipeline {
		p := runnerwire.Pipeline{Implement: am("claude", "opus"), MaxRounds: 1}
		edit(&p)
		return p
	}
	ok := map[string]runnerwire.Pipeline{
		"w5 single review": base(func(p *runnerwire.Pipeline) { p.Review = am("codex", "gpt-5") }),
		"implement only":   base(func(*runnerwire.Pipeline) {}),
		"human only":       base(func(p *runnerwire.Pipeline) { p.Human = true }),
		"reviewers, all": base(func(p *runnerwire.Pipeline) {
			p.Reviewers = []runnerwire.AgentModel{am("codex", "a"), am("claude", "b")}
		}),
		"reviewers, quorum": base(func(p *runnerwire.Pipeline) {
			p.Reviewers, p.Quorum, p.Human = []runnerwire.AgentModel{am("codex", "a"), am("claude", "b")}, 1, true
		}),
	}
	for name, p := range ok {
		if err := ValidatePipeline(p, true); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	bad := map[string]runnerwire.Pipeline{
		"quorum over": base(func(p *runnerwire.Pipeline) { p.Reviewers, p.Quorum = []runnerwire.AgentModel{am("codex", "a")}, 2 }),
		"quorum negative": base(func(p *runnerwire.Pipeline) {
			p.Reviewers, p.Quorum = []runnerwire.AgentModel{am("codex", "a")}, -1
		}),
		"quorum without reviewers": base(func(p *runnerwire.Pipeline) { p.Quorum = 1 }),
		"too many":                 base(func(p *runnerwire.Pipeline) { p.Reviewers = make([]runnerwire.AgentModel, runnerwire.MaxReviewers+1) }),
		"blank reviewer":           base(func(p *runnerwire.Pipeline) { p.Reviewers = []runnerwire.AgentModel{{}} }),
		"reviewer with no model":   base(func(p *runnerwire.Pipeline) { p.Reviewers = []runnerwire.AgentModel{{Agent: "codex"}} }),
	}
	for name, p := range bad {
		if err := ValidatePipeline(p, true); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestReviewersAndQuorum(t *testing.T) {
	two := []runnerwire.AgentModel{am("codex", "a"), am("claude", "b")}
	cases := []struct {
		p         runnerwire.Pipeline
		reviewers int
		quorum    int
	}{
		{runnerwire.Pipeline{Review: am("codex", "a")}, 1, 1},
		{runnerwire.Pipeline{Review: am("codex", "a"), Reviewers: two}, 2, 2}, // Reviewers win
		{runnerwire.Pipeline{Reviewers: two, Quorum: 1}, 2, 1},
		{runnerwire.Pipeline{Reviewers: two, Quorum: 0}, 2, 2},
		{runnerwire.Pipeline{Human: true}, 0, 0},
	}
	for i, c := range cases {
		if n := len(runnerwire.ReviewersOf(c.p)); n != c.reviewers {
			t.Errorf("case %d: %d reviewers", i, n)
		}
		if q := runnerwire.QuorumOf(c.p); q != c.quorum {
			t.Errorf("case %d: quorum %d", i, q)
		}
	}
}

func TestResolvePipeline(t *testing.T) {
	info := &runnerwire.Info{Agents: []runnerwire.Agent{
		{Kind: "claude", Installed: true, Models: []runnerwire.Model{{ID: "opus", Default: true}, {ID: "sonnet"}}},
		{Kind: "codex", Installed: true, Models: []runnerwire.Model{{ID: "gpt-5", Default: true}}},
	}}
	got, err := ResolvePipeline(info, runnerwire.Pipeline{
		Implement: runnerwire.AgentModel{Agent: "claude"},
		Reviewers: []runnerwire.AgentModel{{Agent: "codex"}, {Agent: "claude", Model: "sonnet"}},
		Quorum:    1, Human: true, MaxRounds: 2,
	}, "opus")
	if err != nil || got.Implement != am("claude", "opus") || len(got.Reviewers) != 2 ||
		got.Reviewers[0] != am("codex", "gpt-5") || got.Reviewers[1] != am("claude", "sonnet") ||
		got.Quorum != 1 || !got.Human || got.MaxRounds != 2 || got.Review != (runnerwire.AgentModel{}) {
		t.Fatalf("resolved = %+v %v", got, err)
	}
	if _, err := ResolvePipeline(info, runnerwire.Pipeline{Reviewers: []runnerwire.AgentModel{{Agent: "gemini"}}}, ""); err == nil ||
		!strings.Contains(err.Error(), "reviewer 1") {
		t.Fatalf("missing agent: %v", err)
	}
}

func TestVoteStore(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	p, err := s.CreatePipeline(ctx, NewPipeline{
		OwnerID: "u", CreatedBy: "u", Project: ".", NodeID: "n1",
		Pipeline: runnerwire.Pipeline{Implement: am("claude", "opus"),
			Reviewers: []runnerwire.AgentModel{am("codex", "gpt-5"), am("claude", "sonnet")}, Human: true, MaxRounds: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, runID := range []string{"rev-a", "rev-b"} {
		if err := s.StartVote(ctx, p.ID, 1, i, runnerwire.ReviewersOf(p.Pipeline)[i], runID); err != nil {
			t.Fatal(err)
		}
	}
	if settled, _ := s.SettleVote(ctx, "rev-a", runnerwire.VerdictApprove); !settled {
		t.Fatal("first verdict refused")
	}
	if settled, _ := s.SettleVote(ctx, "rev-a", runnerwire.VerdictChanges); settled {
		t.Fatal("a vote changed after it was cast")
	}
	if _, err := s.SettleVote(ctx, "rev-b", "maybe"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad verdict: %v", err)
	}
	if settled, _ := s.SettleVote(ctx, "rev-b", runnerwire.VerdictAbstain); !settled {
		t.Fatal("abstention refused")
	}
	if settled, _ := s.SettleVote(ctx, "nobody", runnerwire.VerdictApprove); settled {
		t.Fatal("a run with no vote settled one")
	}

	if waiting, _ := s.AwaitHuman(ctx, p.ID, 2); waiting {
		t.Fatal("waited in a round the pipeline is not in")
	}
	if waiting, _ := s.AwaitHuman(ctx, p.ID, 1); !waiting {
		t.Fatal("await refused")
	}
	got, err := s.Pipeline(ctx, p.ID)
	if err != nil || got.Status != runnerwire.PipelineWaitingHuman || len(got.Votes) != 2 ||
		got.Votes[0].Verdict != runnerwire.VerdictApprove || got.Votes[0].Agent != "codex" ||
		got.Votes[1].Verdict != runnerwire.VerdictAbstain || got.HumanVerdict != "" {
		t.Fatalf("waiting = %+v %v", got, err)
	}
	if err := s.HumanVote(ctx, p.ID, 1, "u-bea", "bea", runnerwire.VerdictAbstain, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a person abstaining: %v", err)
	}
	if err := s.HumanVote(ctx, p.ID, 1, "u-bea", "bea", runnerwire.VerdictChanges, "Use a map."); err != nil {
		t.Fatal(err)
	}
	if err := s.HumanVote(ctx, p.ID, 1, "u-bea", "bea", runnerwire.VerdictApprove, ""); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("a second vote in the round: %v", err)
	}
	got, _ = s.Pipeline(ctx, p.ID)
	if got.HumanVerdict != runnerwire.VerdictChanges || len(got.Votes) != 3 ||
		got.Votes[2].Agent != runnerwire.HumanReviewer || got.Votes[2].Model != "bea" {
		t.Fatalf("after the person = %+v", got)
	}
	// A new round leaves the wait; ending clears it too.
	if moved, _ := s.NextRound(ctx, p.ID, 1); !moved {
		t.Fatal("next round refused")
	}
	if got, _ = s.Pipeline(ctx, p.ID); got.Status != runnerwire.PipelineRunning || got.HumanVerdict != "" || got.Round != 2 {
		t.Fatalf("round 2 = %+v", got)
	}
	if waiting, _ := s.AwaitHuman(ctx, p.ID, 2); !waiting {
		t.Fatal("await in round 2 refused")
	}
	if ended, _ := s.EndPipeline(ctx, p.ID, runnerwire.PipelineCancelled, "stop"); !ended {
		t.Fatal("cancelling a waiting pipeline refused")
	}
	if got, _ = s.Pipeline(ctx, p.ID); got.Status != runnerwire.PipelineCancelled || got.Awaiting != "" {
		t.Fatalf("cancelled = %+v", got)
	}
}
