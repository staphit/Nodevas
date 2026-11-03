package server

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
)

// An English app in a Chinese browser names its language in the query: the
// refusal follows the app, not the browser.
func TestExportRefusalFollowsTheLanguageInTheQuery(t *testing.T) {
	server, pm := readerLanguageFixture(t)
	if err := os.WriteFile(pm.Store().GraphPath(),
		[]byte("version: 1\nnodes:\n  - id: [broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/export?language=en", `{"format":"html","scope":"project"}`},
		{http.MethodGet, "/api/projects/export?project=main&language=en", ""},
	} {
		response := askIn(t, server, "zh-TW,zh;q=0.9", request.method, request.path, request.body)
		if response.Code < 400 || response.Code >= 500 {
			t.Fatalf("%s %s = %d %s", request.method, request.path, response.Code, response.Body)
		}
		var refusal struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &refusal); err != nil {
			t.Fatal(err)
		}
		if hasCJK(refusal.Error) {
			t.Fatalf("%s %s with ?language=en reads %q", request.method, request.path, refusal.Error)
		}
	}
}
