package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
)

// The single-user edition (ux-design §1–§2), hub side: a hub without
// accounts welcomes the runner serve started for itself as built in, shows
// what that runner asks before it starts a run and relays the owner's answer
// from the app, and changes that runner's repos and settings from the app.

// localRunsFixture is a single-user hub (no accounts) with a node to work
// on, the runner serve registered for itself, and a live listener.
type localRunsFixture struct {
	*runnerTestServer
	token    string
	runnerID string
	liveURL  string
}

func newLocalRunsFixture(t *testing.T) *localRunsFixture {
	t.Helper()
	ctx := context.Background()
	pm := projectManagerForTest(t)
	database := dbtestOpenForServer(t, pm.Workspace())
	server := serverForTest(t, pm, nil, nil)
	server.UseAudit(audit.New(database))
	server.UseRunnersDB(database)
	token, created, err := server.LocalRunnerToken(ctx, "", runnerwire.LocalRunnerName)
	if err != nil || !created {
		t.Fatalf("local runner: %v", err)
	}
	runner, err := server.runners.Authenticate(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pm.Store().CreateNode(identity.Local, &engine.Node{ID: runsTestNode, Title: "Fix the login bug"}, "The login button does nothing."); err != nil {
		t.Fatalf("create node: %v", err)
	}
	handler := server.Handler()
	live := httptest.NewServer(handler)
	t.Cleanup(live.Close)
	return &localRunsFixture{
		runnerTestServer: &runnerTestServer{server: server, handler: handler},
		token:            token, runnerID: runner.ID, liveURL: live.URL,
	}
}

// builtInHello is the built-in runner's hello: it asks in the app, takes
// answers and repo admin, and lets the hub change its settings.
func builtInHello(repos ...runnerwire.Repo) runnerwire.Envelope {
	env := runsHello(2)
	env.Hello.Capabilities = append(env.Hello.Capabilities, runnerwire.CapOfferAsk, runnerwire.CapAppAccept,
		runnerwire.CapRepoAdmin, runnerwire.CapRemoteSettings, runnerwire.CapGlobalRuns)
	env.Hello.MaxConcurrentCeiling = 8
	if len(repos) > 0 {
		env.Hello.Repos = repos
	}
	return env
}

// dialRunner opens a runner socket with extra headers, says hello and
// returns the welcome.
func dialRunner(t *testing.T, base, token string, header http.Header, hello runnerwire.Envelope) (*websocket.Conn, runnerwire.Welcome) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if header == nil {
		header = http.Header{}
	}
	header.Set("Authorization", "Bearer "+token)
	url := "ws" + strings.TrimPrefix(base, "http") + runnerwire.ConnectPath
	conn, response, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("dial: %v (status %d)", err, status)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	sendEnvelope(t, conn, hello)
	welcome := readEnvelopeForTest(t, conn)
	if welcome.Type != runnerwire.TypeWelcome || welcome.Welcome == nil {
		t.Fatalf("welcome = %+v", welcome)
	}
	return conn, *welcome.Welcome
}

func (f *localRunsFixture) connect(t *testing.T) *websocket.Conn {
	t.Helper()
	conn, welcome := dialRunner(t, f.liveURL, f.token, nil, builtInHello())
	if !welcome.BuiltIn {
		t.Fatalf("welcome = %+v, want built in", welcome)
	}
	return conn
}

func (f *localRunsFixture) runner(t *testing.T, id string) runnerwire.Runner {
	t.Helper()
	response := f.do(t, nil, http.MethodGet, "/api/runners", "", nil)
	var out runnerwire.ListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, runner := range out.Runners {
		if runner.ID == id {
			return runner
		}
	}
	t.Fatalf("runner %s not listed: %s", id, response.Body)
	return runnerwire.Runner{}
}

func (f *localRunsFixture) dispatch(t *testing.T) runnerwire.Run {
	t.Helper()
	body, _ := json.Marshal(runnerwire.CreateRunRequest{
		NodeID: runsTestNode, RunnerID: f.runnerID, Agent: "claude", RepoID: "repo-1", Base: "main",
	})
	response := f.do(t, nil, http.MethodPost, "/api/runs", string(body), nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("POST /api/runs = %d %s", response.Code, response.Body)
	}
	var run runnerwire.Run
	_ = json.Unmarshal(response.Body.Bytes(), &run)
	return run
}

func (f *localRunsFixture) getRun(t *testing.T, id string) runnerwire.Run {
	t.Helper()
	response := f.do(t, nil, http.MethodGet, "/api/runs/"+id, "", nil)
	var run runnerwire.Run
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &run) != nil {
		t.Fatalf("GET run = %d %s", response.Code, response.Body)
	}
	return run
}

// waitRun polls until ok holds for the run: the socket is processed on its
// own goroutine.
func (f *localRunsFixture) waitRun(t *testing.T, id, what string, ok func(runnerwire.Run) bool) runnerwire.Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		run := f.getRun(t, id)
		if ok(run) {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s never %s: %+v", id, what, run)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (f *localRunsFixture) auditRows(t *testing.T, prefix string) []audit.Event {
	t.Helper()
	events, err := f.server.audit.Query(context.Background(), audit.Filter{ActionPrefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	return events
}

var asAgent = map[string]string{auth.AgentRoleHeaderName: string(identity.AgentWorker)}

func codeOf(response *httptest.ResponseRecorder) (string, string) {
	var body struct {
		Code    string `json:"code"`
		Blocked string `json:"blocked"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	return body.Code, body.Blocked
}

func appAsk(runID string) runnerwire.Envelope {
	return runnerwire.Envelope{Type: runnerwire.TypeOfferAsk, OfferAsk: &runnerwire.OfferAsk{
		RunID: runID, Where: runnerwire.AskInApp, ExpiresAt: time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339),
		Reasons: []runnerwire.AskReason{{Code: runnerwire.AskEveryRun}},
	}}
}

// The hub calls a runner built in only when it is the one LocalRunnerToken
// returned at this startup, on a hub without accounts, over a socket
// straight from this machine; and says which edition it is either way.
func TestWelcomeBuiltInOnlyForTheMintedRunnerOnLoopback(t *testing.T) {
	f := newLocalRunsFixture(t)

	conn, welcome := dialRunner(t, f.liveURL, f.token, nil, builtInHello())
	if welcome.Edition != runnerwire.EditionSingle || !welcome.BuiltIn || welcome.OwnerID != identity.Local.ID {
		t.Fatalf("built-in runner's welcome = %+v", welcome)
	}
	if runner := f.runner(t, f.runnerID); !runner.BuiltIn || !runner.Online {
		t.Fatalf("listed = %+v", runner)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")

	// The same token through a proxy, even one naming this machine: not
	// straight from here, so classic.
	for name, header := range map[string]http.Header{
		"X-Forwarded-For 127.0.0.1": {"X-Forwarded-For": {"127.0.0.1"}},
		"X-Forwarded-Proto":         {"X-Forwarded-Proto": {"http"}},
		"Via":                       {"Via": {"1.1 proxy"}},
	} {
		conn, welcome := dialRunner(t, f.liveURL, f.token, header, builtInHello())
		if welcome.BuiltIn || welcome.Edition != runnerwire.EditionSingle {
			t.Fatalf("%s: welcome = %+v", name, welcome)
		}
		if runner := f.runner(t, f.runnerID); runner.BuiltIn {
			t.Fatalf("%s: listed as built in", name)
		}
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}

	// A second runner of the local user (Settings → Agents → Add runner).
	response := f.do(t, nil, http.MethodPost, "/api/runners", `{"name":"other"}`, nil)
	var second runnerwire.CreateResponse
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &second) != nil {
		t.Fatalf("second runner = %d %s", response.Code, response.Body)
	}
	_, welcome = dialRunner(t, f.liveURL, second.Token, nil, builtInHello())
	if welcome.BuiltIn || welcome.Edition != runnerwire.EditionSingle {
		t.Fatalf("second runner's welcome = %+v", welcome)
	}
	if f.runner(t, second.Runner.ID).BuiltIn {
		t.Fatal("the second runner is listed as built in")
	}

	// A restart that keeps the token keeps it the built-in runner.
	again, created, err := f.server.LocalRunnerToken(context.Background(), f.token, runnerwire.LocalRunnerName)
	if err != nil || created || again != f.token || !f.server.runners.IsBuiltInID(f.runnerID) {
		t.Fatalf("restart: created %v, err %v", created, err)
	}
}

// A hub with accounts is a team hub: no runner is ever built in there, even
// one the registry were told about.
func TestWelcomeOnATeamHubIsNeverBuiltIn(t *testing.T) {
	f := newRunsFixture(t)
	f.server.runners.SetBuiltIn(f.runner.Runner.ID)
	_, welcome := dialRunner(t, f.liveURL, f.runner.Token, nil, builtInHello())
	if welcome.Edition != runnerwire.EditionTeam || welcome.BuiltIn {
		t.Fatalf("team welcome = %+v", welcome)
	}
}

// Runs carry who started them: a person's dispatch is "person".
func TestDispatchOriginIsPerson(t *testing.T) {
	f := newLocalRunsFixture(t)
	conn := f.connect(t)
	run := f.dispatch(t)
	if job := expectOffer(t, conn, run.ID); job.Origin != runnerwire.OriginPerson {
		t.Fatalf("offer origin = %q", job.Origin)
	}
	if got := f.getRun(t, run.ID); got.Origin != runnerwire.OriginPerson {
		t.Fatalf("run origin = %q", got.Origin)
	}
}

// The built-in runner asks in the app; the owner answers there; the hub
// relays the answer and the runner decides.
func TestOfferAnswerFromTheApp(t *testing.T) {
	f := newLocalRunsFixture(t)
	conn := f.connect(t)
	run := f.dispatch(t)
	expectOffer(t, conn, run.ID)
	path := "/api/runs/" + run.ID + "/offer"

	// Not asked yet.
	response := f.do(t, nil, http.MethodPost, path, `{"start":true}`, nil)
	if code, _ := codeOf(response); response.Code != http.StatusConflict || code != "not_waiting" {
		t.Fatalf("before an ask = %d %s", response.Code, response.Body)
	}

	sendEnvelope(t, conn, appAsk(run.ID))
	asked := f.waitRun(t, run.ID, "asked", func(r runnerwire.Run) bool { return r.Ask != nil })
	if asked.Status != runnerwire.RunOffered || asked.Ask.Where != runnerwire.AskInApp || len(asked.Ask.Reasons) != 1 ||
		asked.Ask.Reasons[0].Code != runnerwire.AskEveryRun {
		t.Fatalf("asked run = %+v", asked)
	}

	// An agent never answers for the person.
	if response := f.do(t, nil, http.MethodPost, path, `{"start":true}`, asAgent); response.Code != http.StatusForbidden {
		t.Fatalf("agent = %d %s", response.Code, response.Body)
	}
	if response := f.do(t, nil, http.MethodPost, path, `{"start":true,"extra":1}`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d", response.Code)
	}

	response = f.do(t, nil, http.MethodPost, path, `{"start":true,"alwaysAllowShell":true}`, nil)
	if response.Code != http.StatusAccepted {
		t.Fatalf("answer = %d %s", response.Code, response.Body)
	}
	env := readEnvelopeForTest(t, conn)
	if env.Type != runnerwire.TypeOfferAnswer || env.OfferAnswer == nil ||
		*env.OfferAnswer != (runnerwire.OfferAnswer{RunID: run.ID, AccountID: identity.Local.ID, Start: true, AlwaysAllowShell: true}) {
		t.Fatalf("relayed = %+v", env)
	}
	rows := f.auditRows(t, "run.offer.")
	if len(rows) != 1 || rows[0].Action != "run.offer.answer" || rows[0].Target != run.ID ||
		rows[0].Detail["start"] != true || rows[0].Detail["alwaysAllowShell"] != true || rows[0].Detail["origin"] != runnerwire.OriginPerson {
		t.Fatalf("audit = %+v", rows)
	}
	// A second answer (a double click, another tab) finds nothing to answer.
	response = f.do(t, nil, http.MethodPost, path, `{"start":false}`, nil)
	if code, _ := codeOf(response); response.Code != http.StatusConflict || code != "not_waiting" {
		t.Fatalf("second answer = %d %s", response.Code, response.Body)
	}
	if rows := f.auditRows(t, "run.offer."); len(rows) != 1 {
		t.Fatalf("a second answer was recorded: %+v", rows)
	}

	// The runner says no (the owner's answer was "don't start" on another
	// run, or its own timeout): the code stays on the run, the ask goes.
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeDecline, RunID: run.ID,
		Reason: "nobody answered in time", Code: runnerwire.DeclineTimedOut})
	declined := f.waitRun(t, run.ID, "declined", func(r runnerwire.Run) bool { return r.Status == runnerwire.RunDeclined })
	if declined.DeclineCode != runnerwire.DeclineTimedOut || declined.Ask != nil {
		t.Fatalf("declined = %+v", declined)
	}
	response = f.do(t, nil, http.MethodPost, path, `{"start":true}`, nil)
	if code, _ := codeOf(response); response.Code != http.StatusConflict || code != "not_waiting" {
		t.Fatalf("after the decline = %d %s", response.Code, response.Body)
	}

	// The runner went away with an ask outstanding (its asks went with it
	// on the hub; one that slipped in still has nobody to tell).
	second := f.dispatch(t)
	expectOffer(t, conn, second.ID)
	_ = conn.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(5 * time.Second)
	for f.server.runners.Online(f.runnerID) {
		if time.Now().After(deadline) {
			t.Fatal("runner still online")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The socket's Detached may still be dropping asks: try again until the
	// one put back stays.
	for attempt := 0; ; attempt++ {
		if err := f.server.runs.OfferAsk(context.Background(), f.runnerID, *appAsk(second.ID).OfferAsk); err != nil {
			t.Fatal(err)
		}
		response = f.do(t, nil, http.MethodPost, "/api/runs/"+second.ID+"/offer", `{"start":true}`, nil)
		code, blocked := codeOf(response)
		if code == "not_waiting" && attempt < 20 {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		if response.Code != http.StatusServiceUnavailable || code != "RUNNER_OFFLINE" || blocked != "offline" {
			t.Fatalf("offline = %d %s", response.Code, response.Body)
		}
		break
	}
}

// A runner that is not the built-in one is approved on its own PC: the hub
// shows its ask and refuses to answer it.
func TestOfferAnswerRefusedForAClassicRunner(t *testing.T) {
	f := newLocalRunsFixture(t)
	response := f.do(t, nil, http.MethodPost, "/api/runners", `{"name":"other"}`, nil)
	var other runnerwire.CreateResponse
	_ = json.Unmarshal(response.Body.Bytes(), &other)
	// Even claiming app-accept and asking "in the app".
	conn, welcome := dialRunner(t, f.liveURL, other.Token, nil, builtInHello())
	if welcome.BuiltIn {
		t.Fatal("a second runner was welcomed as built in")
	}
	body, _ := json.Marshal(runnerwire.CreateRunRequest{NodeID: runsTestNode, RunnerID: other.Runner.ID, Agent: "claude", RepoID: "repo-1"})
	created := f.do(t, nil, http.MethodPost, "/api/runs", string(body), nil)
	var run runnerwire.Run
	_ = json.Unmarshal(created.Body.Bytes(), &run)
	expectOffer(t, conn, run.ID)
	sendEnvelope(t, conn, appAsk(run.ID))
	f.waitRun(t, run.ID, "asked", func(r runnerwire.Run) bool { return r.Ask != nil })
	response = f.do(t, nil, http.MethodPost, "/api/runs/"+run.ID+"/offer", `{"start":true}`, nil)
	if code, _ := codeOf(response); response.Code != http.StatusConflict || code != "approve_on_runner" {
		t.Fatalf("classic = %d %s", response.Code, response.Body)
	}
	pingPong(t, conn) // no offer_answer went out
}

// On a team hub only the runner's owner may even try, and the answer is
// still the member's PC's to give.
func TestOfferAnswerOnATeamHub(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, runsHello(1))
	run := f.dispatch(t, f.bea, f.runBody(""))
	expectOffer(t, conn, run.ID)
	ask := appAsk(run.ID)
	ask.OfferAsk.Where = runnerwire.AskOnRunner
	ask.OfferAsk.Machine = "desk"
	sendEnvelope(t, conn, ask)
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, _ := f.getRun(t, f.bea, run.ID)
		if got.Ask != nil {
			if got.Ask.Where != runnerwire.AskOnRunner || got.Ask.Machine != "desk" {
				t.Fatalf("ask = %+v", got.Ask)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the ask never showed")
		}
		time.Sleep(20 * time.Millisecond)
	}
	path := "/api/runs/" + run.ID + "/offer"
	for name, b := range map[string]*browser{"cal": &f.cal, "administrator": &f.ann} {
		if response := f.do(t, b, http.MethodPost, path, `{"start":true}`, nil); response.Code != http.StatusNotFound {
			t.Fatalf("%s = %d %s", name, response.Code, response.Body)
		}
	}
	if response := f.doAsAgent(t, &f.bea, http.MethodPost, path, `{"start":true}`); response.Code != http.StatusForbidden {
		t.Fatalf("agent = %d", response.Code)
	}
	response := f.do(t, &f.bea, http.MethodPost, path, `{"start":true}`, nil)
	if code, _ := codeOf(response); response.Code != http.StatusConflict || code != "approve_on_runner" {
		t.Fatalf("owner = %d %s", response.Code, response.Body)
	}
}

// Runs now start on a POST from the app, so a page on another site must
// never send one: a LocalOnly hub still refuses cross-site writes to
// /api/runs and the answer route.
func TestLocalHubRefusesCrossSiteRunWrites(t *testing.T) {
	f := newLocalRunsFixture(t)
	body, _ := json.Marshal(runnerwire.CreateRunRequest{NodeID: runsTestNode, RunnerID: f.runnerID, Agent: "claude", RepoID: "repo-1"})
	for _, path := range []string{"/api/runs", "/api/runs/some-run/offer", "/api/global-runs/some-run/offer"} {
		for name, header := range map[string]map[string]string{
			"foreign Origin":           {"Origin": "https://evil.example"},
			"Sec-Fetch-Site cross":     {"Sec-Fetch-Site": "cross-site"},
			"Sec-Fetch-Site same-site": {"Sec-Fetch-Site": "same-site"},
		} {
			response := f.do(t, nil, http.MethodPost, path, string(body), header)
			if response.Code != http.StatusForbidden {
				t.Fatalf("%s %s = %d %s", path, name, response.Code, response.Body)
			}
		}
	}
}

// The "this computer" panel changes the built-in runner's own settings; any
// other runner refuses them, and the hub checks them first.
func TestBuiltInRunnerLocalSettings(t *testing.T) {
	f := newLocalRunsFixture(t)
	conn := f.connect(t)
	path := "/api/runners/" + f.runnerID + "/settings"

	for name, body := range map[string]string{
		"unknown agent": `{"permission":{"cursor":"full"}}`,
		"bad level":     `{"permission":{"claude":"root"}}`,
		"custom":        `{"permission":{"claude":"custom"}}`,
		"shell unknown": `{"shellWithoutAsking":{"cursor":true}}`,
	} {
		if response := f.do(t, nil, http.MethodPut, path, body, nil); response.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d %s", name, response.Code, response.Body)
		}
	}
	if response := f.do(t, nil, http.MethodPut, path, `{"askEveryRun":true}`, asAgent); response.Code != http.StatusForbidden {
		t.Fatalf("agent = %d", response.Code)
	}
	pingPong(t, conn) // nothing reached the runner

	pending := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		pending <- f.do(t, nil, http.MethodPut, path, `{"permission":{"claude":"full"},"shellWithoutAsking":{"claude":false},"askEveryRun":true,"redetect":true}`, nil)
	}()
	req := expectSettingsRequest(t, conn)
	if req.AccountID != identity.Local.ID || req.MaxConcurrent != nil || req.Permission["claude"] != runnerwire.PermFull ||
		req.ShellWithoutAsking["claude"] || req.AskEveryRun == nil || !*req.AskEveryRun || !req.Redetect {
		t.Fatalf("request = %+v", req)
	}
	hello := builtInHello()
	hello.Hello.Local = &runnerwire.LocalSettings{Permission: map[string]string{"claude": runnerwire.PermFull}, AskEveryRun: true}
	sendEnvelope(t, conn, hello)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeSettingsDone, SettingsDone: &runnerwire.SettingsDone{
		ID: req.ID, MaxConcurrent: 2, Persisted: true,
	}})
	response := <-pending
	var out runnerwire.RunnerSettingsResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &out) != nil ||
		out.Runner.Info == nil || out.Runner.Info.Local == nil || !out.Runner.Info.Local.AskEveryRun || !out.Runner.BuiltIn {
		t.Fatalf("PUT = %d %s", response.Code, response.Body)
	}
	rows := f.auditRows(t, "runner.settings.")
	if len(rows) != 1 || rows[0].Detail["askEveryRun"] != true || rows[0].Detail["maxConcurrent"] != nil {
		t.Fatalf("audit = %+v", rows)
	}
	if permission, _ := rows[0].Detail["permission"].(map[string]any); permission["claude"] != "full" {
		t.Fatalf("audit permission = %+v", rows[0].Detail)
	}

	// The runner's own refusal.
	pending = make(chan *httptest.ResponseRecorder, 1)
	go func() { pending <- f.do(t, nil, http.MethodPut, path, `{"autoPR":false}`, nil) }()
	req = expectSettingsRequest(t, conn)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeSettingsDone, SettingsDone: &runnerwire.SettingsDone{
		ID: req.ID, Code: runnerwire.SettingsErrLocalOnly, MaxConcurrent: 2,
	}})
	response = <-pending
	if code, blocked := codeOf(response); response.Code != http.StatusConflict || code != "FAILED_PRECONDITION" || blocked != "local_only" {
		t.Fatalf("runner said local_only = %d %s", response.Code, response.Body)
	}

	// A classic runner of the same person: local settings are its PC's.
	created := f.do(t, nil, http.MethodPost, "/api/runners", `{"name":"other"}`, nil)
	var other runnerwire.CreateResponse
	_ = json.Unmarshal(created.Body.Bytes(), &other)
	otherConn, _ := dialRunner(t, f.liveURL, other.Token, nil, builtInHello())
	response = f.do(t, nil, http.MethodPut, "/api/runners/"+other.Runner.ID+"/settings", `{"askEveryRun":true}`, nil)
	if code, blocked := codeOf(response); response.Code != http.StatusConflict || code != "FAILED_PRECONDITION" || blocked != "local_only" {
		t.Fatalf("classic = %d %s", response.Code, response.Body)
	}
	pingPong(t, otherConn)
	// Its concurrency is still the web's to change.
	pending = make(chan *httptest.ResponseRecorder, 1)
	go func() {
		pending <- f.do(t, nil, http.MethodPut, "/api/runners/"+other.Runner.ID+"/settings", `{"maxConcurrent":3}`, nil)
	}()
	req = expectSettingsRequest(t, otherConn)
	sendEnvelope(t, otherConn, runnerwire.Envelope{Type: runnerwire.TypeSettingsDone, SettingsDone: &runnerwire.SettingsDone{ID: req.ID, MaxConcurrent: 3}})
	if response := <-pending; response.Code != http.StatusOK {
		t.Fatalf("classic maxConcurrent = %d %s", response.Code, response.Body)
	}
}

// repoCall sends a repo admin request over HTTP and returns what the runner
// was asked, answering it with answer (and a hello first, when given).
func (f *localRunsFixture) repoCall(t *testing.T, conn *websocket.Conn, method, path, body string,
	answer func(runnerwire.RepoAdminRequest) (*runnerwire.Envelope, runnerwire.RepoAdminDone)) (runnerwire.RepoAdminRequest, *httptest.ResponseRecorder) {
	t.Helper()
	pending := make(chan *httptest.ResponseRecorder, 1)
	go func() { pending <- f.do(t, nil, method, path, body, nil) }()
	env := readEnvelopeForTest(t, conn)
	if env.Type != runnerwire.TypeRepoAdmin || env.RepoAdmin == nil || env.RepoAdmin.ID == "" {
		t.Fatalf("got %+v, want a repo admin request", env)
	}
	hello, done := answer(*env.RepoAdmin)
	if hello != nil {
		sendEnvelope(t, conn, *hello)
	}
	done.ID = env.RepoAdmin.ID
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeRepoAdminDone, RepoAdminDone: &done})
	select {
	case response := <-pending:
		return *env.RepoAdmin, response
	case <-time.After(20 * time.Second):
		t.Fatal("the request never answered")
	}
	return runnerwire.RepoAdminRequest{}, nil
}

// Repos are added, made a git project, removed and given a test command
// from the app, on the built-in runner only, by its owner, never in the
// hub's own data folders; each change is audited.
func TestBuiltInRunnerRepoAdmin(t *testing.T) {
	f := newLocalRunsFixture(t)
	conn := f.connect(t)
	base := "/api/runners/" + f.runnerID + "/repos"
	folder := filepath.Join(t.TempDir(), "my-app")
	pathBody := func(path, plan string) string {
		raw, _ := json.Marshal(runnerwire.RepoAddBody{Path: path, InitPlan: plan})
		return string(raw)
	}

	// Inspect: relayed as the owner; the answer is what the runner found.
	req, response := f.repoCall(t, conn, http.MethodPost, base+"/inspect", pathBody(folder, ""),
		func(req runnerwire.RepoAdminRequest) (*runnerwire.Envelope, runnerwire.RepoAdminDone) {
			return nil, runnerwire.RepoAdminDone{Inspect: &runnerwire.RepoInspect{Path: req.Path, State: runnerwire.RepoStateNotRepo,
				Init: &runnerwire.InitPlan{Files: 312, Ignored: []string{"node_modules/", ".env"}, WritesGitignore: true, Hash: "plan-1"}}}
		})
	var inspect runnerwire.RepoInspect
	if req.Op != runnerwire.RepoAdminInspect || req.AccountID != identity.Local.ID || req.Path != folder ||
		response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &inspect) != nil ||
		inspect.State != runnerwire.RepoStateNotRepo || inspect.Init == nil || inspect.Init.Files != 312 {
		t.Fatalf("inspect = %+v → %d %s", req, response.Code, response.Body)
	}

	// Make it a git project with the plan the person confirmed, but the
	// folder changed meanwhile.
	req, response = f.repoCall(t, conn, http.MethodPost, base, pathBody(folder, "plan-1"),
		func(req runnerwire.RepoAdminRequest) (*runnerwire.Envelope, runnerwire.RepoAdminDone) {
			return nil, runnerwire.RepoAdminDone{Code: runnerwire.RepoAdminErrPlanChanged,
				Inspect: &runnerwire.RepoInspect{Path: req.Path, State: runnerwire.RepoStateNotRepo, Init: &runnerwire.InitPlan{Files: 313, Hash: "plan-2"}}}
		})
	var failed struct {
		Code    string                  `json:"code"`
		Inspect *runnerwire.RepoInspect `json:"inspect"`
	}
	if req.Op != runnerwire.RepoAdminInitAdd || req.InitPlan != "plan-1" || response.Code != http.StatusConflict ||
		json.Unmarshal(response.Body.Bytes(), &failed) != nil || failed.Code != "plan_changed" || failed.Inspect == nil || failed.Inspect.Init.Hash != "plan-2" {
		t.Fatalf("plan changed = %+v → %d %s", req, response.Code, response.Body)
	}

	// Added: the runner's hello with the new repo comes first, so the
	// answer holds it.
	repo := runnerwire.Repo{ID: "repo-2", Name: "my-app", Path: folder}
	req, response = f.repoCall(t, conn, http.MethodPost, base, pathBody(folder, "plan-2"),
		func(runnerwire.RepoAdminRequest) (*runnerwire.Envelope, runnerwire.RepoAdminDone) {
			hello := builtInHello(runnerwire.Repo{ID: "repo-1", Name: "visual-editor"}, repo)
			return &hello, runnerwire.RepoAdminDone{Repo: &repo}
		})
	var added runnerwire.RepoAddResponse
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &added) != nil ||
		added.Repo.ID != "repo-2" || added.Runner.Info == nil || len(added.Runner.Info.Repos) != 2 {
		t.Fatalf("add = %d %s", response.Code, response.Body)
	}

	// A test command, then removal.
	req, response = f.repoCall(t, conn, http.MethodPut, base+"/repo-2/test", `{"command":"go test ./...","timeoutMinutes":10}`,
		func(runnerwire.RepoAdminRequest) (*runnerwire.Envelope, runnerwire.RepoAdminDone) {
			return nil, runnerwire.RepoAdminDone{Repo: &repo}
		})
	if req.Op != runnerwire.RepoAdminSetTest || req.RepoID != "repo-2" || req.TestCommand != "go test ./..." || req.TestTimeoutMinutes != 10 ||
		response.Code != http.StatusOK {
		t.Fatalf("test command = %+v → %d %s", req, response.Code, response.Body)
	}
	req, response = f.repoCall(t, conn, http.MethodDelete, base+"/repo-2", "",
		func(runnerwire.RepoAdminRequest) (*runnerwire.Envelope, runnerwire.RepoAdminDone) {
			hello := builtInHello()
			return &hello, runnerwire.RepoAdminDone{}
		})
	var removed runnerwire.RepoRunnerResponse
	if req.Op != runnerwire.RepoAdminRemove || req.RepoID != "repo-2" || response.Code != http.StatusOK ||
		json.Unmarshal(response.Body.Bytes(), &removed) != nil || len(removed.Runner.Info.Repos) != 1 {
		t.Fatalf("remove = %+v → %d %s", req, response.Code, response.Body)
	}

	actions := map[string]string{}
	for _, row := range f.auditRows(t, "runner.repo.") {
		code, _ := row.Detail["code"].(string)
		actions[row.Action+"/"+code] = row.Target
		if row.Action == "runner.repo.test_command" && row.Detail["command"] != "go test ./..." {
			t.Fatalf("test command audit = %+v", row.Detail)
		}
	}
	for _, want := range []string{"runner.repo.init/plan_changed", "runner.repo.init/", "runner.repo.test_command/", "runner.repo.remove/"} {
		if actions[want] != f.runnerID {
			t.Fatalf("audit %s missing: %+v", want, actions)
		}
	}
	if len(f.auditRows(t, "runner.repo.inspect")) != 0 {
		t.Fatal("an inspect was audited")
	}

	// The hub's own data is refused before anything is asked.
	// So is a folder holding it: making that one a Git project would commit
	// the catalog root's secrets.
	for _, path := range []string{f.server.pm.Workspace(), filepath.Join(f.server.pm.Workspace(), "sub"), f.server.pm.CatalogRoot(),
		filepath.Dir(f.server.pm.CatalogRoot())} {
		response := f.do(t, nil, http.MethodPost, base+"/inspect", pathBody(path, ""), nil)
		var got runnerwire.RepoInspect
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &got) != nil ||
			got.State != runnerwire.RepoStateRefused || got.Refused != runnerwire.RepoRefusedNodevas {
			t.Fatalf("inspect %s = %d %s", path, response.Code, response.Body)
		}
		response = f.do(t, nil, http.MethodPost, base, pathBody(path, ""), nil)
		if code, _ := codeOf(response); response.Code != http.StatusBadRequest || code != "sensitive" {
			t.Fatalf("add %s = %d %s", path, response.Code, response.Body)
		}
	}
	// A folder inside a Git project that holds the hub's data: the project
	// would be added whole, so it is refused too.
	sub := filepath.Join(t.TempDir(), "src")
	_, response = f.repoCall(t, conn, http.MethodPost, base+"/inspect", pathBody(sub, ""),
		func(req runnerwire.RepoAdminRequest) (*runnerwire.Envelope, runnerwire.RepoAdminDone) {
			return nil, runnerwire.RepoAdminDone{Inspect: &runnerwire.RepoInspect{Path: req.Path, Top: filepath.Dir(f.server.pm.Workspace()),
				State: runnerwire.RepoStateInsideRepo}}
		})
	if got := (runnerwire.RepoInspect{}); response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &got) != nil ||
		got.State != runnerwire.RepoStateRefused || got.Refused != runnerwire.RepoRefusedNodevas {
		t.Fatalf("inspect inside a project holding the workspace = %d %s", response.Code, response.Body)
	}
	// Bad input, and an agent.
	for _, body := range []string{pathBody("relative/dir", ""), pathBody("", ""), pathBody(folder+"\x00x", "")} {
		if response := f.do(t, nil, http.MethodPost, base+"/inspect", body, nil); response.Code != http.StatusBadRequest {
			t.Fatalf("%q = %d", body, response.Code)
		}
	}
	if response := f.do(t, nil, http.MethodPut, base+"/repo-1/test", `{"command":"a\nb"}`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("two-line command = %d", response.Code)
	}
	if response := f.do(t, nil, http.MethodPost, base, pathBody(folder, ""), asAgent); response.Code != http.StatusForbidden {
		t.Fatalf("agent = %d", response.Code)
	}
	pingPong(t, conn) // none of those reached the runner

	// The runner does not answer in time.
	old := runners.DefaultRepoAdminTimeout
	runners.DefaultRepoAdminTimeout = 300 * time.Millisecond
	t.Cleanup(func() { runners.DefaultRepoAdminTimeout = old })
	response = f.do(t, nil, http.MethodDelete, base+"/repo-1", "", nil)
	if response.Code != http.StatusGatewayTimeout {
		t.Fatalf("timeout = %d %s", response.Code, response.Body)
	}
	if env := readEnvelopeForTest(t, conn); env.Type != runnerwire.TypeRepoAdmin {
		t.Fatalf("timed out request = %+v", env)
	}
	if rows := f.auditRows(t, "runner.repo.remove"); len(rows) != 2 {
		t.Fatalf("timeout not audited: %+v", rows)
	}

	// Gone: nobody to ask.
	_ = conn.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(5 * time.Second)
	for f.server.runners.Online(f.runnerID) {
		if time.Now().After(deadline) {
			t.Fatal("runner still online")
		}
		time.Sleep(20 * time.Millisecond)
	}
	response = f.do(t, nil, http.MethodPost, base+"/inspect", pathBody(folder, ""), nil)
	if code, blocked := codeOf(response); response.Code != http.StatusServiceUnavailable || code != "RUNNER_OFFLINE" || blocked != "offline" {
		t.Fatalf("offline = %d %s", response.Code, response.Body)
	}
}

// Any runner but the built-in one adds repos on its own PC; on a team hub
// that is every runner, and somebody else's is not there at all.
func TestRepoAdminOnlyOnTheBuiltInRunner(t *testing.T) {
	f := newLocalRunsFixture(t)
	created := f.do(t, nil, http.MethodPost, "/api/runners", `{"name":"other"}`, nil)
	var other runnerwire.CreateResponse
	_ = json.Unmarshal(created.Body.Bytes(), &other)
	otherConn, _ := dialRunner(t, f.liveURL, other.Token, nil, builtInHello())
	body := `{"path":` + jsonString(filepath.Join(t.TempDir(), "app")) + `}`
	response := f.do(t, nil, http.MethodPost, "/api/runners/"+other.Runner.ID+"/repos/inspect", body, nil)
	if code, blocked := codeOf(response); response.Code != http.StatusConflict || code != "FAILED_PRECONDITION" || blocked != "add_on_runner" {
		t.Fatalf("classic = %d %s", response.Code, response.Body)
	}
	pingPong(t, otherConn)

	team := newRunsFixture(t)
	team.connect(t, builtInHello())
	path := "/api/runners/" + team.runner.Runner.ID + "/repos/inspect"
	for name, b := range map[string]*browser{"cal": &team.cal, "administrator": &team.ann} {
		if response := team.do(t, b, http.MethodPost, path, body, nil); response.Code != http.StatusNotFound {
			t.Fatalf("%s = %d %s", name, response.Code, response.Body)
		}
	}
	response = team.do(t, &team.bea, http.MethodPost, path, body, nil)
	if _, blocked := codeOf(response); response.Code != http.StatusConflict || blocked != "add_on_runner" {
		t.Fatalf("team owner = %d %s", response.Code, response.Body)
	}
}

func jsonString(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

func TestRepoInspectIsReadClass(t *testing.T) {
	if class, _ := ClassifyRoute(http.MethodPost, "/api/runners/abc/repos/inspect"); class != ClassRead {
		t.Fatalf("inspect class = %v", class)
	}
	if class, _ := ClassifyRoute(http.MethodPost, "/api/runners/abc/repos"); class != ClassWrite {
		t.Fatalf("add class = %v", class)
	}
}
