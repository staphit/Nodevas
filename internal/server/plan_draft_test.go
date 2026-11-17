package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"nodevas/internal/auth"
	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// planRun records a queued plan run of bea's runner on the test node, the
// goal, as a planner; draft makes it a draft plan run (P6).
func (f *runsFixture) planRun(t *testing.T, draft bool) runs.Record {
	t.Helper()
	rec, err := f.server.runs.Store().Create(context.Background(), runs.New{
		Project: f.projectName(), NodeID: runsTestNode, NodeTitle: "Fix the login bug",
		RunnerID: f.runner.Runner.ID, RunnerName: "bea-pc", OwnerID: f.runner.Runner.OwnerID,
		Agent: "claude", Model: "default", RepoID: "repo-1", RepoName: "visual-editor",
		JobPrompt: "# Fix", CreatedBy: f.runner.Runner.OwnerID, CreatedByName: "bea",
		Kind: runnerwire.JobPlan, Role: engine.RolePlanner, Draft: draft,
	})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

type draftAnswer struct {
	OK         bool              `json:"ok"`
	Draft      bool              `json:"draft"`
	IDs        map[string]string `json:"ids"`
	Created    []any             `json:"created"`
	Issues     []any             `json:"issues"`
	Note       string            `json:"note"`
	DraftNodes int               `json:"draftNodes"`
	Code       string            `json:"code"`
	Field      string            `json:"field"`
	Error      string            `json:"error"`
}

func decodeDraftAnswer(t *testing.T, body string) draftAnswer {
	t.Helper()
	var out draftAnswer
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out
}

func (f *runsFixture) nodeCount(t *testing.T) int {
	t.Helper()
	g, _, err := f.server.pm.Store().LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	return len(g.Nodes)
}

func (f *runsFixture) heldDraft(t *testing.T, runID string) runnerwire.PlanDraft {
	t.Helper()
	held, err := f.server.runs.Store().PlanDraft(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	return held
}

func acceptanceJSON() string { return `"acceptance":["it is done","it is tested"]` }

func TestDraftPlanRunCreateSubgraphWritesNothing(t *testing.T) {
	f := newRunsFixture(t)
	run := f.planRun(t, true)
	before := f.nodeCount(t)

	code, body := f.proxied(t, run.ID, http.MethodPost, "graph/subgraph", `{"nodes":[`+
		`{"key":"t1","title":"Add the users table","tags":["goal:`+runsTestNode+`"],`+acceptanceJSON()+`},`+
		`{"key":"t2","title":"Login form","dependsOn":["t1"],`+acceptanceJSON()+`}],`+
		`"edges":[{"from":"t2","to":"`+runsTestNode+`"}]}`, nil)
	if code != http.StatusOK {
		t.Fatalf("draft create_subgraph: %d %s", code, body)
	}
	answer := decodeDraftAnswer(t, body)
	if !answer.OK || !answer.Draft || answer.IDs["t1"] != "draft:t1" || answer.IDs["t2"] != "draft:t2" ||
		answer.Created == nil || len(answer.Created) != 0 || answer.Issues == nil ||
		!strings.Contains(answer.Note, "nothing was created yet") || answer.DraftNodes != 2 {
		t.Fatalf("answer = %s", body)
	}
	if after := f.nodeCount(t); after != before {
		t.Fatalf("the board went from %d to %d nodes", before, after)
	}
	g, _, _ := f.server.pm.Store().LoadGraph()
	for _, n := range g.Nodes {
		if n.Title == "Add the users table" || n.Title == "Login form" {
			t.Fatalf("a draft node reached the board: %+v", n)
		}
		if by := f.server.pm.Store().CreatedByRun(n.ID); by != "" {
			t.Fatalf("%s attributed to run %s", n.ID, by)
		}
	}
	held := f.heldDraft(t, run.ID)
	if held.Calls != 1 || len(held.Nodes) != 2 || held.Nodes[1].DependsOn[0] != "t1" {
		t.Fatalf("held = %+v", held)
	}

	// A second call replaces t1 by key and adds t3 after it.
	code, body = f.proxied(t, run.ID, http.MethodPost, "graph/subgraph", `{"nodes":[`+
		`{"key":"t1","title":"Add the accounts table",`+acceptanceJSON()+`},`+
		`{"key":"t3","title":"Sessions","dependsOn":["draft:t1"],`+acceptanceJSON()+`}]}`, nil)
	if code != http.StatusOK {
		t.Fatalf("second call: %d %s", code, body)
	}
	if answer := decodeDraftAnswer(t, body); answer.DraftNodes != 3 || len(answer.IDs) != 2 || answer.IDs["t3"] != "draft:t3" {
		t.Fatalf("second answer = %s", body)
	}
	held = f.heldDraft(t, run.ID)
	var titles []string
	for _, n := range held.Nodes {
		titles = append(titles, n.Title)
	}
	if held.Calls != 2 || !slices.Equal(titles, []string{"Add the accounts table", "Login form", "Sessions"}) ||
		!slices.Equal(held.Nodes[2].DependsOn, []string{"t1"}) {
		t.Fatalf("held after the second call = %+v", held)
	}

	// An edge into any other node is refused, and nothing is held.
	code, body = f.proxied(t, run.ID, http.MethodPost, "graph/subgraph",
		`{"nodes":[{"key":"t4","title":"Other",`+acceptanceJSON()+`}],"edges":[{"from":"t4","to":"elsewhere"}]}`, nil)
	if answer := decodeDraftAnswer(t, body); code != http.StatusBadRequest || answer.Code != "INVALID_SUBGRAPH" || answer.Field != "edges[0].to" {
		t.Fatalf("edge to another node: %d %s", code, body)
	}
	if held := f.heldDraft(t, run.ID); held.Calls != 2 || len(held.Nodes) != 3 {
		t.Fatalf("a refused call changed the draft: %+v", held)
	}
}

func TestDraftPlanRunOtherWritesGet409(t *testing.T) {
	f := newRunsFixture(t)
	run := f.planRun(t, true)
	goal := "nodes/" + runsTestNode
	for _, write := range []struct{ method, path, body string }{
		{http.MethodPost, "nodes", `{"title":"One by one"}`},
		{http.MethodPut, goal, `{"content":"x"}`},
		{http.MethodPost, goal + "/status", `{"status":"in_progress","by":"run"}`},
		{http.MethodPost, goal + "/claim", `{"owner":"run"}`},
		{http.MethodPost, goal + "/release", `{}`},
		{http.MethodPost, goal + "/acceptance", `{"items":["x"]}`},
		{http.MethodPost, "graph/ops", `{"ops":[]}`},
	} {
		code, body := f.proxied(t, run.ID, write.method, write.path, write.body, nil)
		if answer := decodeDraftAnswer(t, body); code != http.StatusConflict || answer.Code != CodePlanRunDrafts ||
			!strings.Contains(answer.Error, "one create_subgraph call") {
			t.Errorf("%s %s: %d %s", write.method, write.path, code, body)
		}
	}
	for _, read := range []struct{ method, path, body string }{
		{http.MethodGet, "graph", ""},
		{http.MethodGet, goal, ""},
		{http.MethodPost, "agents/heartbeat", `{"sessionId":"planner-1"}`},
	} {
		if code, body := f.proxied(t, run.ID, read.method, read.path, read.body, nil); code != http.StatusOK {
			t.Errorf("%s %s: %d %s", read.method, read.path, code, body)
		}
	}
}

func TestDraftPlanRunCheckFailureAnswersAsACreate(t *testing.T) {
	f := newRunsFixture(t)
	draft, plain := f.planRun(t, true), f.planRun(t, false)
	for _, bad := range []string{
		`{"nodes":[{"key":"a","title":"A","dependsOn":["nope"],` + acceptanceJSON() + `}]}`,
		`{"nodes":[{"key":"a","title":"A",` + acceptanceJSON() + `},{"key":"a","title":"again",` + acceptanceJSON() + `}]}`,
		`{"nodes":[{"key":"a","title":"A","kind":"custom-type-9",` + acceptanceJSON() + `}]}`,
	} {
		draftCode, draftBody := f.proxied(t, draft.ID, http.MethodPost, "graph/subgraph", bad, nil)
		plainCode, plainBody := f.proxied(t, plain.ID, http.MethodPost, "graph/subgraph", bad, nil)
		d, p := decodeDraftAnswer(t, draftBody), decodeDraftAnswer(t, plainBody)
		if draftCode == http.StatusOK || draftCode != plainCode || d.Code != p.Code || d.Field != p.Field {
			t.Errorf("%s:\n draft %d %s\n plain %d %s", bad, draftCode, draftBody, plainCode, plainBody)
		}
	}
	if held := f.heldDraft(t, draft.ID); held.Calls != 0 || len(held.Nodes) != 0 {
		t.Fatalf("refused calls were held: %+v", held)
	}
}

func TestDraftPlanRunCallsAreBounded(t *testing.T) {
	f := newRunsFixture(t)
	// Eleven create_subgraph calls in a row are more than the expensive-work
	// limiter lets one account make at once.
	f.server.SetAbuseOptions(AbuseGuardOptions{ExpensiveGlobalRate: 1000, ExpensiveGlobalBurst: 1000,
		ExpensiveIPRate: 1000, ExpensiveIPBurst: 1000, ExpensiveActorRate: 1000, ExpensiveActorBurst: 1000})
	run := f.planRun(t, true)
	for i := range runnerwire.MaxPlanDraftCalls {
		body := fmt.Sprintf(`{"nodes":[{"key":"k%d","title":"T%d",%s}]}`, i, i, acceptanceJSON())
		if code, answer := f.proxied(t, run.ID, http.MethodPost, "graph/subgraph", body, nil); code != http.StatusOK {
			t.Fatalf("call %d: %d %s", i+1, code, answer)
		}
	}
	code, body := f.proxied(t, run.ID, http.MethodPost, "graph/subgraph", `{"nodes":[{"key":"k9","title":"T9",`+acceptanceJSON()+`}]}`, nil)
	if answer := decodeDraftAnswer(t, body); code != http.StatusRequestEntityTooLarge || answer.Code != "SUBGRAPH_TOO_LARGE" {
		t.Fatalf("ninth call: %d %s", code, body)
	}

	many := func(from, n int) string {
		nodes := make([]string, n)
		for i := range nodes {
			nodes[i] = fmt.Sprintf(`{"key":"m%d","title":"M%d",%s}`, from+i, from+i, acceptanceJSON())
		}
		return `{"nodes":[` + strings.Join(nodes, ",") + `]}`
	}
	other := f.planRun(t, true)
	if code, body := f.proxied(t, other.ID, http.MethodPost, "graph/subgraph", many(0, 60), nil); code != http.StatusOK {
		t.Fatalf("60 nodes: %d %s", code, body)
	}
	code, body = f.proxied(t, other.ID, http.MethodPost, "graph/subgraph", many(60, 41), nil)
	if answer := decodeDraftAnswer(t, body); code != http.StatusRequestEntityTooLarge || answer.Code != "SUBGRAPH_TOO_LARGE" {
		t.Fatalf("101 merged nodes: %d %s", code, body)
	}
	if held := f.heldDraft(t, other.ID); len(held.Nodes) != 60 || held.Calls != 1 {
		t.Fatalf("held after the oversize call: %d nodes, %d calls", len(held.Nodes), held.Calls)
	}
}

func TestDraftPlanRunRefusesFrontMatter(t *testing.T) {
	f := newRunsFixture(t)
	run := f.planRun(t, true)
	for _, front := range []string{
		"---\nassignee: bea\nrequires: x\neffects:\n  - set: x = true\n---\nwork",
		"\n---\npriority: high\n---\n",
	} {
		body, _ := json.Marshal(map[string]any{"nodes": []map[string]any{
			{"key": "t1", "title": "T1", "body": front, "acceptance": []string{"done", "tested"}},
		}})
		code, answer := f.proxied(t, run.ID, http.MethodPost, "graph/subgraph", string(body), nil)
		if got := decodeDraftAnswer(t, answer); code != http.StatusBadRequest || got.Code != "INVALID_SUBGRAPH" || got.Field != "nodes[0].body" {
			t.Errorf("%q: %d %s", front, code, answer)
		}
	}
	if held := f.heldDraft(t, run.ID); held.Calls != 0 || len(held.Nodes) != 0 {
		t.Fatalf("front matter was held: %+v", held)
	}
}

// Every write the runner proxy forwards, decided by hand: refused while a
// draft plan run is active (a board write other than its create_subgraph),
// or allowed. A write route added to runnerProxyRoutes fails this test until
// it is listed here, so each one is a deliberate decision (D3).
var planDraftWriteDecisions = map[string]bool{
	"POST /api/nodes":                     true,
	"PUT /api/nodes/*":                    true,
	"POST /api/nodes/*/claim":             true,
	"POST /api/nodes/*/release":           true,
	"POST /api/nodes/*/status":            true,
	"POST /api/nodes/*/acceptance":        true,
	"POST /api/graph/ops":                 true,
	"POST /api/graph/subgraph":            false, // recorded as the draft, not written
	"POST /api/agents/heartbeat":          false,
	"POST /api/agents/leave":              false,
	"POST /api/agents/messages":           false,
	"POST /api/agents/inbox":              false,
	"POST /api/agents/asks":               false,
	"POST /api/agents/sessions/*/changes": false, // file edits and snapshots, no nodes
	"POST /api/agents/sessions/*/usage":   false,
	"POST /api/verdicts":                  false,
}

func TestPlanDraftRefusesEveryGraphWriteOnTheProxyAllowlist(t *testing.T) {
	seen := map[string]bool{}
	for _, route := range runnerProxyRoutes {
		target := strings.ReplaceAll(route.pattern, "*", "x")
		got := planDraftRefuses(route.method, target)
		if route.method == http.MethodGet {
			if got {
				t.Errorf("GET %s (a read) is refused", route.pattern)
			}
			continue
		}
		name := route.method + " " + route.pattern
		seen[name] = true
		want, decided := planDraftWriteDecisions[name]
		if !decided {
			t.Errorf("%s is on the proxy allowlist but not in planDraftWriteDecisions: decide whether a draft plan run may make it", name)
			continue
		}
		if got != want {
			t.Errorf("%s: refused %v, want %v", name, got, want)
		}
	}
	for name := range planDraftWriteDecisions {
		if !seen[name] {
			t.Errorf("%s is decided but no longer on the proxy allowlist", name)
		}
	}
}

func TestNonDraftPlanRunStillWrites(t *testing.T) {
	f := newRunsFixture(t)
	run := f.planRun(t, false)
	before := f.nodeCount(t)
	code, body := f.proxied(t, run.ID, http.MethodPost, "graph/subgraph", `{"nodes":[`+
		`{"key":"t1","title":"Add the users table",`+acceptanceJSON()+`}]}`, nil)
	if code != http.StatusCreated {
		t.Fatalf("plain plan run: %d %s", code, body)
	}
	answer := decodeDraftAnswer(t, body)
	if answer.Draft || len(answer.Created) != 1 || f.nodeCount(t) != before+1 {
		t.Fatalf("plain plan run: %s", body)
	}
	if by := f.server.pm.Store().CreatedByRun(answer.IDs["t1"]); by != run.ID {
		t.Fatalf("created by %q, want %s", by, run.ID)
	}
	if held := f.heldDraft(t, run.ID); held.Calls != 0 {
		t.Fatalf("a plain run's write was held: %+v", held)
	}
}

// doServerTool is f.do with the copilot server tool's marker on the
// request's context, as serverToolEnv.do sends it in-process.
func (f *runsFixture) doServerTool(t *testing.T, b browser, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	withCookies(request, b.cookies)
	request.Header.Set(auth.CSRFHeaderName, b.csrf)
	request = request.WithContext(auth.WithCopilotServerTool(request.Context()))
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

// POST /api/runs takes draft:true only from the copilot's own server tool,
// replayed in-process (auth.CopilotServerTool); a person's direct POST is
// refused before anything is recorded. (The runs HTTP API has no fixture of
// its own; this is that package's create, through the whole server.)
func TestCreateRunDraftOnlyFromACopilotServerTool(t *testing.T) {
	f := newRunsFixture(t)
	hello := runsHello(1)
	hello.Hello.Capabilities = append(hello.Hello.Capabilities, runnerwire.CapPlan)
	f.connect(t, hello)
	body, _ := json.Marshal(runnerwire.CreateRunRequest{
		NodeID: runsTestNode, RunnerID: f.runner.Runner.ID, Agent: "claude", RepoID: "repo-1",
		Kind: runnerwire.JobPlan, Draft: true,
	})

	response := f.do(t, &f.bea, http.MethodPost, "/api/runs", string(body), nil)
	if response.Code != http.StatusBadRequest || errorCode(t, response) != "INVALID_ARGUMENT" ||
		!strings.Contains(response.Body.String(), "draft is only") {
		t.Fatalf("a person's draft POST: %d %s", response.Code, response.Body)
	}
	all, err := f.server.runs.Store().List(context.Background(), runs.Filter{Project: f.projectName(), Limit: 50})
	if err != nil || len(all) != 0 {
		t.Fatalf("runs after a refused draft = %d, %v", len(all), err)
	}

	response = f.doServerTool(t, f.bea, http.MethodPost, "/api/runs", string(body))
	if response.Code != http.StatusCreated {
		t.Fatalf("the copilot's draft POST: %d %s", response.Code, response.Body)
	}
	var run runnerwire.Run
	if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil || !run.Draft || run.Kind != runnerwire.JobPlan {
		t.Fatalf("run = %+v (%v)", run, err)
	}
}

// The hub records a draft node's kind as the store reads it (lowercase, an
// alias as its canonical id), so the card and Create see the board's ids.
func TestDraftPlanRunRecordsCanonicalKinds(t *testing.T) {
	f := newRunsFixture(t)
	run := f.planRun(t, true)
	code, body := f.proxied(t, run.ID, http.MethodPost, "graph/subgraph", `{"nodes":[`+
		`{"key":"r1","title":"Editorial pass","kind":"review"},`+
		`{"key":"t1","title":"Write it","kind":" Task ",`+acceptanceJSON()+`},`+
		`{"key":"t2","title":"Ship it",`+acceptanceJSON()+`}]}`, nil)
	if code != http.StatusOK {
		t.Fatalf("draft create_subgraph: %d %s", code, body)
	}
	var kinds []string
	for _, n := range f.heldDraft(t, run.ID).Nodes {
		kinds = append(kinds, n.Kind)
	}
	if !slices.Equal(kinds, []string{"gate", "task", "task"}) {
		t.Fatalf("held kinds = %q", kinds)
	}
}
