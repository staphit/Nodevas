package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nodevas/internal/realtime"
	"nodevas/internal/runnerwire"
	"nodevas/internal/usage"
)

func TestSessionUsageThroughTheRunnerProxy(t *testing.T) {
	server, _, inbox, database, users := accountServerPartsForTest(t)
	for _, name := range []string{"bea", "cal"} {
		if err := users.AddWithEmail(t.Context(), name, "correct-horse-battery", "", name+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	server.UseRunnersDB(database)
	server.UseAgentsDB(database)
	s := &runnerTestServer{server: server, handler: server.Handler(), inbox: inbox}
	bea, cal, ann := s.signIn(t, "bea@example.test"), s.signIn(t, "cal@example.test"), s.signIn(t, testEmail)
	runner := s.create(t, bea, "bea-pc")
	live := httptest.NewServer(s.handler)
	t.Cleanup(live.Close)

	bearer := map[string]string{"Authorization": "Bearer " + runner.Token}
	proxy := runnerwire.ProxyPathPrefix
	post := func(path, body string) int {
		t.Helper()
		return s.do(t, nil, http.MethodPost, proxy+path, body, bearer).Code
	}
	if code := post("agents/heartbeat", `{"sessionId":"claude:s1","name":"bea-claude","agent":"claude"}`); code != http.StatusOK {
		t.Fatalf("heartbeat = %d", code)
	}
	if code := post("agents/sessions/claude:nobody/usage", `{"models":[{"model":"claude-opus-5-5","inputTokens":1}]}`); code != http.StatusNotFound {
		t.Fatalf("usage of an unknown session = %d", code)
	}

	peer := dialHubAs(t, live.URL, bea)
	report := `{"agent":"claude","models":[{"model":"claude-opus-5-5","inputTokens":1000000,"outputTokens":100000}]}`
	if code := post("agents/sessions/claude:s1/usage", report); code != http.StatusOK {
		t.Fatalf("usage = %d", code)
	}
	peer.await(usage.ChangedEvent, func(realtime.Event) bool { return true })
	// A later report replaces the totals.
	report = `{"agent":"claude","models":[{"model":"claude-opus-5-5","inputTokens":2000000,"outputTokens":100000}]}`
	if code := post("agents/sessions/claude:s1/usage", report); code != http.StatusOK {
		t.Fatalf("second usage = %d", code)
	}

	get := func(b *browser, path string) (usage.Report, int) {
		t.Helper()
		response := s.do(t, b, http.MethodGet, path, "", nil)
		var out usage.Report
		_ = json.Unmarshal(response.Body.Bytes(), &out)
		return out, response.Code
	}
	got, code := get(&bea, "/api/usage?days=7")
	if code != http.StatusOK || got.Days != 7 || len(got.Sessions) != 1 {
		t.Fatalf("GET usage = %d %+v", code, got)
	}
	e := got.Sessions[0]
	// 2M input at $4 and 0.1M output at $20.
	if e.ID != "claude:s1" || e.Source != usage.SourceSession || e.Name != "bea-claude" || e.Agent != "claude" ||
		e.InputTokens != 2_000_000 || e.CostUSD != 10 || got.Totals.CostUSD != 10 {
		t.Fatalf("session = %+v", e)
	}
	if _, code := get(&bea, "/api/usage?days=0"); code != http.StatusBadRequest {
		t.Fatalf("days=0 = %d", code)
	}
	if _, code := get(nil, "/api/usage"); code != http.StatusUnauthorized {
		t.Fatalf("signed out = %d", code)
	}

	// Somebody else cannot write into bea's session; bad input is refused.
	if code := s.do(t, &cal, http.MethodPost, "/api/agents/sessions/claude:s1/usage", report, nil).Code; code != http.StatusForbidden {
		t.Fatalf("cal writing bea's usage = %d", code)
	}
	for _, bad := range []string{
		`{"models":[]}`,
		`{"models":[{"model":"m","inputTokens":-1}]}`,
		`{"models":[{"model":"m"},{"model":"M"}]}`,
	} {
		if code := post("agents/sessions/claude:s1/usage", bad); code != http.StatusBadRequest {
			t.Fatalf("%s = %d", bad, code)
		}
	}

	// Prices: anyone signed in reads them, only an administrator sets them.
	prices := func(b *browser) (struct {
		Prices []usage.Price `json:"prices"`
		AsOf   string        `json:"asOf"`
	}, int) {
		response := s.do(t, b, http.MethodGet, "/api/usage/prices", "", nil)
		var out struct {
			Prices []usage.Price `json:"prices"`
			AsOf   string        `json:"asOf"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &out)
		return out, response.Code
	}
	list, code := prices(&bea)
	if code != http.StatusOK || list.AsOf != usage.PricesAsOf || len(list.Prices) != len(usage.BuiltinPrices()) {
		t.Fatalf("GET prices = %d %+v", code, list)
	}
	custom := `{"prices":[{"model":"claude-opus-5-5","inputPerMTok":1,"outputPerMTok":2}]}`
	if code := s.do(t, &bea, http.MethodPut, "/api/usage/prices", custom, nil).Code; code != http.StatusForbidden {
		t.Fatalf("member PUT prices = %d", code)
	}
	if code := s.do(t, &ann, http.MethodPut, "/api/usage/prices", `{"prices":[{"model":"m","inputPerMTok":-1}]}`, nil).Code; code != http.StatusBadRequest {
		t.Fatalf("bad PUT prices = %d", code)
	}
	if code := s.do(t, &ann, http.MethodPut, "/api/usage/prices", custom, nil).Code; code != http.StatusOK {
		t.Fatalf("admin PUT prices = %d", code)
	}
	if list, _ := prices(&bea); list.Prices[0].Source != usage.SourceCustom || list.Prices[0].Model != "claude-opus-5-5" {
		t.Fatalf("custom price missing: %+v", list.Prices[0])
	}
	// The custom price costs the session now: 2M×1 + 0.1M×2.
	if got, _ := get(&bea, "/api/usage?days=7"); got.Totals.CostUSD != 2.2 {
		t.Fatalf("repriced = %+v", got.Totals)
	}
}
