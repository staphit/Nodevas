package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"nodevas/internal/agents"
	"nodevas/internal/autopilot"
	stationapi "nodevas/internal/httpapi/station"
	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

func (f *membersFixture) building(t *testing.T, b browser, query string) stationapi.BuildingResponse {
	t.Helper()
	response := f.do(t, &b, http.MethodGet, "/api/station/building"+query, "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/station/building%s = %d %s", query, response.Code, response.Body)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	var out stationapi.BuildingResponse
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func buildingByName(out stationapi.BuildingResponse) map[string]stationapi.BuildingProject {
	m := map[string]stationapi.BuildingProject{}
	for _, p := range out.Projects {
		m[p.Name] = p
	}
	return m
}

// actorID is the account id behind a signed-in browser.
func (f *membersFixture) actorID(t *testing.T, b browser) string {
	t.Helper()
	response := f.do(t, &b, http.MethodGet, "/api/auth/status", "", nil)
	var status struct {
		Actor identity.Actor `json:"actor"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil || status.Actor.ID == "" {
		t.Fatalf("auth status = %s, %v", response.Body, err)
	}
	return status.Actor.ID
}

func (f *membersFixture) seedRun(t *testing.T, project string, b browser, name string) runs.Record {
	t.Helper()
	by := f.actorID(t, b)
	r, err := f.server.runs.Store().Create(context.Background(), runs.New{
		Project: project, NodeID: "n-" + name, NodeTitle: "Work of " + name, RunnerID: "r-" + name, RunnerName: name + "-pc",
		OwnerID: by, Agent: "claude", Model: "opus", RepoID: "repo", JobPrompt: "# Work", CreatedBy: by, CreatedByName: name,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The building shows each viewer the projects they may read: their own runs
// in full, others' runs only as a count.
func TestStationBuildingIsFilteredPerProject(t *testing.T) {
	f := newMembersFixture(t)
	// other becomes bea's (owner) and cal's; dan may not read it.
	if response := f.setMember(t, f.bea, "cal", "viewer"); response.Code != http.StatusOK {
		t.Fatalf("restricting: %d %s", response.Code, response.Body)
	}
	beas := f.seedRun(t, "main", f.bea, "bea")
	f.seedRun(t, "main", f.cal, "cal")
	inOther := f.seedRun(t, "other", f.bea, "bea")

	got := buildingByName(f.building(t, f.bea, ""))
	main, other := got["main"], got["other"]
	if len(got) != 2 || len(main.Runs) != 1 || main.Runs[0].ID != beas.ID || main.Runs[0].Title != "Work of bea" ||
		main.Runs[0].RunnerName != "bea-pc" || main.OthersActive != 1 {
		t.Fatalf("bea's main = %+v (projects %d)", main, len(got))
	}
	if len(other.Runs) != 1 || other.Runs[0].ID != inOther.ID || other.Label != "other" ||
		other.OfficeStyle == "" || other.Waiting.Total != 0 {
		t.Fatalf("bea's other = %+v", other)
	}

	dans := f.building(t, f.dan, "")
	got = buildingByName(dans)
	if _, ok := got["other"]; ok || dans.Total != 1 {
		t.Fatalf("dan sees a project he may not read: %+v", dans)
	}
	if main := got["main"]; len(main.Runs) != 0 || main.OthersActive != 2 {
		t.Fatalf("dan's main = %+v", main)
	}

	got = buildingByName(f.building(t, f.ann, ""))
	if main := got["main"]; len(main.Runs) != 2 || main.OthersActive != 0 || len(got) != 2 {
		t.Fatalf("the administrator's main = %+v", main)
	}

	// The route checks every project itself: naming one dan may not read
	// neither gates the request nor lets that project in.
	if got := buildingByName(f.building(t, f.dan, "?project=other")); len(got) != 1 || got["main"].Name != "main" {
		t.Fatalf("dan naming other = %+v", got)
	}
}

// Asks and held mail are counted for the person who may answer them.
func TestStationBuildingCountsWhatWaitsOnTheViewer(t *testing.T) {
	f := newMembersFixture(t)
	ctx := context.Background()
	root := f.server.pm.Store().Root()
	hub := f.server.agents
	bea, cal := f.actorID(t, f.bea), f.actorID(t, f.cal)
	for _, hb := range []agents.Heartbeat{
		{SessionID: "s-bea", Project: root, ActorID: bea, Name: "bea-claude", Agent: "claude", State: agents.StateBusy},
		{SessionID: "s-cal", Project: root, ActorID: cal, Name: "cal-codex", Agent: "codex"},
	} {
		if _, err := hub.Touch(hb); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := hub.SetInbound(root, "s-bea", agents.InboundHold, bea, false); err != nil {
		t.Fatal(err)
	}
	if res, err := hub.Send(ctx, agents.SendInput{
		Project: root, SenderActor: cal, SenderSession: "cal-codex", To: "bea-claude", Body: "hi",
	}); err != nil || res.Deliveries[0].Status != agents.StatusHeld {
		t.Fatalf("send = %+v, %v", res, err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = hub.CreateAndWait(ctx, agents.AskInput{
			Project: root, SessionID: "s-bea", SessionName: "bea-claude", ActorID: bea, Question: "Ship it?",
		}, 5*time.Second)
	}()
	t.Cleanup(func() { <-done })
	var pending []agents.Ask
	for range 200 {
		if pending, _ = hub.ListAsks(ctx, root, agents.AskPending); len(pending) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(pending) != 1 {
		t.Fatal("the ask never became pending")
	}
	defer func() {
		_, _ = hub.Answer(ctx, root, pending[0].ID, agents.AskApproved, "", agents.Viewer{ID: bea})
	}()

	main := buildingByName(f.building(t, f.bea, ""))["main"]
	if main.Waiting != (stationapi.BuildingWaiting{Asks: 1, Held: 1, Total: 2}) || len(main.Asks) != 1 ||
		main.Asks[0].Question != "Ship it?" || len(main.Sessions) != 2 {
		t.Fatalf("bea's main = %+v", main)
	}
	main = buildingByName(f.building(t, f.cal, ""))["main"]
	if main.Waiting.Total != 0 || len(main.Asks) != 0 {
		t.Fatalf("cal sees bea's questions: %+v", main.Waiting)
	}
}

// Pings are throttled per project: a burst becomes the first ping and one
// trailing ping.
func TestBuildingPingsAreThrottled(t *testing.T) {
	pings := newBuildingPings(50 * time.Millisecond)
	var sent atomic.Int32
	send := func(string) { sent.Add(1) }
	pings.ping("/a", send)
	pings.ping("/a", send)
	pings.ping("/a", send)
	pings.ping("/b", send) // another project has its own budget
	if n := sent.Load(); n != 2 {
		t.Fatalf("immediate pings = %d, want 2", n)
	}
	deadline := time.Now().Add(2 * time.Second)
	for sent.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if n := sent.Load(); n != 3 {
		t.Fatalf("pings after the interval = %d, want 3 (one trailing)", n)
	}
}

// A change in one project reaches a client watching another project, as
// building-changed with an empty id; run event streams do not.
func TestBuildingChangedReachesReadersOfOtherProjects(t *testing.T) {
	httpServer, server := hubTestServer(t)
	server.buildingPings = newBuildingPings(10 * time.Millisecond)
	watcher := joinRoom(t, httpServer, "main")

	server.broadcastProject("other", runnerwire.RunEventsEvent, "run-1")
	watcher.awaitNone(BuildingChangedEvent, 100*time.Millisecond)

	server.broadcastProject("other", runnerwire.RunsChangedEvent, "node-1")
	if event := watcher.await(BuildingChangedEvent, nil); event.ID != "" {
		t.Fatalf("building-changed carries an id: %+v", event)
	}
	time.Sleep(30 * time.Millisecond)
	server.broadcastProject("other", autopilot.ChangedEvent, "ap-1")
	watcher.await(BuildingChangedEvent, nil)

	// An agent's presence change pings the building; a plain heartbeat does
	// not, and presence itself is not broadcast as its own event.
	time.Sleep(30 * time.Millisecond)
	other, err := server.pm.StoreFor("other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.agents.Touch(agents.Heartbeat{SessionID: "s1", Project: other.Root(), ActorID: "local", Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	watcher.await(BuildingChangedEvent, nil)
	time.Sleep(30 * time.Millisecond)
	if _, err := server.agents.Touch(agents.Heartbeat{SessionID: "s1", Project: other.Root(), ActorID: "local"}); err != nil {
		t.Fatal(err)
	}
	watcher.awaitNone(BuildingChangedEvent, 100*time.Millisecond)
}
