package runners_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	runnersapi "nodevas/internal/httpapi/runners"
	"nodevas/internal/identity"
	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
	"nodevas/internal/testpg"
)

// settingsConn is a runner's socket that answers every settings request
// with answer, as the runner would.
type settingsConn struct {
	relay    *runners.SettingsRelay
	runnerID string

	mu     sync.Mutex
	sent   []runnerwire.SettingsRequest
	answer func(runnerwire.SettingsRequest) runnerwire.SettingsDone
}

func (c *settingsConn) Bye(string) {}

func (c *settingsConn) Send(env runnerwire.Envelope) error {
	if env.Type != runnerwire.TypeSettingsRequest || env.Settings == nil {
		return nil
	}
	c.mu.Lock()
	c.sent = append(c.sent, *env.Settings)
	answer := c.answer
	c.mu.Unlock()
	done := runnerwire.SettingsDone{MaxConcurrent: 1, Persisted: true}
	if answer != nil {
		done = answer(*env.Settings)
	}
	done.ID = env.Settings.ID
	go func() {
		_ = c.relay.Deliver(c.runnerID, runnerwire.Envelope{Type: runnerwire.TypeSettingsDone, SettingsDone: &done})
	}()
	return nil
}

func (c *settingsConn) requests() []runnerwire.SettingsRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]runnerwire.SettingsRequest(nil), c.sent...)
}

type auditRow struct {
	action string
	detail map[string]any
}

// settingsHub is PUT /api/runners/:id/settings with the actor named by the
// X-Test-Actor header (ann, bob or agent), and its audit rows.
type settingsHub struct {
	engine *gin.Engine
	mu     sync.Mutex
	audits []auditRow
}

func (h *settingsHub) put(t *testing.T, actor, runnerID, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/runners/"+runnerID+"/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Actor", actor)
	rec := httptest.NewRecorder()
	h.engine.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (h *settingsHub) lastAudit(t *testing.T) auditRow {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.audits) == 0 {
		t.Fatal("no audit row")
	}
	return h.audits[len(h.audits)-1]
}

// An agent path and the permission relay from the app: the built-in
// runner's owner only, one agent at a time; the runner's refusal reaches the
// app with why, and the audit never holds the path.
func TestPutSettingsAgentPathAndRelay(t *testing.T) {
	ctx := t.Context()
	reg := runners.NewRegistry(testpg.New(t), nil)
	relay := runners.NewSettingsRelay(reg)
	hub := &settingsHub{}
	gin.SetMode(gin.TestMode)
	hub.engine = gin.New()
	api := hub.engine.Group("/api", func(c *gin.Context) {
		actor := map[string]identity.Actor{
			"ann":   ann,
			"bob":   bob,
			"agent": {ID: ann.ID, Name: ann.Name, Role: identity.RoleMember, Agent: "claude"},
		}[c.GetHeader("X-Test-Actor")]
		c.Request = c.Request.WithContext(auth.WithActor(c.Request.Context(), actor))
	})
	runnersapi.New(reg, nil).WithSettings(relay).WithLinks(runnersapi.LinkOptions{
		Audit: func(_ *http.Request, action string, _ identity.Actor, _ string, detail map[string]any) {
			hub.mu.Lock()
			hub.audits = append(hub.audits, auditRow{action, detail})
			hub.mu.Unlock()
		},
	}).Register(api)

	info := runnerwire.Info{Version: "v", MaxConcurrent: 1, MaxConcurrentCeiling: 8,
		Capabilities: []string{runnerwire.CapHeadless, runnerwire.CapRemoteSettings, runnerwire.CapGlobalRuns},
		Agents:       []runnerwire.Agent{{Kind: "claude"}, {Kind: "codex"}}}
	attach := func(name string, builtIn bool) (runnerwire.Runner, *settingsConn) {
		runner, _, err := reg.Create(ctx, ann, name)
		if err != nil {
			t.Fatal(err)
		}
		if err := reg.SetHello(ctx, runner.ID, info); err != nil {
			t.Fatal(err)
		}
		conn := &settingsConn{relay: relay, runnerID: runner.ID}
		t.Cleanup(reg.Attach(runner.ID, conn))
		reg.SetWelcomed(runner.ID, builtIn)
		return runner, conn
	}
	builtIn, conn := attach("this pc", true)
	classic, classicConn := attach("team pc", false)
	const path = `/home/ann/.local/bin/claude`
	set := `{"agentPath":{"claude":"` + path + `"}}`

	for name, c := range map[string]struct {
		actor, runner, body string
		status              int
		refused             string
	}{
		"two agents":         {"ann", builtIn.ID, `{"agentPath":{"claude":"/a/claude","codex":"/a/codex"}}`, http.StatusBadRequest, runnerwire.AgentPathRefusedInvalid},
		"unknown kind":       {"ann", builtIn.ID, `{"agentPath":{"gemini":"/a/gemini"}}`, http.StatusBadRequest, runnerwire.AgentPathRefusedInvalid},
		"too long":           {"ann", builtIn.ID, `{"agentPath":{"claude":"/` + strings.Repeat("a", runnerwire.MaxAgentPathBytes) + `"}}`, http.StatusBadRequest, runnerwire.AgentPathRefusedInvalid},
		"nul":                {"ann", builtIn.ID, `{"agentPath":{"claude":"/a/claude\u0000"}}`, http.StatusBadRequest, runnerwire.AgentPathRefusedInvalid},
		"an agent":           {"agent", builtIn.ID, set, http.StatusForbidden, ""},
		"someone else":       {"bob", builtIn.ID, set, http.StatusNotFound, ""},
		"classic path":       {"ann", classic.ID, set, http.StatusConflict, ""},
		"classic relay":      {"ann", classic.ID, `{"permissionRelay":true}`, http.StatusConflict, ""},
		"agent relay":        {"agent", builtIn.ID, `{"permissionRelay":true}`, http.StatusForbidden, ""},
		"someone else relay": {"bob", builtIn.ID, `{"permissionRelay":true}`, http.StatusNotFound, ""},
	} {
		status, body := hub.put(t, c.actor, c.runner, c.body)
		if status != c.status {
			t.Errorf("%s: %d %v, want %d", name, status, body, c.status)
			continue
		}
		if c.refused != "" && (body["refused"] != c.refused || body["field"] != "agentPath") {
			t.Errorf("%s: %v", name, body)
		}
		if strings.HasPrefix(name, "classic") && body["blocked"] != runnerwire.SettingsErrLocalOnly {
			t.Errorf("%s: %v", name, body)
		}
	}
	if sent := append(conn.requests(), classicConn.requests()...); len(sent) != 0 {
		t.Fatalf("refused changes reached a runner: %+v", sent)
	}

	// The runner's own refusal reaches the app with why.
	conn.mu.Lock()
	conn.answer = func(runnerwire.SettingsRequest) runnerwire.SettingsDone {
		return runnerwire.SettingsDone{Code: runnerwire.SettingsErrInvalid, Refused: runnerwire.AgentPathRefusedPlace,
			Error: "agentPath: that file is inside a folder agents work in"}
	}
	conn.mu.Unlock()
	status, body := hub.put(t, "ann", builtIn.ID, set)
	if status != http.StatusBadRequest || body["refused"] != runnerwire.AgentPathRefusedPlace || body["field"] != "agentPath" ||
		body["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("refused by the runner: %d %v", status, body)
	}

	// Taken: the runner was asked to look for its agents again too.
	conn.mu.Lock()
	conn.answer = nil
	conn.mu.Unlock()
	if status, body := hub.put(t, "ann", builtIn.ID, set); status != http.StatusOK {
		t.Fatalf("taken: %d %v", status, body)
	}
	sent := conn.requests()
	last := sent[len(sent)-1]
	if !last.Redetect || last.AgentPath["claude"] != path || last.AccountID != ann.ID {
		t.Fatalf("sent %+v", last)
	}
	row := hub.lastAudit(t)
	raw, _ := json.Marshal(row.detail)
	if row.action != runnersapi.ActionSettingsUpdate || !strings.Contains(string(raw), `"agentPath":{"claude":"set"}`) ||
		strings.Contains(string(raw), ".local") || strings.Contains(string(raw), "/home") {
		t.Fatalf("audit %s %s", row.action, raw)
	}
	if status, _ := hub.put(t, "ann", builtIn.ID, `{"agentPath":{"claude":""}}`); status != http.StatusOK {
		t.Fatalf("cleared: %d", status)
	}
	if raw, _ := json.Marshal(hub.lastAudit(t).detail); !strings.Contains(string(raw), `"agentPath":{"claude":"cleared"}`) {
		t.Fatalf("audit %s", raw)
	}

	if status, body := hub.put(t, "ann", builtIn.ID, `{"permissionRelay":false}`); status != http.StatusOK {
		t.Fatalf("relay: %d %v", status, body)
	}
	sent = conn.requests()
	if last := sent[len(sent)-1]; last.PermissionRelay == nil || *last.PermissionRelay || last.Redetect {
		t.Fatalf("relay sent %+v", last)
	}
	if detail := hub.lastAudit(t).detail; detail["permissionRelay"] != false {
		t.Fatalf("relay audit %v", detail)
	}
}
