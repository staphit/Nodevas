package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
	prefsapi "nodevas/internal/httpapi/prefs"
	"nodevas/internal/prefs"
)

// Settings that persist (ux-design §3): /api/me/preferences on both
// editions, through the whole middleware chain.

func preferencesOf(t *testing.T, response *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	var body prefs.Snapshot
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for key, value := range body.Values {
		out[key] = string(value)
	}
	return out
}

// localDo is a request from this machine to a hub without accounts.
func localDo(handler http.Handler, method, path, body string, header map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:50000"
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range header {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// A single-user hub keeps the person at the keyboard's preferences as
// "local"'s. Their agents get none, and a project the browser still names
// after it is gone does not stand in the way.
func TestPreferencesOnAHubWithoutAccounts(t *testing.T) {
	pm := projectManagerForTest(t)
	database := dbtestOpenForServer(t, pm.Workspace())
	server := serverForTest(t, pm, nil, nil)
	server.UseAudit(audit.New(database))
	server.UseAgentsDB(database)
	handler := server.Handler()

	got := preferencesOf(t, localDo(handler, http.MethodPatch, prefsapi.Path, `{"set":{"theme":"dark","textScale":1.1}}`, nil))
	if got["theme"] != `"dark"` || got["textScale"] != "1.1" {
		t.Fatalf("after set = %v", got)
	}
	var account string
	if err := database.QueryRow(`SELECT DISTINCT account_id FROM account_preferences`).Scan(&account); err != nil || account != "local" {
		t.Fatalf("stored for %q, %v", account, err)
	}
	if got := preferencesOf(t, localDo(handler, http.MethodGet, prefsapi.Path+"?project=gone", "", map[string]string{"X-Nodevas-Project": "gone"})); got["theme"] != `"dark"` {
		t.Fatalf("with a project that is gone = %v", got)
	}
	for _, method := range []string{http.MethodGet, http.MethodPatch} {
		body := ""
		if method == http.MethodPatch {
			body = `{"remove":["theme"]}`
		}
		response := localDo(handler, method, prefsapi.Path, body, map[string]string{auth.AgentRoleHeaderName: "worker"})
		if response.Code != http.StatusForbidden || errorCode(t, response) != "PERMISSION_DENIED" {
			t.Errorf("agent %s = %d %s", method, response.Code, response.Body)
		}
	}
	if got := preferencesOf(t, localDo(handler, http.MethodGet, prefsapi.Path, "", nil)); got["theme"] != `"dark"` {
		t.Fatalf("an agent's refused change was kept: %v", got)
	}
}

// Without a database there is nowhere to keep them: the route is not there
// and the app keeps its own copy.
func TestPreferencesNeedADatabase(t *testing.T) {
	server := serverForTest(t, projectManagerForTest(t), nil, nil)
	if response := localDo(server.Handler(), http.MethodGet, prefsapi.Path, "", nil); response.Code != http.StatusNotFound {
		t.Fatalf("GET without a database = %d", response.Code)
	}
}

// On a team hub each account has its own; a request needs a session and,
// to change them, the CSRF token; the project gate does not apply; nothing
// lands in the audit trail; and removing an account removes them.
func TestPreferencesOnAHubWithAccounts(t *testing.T) {
	f := newMembersFixture(t)
	ctx := context.Background()

	// "other" is restricted to bea; cal may not read it.
	if response := f.setMember(t, f.ann, "bea", "viewer"); response.Code != http.StatusOK {
		t.Fatalf("restrict other: %d %s", response.Code, response.Body)
	}
	if got := preferencesOf(t, f.do(t, &f.bea, http.MethodPatch, prefsapi.Path, `{"set":{"theme":"dark","language":"zh-TW"}}`, nil)); len(got) != 2 {
		t.Fatalf("bea after set = %v", got)
	}
	preferencesOf(t, f.do(t, &f.cal, http.MethodPatch, prefsapi.Path+"?project=other", `{"set":{"theme":"light"}}`, nil))
	bea := preferencesOf(t, f.do(t, &f.bea, http.MethodGet, prefsapi.Path, "", nil))
	cal := preferencesOf(t, f.do(t, &f.cal, http.MethodGet, prefsapi.Path, "", map[string]string{"X-Nodevas-Project": "other"}))
	if bea["theme"] != `"dark"` || len(bea) != 2 || cal["theme"] != `"light"` || len(cal) != 1 {
		t.Fatalf("bea = %v, cal = %v", bea, cal)
	}

	if response := f.do(t, nil, http.MethodGet, prefsapi.Path, "", nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("signed out = %d", response.Code)
	}
	noToken := f.bea
	noToken.csrf = ""
	if response := f.do(t, &noToken, http.MethodPatch, prefsapi.Path, `{"set":{"theme":"light"}}`, nil); response.Code != http.StatusForbidden {
		t.Fatalf("change without the CSRF token = %d", response.Code)
	}
	if got := preferencesOf(t, f.do(t, &f.bea, http.MethodGet, prefsapi.Path, "", nil)); got["theme"] != `"dark"` {
		t.Fatalf("a change without the CSRF token was kept: %v", got)
	}

	if err := f.server.auditQueue.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var members, preferences int
	if err := f.server.database.QueryRow(`SELECT
  count(*) FILTER (WHERE action = 'PUT projects/members'),
  count(*) FILTER (WHERE action LIKE '%preferences%' OR detail LIKE '%dark%')
FROM audit_events`).Scan(&members, &preferences); err != nil {
		t.Fatal(err)
	}
	if members == 0 || preferences != 0 {
		t.Fatalf("audit rows: %d membership, %d preferences; want some and none", members, preferences)
	}

	beaID, calID := accountID(t, f, "bea"), accountID(t, f, "cal")
	if err := f.server.users.Remove(ctx, "bea"); err != nil {
		t.Fatal(err)
	}
	store := prefs.NewStore(f.server.database)
	gone, err := store.Get(ctx, beaID)
	if err != nil || len(gone.Values) != 0 {
		t.Fatalf("a removed account's preferences = %v, %v", gone.Values, err)
	}
	if kept, err := store.Get(ctx, calID); err != nil || len(kept.Values) != 1 {
		t.Fatalf("cal's preferences = %v, %v", kept.Values, err)
	}
}

// Saving preferences follows a person's clicks: past the burst it waits as
// a read does instead of being refused.
func TestPreferencesAreReadClass(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPatch} {
		if class, cost := ClassifyRoute(method, prefsapi.Path); class != ClassRead || cost != 1 {
			t.Errorf("%s = %s %v, want read 1", method, class, cost)
		}
	}
}
