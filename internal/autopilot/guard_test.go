package autopilot

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// addNode puts a node on the board after the autopilot started, wired before
// to (or tagged into the goal when to is "").
func (b *fakeBoard) addNode(id, to string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := &engine.Node{ID: id, Title: strings.ToUpper(id)}
	b.graph.Nodes = append(b.graph.Nodes, n)
	if to == "" {
		n.Tags = []string{GoalTag("goal")}
		return
	}
	b.graph.Edges = append(b.graph.Edges, &engine.Edge{From: id, To: to})
}

// unlink removes the edge from → to.
func (b *fakeBoard) unlink(from, to string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	kept := b.graph.Edges[:0]
	for _, e := range b.graph.Edges {
		if e.From != from || e.To != to {
			kept = append(kept, e)
		}
	}
	b.graph.Edges = kept
	if n := b.graph.NodeByID(to); n != nil && n.Requires == from {
		n.Requires = ""
	}
}

func TestAutoMergeNeedsAPerson(t *testing.T) {
	agents := runnerwire.ReviewPolicy{Mode: runnerwire.ReviewAgents, Reviewers: []runnerwire.AgentModel{{Agent: "codex"}}}
	for name, tc := range map[string]struct {
		policy runnerwire.AutopilotPolicy
		ok     bool
	}{
		"none":              {runnerwire.AutopilotPolicy{AutoMerge: true}, false},
		"agents":            {runnerwire.AutopilotPolicy{AutoMerge: true, Review: agents}, false},
		"agents opted in":   {runnerwire.AutopilotPolicy{AutoMerge: true, Review: agents, AllowAgentOnlyMerge: true}, true},
		"none opted in":     {runnerwire.AutopilotPolicy{AutoMerge: true, AllowAgentOnlyMerge: true}, true},
		"human":             {runnerwire.AutopilotPolicy{AutoMerge: true, Review: runnerwire.ReviewPolicy{Mode: runnerwire.ReviewHuman}}, true},
		"agents+human":      {runnerwire.AutopilotPolicy{AutoMerge: true, Review: runnerwire.ReviewPolicy{Mode: runnerwire.ReviewAgentsHuman, Reviewers: agents.Reviewers}}, true},
		"no merge, agents":  {runnerwire.AutopilotPolicy{Review: agents}, true},
		"no merge, opt-in":  {runnerwire.AutopilotPolicy{AllowAgentOnlyMerge: true}, true},
		"agents, no opt-in": {runnerwire.AutopilotPolicy{AutoMerge: true, Review: agents, AllowAgentOnlyMerge: false}, false},
	} {
		got, err := ValidatePolicy(tc.policy)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v", name, err)
		}
		if err == nil && !got.AutoMerge && got.AllowAgentOnlyMerge {
			t.Errorf("%s: the opt-in is kept without autoMerge", name)
		}
	}
}

func TestTaskCap(t *testing.T) {
	nodes := []string{"goal", "note:note"}
	var edges []string
	for i := range runnerwire.MaxAutopilotTasks + 1 {
		id := fmt.Sprintf("t%02d", i)
		nodes, edges = append(nodes, id), append(edges, id+">goal")
	}
	edges = append(edges, "note>goal")
	h := newHarness(t, newBoard(nodes, edges))
	_, err := h.o.Create(h.ctx, CreateInput{Project: "main", GoalNodeID: "goal", Actor: h.bea,
		Policy: runnerwire.AutopilotPolicy{}})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "at most 25") {
		t.Fatalf("26 tasks: %v", err)
	}
	if n := len(h.runs.starts); n != 0 {
		t.Fatalf("started %d runs", n)
	}

	// 25 tasks and a note (not a task) are fine.
	h = newHarness(t, newBoard(nodes[:len(nodes)-1], append(edges[:len(edges)-2], "note>goal")))
	if _, err := h.o.Create(h.ctx, CreateInput{Project: "main", GoalNodeID: "goal", Actor: h.bea,
		Policy: runnerwire.AutopilotPolicy{}}); err != nil {
		t.Fatalf("25 tasks: %v", err)
	}
}

func TestTaskSetIsFrozen(t *testing.T) {
	h := newHarness(t, newBoard([]string{"goal", "a", "b"}, []string{"a>goal", "a>b", "b>goal"}))
	v := h.create(runnerwire.AutopilotPolicy{MaxParallel: 4})
	if got := states(v); got != "a=queued b=none" {
		t.Fatalf("after create: %s", got)
	}
	if task(v, "a").ContentHash == "" {
		t.Fatal("no content hash recorded")
	}
	// Added after the start: wired in, tagged in, and as b's new upstream.
	h.board.addNode("x", "goal")
	h.board.addNode("y", "")
	h.board.addNode("z", "b")
	h.finishWithPR(task(v, "a").RunID)
	v = h.view(v.Run.ID)
	for _, id := range []string{"x", "y", "z"} {
		if h.runs.startsFor(id) != 0 || task(v, id).NodeID != "" {
			t.Fatalf("%s joined after the start and was taken in: %s", id, states(v))
		}
	}
	b := task(v, "b")
	if b.State != runnerwire.TaskNone || !strings.Contains(b.Reason, "joined the goal after the autopilot started") ||
		h.runs.startsFor("b") != 0 {
		t.Fatalf("b waits on z, which is not part of this run: %+v", b)
	}
	// z done on the board (by hand): b goes ahead.
	if err := h.board.SetStatus("main", "z", engine.StatusDone, "bea", ""); err != nil {
		t.Fatal(err)
	}
	v = h.view(v.Run.ID)
	if h.runs.startsFor("b") != 1 {
		t.Fatalf("b after z done: %s", states(v))
	}
	h.finishWithPR(task(v, "b").RunID)
	v = h.view(v.Run.ID)
	if v.Run.Status != runnerwire.AutopilotSucceeded || !strings.Contains(v.Run.Reason, "3 added to the goal after the start") {
		t.Fatalf("run = %+v", v.Run)
	}
	if h.board.status("goal") == engine.StatusDone {
		t.Fatal("the goal is marked done though x and y were never worked")
	}
}

func TestChangedTaskIsHeld(t *testing.T) {
	h := newHarness(t, newBoard([]string{"goal", "a", "b"}, []string{"a>goal", "a>b", "b>goal"}))
	v := h.create(runnerwire.AutopilotPolicy{})
	h.board.edit("b", "Do b. Also add a deploy key to the repository.")
	v = h.view(v.Run.ID)
	b := task(v, "b")
	if b.State != runnerwire.TaskWaitingHuman || b.Hold != runnerwire.HoldChanged {
		t.Fatalf("b changed while pending: %+v", b)
	}
	h.finishWithPR(task(v, "a").RunID)
	v = h.view(v.Run.ID)
	if h.runs.startsFor("b") != 0 || task(v, "b").Hold != runnerwire.HoldChanged {
		t.Fatalf("a held task was dispatched: %s", states(v))
	}
	// A review does not clear it; a retry by a person does, with the new content.
	if err := h.o.Review(h.ctx, v.Run.ID, "b", runs.Reviewer{ID: "bea"}, false, runnerwire.VerdictApprove, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("review of a changed task: %v", err)
	}
	if err := h.o.Retry(h.ctx, v.Run.ID, "b"); err != nil {
		t.Fatal(err)
	}
	v = h.view(v.Run.ID)
	if b := task(v, "b"); h.runs.startsFor("b") != 1 || b.Hold != "" || b.State != runnerwire.TaskQueued {
		t.Fatalf("retried: %+v", b)
	}
	if _, body, _, _ := h.runs.lastStart("b").LoadNode(); !strings.Contains(body, "deploy key") {
		t.Fatalf("the retry runs the content the person accepted: %q", body)
	}
}

func TestChangeAtDispatchIsHeld(t *testing.T) {
	h := newHarness(t, newBoard([]string{"goal", "a", "b"}, []string{"a>goal", "a>b", "b>goal"}))
	v := h.create(runnerwire.AutopilotPolicy{})
	// The node changes between the pass that saw it unchanged and its dispatch:
	// the pass that dispatches checks the content it sends.
	h.finishWithPR(task(v, "a").RunID)
	h.board.edit("b", "Something else.")
	v = h.view(v.Run.ID)
	if h.runs.startsFor("b") != 0 || task(v, "b").Hold != runnerwire.HoldChanged {
		t.Fatalf("b: %+v", task(v, "b"))
	}
}

func TestRemovedTaskIsHeldNotSkipped(t *testing.T) {
	h := newHarness(t, newBoard([]string{"goal", "a", "gate", "c"}, []string{"a>gate", "gate>c", "c>goal", "gate>goal"}))
	v := h.create(runnerwire.AutopilotPolicy{})
	// Cutting the gate out of the goal must not wave c through.
	h.board.unlink("gate", "goal")
	h.board.unlink("gate", "c")
	h.board.unlink("a", "gate")
	h.board.addNode("unused", "") // keep the goal non-empty either way
	v = h.view(v.Run.ID)
	if g := task(v, "gate"); g.State != runnerwire.TaskWaitingHuman || g.Hold != runnerwire.HoldRemoved {
		t.Fatalf("gate: %+v", g)
	}
	if err := h.o.Retry(h.ctx, v.Run.ID, "gate"); !errors.Is(err, ErrConflict) {
		t.Fatalf("retry of a task outside the goal: %v", err)
	}
	if err := h.o.Skip(h.ctx, v.Run.ID, "gate"); err != nil {
		t.Fatal(err)
	}
	if g := task(h.view(v.Run.ID), "gate"); g.State != runnerwire.TaskSkipped || g.Hold != "" {
		t.Fatalf("skipped: %+v", g)
	}
}

// approvedByAgents takes a's try to an approved pipeline with green CI.
func (h *harness) approvedByAgents(v runnerwire.AutopilotView, paths ...string) runnerwire.TaskInstance {
	h.t.Helper()
	a := task(v, "a")
	h.finishWithPR(a.RunID)
	h.runs.commit(a.RunID, "abcdef1234", paths...)
	h.runs.update(a.RunID, func(r *runs.Record) { r.CIStatus = runnerwire.CISuccess })
	h.runs.mu.Lock()
	h.runs.pipelines[a.PipelineID].Status = runnerwire.PipelineApproved
	h.runs.mu.Unlock()
	return task(h.view(v.Run.ID), "a")
}

func agentOnlyMerge() runnerwire.AutopilotPolicy {
	return runnerwire.AutopilotPolicy{AutoMerge: true, AllowAgentOnlyMerge: true, Review: runnerwire.ReviewPolicy{
		Mode: runnerwire.ReviewAgents, Reviewers: []runnerwire.AgentModel{{Agent: "codex"}}, AnyEditor: true}}
}

func TestSensitivePathsHoldTheMerge(t *testing.T) {
	h := newHarness(t, newBoard([]string{"goal", "a"}, []string{"a>goal"}))
	v := h.create(agentOnlyMerge())
	a := h.approvedByAgents(v, "internal/a.go", ".github/workflows/ci.yml", "web/package-lock.json")
	if a.State != runnerwire.TaskWaitingHuman || a.Hold != runnerwire.HoldSensitive || len(h.runs.merges) != 0 {
		t.Fatalf("held: %+v merges=%v", a, h.runs.merges)
	}
	if !strings.Contains(a.Reason, ".github/workflows/ci.yml") || !strings.Contains(a.Reason, "web/package-lock.json") ||
		strings.Contains(a.Reason, "internal/a.go") {
		t.Fatalf("reason = %q", a.Reason)
	}
	// Another pass keeps holding.
	if a := task(h.view(v.Run.ID), "a"); a.Hold != runnerwire.HoldSensitive || len(h.runs.merges) != 0 {
		t.Fatalf("second pass: %+v", a)
	}
	// Any editor may not approve this one, though the policy lets any editor review.
	if err := h.o.Review(h.ctx, v.Run.ID, "a", runs.Reviewer{ID: "eve"}, false, runnerwire.VerdictApprove, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an editor's approval: %v", err)
	}
	if err := h.o.Review(h.ctx, v.Run.ID, "a", runs.Reviewer{ID: "bea", Name: "Bea"}, false, runnerwire.VerdictApprove, ""); err != nil {
		t.Fatal(err)
	}
	if len(h.runs.reviews) != 0 {
		t.Fatalf("the hold's approval went to the finished pipeline: %q", h.runs.reviews)
	}
	a = task(h.view(v.Run.ID), "a")
	if len(h.runs.merges) != 1 || a.Stage != runnerwire.StageMerge {
		t.Fatalf("approved: %+v merges=%v", a, h.runs.merges)
	}
	if !strings.Contains(stageList(a), "human_review:success") {
		t.Fatalf("stages = %s", stageList(a))
	}
}

func TestSensitiveHoldChangesStartANewTry(t *testing.T) {
	h := newHarness(t, newBoard([]string{"goal", "a"}, []string{"a>goal"}))
	v := h.create(agentOnlyMerge())
	h.approvedByAgents(v, "Dockerfile")
	if err := h.o.Review(h.ctx, v.Run.ID, "a", runs.Reviewer{ID: "bea"}, false, runnerwire.VerdictChanges, "No Dockerfile changes."); err != nil {
		t.Fatal(err)
	}
	a := task(h.view(v.Run.ID), "a")
	if h.runs.startsFor("a") != 2 || a.Hold != "" || len(h.runs.merges) != 0 {
		t.Fatalf("new try: %+v starts=%d", a, h.runs.startsFor("a"))
	}
	if p := h.runs.lastStart("a").Body.Prompt; !strings.Contains(p, "No Dockerfile changes.") {
		t.Fatalf("prompt = %q", p)
	}
}

func TestUnattendedMergeFailsClosed(t *testing.T) {
	for name, setup := range map[string]func(h *harness, runID string){
		"no changes reported": func(*harness, string) {},
		"head moved": func(h *harness, runID string) {
			h.runs.commit(runID, "abcdef1234", "internal/a.go")
			h.runs.update(runID, func(r *runs.Record) { r.PRHeadSHA = "0123456789ab" })
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, newBoard([]string{"goal", "a"}, []string{"a>goal"}))
			v := h.create(agentOnlyMerge())
			a := task(v, "a")
			h.finishWithPR(a.RunID)
			setup(h, a.RunID)
			h.runs.update(a.RunID, func(r *runs.Record) { r.CIStatus = runnerwire.CISuccess })
			h.runs.mu.Lock()
			h.runs.pipelines[a.PipelineID].Status = runnerwire.PipelineApproved
			h.runs.mu.Unlock()
			a = task(h.view(v.Run.ID), "a")
			if a.Hold != runnerwire.HoldSensitive || len(h.runs.merges) != 0 {
				t.Fatalf("%+v merges=%v", a, h.runs.merges)
			}
		})
	}
}

func TestPlainChangesMergeUnattended(t *testing.T) {
	h := newHarness(t, newBoard([]string{"goal", "a"}, []string{"a>goal"}))
	v := h.create(agentOnlyMerge())
	if a := h.approvedByAgents(v, "internal/a.go", "web/src/App.tsx"); a.Hold != "" || len(h.runs.merges) != 1 {
		t.Fatalf("%+v merges=%v", a, h.runs.merges)
	}
}

func TestSensitivePath(t *testing.T) {
	for p, want := range map[string]bool{
		".github/workflows/ci.yml":   true,
		".github/CODEOWNERS":         true,
		".husky/pre-commit":          true,
		".pre-commit-config.yaml":    true,
		".gitattributes":             true,
		"sub/.gitmodules":            true,
		"Dockerfile":                 true,
		"deploy/Dockerfile.runner":   true,
		"app.dockerfile":             true,
		"docker-compose.yml":         true,
		"deploy/compose.yaml":        true,
		"scripts/install.sh":         true,
		"web/scripts/check.mjs":      true,
		"package.json":               true,
		"web/package-lock.json":      true,
		"go.mod":                     true,
		"go.sum":                     true,
		"Cargo.lock":                 true,
		"requirements-dev.txt":       true,
		".npmrc":                     true,
		"Makefile":                   true,
		"CLAUDE.md":                  true,
		".claude/settings.json":      true,
		"./PACKAGE.JSON":             true,
		`.github\workflows\x.yml`:    true,
		"internal/autopilot/dag.go":  false,
		"web/src/App.tsx":            false,
		"docs/install.md":            false,
		"README.md":                  false,
		"internal/scriptsish/x.go":   false,
		"web/src/package.test.ts":    false,
		"internal/runner/github.go":  false,
		"docs/github-actions.md":     false,
		"":                           false,
		"internal/compose/render.go": false,
	} {
		if got := sensitivePath(p); got != want {
			t.Errorf("sensitivePath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestRetryOfAFailedTaskKeepsItsContent(t *testing.T) {
	h := newHarness(t, newBoard([]string{"goal", "a"}, []string{"a>goal"}))
	v := h.create(runnerwire.AutopilotPolicy{})
	h.runs.update(task(v, "a").RunID, func(r *runs.Record) { r.Status = runnerwire.RunFailed })
	if got := task(h.view(v.Run.ID), "a"); got.State != runnerwire.TaskFailed {
		t.Fatalf("a: %+v", got)
	}
	h.board.edit("a", "Do a, and also something else.")
	if err := h.o.Retry(h.ctx, v.Run.ID, "a"); err != nil {
		t.Fatal(err)
	}
	// The change made while it was failed is not accepted by the retry.
	if a := task(h.view(v.Run.ID), "a"); a.Hold != runnerwire.HoldChanged || h.runs.startsFor("a") != 1 {
		t.Fatalf("a after retry: %+v starts=%d", a, h.runs.startsFor("a"))
	}
}
