package station

import (
	"encoding/json"
	"strings"
	"testing"

	"nodevas/internal/agents"
	"nodevas/internal/project"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// The building carries what each run's agent and each session last did (the
// tool it called, and when), so a desk can show it; one that has called none
// carries nothing.
func TestAssembleCopiesTheLastTool(t *testing.T) {
	working := run("r1", "app", "u-bea", runnerwire.RunRunning)
	working.LastTool, working.LastToolAt = "Edit", "2026-10-27T11:59:00.000Z"
	src := &fakeSources{
		projects: []project.ProjectInfo{info("app")},
		runs:     []runs.Record{working, run("r2", "app", "u-bea", runnerwire.RunQueued)},
		sessions: map[string][]agents.Session{
			root("app"): {
				{ID: "s1", Name: "claude-1", Online: true, State: "busy", LastTool: "Bash", LastToolAt: 1793102340000},
				{ID: "s2", Name: "claude-2", Online: true, State: "idle"},
			},
		},
	}
	app := byName(assemble(t, src, bea, nil))["app"]
	tools := map[string]BuildingRun{}
	for _, r := range app.Runs {
		tools[r.ID] = r
	}
	if r := tools["r1"]; r.LastTool != "Edit" || r.LastToolAt != "2026-10-27T11:59:00.000Z" {
		t.Fatalf("run r1 = %+v, want Edit at 11:59", r)
	}
	if r := tools["r2"]; r.LastTool != "" || r.LastToolAt != "" {
		t.Fatalf("run r2 = %+v, want no tool", r)
	}
	if len(app.Sessions) != 2 || app.Sessions[0].LastTool != "Bash" || app.Sessions[0].LastToolAt != 1793102340000 {
		t.Fatalf("sessions = %+v, want s1 on Bash", app.Sessions)
	}

	data, err := json.Marshal(app)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"lastTool":"Edit","lastToolAt":"2026-10-27T11:59:00.000Z"`, `"lastTool":"Bash","lastToolAt":1793102340000`} {
		if !strings.Contains(string(data), field) {
			t.Fatalf("%s missing in %s", field, data)
		}
	}
	if n := strings.Count(string(data), `"lastTool"`); n != 2 {
		t.Fatalf("%d lastTool fields in %s, want 2 (none for what called no tool)", n, data)
	}
}
