package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/realtime"
	"nodevas/internal/runnerwire"
)

// activeProject is what GET /api/projects says b is working in.
func activeProject(t *testing.T, f *membersFixture, b browser) string {
	t.Helper()
	response := f.do(t, &b, http.MethodGet, "/api/projects", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/projects = %d %s", response.Code, response.Body)
	}
	var out struct {
		Active string `json:"active"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Active
}

// graphShowsOther reports whether a project-less GET /api/graph as b is
// served from "other", the fixture's project holding secret-plan.
func graphShowsOther(t *testing.T, f *membersFixture, b *browser, header map[string]string, path string) bool {
	t.Helper()
	response := f.do(t, b, http.MethodGet, path, "", header)
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", path, response.Code, response.Body)
	}
	return strings.Contains(response.Body.String(), "secret-plan")
}

func accountID(t *testing.T, f *membersFixture, name string) string {
	t.Helper()
	id, _, ok := f.server.users.FindAccount(context.Background(), name)
	if !ok {
		t.Fatalf("no account %q", name)
	}
	return id
}

// Two accounts switch projects independently, the server's active project
// does not move, and each choice survives a restart.
func TestCurrentProjectIsPerAccount(t *testing.T) {
	f := newMembersFixture(t)
	mainRoot := f.server.pm.Store().Root()

	if active := activeProject(t, f, f.bea); active != "main" {
		t.Fatalf("bea starts on %q, want the server's active project", active)
	}
	if code := f.code(t, f.bea, http.MethodPost, "/api/projects/open", `{"name":"other"}`); code != http.StatusOK {
		t.Fatalf("bea opening other: %d", code)
	}
	if got := activeProject(t, f, f.bea); got != "other" {
		t.Fatalf("bea is on %q after opening other", got)
	}
	if got := activeProject(t, f, f.cal); got != "main" {
		t.Fatalf("bea's switch moved cal to %q", got)
	}
	if f.server.pm.Store().Root() != mainRoot {
		t.Fatal("a per-account switch moved the server's active project")
	}
	if !graphShowsOther(t, f, &f.bea, nil, "/api/graph") {
		t.Fatal("bea's project-less request is not served from her current project")
	}
	if graphShowsOther(t, f, &f.cal, nil, "/api/graph") {
		t.Fatal("cal's project-less request followed bea's switch")
	}
	// A request that names its project is never redirected.
	if graphShowsOther(t, f, &f.bea, nil, "/api/graph?project=main") {
		t.Fatal("?project=main was served from bea's current project")
	}
	if !graphShowsOther(t, f, &f.cal, map[string]string{"X-Nodevas-Project": "other"}, "/api/graph") {
		t.Fatal("X-Nodevas-Project was ignored")
	}

	// Now the other way round, at the same time.
	if code := f.code(t, f.cal, http.MethodPost, "/api/projects/open", `{"name":"other"}`); code != http.StatusOK {
		t.Fatalf("cal opening other: %d", code)
	}
	if code := f.code(t, f.bea, http.MethodPost, "/api/projects/open", `{"name":"main"}`); code != http.StatusOK {
		t.Fatalf("bea opening main: %d", code)
	}
	if got := activeProject(t, f, f.bea); got != "main" {
		t.Fatalf("bea is on %q", got)
	}
	if got := activeProject(t, f, f.cal); got != "other" {
		t.Fatalf("cal is on %q", got)
	}

	// The choice is in the database, not only in memory.
	reloaded := newCurrentProjects(f.server.current.database)
	if err := reloaded.load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.get(accountID(t, f, "cal")); got != "other" {
		t.Fatalf("cal's saved project = %q", got)
	}
	if got := reloaded.get(accountID(t, f, "bea")); got != "main" {
		t.Fatalf("bea's saved project = %q", got)
	}

	// An administrator moving the project keeps cal in it.
	if code := f.code(t, f.ann, http.MethodPost, "/api/projects/move", `{"name":"other","newName":"renamed"}`); code != http.StatusOK {
		t.Fatalf("move: %d", code)
	}
	if got := activeProject(t, f, f.cal); got != "renamed" {
		t.Fatalf("cal is on %q after other was renamed", got)
	}
}

// Nobody is defaulted, or left, in a project they may not open.
func TestCurrentProjectNeverDefaultsIntoRestricted(t *testing.T) {
	f := newMembersFixture(t)
	// dan chose main while it was open.
	if code := f.code(t, f.dan, http.MethodPost, "/api/projects/open", `{"name":"main"}`); code != http.StatusOK {
		t.Fatalf("dan opening main: %d", code)
	}
	// Then main is restricted to bea.
	restrict := f.do(t, &f.ann, http.MethodPut, "/api/projects/members",
		`{"project":"main","user":"bea","role":"editor"}`, nil)
	if restrict.Code != http.StatusOK {
		t.Fatalf("restricting main: %d %s", restrict.Code, restrict.Body)
	}
	for _, b := range []browser{f.dan, f.cal} {
		if got := activeProject(t, f, b); got != "other" {
			t.Fatalf("a non-member of main starts on %q, want other", got)
		}
		b := b
		if !graphShowsOther(t, f, &b, nil, "/api/graph") {
			t.Fatal("a non-member's project-less request is not served from a project they may open")
		}
	}
	if got := activeProject(t, f, f.bea); got != "main" {
		t.Fatalf("bea, a member, starts on %q", got)
	}
	// Opening a project one may not read is refused, as if it did not exist.
	response := f.do(t, &f.dan, http.MethodPost, "/api/projects/open", `{"name":"main"}`, nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("dan opening restricted main: %d %s", response.Code, response.Body)
	}
	// A name that does not exist answers the same, so probing names teaches
	// nothing about which restricted projects exist.
	missing := f.do(t, &f.dan, http.MethodPost, "/api/projects/open", `{"name":"no-such-project"}`, nil)
	if missing.Code != http.StatusNotFound || errorCode(t, missing) != errorCode(t, response) {
		t.Fatalf("dan opening a missing project: %d %s, want the restricted answer %d %s",
			missing.Code, missing.Body, response.Code, response.Body)
	}

	// With nothing left to open, dan is told so rather than served anything.
	restrict = f.do(t, &f.ann, http.MethodPut, "/api/projects/members",
		`{"project":"other","user":"bea","role":"editor"}`, nil)
	if restrict.Code != http.StatusOK {
		t.Fatalf("restricting other: %d %s", restrict.Code, restrict.Body)
	}
	if got := activeProject(t, f, f.dan); got != "" {
		t.Fatalf("dan, a member of nothing, is on %q", got)
	}
	denied := f.do(t, &f.dan, http.MethodGet, "/api/graph", "", nil)
	if denied.Code != http.StatusForbidden || errorCode(t, denied) != "PROJECT_ACCESS_DENIED" {
		t.Fatalf("dan's graph with no project to open: %d %s", denied.Code, denied.Body)
	}
}

// Opening a project still creates one only for an administrator, and the new
// project is the creator's, not everyone's.
func TestCurrentProjectCreate(t *testing.T) {
	f := newMembersFixture(t)
	mainRoot := f.server.pm.Store().Root()
	if code := f.code(t, f.bea, http.MethodPost, "/api/projects/open", `{"name":"fresh","create":true}`); code != http.StatusForbidden {
		t.Fatalf("a member creating a project: %d", code)
	}
	if code := f.code(t, f.ann, http.MethodPost, "/api/projects/open", `{"name":"fresh","create":true}`); code != http.StatusOK {
		t.Fatalf("an administrator creating a project: %d", code)
	}
	if got := activeProject(t, f, f.ann); got != "fresh" {
		t.Fatalf("ann is on %q after creating fresh", got)
	}
	if got := activeProject(t, f, f.bea); got != "main" {
		t.Fatalf("ann's new project moved bea to %q", got)
	}
	if f.server.pm.Store().Root() != mainRoot {
		t.Fatal("creating a project moved the server's active project")
	}
}

// A switch reaches the switching account's own connections and nobody else's.
func TestCurrentProjectSwitchNotifiesOnlyTheAccount(t *testing.T) {
	f := newMembersFixture(t)
	live := httptest.NewServer(f.handler)
	t.Cleanup(live.Close)
	beaTab1 := f.dialAs(t, live, f.bea)
	beaTab2 := f.dialAs(t, live, f.bea)
	cal := f.dialAs(t, live, f.cal)
	for _, peer := range []*wsPeer{beaTab1, beaTab2, cal} {
		peer.send(map[string]any{"type": "subscribe", "project": "main"})
		peer.await("presence", func(event realtime.Event) bool { return event.Project == "main" })
	}

	if code := f.code(t, f.bea, http.MethodPost, "/api/projects/open", `{"name":"other"}`); code != http.StatusOK {
		t.Fatalf("bea opening other: %d", code)
	}
	f.server.hub.Broadcast("marker", "")
	for _, tab := range []*wsPeer{beaTab1, beaTab2} {
		if !seenBefore(t, tab, "project-changed", "marker") {
			t.Fatal("one of bea's tabs was not told her project changed")
		}
	}
	if seenBefore(t, cal, "project-changed", "marker") {
		t.Fatal("cal was pulled along by bea's switch")
	}

	// bea's tab follows into other's room; main's events stop reaching it
	// and stay with cal.
	beaTab1.send(map[string]any{"type": "subscribe", "project": "other"})
	beaTab1.await("presence", func(event realtime.Event) bool { return event.Project == "other" })
	f.server.hub.BroadcastRoom(f.server.pm.Store().Root(), "node-changed", "x")
	f.server.hub.Broadcast("marker", "")
	if seenBefore(t, beaTab1, "node-changed", "marker") {
		t.Fatal("bea's tab still gets the project she left")
	}
	if !seenBefore(t, cal, "node-changed", "marker") {
		t.Fatal("cal missed his own project's event")
	}
}

// A project someone has open is watched like the active one, so edits made
// outside Nodevas reach the people in it. A move drops the watcher (Windows
// will not rename a watched directory) and the next request re-arms it.
func TestCurrentProjectIsWatched(t *testing.T) {
	f := newMembersFixture(t)
	pm := f.server.pm
	if pm.Watching(f.otherRoot) {
		t.Fatal("a project nobody has open is watched")
	}
	if code := f.code(t, f.bea, http.MethodPost, "/api/projects/open", `{"name":"other"}`); code != http.StatusOK {
		t.Fatalf("bea opening other: %d", code)
	}
	if !pm.Watching(f.otherRoot) {
		t.Fatal("bea's current project is not watched")
	}
	if code := f.code(t, f.ann, http.MethodPost, "/api/projects/move", `{"name":"other","newName":"renamed"}`); code != http.StatusOK {
		t.Fatalf("move: %d", code)
	}
	if pm.Watching(f.otherRoot) {
		t.Fatal("the moved project's old directory is still watched")
	}
	renamed := filepath.Join(filepath.Dir(f.otherRoot), "renamed")
	if !graphShowsOther(t, f, &f.bea, nil, "/api/graph") {
		t.Fatal("bea did not follow the move")
	}
	if !pm.Watching(renamed) {
		t.Fatal("bea's request did not re-arm the watcher on the moved project")
	}
}

// MCP sessions reach an accounts server through a runner's proxy, as the
// runner's owner: they resolve the owner's current project unless the call
// names one.
func TestCurrentProjectThroughRunnerProxy(t *testing.T) {
	f := newMembersFixture(t)
	created := f.create(t, f.bea, "bea-pc")
	bearer := map[string]string{"Authorization": "Bearer " + created.Token}
	proxy := runnerwire.ProxyPathPrefix

	if graphShowsOther(t, f, nil, bearer, proxy+"graph") {
		t.Fatal("a proxied call before any switch is not on the owner's default project")
	}
	if code := f.code(t, f.bea, http.MethodPost, "/api/projects/open", `{"name":"other"}`); code != http.StatusOK {
		t.Fatalf("bea opening other: %d", code)
	}
	if !graphShowsOther(t, f, nil, bearer, proxy+"graph") {
		t.Fatal("a proxied call does not follow its owner's current project")
	}
	if graphShowsOther(t, f, nil, bearer, proxy+"graph?project=main") {
		t.Fatal("a proxied call naming its project was redirected")
	}
	// The catalog the MCP session reads says the same.
	response := f.do(t, nil, http.MethodGet, proxy+"projects", "", bearer)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"active":"other"`) {
		t.Fatalf("proxied catalog: %d %s", response.Code, response.Body)
	}
}

// On loopback there is one person and one active project, as before.
func TestCurrentProjectLoopbackUnchanged(t *testing.T) {
	pm := projectManagerForTest(t)
	if err := pm.Activate("other", true, "", ""); err != nil {
		t.Fatal(err)
	}
	otherRoot := pm.Store().Root()
	if err := pm.Activate("main", false, "", ""); err != nil {
		t.Fatal(err)
	}
	hub := realtime.NewHub()
	server := serverForTest(t, pm, hub, nil)
	s := &runnerTestServer{server: server, handler: server.Handler()}
	if server.perAccountProjects() {
		t.Fatal("a loopback server keeps per-account projects")
	}
	if code := s.do(t, nil, http.MethodPost, "/api/projects/open", `{"name":"other"}`, nil).Code; code != http.StatusOK {
		t.Fatalf("open: %d", code)
	}
	if pm.Store().Root() != otherRoot {
		t.Fatal("opening a project on loopback did not move the active project")
	}
	response := s.do(t, nil, http.MethodGet, "/api/projects", "", nil)
	if !strings.Contains(response.Body.String(), `"active":"other"`) {
		t.Fatalf("catalog after open: %s", response.Body)
	}
}
