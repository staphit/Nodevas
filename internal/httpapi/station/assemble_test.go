package station

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/agents"
	"nodevas/internal/auth"
	"nodevas/internal/autopilot"
	"nodevas/internal/identity"
	"nodevas/internal/project"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// fakeSources is the building's inputs, held in memory. Runs and asks are
// filtered the way the stores filter them, so the assembly is what is tested.
type fakeSources struct {
	projects   []project.ProjectInfo
	unresolved map[string]bool
	runs       []runs.Record
	sessions   map[string][]agents.Session // by root
	asks       []agents.Ask
	held       []agents.Message
	reviews    []autopilot.WaitingHumanRow

	runsViewer   string
	runsSince    string
	othersCalled bool
	heldViewer   agents.Viewer
}

func root(name string) string { return "/ws/" + name }

func (f *fakeSources) Projects() ([]project.ProjectInfo, error) { return f.projects, nil }

func (f *fakeSources) Root(name string) (string, bool) {
	if f.unresolved[name] {
		return "", false
	}
	return root(name), true
}

func in(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (f *fakeSources) Runs(_ context.Context, names []string, viewer, since string) ([]runs.Record, error) {
	f.runsViewer, f.runsSince = viewer, since
	var out []runs.Record
	for _, r := range f.runs {
		if in(names, r.Project) && (viewer == "" || r.CreatedBy == viewer || r.OwnerID == viewer) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeSources) OthersActive(_ context.Context, names []string, viewer string) (map[string]int, error) {
	f.othersCalled = true
	out := map[string]int{}
	for _, r := range f.runs {
		if in(names, r.Project) && activeRun(r.Status) && r.CreatedBy != viewer && r.OwnerID != viewer {
			out[r.Project]++
		}
	}
	return out, nil
}

func (f *fakeSources) Sessions(root string) []agents.Session { return f.sessions[root] }

func trimmed(roots []string) []string {
	out := make([]string, len(roots))
	for i, r := range roots {
		out[i] = strings.Trim(r, "/")
	}
	return out
}

func (f *fakeSources) PendingAsksIn(_ context.Context, roots []string) ([]agents.Ask, error) {
	keys := trimmed(roots)
	var out []agents.Ask
	for _, a := range f.asks {
		if in(keys, a.Project) {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeSources) HeldIn(_ context.Context, roots []string, v agents.Viewer) ([]agents.Message, error) {
	f.heldViewer = v
	keys := trimmed(roots)
	var out []agents.Message
	for _, m := range f.held {
		if in(keys, m.Project) && (v.Admin || m.RecipientActor == v.ID) {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeSources) WaitingHuman(_ context.Context, names []string) ([]autopilot.WaitingHumanRow, error) {
	var out []autopilot.WaitingHumanRow
	for _, r := range f.reviews {
		if in(names, r.Project) {
			out = append(out, r)
		}
	}
	return out, nil
}

func info(name string) project.ProjectInfo {
	return project.ProjectInfo{Name: name, Label: name, Path: "/dir/" + name, Layout: "workflow", OfficeStyle: "engineering"}
}

func run(id, project, by, status string) runs.Record {
	r := runs.Record{OwnerID: by, NodeTitle: "Title " + id}
	r.ID, r.Project, r.NodeID, r.Status, r.CreatedBy = id, project, "n-"+id, status, by
	r.Agent, r.Model, r.RunnerID, r.RunnerName = "claude", "opus", "r1", "desk"
	return r
}

var (
	bea   = identity.Actor{ID: "u-bea", Name: "bea", Role: identity.RoleMember}
	admin = identity.Actor{ID: "u-ann", Name: "ann", Role: identity.RoleAdmin}
	now   = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
)

func assemble(t *testing.T, src Sources, actor identity.Actor, readable func(string) bool) BuildingResponse {
	t.Helper()
	out, err := Assemble(context.Background(), src, actor, readable, now)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func byName(out BuildingResponse) map[string]BuildingProject {
	m := map[string]BuildingProject{}
	for _, p := range out.Projects {
		m[p.Name] = p
	}
	return m
}

func TestAssembleFiltersAndCounts(t *testing.T) {
	folder := info("group")
	folder.IsFolder = true
	src := &fakeSources{
		projects:   []project.ProjectInfo{info("app"), info("novel"), info("secret"), folder, info("moved")},
		unresolved: map[string]bool{"moved": true},
		runs: []runs.Record{
			run("r1", "app", "u-bea", runnerwire.RunRunning),
			run("r2", "app", "u-cal", runnerwire.RunRunning),
			run("r3", "app", "u-cal", runnerwire.RunQueued),
			run("r4", "secret", "u-bea", runnerwire.RunRunning),
			run("r5", "novel", "u-bea", runnerwire.RunSucceeded),
		},
		sessions: map[string][]agents.Session{
			root("app"): {{ID: "s1", Name: "claude-1", Online: true, State: "busy"}, {ID: "s2", Name: "gone", Online: false}},
		},
		asks: []agents.Ask{
			{ID: "a1", Project: "ws/app", ActorID: "u-bea", Question: strings.Repeat("問", 200), Status: agents.AskPending},
			{ID: "a2", Project: "ws/app", ActorID: "u-cal", Question: "Not bea's", Status: agents.AskPending},
			{ID: "a3", Project: "ws/secret", ActorID: "u-bea", Question: "Hidden", Status: agents.AskPending},
		},
		held: []agents.Message{
			{ID: "m1", Project: "ws/novel", RecipientActor: "u-bea", Status: agents.StatusHeld},
			{ID: "m2", Project: "ws/novel", RecipientActor: "u-cal", Status: agents.StatusHeld},
		},
		reviews: []autopilot.WaitingHumanRow{
			{Project: "novel", AutopilotID: "ap1", GoalNodeID: "g", NodeID: "t1", PipelineID: "p1"},
			{Project: "secret", AutopilotID: "ap2", GoalNodeID: "g", NodeID: "t2", PipelineID: "p2"},
		},
	}
	readable := func(dir string) bool { return dir != "/dir/secret" }
	out := assemble(t, src, bea, readable)

	got := byName(out)
	if _, ok := got["secret"]; ok {
		t.Fatal("an unreadable project is in the building")
	}
	if _, ok := got["group"]; ok {
		t.Fatal("a folder is in the building")
	}
	if _, ok := got["moved"]; ok {
		t.Fatal("a project whose root does not resolve is in the building")
	}
	if out.Total != 3 || out.Truncated || len(out.Projects) != 2 {
		t.Fatalf("total %d truncated %v projects %d", out.Total, out.Truncated, len(out.Projects))
	}
	if src.runsViewer != "u-bea" || src.runsSince != runs.FormatStamp(now.Add(-24*time.Hour)) || !src.othersCalled {
		t.Fatalf("runs asked for viewer %q since %q, others %v", src.runsViewer, src.runsSince, src.othersCalled)
	}
	if src.heldViewer != (agents.Viewer{ID: "u-bea"}) {
		t.Fatalf("held viewer = %+v", src.heldViewer)
	}

	app := got["app"]
	if len(app.Runs) != 1 || app.Runs[0].ID != "r1" || app.Runs[0].Title != "Title r1" || app.OthersActive != 2 {
		t.Fatalf("app runs = %+v, others %d", app.Runs, app.OthersActive)
	}
	if len(app.Sessions) != 1 || app.Sessions[0].ID != "s1" {
		t.Fatalf("app sessions = %+v", app.Sessions)
	}
	if app.Waiting != (BuildingWaiting{Asks: 1, Total: 1}) || len(app.Asks) != 1 || app.Asks[0].ID != "a1" {
		t.Fatalf("app waiting = %+v, asks %+v", app.Waiting, app.Asks)
	}
	if q := app.Asks[0].Question; len([]rune(q)) != 120 || !strings.HasSuffix(q, "…") {
		t.Fatalf("question not excerpted: %d runes", len([]rune(q)))
	}
	novel := got["novel"]
	if novel.Waiting != (BuildingWaiting{Held: 1, Reviews: 1, Total: 2}) || len(novel.Reviews) != 1 || novel.Reviews[0].AutopilotID != "ap1" {
		t.Fatalf("novel waiting = %+v, reviews %+v", novel.Waiting, novel.Reviews)
	}
	if len(novel.Runs) != 1 || novel.Runs[0].Status != runnerwire.RunSucceeded {
		t.Fatalf("novel runs = %+v", novel.Runs)
	}
	// Busy first: app (1 + 2 others + 1 waiting) before novel (2 waiting),
	// though "app" < "novel" would not need it.
	if out.Projects[0].Name != "app" || out.Projects[1].Name != "novel" {
		t.Fatalf("order = %s, %s", out.Projects[0].Name, out.Projects[1].Name)
	}
}

func TestAssembleAdminSeesEverythingWithoutOthers(t *testing.T) {
	src := &fakeSources{
		projects: []project.ProjectInfo{info("app")},
		runs: []runs.Record{
			run("r1", "app", "u-bea", runnerwire.RunRunning),
			run("r2", "app", "u-cal", runnerwire.RunRunning),
		},
		asks: []agents.Ask{{ID: "a1", Project: "ws/app", ActorID: "u-cal", Status: agents.AskPending}},
		held: []agents.Message{{ID: "m1", Project: "ws/app", RecipientActor: "u-cal", Status: agents.StatusHeld}},
	}
	out := assemble(t, src, admin, nil)
	app := out.Projects[0]
	if src.runsViewer != "" || src.othersCalled {
		t.Fatalf("admin was filtered: viewer %q, others asked %v", src.runsViewer, src.othersCalled)
	}
	if len(app.Runs) != 2 || app.OthersActive != 0 || app.Waiting.Asks != 1 || app.Waiting.Held != 1 {
		t.Fatalf("admin's app = %+v", app)
	}
}

func TestAssembleCaps(t *testing.T) {
	src := &fakeSources{sessions: map[string][]agents.Session{}}
	for i := 0; i < 70; i++ {
		src.projects = append(src.projects, info(fmt.Sprintf("p%02d", i)))
	}
	for i := 0; i < 30; i++ {
		src.runs = append(src.runs, run(fmt.Sprintf("a%02d", i), "p05", "u-bea", runnerwire.RunRunning))
	}
	for i := 0; i < 50; i++ {
		src.runs = append(src.runs, run(fmt.Sprintf("f%02d", i), "p05", "u-bea", runnerwire.RunFailed))
	}
	for i := 0; i < 20; i++ {
		src.sessions[root("p05")] = append(src.sessions[root("p05")], agents.Session{ID: fmt.Sprint(i), Online: true})
		src.asks = append(src.asks, agents.Ask{ID: fmt.Sprint(i), Project: "ws/p05", ActorID: "u-bea"})
		src.reviews = append(src.reviews, autopilot.WaitingHumanRow{Project: "p05", AutopilotID: fmt.Sprint(i)})
	}
	src.runs = append(src.runs, run("x", "p69", "u-bea", runnerwire.RunQueued))

	out := assemble(t, src, bea, nil)
	if len(out.Projects) != MaxProjects || !out.Truncated || out.Total != 70 {
		t.Fatalf("projects %d truncated %v total %d", len(out.Projects), out.Truncated, out.Total)
	}
	busy := out.Projects[0]
	if busy.Name != "p05" || out.Projects[1].Name != "p69" || out.Projects[2].Name != "p00" {
		t.Fatalf("busy-first order = %s, %s, %s", busy.Name, out.Projects[1].Name, out.Projects[2].Name)
	}
	var active, ended int
	for _, r := range busy.Runs {
		if activeRun(r.Status) {
			active++
		} else {
			ended++
		}
	}
	if active != 24 || ended != 40 || !busy.RunsTruncated {
		t.Fatalf("runs kept: %d active, %d ended, truncated %v", active, ended, busy.RunsTruncated)
	}
	if len(busy.Sessions) != 16 || len(busy.Asks) != 3 || len(busy.Reviews) != 3 {
		t.Fatalf("sessions %d asks %d reviews %d", len(busy.Sessions), len(busy.Asks), len(busy.Reviews))
	}
	if busy.Waiting != (BuildingWaiting{Asks: 20, Reviews: 20, Total: 40}) {
		t.Fatalf("counts are not complete: %+v", busy.Waiting)
	}
}

func TestAssembleSendsArraysNotNull(t *testing.T) {
	out := assemble(t, &fakeSources{projects: []project.ProjectInfo{info("app")}}, bea, nil)
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"runs":[]`, `"sessions":[]`, `"asks":[]`, `"reviews":[]`} {
		if !strings.Contains(string(data), field) {
			t.Fatalf("%s missing in %s", field, data)
		}
	}
	if strings.Contains(string(data), "null") {
		t.Fatalf("null in %s", data)
	}
	empty, _ := json.Marshal(assemble(t, &fakeSources{}, bea, nil))
	if string(empty) != `{"projects":[],"truncated":false,"total":0}` {
		t.Fatalf("empty building = %s", empty)
	}
}

func TestBuildingRefusesAgents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(nil, &fakeSources{projects: []project.ProjectInfo{info("app")}}, nil).Register(router.Group("/api"))

	request := httptest.NewRequest(http.MethodGet, "/api/station/building", nil)
	request = request.WithContext(auth.WithActor(request.Context(), identity.Actor{ID: "mcp:claude", Agent: identity.AgentWorker}))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "PERMISSION_DENIED") {
		t.Fatalf("agent: %d %s", response.Code, response.Body)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/station/building", nil)
	request = request.WithContext(auth.WithActor(request.Context(), identity.Local))
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("person: %d %s", response.Code, response.Body)
	}
}
