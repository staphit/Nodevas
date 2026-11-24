package autopilot

import (
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
)

func TestAutopilotQueuesWithoutAsking(t *testing.T) {
	// 1. Task dispatch (orchestrator.go) sends OnBusy: "queue".
	t.Run("task dispatch", func(t *testing.T) {
		b := newBoard([]string{"goal", "t1", "t2"}, []string{"t1>goal", "t2>goal"})
		b.graph.NodeByID("goal").Tags = []string{"goal"}
		b.graph.NodeByID("t1").Tags = []string{"goal:goal"}
		b.graph.NodeByID("t2").Tags = []string{"goal:goal"}
		h := newHarness(t, b)
		pol := runnerwire.AutopilotPolicy{
			MaxParallel: 2,
			MaxRounds:   1,
			RunnerID:    "runner-1",
			RepoID:      "repo-1",
			Implement:   []runnerwire.AgentModel{{Agent: "claude", Model: "default"}},
		}
		v := h.create(pol)
		_ = h.view(v.Run.ID)

		if len(h.runs.starts) == 0 {
			t.Fatal("expected autopilot to dispatch at least one task")
		}
		for i, req := range h.runs.starts {
			if req.Body.OnBusy != runnerwire.OnBusyQueue {
				t.Fatalf("start[%d] node=%s OnBusy = %q, want %q", i, req.Body.NodeID, req.Body.OnBusy, runnerwire.OnBusyQueue)
			}
		}
	})

	// 2. Goal flow dispatch (goalflow.go) sends OnBusy: "queue".
	t.Run("goal flow dispatch", func(t *testing.T) {
		b := newBoard([]string{"goal"}, nil)
		b.graph.NodeByID("goal").Tags = []string{"goal"}
		wf := engine.Workflow{
			Preset: "standard",
			Stages: engine.Stages{Planner: true},
		}
		b.graph.Workflow = &wf
		h := newHarness(t, b)
		r := onlineRunner("bea")
		r.Info.Capabilities = append(r.Info.Capabilities, runnerwire.CapPlan, runnerwire.CapRoles)
		h.o = New(Options{Store: h.store, Runs: h.runs, Runners: fakeRunners{list: []runnerwire.Runner{r}}, Board: b})

		pol := runnerwire.AutopilotPolicy{
			MaxParallel: 1,
			MaxRounds:   1,
			RunnerID:    "runner-1",
			RepoID:      "repo-1",
			Implement:   []runnerwire.AgentModel{{Agent: "claude", Model: "default"}},
		}
		v := h.create(pol)
		_ = h.view(v.Run.ID)

		if len(h.runs.starts) == 0 {
			t.Fatal("expected goal flow to dispatch a goal run")
		}
		if h.runs.starts[0].Body.NodeID != "goal" || h.runs.starts[0].Body.OnBusy != runnerwire.OnBusyQueue {
			t.Fatalf("goal start = node:%q onBusy:%q, want goal / %q",
				h.runs.starts[0].Body.NodeID, h.runs.starts[0].Body.OnBusy, runnerwire.OnBusyQueue)
		}
	})
}
