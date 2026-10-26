package copilot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"nodevas/internal/auth"
	"nodevas/internal/copilot/provider"
	"nodevas/internal/copilot/toolbridge"
	"nodevas/internal/runnerwire"
)

// fakeAPI is the slice of the server's API the server tools call.
type fakeAPI struct {
	mu       sync.Mutex
	requests []recorded
	runners  []runnerwire.Runner
	// runsStatus and runsError make POST /api/runs fail.
	runsStatus int
	runsError  string
	autopilot  string // the body POST /api/autopilot answers with
	// For revise_goal (revise_test.go): the board outline GET /api/graph
	// answers with, the runs still going, the goal's latest autopilot (GET
	// /api/autopilot?goal=; empty is none) and the goal node's content.
	graph       string
	active      []runnerwire.Run
	latest      string
	goalContent string
}

type recorded struct {
	method, path, project, cookie string
	agent                         bool
	body                          map[string]any
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	_, agent := auth.InProcessAgent(r.Context())
	f.mu.Lock()
	f.requests = append(f.requests, recorded{method: r.Method, path: r.URL.Path,
		project: r.URL.Query().Get("project"), cookie: r.Header.Get("Cookie"), agent: agent, body: body})
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.Method + " " + r.URL.Path {
	case "GET /api/auth/status":
		_, _ = w.Write([]byte(`{"mode":"local"}`))
	case "GET /api/runners":
		_ = json.NewEncoder(w).Encode(runnerwire.ListResponse{Runners: f.runners})
	case "POST /api/nodes":
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true,"id":"node-0042"}`))
	case "POST /api/runs":
		if f.runsStatus != 0 {
			w.WriteHeader(f.runsStatus)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": f.runsError})
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"run-7","status":"queued"}`))
	case "POST /api/autopilot":
		w.WriteHeader(http.StatusCreated)
		answer := f.autopilot
		if answer == "" {
			answer = `{"id":"ap-1","status":"running"}`
		}
		_, _ = w.Write([]byte(answer))
	case "GET /api/graph":
		if f.graph == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(f.graph))
	case "GET /api/runs":
		_ = json.NewEncoder(w).Encode(runnerwire.RunListResponse{Runs: f.active})
	case "GET /api/autopilot":
		if f.latest == "" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"autopilot not found"}`))
			return
		}
		_, _ = w.Write([]byte(f.latest))
	case "GET /api/nodes/node-0042":
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "node-0042", "content": f.goalContent, "rev": "rev-1"})
	case "PUT /api/nodes/node-0042":
		_, _ = w.Write([]byte(`{"ok":true,"rev":"rev-2"}`))
	case "POST /api/autopilot/ap-1/pause", "POST /api/autopilot/ap-1/adopt":
		_, _ = w.Write([]byte(`{"run":{"id":"ap-1","status":"running","counts":{"total":4}},"tasks":[]}`))
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"no such route"}`))
	}
}

func (f *fakeAPI) calls() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.requests...)
}

func (f *fakeAPI) find(method, path string) *recorded {
	for _, r := range f.calls() {
		if r.method == method && r.path == path {
			return &r
		}
	}
	return nil
}

func runnerInfo(repos ...runnerwire.Repo) *runnerwire.Info {
	return &runnerwire.Info{Capabilities: []string{runnerwire.CapHeadless}, Repos: repos,
		Agents: []runnerwire.Agent{
			{Kind: "codex", Installed: true, Models: []runnerwire.Model{{ID: "gpt-5.5", Default: true}}},
			{Kind: "claude", Installed: true, Models: []runnerwire.Model{{ID: "opus"}, {ID: "default", Default: true}}},
			{Kind: "gemini", Installed: false},
		}}
}

func standardAPI() *fakeAPI {
	return &fakeAPI{runners: []runnerwire.Runner{
		{ID: "r-bob", OwnerID: "u-bob", Name: "bob pc", Online: true, Info: runnerInfo(runnerwire.Repo{ID: "rb", Name: "bob-repo"})},
		{ID: "r-off", OwnerID: "u-alice", Name: "laptop", Online: false, Info: runnerInfo(runnerwire.Repo{ID: "r0", Name: "old"})},
		{ID: "r-ann", OwnerID: "u-alice", Name: "pc", Online: true,
			Info: runnerInfo(runnerwire.Repo{ID: "r1", Name: "app"}, runnerwire.Repo{ID: "r2", Name: "site"})},
	}}
}

func toolEnv(api *fakeAPI) *serverToolEnv {
	return &serverToolEnv{handler: api, host: "nodevas.example", cookie: "session=abc",
		remoteAddr: "203.0.113.9:5000", project: "main", accountID: "u-alice"}
}

func TestStartBreakdownFilesTheGoalAndDispatchesAPlanRun(t *testing.T) {
	api := standardAPI()
	result := runStartBreakdown(context.Background(), toolEnv(api),
		json.RawMessage(`{"requirement":"Add CSV export to the reports page\nwith a header row"}`))
	if result.IsError {
		t.Fatalf("result = %s", result.Output)
	}
	node := api.find(http.MethodPost, "/api/nodes")
	if node == nil || node.body["title"] != "Add CSV export to the reports page" ||
		!strings.Contains(node.body["body"].(string), "header row") || node.body["kind"] != "task" {
		t.Fatalf("node request = %+v", node)
	}
	run := api.find(http.MethodPost, "/api/runs")
	if run == nil {
		t.Fatal("no run dispatched")
	}
	want := map[string]any{"nodeId": "node-0042", "runnerId": "r-ann", "agent": "claude", "model": "default",
		"repoId": "r1", "kind": runnerwire.JobPlan}
	for k, v := range want {
		if run.body[k] != v {
			t.Errorf("run %s = %v, want %v", k, run.body[k], v)
		}
	}
	for _, r := range api.calls() {
		if r.project != "main" || r.cookie != "session=abc" || r.agent {
			t.Errorf("%s %s: project %q cookie %q in-process agent %v", r.method, r.path, r.project, r.cookie, r.agent)
		}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil || out["goalNodeId"] != "node-0042" ||
		out["runId"] != "run-7" || out["goalCreated"] != true || out["otherRepos"] == nil {
		t.Fatalf("output = %s", result.Output)
	}
}

func TestStartBreakdownChoices(t *testing.T) {
	api := standardAPI()
	result := runStartBreakdown(context.Background(), toolEnv(api),
		json.RawMessage(`{"goalNodeId":"node-0001","agent":"codex","repoId":"site"}`))
	if result.IsError {
		t.Fatalf("result = %s", result.Output)
	}
	if api.find(http.MethodPost, "/api/nodes") != nil {
		t.Fatal("a node was created for an existing goal")
	}
	run := api.find(http.MethodPost, "/api/runs")
	if run.body["nodeId"] != "node-0001" || run.body["agent"] != "codex" || run.body["model"] != "gpt-5.5" || run.body["repoId"] != "r2" {
		t.Fatalf("run = %+v", run.body)
	}
}

func TestStartBreakdownErrors(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name, input string
		api         func(*fakeAPI)
		want        string
		noNode      bool
	}{
		{"nothing to break down", `{}`, nil, "give the requirement", true},
		{"no runner online", `{"requirement":"x"}`, func(f *fakeAPI) { f.runners = f.runners[:2] },
			"none of your runners is online", true},
		{"only someone else's runner", `{"requirement":"x","runnerId":"r-bob"}`, nil, "not one of your online runners", true},
		{"agent not installed", `{"requirement":"x","agent":"gemini"}`, nil, "gemini installed", true},
		{"the API refuses the run", `{"requirement":"x"}`, func(f *fakeAPI) {
			f.runsStatus, f.runsError = http.StatusBadRequest, "kind must be plan or empty"
		}, "Created the goal node node-0042 but could not start the plan run", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := standardAPI()
			if tc.api != nil {
				tc.api(api)
			}
			result := runStartBreakdown(ctx, toolEnv(api), json.RawMessage(tc.input))
			if !result.IsError || !strings.Contains(result.Output, tc.want) {
				t.Fatalf("result = %+v, want an error with %q", result, tc.want)
			}
			if tc.noNode && api.find(http.MethodPost, "/api/nodes") != nil {
				t.Fatal("a goal node was created anyway")
			}
		})
	}
	api := standardAPI()
	api.runsStatus, api.runsError = http.StatusBadRequest, "kind must be plan or empty"
	result := runStartBreakdown(ctx, toolEnv(api), json.RawMessage(`{"requirement":"x"}`))
	if !strings.Contains(result.Output, "kind must be plan or empty") {
		t.Fatalf("the API's reason is lost: %s", result.Output)
	}
}

func TestStartAutopilotPolicy(t *testing.T) {
	ctx := context.Background()
	api := standardAPI()
	result := runStartAutopilot(ctx, toolEnv(api), json.RawMessage(`{"goalNodeId":"node-0042"}`))
	if result.IsError {
		t.Fatalf("result = %s", result.Output)
	}
	call := api.find(http.MethodPost, "/api/autopilot")
	raw, _ := json.Marshal(call.body)
	var sent runnerwire.CreateAutopilotRequest
	if err := json.Unmarshal(raw, &sent); err != nil {
		t.Fatal(err)
	}
	p := sent.Policy
	if sent.GoalNodeID != "node-0042" || p.Review.Mode != runnerwire.ReviewHuman || p.MaxParallel != 2 ||
		p.MaxRounds != 2 || p.AutoMerge || p.Advisor != runnerwire.AdvisorRules {
		t.Fatalf("default policy = %+v", sent)
	}
	if !strings.Contains(result.Output, `"autopilotId":"ap-1"`) {
		t.Fatalf("output = %s", result.Output)
	}

	api = standardAPI()
	api.autopilot = `{"run":{"id":"ap-2","status":"running"},"tasks":[]}`
	result = runStartAutopilot(ctx, toolEnv(api), json.RawMessage(`{"goalNodeId":"node-0042","maxParallel":20,`+
		`"reviewMode":"agents+human","reviewers":[{"agent":"claude","model":"opus"},{"agent":"codex","model":"gpt-5.5"}],`+
		`"quorum":2,"autoMerge":true,"advisor":"llm"}`))
	if result.IsError || !strings.Contains(result.Output, `"autopilotId":"ap-2"`) {
		t.Fatalf("result = %s", result.Output)
	}
	raw, _ = json.Marshal(api.find(http.MethodPost, "/api/autopilot").body)
	_ = json.Unmarshal(raw, &sent)
	p = sent.Policy
	if p.MaxParallel != runnerwire.MaxAutopilotParallel || p.Review.Mode != runnerwire.ReviewAgentsHuman ||
		len(p.Review.Reviewers) != 2 || p.Review.Quorum != 2 || !p.AutoMerge || p.Advisor != runnerwire.AdvisorLLM {
		t.Fatalf("policy = %+v", p)
	}

	for input, want := range map[string]string{
		`{}`: "give the goalNodeId",
		`{"goalNodeId":"n","reviewMode":"agents"}`: "needs reviewers",
		`{"goalNodeId":"n","reviewMode":"vote"}`:   "reviewMode must be",
		`{"goalNodeId":"n","advisor":"oracle"}`:    "advisor must be",
		`{"goalNodeId":"n","reviewMode":"agents","reviewers":[{"agent":"codex","model":"m"}],"autoMerge":true}`: "autoMerge needs",
		`{"goalNodeId":"n","reviewMode":"none","autoMerge":true}`:                                               "autoMerge needs",
	} {
		api := standardAPI()
		if result := runStartAutopilot(ctx, toolEnv(api), json.RawMessage(input)); !result.IsError ||
			!strings.Contains(result.Output, want) || api.find(http.MethodPost, "/api/autopilot") != nil {
			t.Errorf("%s: %+v", input, result)
		}
	}
}

// Who implements when the person names nobody: the advisor their own runner
// offers (System One first, then the fast model), not the routing rules' last
// resort; and the result says who picks, so the copilot can tell them.
func TestStartAutopilotImplementer(t *testing.T) {
	ctx := context.Background()
	sentPolicy := func(api *fakeAPI) runnerwire.AutopilotPolicy {
		t.Helper()
		raw, _ := json.Marshal(api.find(http.MethodPost, "/api/autopilot").body)
		var sent runnerwire.CreateAutopilotRequest
		if err := json.Unmarshal(raw, &sent); err != nil {
			t.Fatal(err)
		}
		return sent.Policy
	}
	withCaps := func(caps ...string) *fakeAPI {
		api := standardAPI()
		for i := range api.runners {
			if api.runners[i].ID == "r-ann" {
				api.runners[i].Info.Capabilities = append(api.runners[i].Info.Capabilities, caps...)
			}
		}
		// Another account's runner offering System One does not count.
		for i := range api.runners {
			if api.runners[i].ID == "r-bob" {
				api.runners[i].Info.Capabilities = append(api.runners[i].Info.Capabilities, runnerwire.CapDecide)
			}
		}
		return api
	}
	goal := json.RawMessage(`{"goalNodeId":"node-0042"}`)

	api := withCaps(runnerwire.CapLLMAdvisor, runnerwire.CapDecide)
	result := runStartAutopilot(ctx, toolEnv(api), goal)
	if p := sentPolicy(api); result.IsError || p.Advisor != runnerwire.AdvisorSystemOne ||
		!strings.Contains(result.Output, "System One advisor on the runner (chosen because none was named)") {
		t.Fatalf("System One default: %+v %s", p, result.Output)
	}

	api = withCaps(runnerwire.CapLLMAdvisor)
	result = runStartAutopilot(ctx, toolEnv(api), goal)
	if p := sentPolicy(api); p.Advisor != runnerwire.AdvisorLLM || !strings.Contains(result.Output, "fast advisor model") {
		t.Fatalf("fast model default: %+v %s", p, result.Output)
	}

	api = withCaps()
	result = runStartAutopilot(ctx, toolEnv(api), goal)
	if p := sentPolicy(api); p.Advisor != runnerwire.AdvisorRules ||
		!strings.Contains(result.Output, "first installed agent") {
		t.Fatalf("rules default: %+v %s", p, result.Output)
	}

	// A named chain wins, and is said back; the advisor stays the rules.
	api = withCaps(runnerwire.CapDecide)
	result = runStartAutopilot(ctx, toolEnv(api), json.RawMessage(`{"goalNodeId":"node-0042",`+
		`"implement":[{"agent":"opencode","model":"opencode/deepseek-v4.1-flash-free"}]}`))
	if p := sentPolicy(api); len(p.Implement) != 1 || p.Implement[0].Agent != "opencode" || p.Advisor != runnerwire.AdvisorRules ||
		!strings.Contains(result.Output, "the implement chain: opencode/opencode/deepseek-v4.1-flash-free") {
		t.Fatalf("named chain: %+v %s", p, result.Output)
	}

	api = withCaps()
	result = runStartAutopilot(ctx, toolEnv(api), json.RawMessage(`{"goalNodeId":"node-0042","advisor":"systemone"}`))
	if p := sentPolicy(api); p.Advisor != runnerwire.AdvisorSystemOne {
		t.Fatalf("systemone asked for: %+v %s", p, result.Output)
	}
	api = withCaps()
	if result := runStartAutopilot(ctx, toolEnv(api), json.RawMessage(`{"goalNodeId":"n","implement":[`+
		`{"agent":"a","model":"1"},{"agent":"a","model":"2"},{"agent":"a","model":"3"},{"agent":"a","model":"4"}]}`)); !result.IsError {
		t.Fatalf("four implementers accepted: %s", result.Output)
	}
}

// The server tools are offered only where the copilot has the router, and
// never as MCP tools: an agent cannot reach them.
func TestServerToolsAreCopilotOnly(t *testing.T) {
	_, without := collectTools(nil, nil, nil)
	if _, ok := without["start_breakdown"]; ok {
		t.Fatal("server tools offered without a router")
	}
	tools, info := collectTools(nil, nil, serverToolNames())
	for _, name := range []string{"start_breakdown", "start_autopilot", "revise_goal"} {
		ti, ok := info[name]
		if !ok || !ti.server || !ti.alwaysAsk || ti.kind != toolbridge.KindRisky {
			t.Fatalf("%s info = %+v", name, ti)
		}
	}
	if len(tools) != 3 {
		t.Fatalf("tools = %+v", tools)
	}

	bridge, err := toolbridge.Open(context.Background(), toolbridge.Options{Handler: standardAPI(), Project: "main"})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	if len(bridge.Tools()) == 0 {
		t.Fatal("the MCP bridge lists no tools")
	}
	for _, tool := range bridge.Tools() {
		if _, ours := serverTools[tool.Name]; ours {
			t.Fatalf("%s is an MCP tool: agents could reach it", tool.Name)
		}
	}
}

func callWith(id, name, input string) step {
	return func(_ *testing.T, _ context.Context, _ provider.Request, _ func(provider.Delta)) (provider.Response, error) {
		return provider.Response{
			Message: provider.Message{Role: provider.RoleAssistant, Blocks: []provider.Block{
				{Type: provider.BlockToolCall, ToolCallID: id, ToolName: name, Input: json.RawMessage(input)},
			}},
			Stop: provider.StopToolUse,
		}, nil
	}
}

// Dispatching asks the person even in bypass mode, runs as the person once
// allowed, and is refused in plan mode.
func TestDispatchToolsAlwaysAsk(t *testing.T) {
	api := standardAPI()
	h := newHarness(t, func(opts *Options) { opts.Handler = func() http.Handler { return api } })
	conversation := h.conversation(alice, ModeBypass)
	h.model.steps = []step{
		func(t *testing.T, ctx context.Context, req provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
			names := map[string]bool{}
			for _, tool := range req.Tools {
				names[tool.Name] = true
			}
			if !names["start_breakdown"] || !names["start_autopilot"] {
				t.Errorf("tools offered = %v", names)
			}
			return callWith("c1", "start_autopilot", `{"goalNodeId":"node-0042"}`)(t, ctx, req, onDelta)
		},
		expectResult("c1", false, "ap-1"),
	}
	turn := h.start(h.turn(conversation, ModeBypass))
	started := turn.until(EventTurnStarted)
	request := turn.until(EventPermissionRequest)
	if request.Name != "start_autopilot" || api.find(http.MethodPost, "/api/autopilot") != nil {
		t.Fatalf("request = %+v; dispatched before the answer: %v", request, api.find(http.MethodPost, "/api/autopilot") != nil)
	}
	if !request.EveryTime || !strings.Contains(string(request.Input), "node-0042") {
		t.Fatalf("the request does not carry the full input to approve each time: %+v", request)
	}
	// "Allow for this session" counts once: the next dispatch asks again.
	if err := h.service.Decide(alice, conversation.ID, started.TurnID, request.RequestID, DecisionAllowSession, ""); err != nil {
		t.Fatal(err)
	}
	turn.finish()
	if call := api.find(http.MethodPost, "/api/autopilot"); call == nil || call.agent {
		t.Fatalf("autopilot call = %+v", call)
	}
	h.model.mu.Lock()
	h.model.steps = append(h.model.steps, callWith("c2", "start_breakdown", `{"requirement":"y"}`), expectResult("c2", true, "denied"))
	h.model.mu.Unlock()
	turn = h.start(h.turn(conversation, ModeBypass))
	started = turn.until(EventTurnStarted)
	request = turn.until(EventPermissionRequest)
	if request.Name != "start_breakdown" {
		t.Fatalf("second dispatch: %+v", request)
	}
	if err := h.service.Decide(alice, conversation.ID, started.TurnID, request.RequestID, DecisionDeny, ""); err != nil {
		t.Fatal(err)
	}
	turn.finish()

	plan := standardAPI()
	h = newHarness(t, func(opts *Options) { opts.Handler = func() http.Handler { return plan } })
	conversation = h.conversation(alice, ModePlan)
	h.model.steps = []step{callWith("c1", "start_breakdown", `{"requirement":"x"}`), expectResult("c1", true, "plan mode")}
	for _, event := range h.start(h.turn(conversation, ModePlan)).finish() {
		if event.Type == EventPermissionRequest {
			t.Fatal("plan mode asked")
		}
	}
	if len(plan.calls()) != 0 {
		t.Fatalf("plan mode reached the API: %+v", plan.calls())
	}
}
