package station

import (
	"testing"

	"nodevas/internal/agents"
	"nodevas/internal/project"
)

// An admin cannot answer another member's permission prompt, so it does not
// wait on them in the building; the member's question still does.
func TestAssembleLeavesOthersPermissionPromptsOffAnAdmin(t *testing.T) {
	src := &fakeSources{
		projects: []project.ProjectInfo{info("app")},
		asks: []agents.Ask{
			{ID: "p1", Project: "ws/app", ActorID: "u-bea", Kind: agents.AskKindPermission, Tool: "Bash",
				Question: "Allow Bash?", Status: agents.AskPending, CreatedAt: 2},
			{ID: "q1", Project: "ws/app", ActorID: "u-bea", Kind: agents.AskKindQuestion,
				Question: "Which port?", Status: agents.AskPending, CreatedAt: 1},
		},
	}
	app := byName(assemble(t, src, admin, nil))["app"]
	if app.Waiting.Asks != 1 || len(app.Asks) != 1 || app.Asks[0].ID != "q1" {
		t.Fatalf("admin's waiting = %+v, asks %+v; want only bea's question", app.Waiting, app.Asks)
	}
	if app := byName(assemble(t, src, bea, nil))["app"]; app.Waiting.Asks != 2 {
		t.Fatalf("bea's waiting = %+v, want both of her asks", app.Waiting)
	}
}
