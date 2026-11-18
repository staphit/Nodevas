package runs

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"nodevas/internal/runnerwire"
)

// draftNew is a plain plan run of ann's on her runner, as a draft.
func draftNew() New {
	return New{
		Project: ".", NodeID: "n1", NodeTitle: "Goal", RunnerID: "r1", RunnerName: "desk",
		OwnerID: "u-owner", Agent: "claude", Model: "default", RepoID: "repo", RepoName: "repo",
		JobPrompt: "# Goal", CreatedBy: "u-ann", CreatedByName: "ann",
		Kind: runnerwire.JobPlan, Draft: true, Role: "planner",
	}
}

func TestCreateRefusesADraftOutsideAPlainPlanRun(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	implement := draftNew()
	implement.Kind = runnerwire.JobImplement
	if _, err := s.Create(ctx, implement); !errors.Is(err, ErrInvalid) {
		t.Fatalf("implement draft err = %v, want ErrInvalid", err)
	}
	replan := draftNew()
	replan.Review = "the goal changed"
	if _, err := s.Create(ctx, replan); !errors.Is(err, ErrInvalid) {
		t.Fatalf("replan draft err = %v, want ErrInvalid", err)
	}
	r, err := s.Create(ctx, draftNew())
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Draft || !got.Job().Draft {
		t.Fatalf("draft run = %+v, job draft %v", got.Run, got.Job().Draft)
	}
	plain := createRun(t, s, "n2")
	if plain.Draft || plain.Job().Draft {
		t.Fatalf("plain run is a draft: %+v", plain.Run)
	}
}

func TestMergePlanDraftReplacesByKeyAndRewritesDraftIDs(t *testing.T) {
	held := runnerwire.PlanDraft{Calls: 1, Nodes: []runnerwire.PlanDraftNode{
		{Key: "a", Title: "A"}, {Key: "b", Title: "B"},
	}}
	add := []runnerwire.PlanDraftNode{
		{Key: "b", Title: "B2"},
		{Key: "c", Title: "C", DependsOn: []string{"draft:a", "draft:z", "n-9"}, Refs: []string{"draft:b"}},
	}
	got := MergePlanDraft(held, add)
	if got.Calls != 2 {
		t.Fatalf("calls = %d", got.Calls)
	}
	var keys, titles []string
	for _, n := range got.Nodes {
		keys, titles = append(keys, n.Key), append(titles, n.Title)
	}
	if !slices.Equal(keys, []string{"a", "b", "c"}) || !slices.Equal(titles, []string{"A", "B2", "C"}) {
		t.Fatalf("nodes = %v %v", keys, titles)
	}
	c := got.Nodes[2]
	if !slices.Equal(c.DependsOn, []string{"a", "draft:z", "n-9"}) || !slices.Equal(c.Refs, []string{"b"}) {
		t.Fatalf("c = %+v", c)
	}
	// Pure: the inputs are untouched.
	if held.Nodes[1].Title != "B" || add[1].DependsOn[0] != "draft:a" || held.Calls != 1 {
		t.Fatalf("inputs changed: %+v %+v", held, add)
	}
}

func TestUpdatePlanDraftStoresOnlyWhatUpdateAccepts(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r, err := s.Create(ctx, draftNew())
	if err != nil {
		t.Fatal(err)
	}
	if held, err := s.PlanDraft(ctx, r.ID); err != nil || held.Calls != 0 || len(held.Nodes) != 0 {
		t.Fatalf("empty draft = %+v, %v", held, err)
	}
	refused := errors.New("refused")
	err = s.UpdatePlanDraft(ctx, r.ID, func(held runnerwire.PlanDraft) (runnerwire.PlanDraft, error) {
		return MergePlanDraft(held, []runnerwire.PlanDraftNode{{Key: "x", Title: "X"}}), refused
	})
	if !errors.Is(err, refused) {
		t.Fatalf("err = %v, want the update's own", err)
	}
	if held, _ := s.PlanDraft(ctx, r.ID); held.Calls != 0 || len(held.Nodes) != 0 {
		t.Fatalf("a refused update was stored: %+v", held)
	}

	// Two at once both land, one after the other.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, key := range []string{"t1", "t2"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = s.UpdatePlanDraft(ctx, r.ID, func(held runnerwire.PlanDraft) (runnerwire.PlanDraft, error) {
				return MergePlanDraft(held, []runnerwire.PlanDraftNode{{Key: key, Title: key}}), nil
			})
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	held, err := s.PlanDraft(ctx, r.ID)
	if err != nil || held.Calls != 2 || len(held.Nodes) != 2 {
		t.Fatalf("after two updates = %+v, %v", held, err)
	}
}

func TestPlanDraftForOnlyItsCreatorOwnerOrAdmin(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r, err := s.Create(ctx, draftNew())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdatePlanDraft(ctx, r.ID, func(held runnerwire.PlanDraft) (runnerwire.PlanDraft, error) {
		return MergePlanDraft(held, []runnerwire.PlanDraftNode{{Key: "t1", Title: "T1"}}), nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, who := range []struct {
		account string
		admin   bool
	}{{"u-ann", false}, {"u-owner", false}, {"u-root", true}} {
		got, err := s.PlanDraftFor(ctx, r.ID, who.account, who.admin)
		if err != nil || len(got.Nodes) != 1 {
			t.Fatalf("%s: %+v, %v", who.account, got, err)
		}
	}
	if _, err := s.PlanDraftFor(ctx, r.ID, "u-bea", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stranger err = %v, want ErrNotFound", err)
	}
	plain := createRun(t, s, "n2")
	if _, err := s.PlanDraftFor(ctx, plain.ID, "u-ann", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-draft run err = %v, want ErrNotFound", err)
	}
	if _, err := s.PlanDraftFor(ctx, "missing", "u-ann", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing run err = %v, want ErrNotFound", err)
	}
}

func TestFallbackOfADraftPlanRunIsADraft(t *testing.T) {
	f := newHandoffFixture(t)
	first := f.start(t, func(in *New) {
		in.Kind, in.Draft, in.Role = runnerwire.JobPlan, true, "planner"
		in.Fallbacks = []runnerwire.AgentModel{{Agent: "codex"}}
	})
	f.fail(t, first.ID, runnerwire.RunResult{FailureKind: runnerwire.FailureQuota, Error: "usage limit"})
	next, ok := f.follower(t, first.ID)
	if !ok || next.Agent != "codex" || !next.Draft || next.Kind != runnerwire.JobPlan {
		t.Fatalf("fallback = %+v", next.Run)
	}
	if job := f.conn.last(t, next.ID); !job.Draft {
		t.Fatalf("fallback offer = %+v", job)
	}
}
