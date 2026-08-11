package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/project"
	"nodevas/internal/realtime"
)

// readyQueueServer builds a workspace whose active project is a short chain:
// design blocks build, and ship waits on a flag nothing has set.
func readyQueueServer(t *testing.T) (*Server, *project.ProjectManager) {
	t.Helper()
	pm := projectManagerForTest(t)
	graph := &engine.Graph{
		Version: 1,
		// An assignee must name a declared user; the graph will not save
		// otherwise.
		Users: []engine.User{{ID: "claude", Name: "claude"}},
		Nodes: []*engine.Node{
			{ID: "design", Title: "Design", Priority: "high"},
			{ID: "build", Title: "Build", Assignee: "claude", Tags: []string{"backend"}, Requires: "design"},
			{ID: "ship", Title: "Ship", Requires: "flag(approved)"},
		},
		Edges: []*engine.Edge{{From: "design", To: "build"}},
		Flags: map[string]any{"approved": false},
	}
	data, err := engine.MarshalGraph(graph)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pm.Store().Root(), "graph.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return serverForTest(t, pm, realtime.NewHub(), nil), pm
}

type readyResponse struct {
	Tasks   []engine.ReadyNode `json:"tasks"`
	Blocked []engine.ReadyNode `json:"blocked"`
	Ready   int                `json:"ready"`
	Busy    int                `json:"busy"`
	Waiting int                `json:"waiting"`
	Cursor  string             `json:"cursor"`
}

func getReadyQueue(t *testing.T, server *Server, query string) readyResponse {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/ready"+query, nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	var body readyResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body %s)", err, response.Body)
	}
	return body
}

func taskIDs(tasks []engine.ReadyNode) []string {
	out := make([]string, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task.ID)
	}
	return out
}

func TestTheReadyQueueOffersOnlyWorkWhoseDependenciesAreMet(t *testing.T) {
	server, _ := readyQueueServer(t)

	queue := getReadyQueue(t, server, "")
	if got := taskIDs(queue.Tasks); len(got) != 1 || got[0] != "design" {
		t.Fatalf("tasks = %v, want only design", got)
	}
	if queue.Waiting != 2 {
		t.Fatalf("waiting = %d, want 2 (build and ship)", queue.Waiting)
	}
	// Without asking, the blocked list stays off the wire.
	if queue.Blocked != nil {
		t.Fatalf("blocked was returned unasked: %v", taskIDs(queue.Blocked))
	}
}

// An empty queue means two opposite things — finished, or waiting on a person.
// The agent must be able to tell them apart without a second round trip.
func TestTheQueueSaysWhatEverythingIsWaitingOn(t *testing.T) {
	server, _ := readyQueueServer(t)

	queue := getReadyQueue(t, server, "?includeBlocked=true")
	reasons := map[string]string{}
	waiting := map[string][]string{}
	for _, task := range queue.Blocked {
		reasons[task.ID] = task.Reason
		waiting[task.ID] = task.BlockedBy
	}
	if reasons["build"] != "prerequisites" {
		t.Fatalf("build blocked for %q, want prerequisites", reasons["build"])
	}
	if len(waiting["build"]) != 1 || waiting["build"][0] != "design" {
		t.Fatalf("build waits on %v, want design", waiting["build"])
	}
	if reasons["ship"] != "requires" {
		t.Fatalf("ship blocked for %q, want requires", reasons["ship"])
	}
}

func TestFinishingATaskMovesTheNextOneIntoTheQueue(t *testing.T) {
	server, pm := readyQueueServer(t)

	if _, err := pm.Store().SetStatus("design", engine.StatusDone, "claude", ""); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	queue := getReadyQueue(t, server, "")
	if got := taskIDs(queue.Tasks); len(got) != 1 || got[0] != "build" {
		t.Fatalf("tasks = %v, want only build", got)
	}
	if queue.Busy != 1 {
		t.Fatalf("busy = %d, want 1 (design)", queue.Busy)
	}
}

func TestTheQueueCanBeNarrowedToOneAssigneeOrTag(t *testing.T) {
	server, pm := readyQueueServer(t)
	if _, err := pm.Store().SetStatus("design", engine.StatusDone, "someone", ""); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	if got := taskIDs(getReadyQueue(t, server, "?assignee=claude").Tasks); len(got) != 1 ||
		got[0] != "build" {
		t.Fatalf("assignee filter returned %v, want build", got)
	}
	if got := taskIDs(getReadyQueue(t, server, "?assignee=nobody").Tasks); len(got) != 0 {
		t.Fatalf("assignee filter returned %v for an unknown person", got)
	}
	if got := taskIDs(getReadyQueue(t, server, "?tag=backend").Tasks); len(got) != 1 ||
		got[0] != "build" {
		t.Fatalf("tag filter returned %v, want build", got)
	}
}

// A limit the caller cannot have meant is refused rather than quietly clamped:
// silently returning 200 of 5000 reads as "that was all of them".
func TestAnOversizedLimitIsRefused(t *testing.T) {
	server, _ := readyQueueServer(t)

	request := httptest.NewRequest(http.MethodGet, "/api/ready?limit=5000", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}

func TestTheQueuePagesWithoutRepeatingOrSkipping(t *testing.T) {
	server, pm := readyQueueServer(t)
	graph, rev, err := pm.Store().LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	// Added rather than replaced: a graph save may not remove nodes, and the
	// point here is the paging, not the fixture.
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		graph.Nodes = append(graph.Nodes, &engine.Node{ID: id, Title: id})
	}
	if _, err := pm.Store().SaveGraph(graph, rev); err != nil {
		t.Fatalf("SaveGraph: %v", err)
	}

	seen := []string{}
	cursor := ""
	for range 10 {
		query := "?limit=2"
		if cursor != "" {
			query += "&cursor=" + cursor
		}
		body := getReadyQueue(t, server, query)
		seen = append(seen, taskIDs(body.Tasks)...)
		if body.Cursor == "" {
			break
		}
		cursor = body.Cursor
	}
	// design plus the five added ones; build and ship are blocked.
	if len(seen) != 6 {
		t.Fatalf("paging saw %v, want six distinct nodes", seen)
	}
	unique := map[string]bool{}
	for _, id := range seen {
		if unique[id] {
			t.Fatalf("paging returned %q twice: %v", id, seen)
		}
		unique[id] = true
	}
}

func TestTheReadyQueueCarriesAnETag(t *testing.T) {
	server, _ := readyQueueServer(t)

	first := conditionalGet(t, server, "/api/ready", "")
	tag := first.Header().Get("ETag")
	if tag == "" {
		t.Fatal("/api/ready did not carry an ETag")
	}
	if repeat := conditionalGet(t, server, "/api/ready", tag); repeat.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304 for an unchanged queue", repeat.Code)
	}
}
