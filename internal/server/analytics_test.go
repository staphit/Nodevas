package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"nodevas/internal/analytics"
	"nodevas/internal/auth"
	"nodevas/internal/db"
	"nodevas/internal/runnerwire"
)

// analyticsDB is the fixture server's database, for seeding run history.
func (f *runsFixture) analyticsDB(t *testing.T) *db.DB {
	t.Helper()
	return dbtestOpenForServer(t, f.server.pm.Workspace())
}

// seedHistory writes n delivered executor units of agent/model at cost
// each, on runs owned by owner, in the fixture's project, yesterday.
func seedHistory(t *testing.T, database *db.DB, owner, agent, model string, n int, cost float64) {
	t.Helper()
	day := time.Now().UTC().Add(-24 * time.Hour)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("h-%s-%s-%d", owner, agent, i)
		created := day.Add(time.Duration(i) * time.Minute).Format("2006-01-02T15:04:05.000000Z07:00")
		finished := day.Add(time.Duration(i)*time.Minute + 30*time.Second).Format("2006-01-02T15:04:05.000000Z07:00")
		if _, err := database.Exec(`
INSERT INTO runs (id, project, node_id, node_title, runner_id, owner_id, agent, model, repo_id, status, kind, role,
    created_by, created_at, started_at, finished_at, cost_usd, cost_settled, node_type)
VALUES (?, '.', ?, 'History', 'rn', ?, ?, ?, 'repo', 'succeeded', '', 'executor', ?, ?, ?, ?, ?, true, 'task')`,
			id, id, owner, agent, model, owner, created, created, finished, cost); err != nil {
			t.Fatalf("seed history: %v", err)
		}
	}
}

func decode[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return out
}

// B-13: the retention setting is the administrator's.
func TestAnalyticsRetentionSetting(t *testing.T) {
	f := newRunsFixture(t)
	if code := f.do(t, &f.bea, http.MethodGet, "/api/analytics/retention", "", nil).Code; code != http.StatusForbidden {
		t.Fatalf("member GET retention = %d", code)
	}
	if code := f.do(t, &f.bea, http.MethodPut, "/api/analytics/retention", `{"days":90}`, nil).Code; code != http.StatusForbidden {
		t.Fatalf("member PUT retention = %d", code)
	}
	response := f.do(t, &f.ann, http.MethodGet, "/api/analytics/retention", "", nil)
	if response.Code != http.StatusOK || decode[struct{ Days int }](t, response.Body.Bytes()).Days != analytics.DefaultRetentionDays {
		t.Fatalf("admin GET retention = %d %s", response.Code, response.Body)
	}
	for _, bad := range []string{`{"days":7}`, `{"days":4000}`, `{"days":-1}`} {
		if code := f.do(t, &f.ann, http.MethodPut, "/api/analytics/retention", bad, nil).Code; code != http.StatusBadRequest {
			t.Errorf("PUT %s = %d, want 400", bad, code)
		}
	}
	if code := f.do(t, &f.ann, http.MethodPut, "/api/analytics/retention", `{"days":0}`, nil).Code; code != http.StatusOK {
		t.Fatalf("PUT forever = %d", code)
	}
	response = f.do(t, &f.ann, http.MethodGet, "/api/analytics/retention", "", nil)
	if decode[struct{ Days int }](t, response.Body.Bytes()).Days != 0 {
		t.Fatalf("after forever: %s", response.Body)
	}
}

type insightsAnswer struct {
	Totals      analytics.Totals       `json:"totals"`
	Suggestions []analytics.Suggestion `json:"suggestions"`
}

// B-37: applying a suggestion changes exactly what it says, only when it is
// still the suggestion, only for a person who may write.
func TestApplySuggestion(t *testing.T) {
	f := newRunsFixture(t)
	owner := f.runner.Runner.OwnerID
	database := f.analyticsDB(t)
	seedHistory(t, database, owner, "claude", "sonnet", 10, 1.50)
	seedHistory(t, database, owner, "codex", "gpt-5.5", 10, 0.90)
	rules := `{"rules":[{"id":"main","match":"default","target":{"agent":"claude","model":"sonnet"}},
	                    {"id":"docs","match":"tag","value":"docs","target":{"agent":"claude","model":"sonnet"}}]}`
	if code, _ := f.routingCall(t, &f.bea, http.MethodPut, rules); code != http.StatusOK {
		t.Fatalf("PUT routing = %d", code)
	}

	response := f.do(t, &f.bea, http.MethodGet, "/api/analytics/insights?days=30", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("GET insights = %d %s", response.Code, response.Body)
	}
	got := decode[insightsAnswer](t, response.Body.Bytes())
	var pick *analytics.Suggestion
	for i := range got.Suggestions {
		if got.Suggestions[i].Key == "cheaper_equal_tier:main:codex/gpt-5.5" {
			pick = &got.Suggestions[i]
		}
	}
	if pick == nil || pick.Change == nil {
		t.Fatalf("suggestions = %+v, want cheaper_equal_tier:main", got.Suggestions)
	}

	apply := func(b *browser, key string, change any, header map[string]string) (int, string) {
		raw, _ := json.Marshal(map[string]any{"key": key, "change": change})
		response := f.do(t, b, http.MethodPost, "/api/analytics/suggestions/apply", string(raw), header)
		return response.Code, response.Body.String()
	}
	// An agent never applies.
	if code, body := apply(&f.bea, pick.Key, pick.Change, map[string]string{auth.AgentRoleHeaderName: "worker"}); code != http.StatusForbidden || !strings.Contains(body, "PERMISSION_DENIED") {
		t.Fatalf("agent apply = %d %s", code, body)
	}
	// A change that is not the suggestion's is stale.
	stale := *pick.Change
	stale.To = &runnerwire.AgentModel{Agent: "codex", Model: "gpt-9"}
	if code, body := apply(&f.bea, pick.Key, stale, nil); code != http.StatusConflict || !strings.Contains(body, "STALE_SUGGESTION") || !strings.Contains(body, "suggestion") {
		t.Fatalf("stale apply = %d %s", code, body)
	}
	if code, body := apply(&f.bea, "tester_first:.", analytics.Change{Type: analytics.ChangeOpenSettings, Row: "stages"}, nil); code != http.StatusBadRequest {
		t.Fatalf("open_settings apply = %d %s", code, body)
	}
	if code, body := apply(&f.bea, pick.Key, pick.Change, nil); code != http.StatusOK {
		t.Fatalf("apply = %d %s", code, body)
	}
	_, stored := f.routingCall(t, &f.bea, http.MethodGet, "")
	if len(stored.Rules) != 2 || stored.Rules[0].Target != (runnerwire.AgentModel{Agent: "codex", Model: "gpt-5.5"}) ||
		stored.Rules[1].Target != (runnerwire.AgentModel{Agent: "claude", Model: "sonnet"}) {
		t.Fatalf("rules after apply = %+v, want only main retargeted", stored.Rules)
	}
	audit := f.do(t, &f.ann, http.MethodGet, "/api/audit", "", nil)
	if !strings.Contains(audit.Body.String(), "analytics.suggestion.applied") {
		t.Fatalf("no audit event for the apply: %s", audit.Body)
	}
	if code := f.do(t, &f.bea, http.MethodPost, "/api/analytics/suggestions/dismiss", `{"key":"`+pick.Key+`"}`, nil).Code; code != http.StatusOK {
		t.Fatalf("dismiss = %d", code)
	}
}

// B-37: a project viewer may not apply.
func TestApplySuggestionViewerReadOnly(t *testing.T) {
	f := newMembersFixture(t)
	if response := f.setMember(t, f.bea, "cal", "viewer"); response.Code != http.StatusOK {
		t.Fatalf("bea adding cal: %d %s", response.Code, response.Body)
	}
	response := f.do(t, &f.cal, http.MethodPost, "/api/analytics/suggestions/apply?project=other",
		`{"key":"enable_auto_routing:other","change":{"type":"auto_routing","project":"other","on":true}}`, nil)
	if response.Code != http.StatusForbidden || errorCode(t, response) != "PROJECT_READ_ONLY" {
		t.Fatalf("viewer apply = %d %s", response.Code, response.Body)
	}
	if code := f.code(t, f.cal, http.MethodGet, "/api/analytics/insights?project=other", ""); code != http.StatusOK {
		t.Fatalf("viewer reading insights = %d", code)
	}
	if code := f.code(t, f.dan, http.MethodGet, "/api/analytics/insights?project=other", ""); code != http.StatusForbidden {
		t.Fatalf("non-member reading insights = %d", code)
	}
}

// B-38: scorecards and tiers are the asker's own; settings are validated;
// auto routing answers the dispatch dialog's route and logs its pick.
func TestScorecardsSettingsAndAutoRoute(t *testing.T) {
	f := newRunsFixture(t)
	bea := f.runner.Runner.OwnerID
	cal := f.create(t, f.cal, "cal-pc").Runner.OwnerID
	database := f.analyticsDB(t)
	seedHistory(t, database, bea, "claude", "sonnet", 3, 1)
	seedHistory(t, database, cal, "codex", "gpt-5.5", 3, 1)

	agentsOf := func(b *browser, query string) (int, []string) {
		response := f.do(t, b, http.MethodGet, "/api/analytics/scorecards?days=30&track=all"+query, "", nil)
		var out analytics.Scorecards
		_ = json.Unmarshal(response.Body.Bytes(), &out)
		var agents []string
		for _, row := range out.Rows {
			agents = append(agents, row.Agent)
		}
		return response.Code, agents
	}
	if code, agents := agentsOf(&f.bea, ""); code != http.StatusOK || strings.Join(agents, ",") != "claude" {
		t.Fatalf("bea's scorecards = %d %v", code, agents)
	}
	if code, agents := agentsOf(&f.ann, "&owner="+cal); code != http.StatusOK || strings.Join(agents, ",") != "codex" {
		t.Fatalf("admin reading cal's = %d %v", code, agents)
	}
	if code, _ := agentsOf(&f.bea, "&owner="+cal); code != http.StatusForbidden {
		t.Fatalf("bea reading cal's = %d", code)
	}
	tiers := f.do(t, &f.bea, http.MethodGet, "/api/analytics/tiers", "", nil)
	if tiers.Code != http.StatusOK || !strings.Contains(tiers.Body.String(), `"minSamples":10`) || !strings.Contains(tiers.Body.String(), `"probation"`) {
		t.Fatalf("tiers = %d %s", tiers.Code, tiers.Body)
	}

	for _, bad := range []string{
		`{"autoRouting":true,"explorationPct":60,"requiredTiers":{}}`,
		`{"autoRouting":true,"explorationPct":10,"requiredTiers":{"executor":"legend"}}`,
		`{"autoRouting":true,"explorationPct":10,"requiredTiers":{"wizard":"mid"}}`,
	} {
		if code := f.do(t, &f.bea, http.MethodPut, "/api/analytics/settings", bad, nil).Code; code != http.StatusBadRequest {
			t.Errorf("PUT settings %s = %d, want 400", bad, code)
		}
	}
	settings := f.do(t, &f.bea, http.MethodGet, "/api/analytics/settings", "", nil)
	if settings.Code != http.StatusOK || !strings.Contains(settings.Body.String(), `"autoRouting":false`) || !strings.Contains(settings.Body.String(), `"explorationPct":10`) {
		t.Fatalf("default settings = %d %s", settings.Code, settings.Body)
	}
	if code := f.do(t, &f.bea, http.MethodPut, "/api/analytics/settings",
		`{"autoRouting":true,"explorationPct":0,"requiredTiers":{"executor":"probation"}}`, nil).Code; code != http.StatusOK {
		t.Fatalf("PUT settings = %d", code)
	}

	f.connect(t, runsHello(1))
	code, suggestion := f.suggest(t, runsTestNode)
	if code != http.StatusOK || suggestion.Match != runnerwire.RouteAuto || !strings.HasPrefix(suggestion.RuleID, "auto:") ||
		suggestion.RunnerID != f.runner.Runner.ID || suggestion.Target.Agent != "claude" {
		t.Fatalf("route = %d %+v, want an auto pick", code, suggestion)
	}
	picks := f.do(t, &f.bea, http.MethodGet, "/api/analytics/route-picks?limit=50", "", nil)
	if picks.Code != http.StatusOK || !strings.Contains(picks.Body.String(), strings.TrimPrefix(suggestion.RuleID, "auto:")) ||
		!strings.Contains(picks.Body.String(), "cheapest_meeting_tier") {
		t.Fatalf("route picks = %d %s", picks.Code, picks.Body)
	}
}

// B-39: a dispatch that followed an auto pick links it; another owner's
// pick is ignored.
func TestRoutePickLinkedToItsRun(t *testing.T) {
	f := newRunsFixture(t)
	database := f.analyticsDB(t)
	if code := f.do(t, &f.bea, http.MethodPut, "/api/analytics/settings",
		`{"autoRouting":true,"explorationPct":0,"requiredTiers":{"executor":"probation"}}`, nil).Code; code != http.StatusOK {
		t.Fatalf("PUT settings = %d", code)
	}
	f.connect(t, runsHello(4))
	_, suggestion := f.suggest(t, runsTestNode)
	pickID := strings.TrimPrefix(suggestion.RuleID, "auto:")
	if pickID == "" || pickID == suggestion.RuleID {
		t.Fatalf("no auto pick: %+v", suggestion)
	}
	run := f.dispatch(t, f.bea, f.createBody(func(r *runnerwire.CreateRunRequest) { r.RoutePick = pickID }))
	var linked string
	if err := database.QueryRow(`SELECT run_id FROM route_picks WHERE id = ?`, pickID).Scan(&linked); err != nil || linked != run.ID {
		t.Fatalf("pick run_id = %q, %v; want %s", linked, err, run.ID)
	}

	if _, err := database.Exec(`INSERT INTO route_picks (id, owner_id, project, node_id, source, reason, created_at)
VALUES ('cals-pick', 'u-cal', '.', ?, 'auto', '{}', '2026-10-28T00:00:00.000000Z')`, runsTestNode); err != nil {
		t.Fatal(err)
	}
	f.dispatch(t, f.bea, f.createBody(func(r *runnerwire.CreateRunRequest) { r.RoutePick = "cals-pick" }))
	if err := database.QueryRow(`SELECT run_id FROM route_picks WHERE id = 'cals-pick'`).Scan(&linked); err != nil || linked != "" {
		t.Fatalf("another owner's pick linked to %q, %v", linked, err)
	}
}
