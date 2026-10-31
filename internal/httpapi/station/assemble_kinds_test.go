package station

import (
	"testing"

	"nodevas/internal/agents"
	"nodevas/internal/project"
)

// Permission prompts wait on the viewer like any ask, and the building says
// which kind each is, the tool a permission prompt is for, its run and when
// it stops waiting.
func TestAssembleCarriesTheAsksKind(t *testing.T) {
	src := &fakeSources{
		projects: []project.ProjectInfo{info("app")},
		asks: []agents.Ask{
			{ID: "p1", Project: "ws/app", ActorID: "u-bea", Kind: agents.AskKindPermission, Tool: "Bash",
				RunID: "run-1", Question: "Allow Bash?", Status: agents.AskPending, CreatedAt: 2, ExpiresAt: 300002},
			{ID: "q1", Project: "ws/app", ActorID: "u-bea", Kind: agents.AskKindChoice, Options: []string{"a", "b"},
				Question: "Which?", Status: agents.AskPending, CreatedAt: 1, ExpiresAt: 600001},
		},
	}
	app := byName(assemble(t, src, bea, func(string) bool { return true }))["app"]
	if app.Waiting.Asks != 2 || len(app.Asks) != 2 {
		t.Fatalf("app waiting = %+v, asks %+v", app.Waiting, app.Asks)
	}
	kinds := map[string]BuildingAsk{}
	for _, a := range app.Asks {
		kinds[a.ID] = a
	}
	if p := kinds["p1"]; p.Kind != agents.AskKindPermission || p.Tool != "Bash" || p.RunID != "run-1" || p.ExpiresAt != 300002 {
		t.Fatalf("permission ask = %+v", p)
	}
	if q := kinds["q1"]; q.Kind != agents.AskKindChoice || q.Tool != "" || len(q.Options) != 2 {
		t.Fatalf("choice ask = %+v", q)
	}
}
