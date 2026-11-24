package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"nodevas/internal/audit"
	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
)

// settingsHello is a runner whose owner lets the hub change maxConcurrent,
// up to 4.
func settingsHello(maxConcurrent int) runnerwire.Envelope {
	env := workspaceHello()
	env.Hello.MaxConcurrent = maxConcurrent
	env.Hello.MaxConcurrentCeiling = 4
	env.Hello.Capabilities = append(env.Hello.Capabilities, runnerwire.CapGlobalRuns, runnerwire.CapRemoteSettings)
	return env
}

type settingsResult struct {
	code     int
	response runnerwire.RunnerSettingsResponse
	failure  struct {
		Code    string `json:"code"`
		Blocked string `json:"blocked"`
		Max     *int   `json:"max"`
	}
}

func (f *runsFixture) putSettings(t *testing.T, b *browser, runnerID, body string) settingsResult {
	t.Helper()
	response := f.do(t, b, http.MethodPut, "/api/runners/"+runnerID+"/settings", body, nil)
	var out settingsResult
	out.code = response.Code
	_ = json.Unmarshal(response.Body.Bytes(), &out.response)
	_ = json.Unmarshal(response.Body.Bytes(), &out.failure)
	return out
}

// putSettingsAsync sends a PUT that waits for the runner's answer.
func (f *runsFixture) putSettingsAsync(t *testing.T, b *browser, body string) chan settingsResult {
	t.Helper()
	out := make(chan settingsResult, 1)
	go func() { out <- f.putSettings(t, b, f.runner.Runner.ID, body) }()
	return out
}

func awaitSettings(t *testing.T, ch chan settingsResult) settingsResult {
	t.Helper()
	select {
	case res := <-ch:
		return res
	case <-time.After(20 * time.Second):
		t.Fatal("the PUT never answered")
	}
	return settingsResult{}
}

func expectSettingsRequest(t *testing.T, conn *websocket.Conn) runnerwire.SettingsRequest {
	t.Helper()
	env := readEnvelopeForTest(t, conn)
	if env.Type != runnerwire.TypeSettingsRequest || env.Settings == nil || env.Settings.ID == "" {
		t.Fatalf("got %+v, want a settings request", env)
	}
	return *env.Settings
}

func maxBody(n int) string { return `{"maxConcurrent":` + strconv.Itoa(n) + `}` }

func (f *runsFixture) settingsAudit(t *testing.T) []audit.Event {
	t.Helper()
	events, err := f.server.audit.Query(context.Background(), audit.Filter{ActionPrefix: "runner.settings."})
	if err != nil {
		t.Fatal(err)
	}
	return events
}

// The owner raises maxConcurrent from the web: the runner is asked as its
// owner, says hello with the new limit before it answers, and the hub
// offers the queued runs the new room fits.
func TestRunnerSettingsOwnerRaisesMaxConcurrent(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, settingsHello(1))

	first := f.dispatch(t, f.bea, f.runBody(""))
	expectOffer(t, conn, first.ID)
	second := f.dispatch(t, f.bea, f.runBodyBoth(""))
	third := f.dispatch(t, f.bea, f.runBodyBoth(""))
	if second.Status != runnerwire.RunQueued || third.Status != runnerwire.RunQueued {
		t.Fatalf("with one slot: %s, %s", second.Status, third.Status)
	}

	pending := f.putSettingsAsync(t, &f.bea, maxBody(3))
	req := expectSettingsRequest(t, conn)
	if req.AccountID != f.runner.Runner.OwnerID || req.MaxConcurrent == nil || *req.MaxConcurrent != 3 {
		t.Fatalf("request = %+v", req)
	}
	// The runner's order: its new hello first, then the answer.
	sendEnvelope(t, conn, settingsHello(3))
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeSettingsDone, SettingsDone: &runnerwire.SettingsDone{
		ID: req.ID, MaxConcurrent: 3, Persisted: true,
	}})
	expectOffer(t, conn, second.ID)
	expectOffer(t, conn, third.ID)
	res := awaitSettings(t, pending)
	if res.code != http.StatusOK || !res.response.Persisted || res.response.Runner.ID != f.runner.Runner.ID ||
		res.response.Runner.Info == nil || res.response.Runner.Info.MaxConcurrent != 3 || !res.response.Runner.Online {
		t.Fatalf("PUT = %d %+v", res.code, res.response)
	}

	events := f.settingsAudit(t)
	if len(events) != 1 || events[0].Action != "runner.settings.update" || events[0].Target != f.runner.Runner.ID ||
		events[0].ActorID != f.runner.Runner.OwnerID {
		t.Fatalf("audit = %+v", events)
	}
	detail := events[0].Detail
	change, _ := detail["maxConcurrent"].(map[string]any)
	if detail["runner"] != f.runner.Runner.ID || detail["persisted"] != true || detail["code"] != "" ||
		change["from"] != float64(1) || change["asked"] != float64(3) || change["to"] != float64(3) {
		t.Fatalf("audit detail = %+v", detail)
	}
	// The middleware's row for the write, too.
	rows, err := f.server.audit.Query(context.Background(), audit.Filter{ActionPrefix: "PUT runners/"})
	if err != nil || len(rows) != 1 || !strings.HasSuffix(rows[0].Action, "/settings") {
		t.Fatalf("middleware rows = %+v, %v", rows, err)
	}
}

// Only the runner's owner, a person, may change it; and only within what
// the runner allows, while it is there to ask.
func TestRunnerSettingsRefusals(t *testing.T) {
	old := runners.DefaultSettingsTimeout
	runners.DefaultSettingsTimeout = 300 * time.Millisecond
	t.Cleanup(func() { runners.DefaultSettingsTimeout = old })

	f := newRunsFixture(t)
	conn := f.connect(t, settingsHello(2))
	id := f.runner.Runner.ID

	// Another member, and the administrator, who may list it: not theirs.
	for name, b := range map[string]*browser{"cal": &f.cal, "administrator": &f.ann} {
		if res := f.putSettings(t, b, id, maxBody(3)); res.code != http.StatusNotFound || res.failure.Code != "NOT_FOUND" {
			t.Fatalf("%s = %d %+v", name, res.code, res.failure)
		}
	}
	if res := f.putSettings(t, &f.bea, "no-such-runner", maxBody(3)); res.code != http.StatusNotFound {
		t.Fatalf("unknown runner = %d", res.code)
	}
	// An agent, even its owner's.
	if response := f.doAsAgent(t, &f.bea, http.MethodPut, "/api/runners/"+id+"/settings", maxBody(3)); response.Code != http.StatusForbidden ||
		errorCode(t, response) != "PERMISSION_DENIED" {
		t.Fatalf("agent = %d %s", response.Code, response.Body)
	}
	// Out of range, or nothing asked.
	for _, body := range []string{maxBody(0), maxBody(5), maxBody(-1), `{}`, `{"maxConcurrent":null}`} {
		res := f.putSettings(t, &f.bea, id, body)
		if res.code != http.StatusBadRequest || res.failure.Code != "INVALID_ARGUMENT" || res.failure.Max == nil || *res.failure.Max != 4 {
			t.Fatalf("%s = %d %+v", body, res.code, res.failure)
		}
	}
	if res := f.putSettings(t, &f.bea, id, `{"maxConcurrent":`+strings.Repeat(" ", 5000)+`2}`); res.code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body = %d", res.code)
	}
	// Nothing reached the runner.
	pingPong(t, conn)
	if events := f.settingsAudit(t); len(events) != 0 {
		t.Fatalf("refusals were audited as changes: %+v", events)
	}

	// The runner's own answers.
	answers := []struct {
		done   runnerwire.SettingsDone
		status int
		code   string
	}{
		{runnerwire.SettingsDone{Code: runnerwire.SettingsErrRefused, Error: "not my owner", MaxConcurrent: 2}, http.StatusForbidden, "PERMISSION_DENIED"},
		{runnerwire.SettingsDone{Code: runnerwire.SettingsErrOff, Error: "off", MaxConcurrent: 2}, http.StatusConflict, "FAILED_PRECONDITION"},
		{runnerwire.SettingsDone{Code: runnerwire.SettingsErrRange, Error: "past 3", MaxConcurrent: 2}, http.StatusBadRequest, "INVALID_ARGUMENT"},
		{runnerwire.SettingsDone{MaxConcurrent: 3, Error: "runner.json: read-only"}, http.StatusOK, ""},
	}
	for _, a := range answers {
		pending := f.putSettingsAsync(t, &f.bea, maxBody(3))
		req := expectSettingsRequest(t, conn)
		if a.code == "" {
			sendEnvelope(t, conn, settingsHello(3))
		}
		a.done.ID = req.ID
		sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeSettingsDone, SettingsDone: &a.done})
		res := awaitSettings(t, pending)
		if res.code != a.status || res.failure.Code != a.code {
			t.Fatalf("runner answer %+v = %d %+v", a.done, res.code, res.failure)
		}
		switch a.done.Code {
		case runnerwire.SettingsErrOff:
			if res.failure.Blocked != runnerwire.RunnerBlockedSettingsOff {
				t.Fatalf("settings off = %+v", res.failure)
			}
		case runnerwire.SettingsErrRange:
			if res.failure.Max == nil || *res.failure.Max != 4 {
				t.Fatalf("out of range = %+v", res.failure)
			}
		case "":
			// Applied, but only until the runner restarts.
			if res.response.Persisted || res.response.Runner.Info.MaxConcurrent != 3 {
				t.Fatalf("not persisted = %+v", res.response)
			}
		}
	}
	if events := f.settingsAudit(t); len(events) != len(answers) {
		t.Fatalf("audit rows = %d, want one per answer", len(events))
	}

	// No answer in time: it may have been applied all the same.
	pending := f.putSettingsAsync(t, &f.bea, maxBody(2))
	expectSettingsRequest(t, conn)
	if res := awaitSettings(t, pending); res.code != http.StatusGatewayTimeout || res.failure.Code != "TIMEOUT" {
		t.Fatalf("unanswered = %d %+v", res.code, res.failure)
	}
	if events := f.settingsAudit(t); len(events) != len(answers)+1 || events[0].Detail["code"] != "timeout" {
		t.Fatalf("timeout audit = %+v", events)
	}

	// A runner whose owner turned remote settings off; one too old to know
	// them (phase 1 knows run workspaces, not this); one that is away.
	reconnect := func(hello runnerwire.Envelope) {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		f.waitOffline(t)
		conn = f.connect(t, hello)
		pingPong(t, conn)
	}
	off := workspaceHello()
	off.Hello.Capabilities = append(off.Hello.Capabilities, runnerwire.CapGlobalRuns)
	reconnect(off)
	if res := f.putSettings(t, &f.bea, id, maxBody(2)); res.code != http.StatusConflict || res.failure.Code != "FAILED_PRECONDITION" ||
		res.failure.Blocked != runnerwire.RunnerBlockedSettingsOff {
		t.Fatalf("settings off = %d %+v", res.code, res.failure)
	}
	for _, hello := range []runnerwire.Envelope{workspaceHello(), runsHello(1)} {
		reconnect(hello)
		if res := f.putSettings(t, &f.bea, id, maxBody(2)); res.code != http.StatusPreconditionFailed || res.failure.Code != "RUNNER_OUTDATED" ||
			res.failure.Blocked != runnerwire.RunnerBlockedOutdated {
			t.Fatalf("outdated %v = %d %+v", hello.Hello.Capabilities, res.code, res.failure)
		}
	}
	reconnect(settingsHello(2))
	_ = conn.Close(websocket.StatusNormalClosure, "")
	f.waitOffline(t)
	if res := f.putSettings(t, &f.bea, id, maxBody(2)); res.code != http.StatusServiceUnavailable || res.failure.Code != "RUNNER_OFFLINE" ||
		res.failure.Blocked != runnerwire.RunnerBlockedOffline {
		t.Fatalf("offline = %d %+v", res.code, res.failure)
	}
}

// The runner proxy never reaches runner settings: an agent's calls are an
// allowlist, and this is not on it.
func TestRunnerSettingsNotProxied(t *testing.T) {
	if runnerProxyAllows(http.MethodPut, "/api/runners/r1/settings") {
		t.Fatal("the proxy allowlist names runner settings")
	}
}
