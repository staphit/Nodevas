package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

// The hub's refusals — a node that is not there, a field it will not take,
// a run it will not start or release — come back in the reader's language,
// with the names in them kept as they were given.

// refusalIn is the {error} of a refused response, and fails the test when
// the response was not refused with want.
func refusalIn(t *testing.T, label string, response *httptest.ResponseRecorder, want int) string {
	t.Helper()
	if response.Code != want {
		t.Fatalf("%s: status = %d, want %d, body = %s", label, response.Code, want, response.Body)
	}
	var refusal struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &refusal); err != nil || refusal.Error == "" {
		t.Fatalf("%s: body %s has no error", label, response.Body)
	}
	return refusal.Error
}

// readsAs fails the test when refusal is not in the language asked for, or
// lost a name it had to keep.
func readsAs(t *testing.T, label, language, refusal string, keep ...string) {
	t.Helper()
	if chinese := strings.HasPrefix(language, "zh"); hasCJK(refusal) != chinese {
		t.Errorf("%s for %s reads %q", label, language, refusal)
	}
	for _, name := range keep {
		if !strings.Contains(refusal, name) {
			t.Errorf("%s for %s: %q lost %q", label, language, refusal, name)
		}
	}
}

func TestBoardRefusalsSpeakTheReadersLanguage(t *testing.T) {
	cases := []struct {
		label, method, path, body string
		status                    int
		keep                      []string
	}{
		// A node the request names that the board does not hold: the
		// handler's own refusal, the store's and the exporter's.
		{"context of a missing node", http.MethodGet, "/api/nodes/ghost/context", "", http.StatusNotFound, []string{"ghost"}},
		{"op on a missing node", http.MethodPost, "/api/graph/ops",
			`{"ops":[{"kind":"move","nodeId":"ghost","x":1,"y":2}]}`, http.StatusBadRequest, []string{"ghost"}},
		{"export of a missing node", http.MethodPost, "/api/export",
			`{"format":"md","scope":"node","nodeId":"ghost"}`, http.StatusBadRequest, []string{"ghost"}},
		// Validation.
		{"unknown export format", http.MethodPost, "/api/export", `{"format":"pdfx"}`, http.StatusBadRequest, []string{"pdfx"}},
		{"delete with no ids", http.MethodPost, "/api/nodes/delete", `{"ids":[]}`, http.StatusBadRequest, nil},
		{"body that is not JSON", http.MethodPost, "/api/nodes/delete", `{"ids":`, http.StatusBadRequest, nil},
		{"two JSON values", http.MethodPost, "/api/nodes/delete", `{"ids":["alpha"]} {}`, http.StatusBadRequest, nil},
		{"unknown transfer mode", http.MethodPost, "/api/nodes/transfer",
			`{"target":"other","ids":["bravo"],"mode":"teleport"}`, http.StatusBadRequest, []string{"teleport"}},
	}
	for _, language := range []string{"en-US,en;q=0.9", "zh-TW,zh;q=0.9"} {
		t.Run(language, func(t *testing.T) {
			server, _ := readerLanguageFixture(t)
			for _, tc := range cases {
				response := askIn(t, server, language, tc.method, tc.path, tc.body)
				readsAs(t, tc.label, language, refusalIn(t, tc.label, response, tc.status), tc.keep...)
			}
		})
	}
}

func TestRunRefusalsSpeakTheReadersLanguage(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, runsHello(1))
	run := f.dispatch(t, f.bea, f.runBody(""))
	expectOffer(t, conn, run.ID)

	badNode, _ := json.Marshal(runnerwire.CreateRunRequest{
		NodeID: "not a node id", RunnerID: f.runner.Runner.ID, Agent: "claude", RepoID: "repo-1", Base: "main",
	})
	missingNode, _ := json.Marshal(runnerwire.CreateRunRequest{
		NodeID: "ghost", RunnerID: f.runner.Runner.ID, Agent: "claude", RepoID: "repo-1", Base: "main",
	})
	cases := []struct {
		label, method, path, body string
		status                    int
	}{
		{"run start with a bad node id", http.MethodPost, "/api/runs", string(badNode), http.StatusBadRequest},
		{"run start on a missing node", http.MethodPost, "/api/runs", string(missingNode), http.StatusNotFound},
		{"a run that is not there", http.MethodGet, "/api/runs/nope", "", http.StatusNotFound},
		{"bad event cursor", http.MethodGet, "/api/runs/" + run.ID + "/events?after=-1", "", http.StatusBadRequest},
		// The handoff: a run that is not waiting for its limit to reset has
		// nothing to release.
		{"retry now on a run not waiting", http.MethodPost, "/api/runs/" + run.ID + "/retry-now", "", http.StatusConflict},
	}
	for _, language := range []string{"en-US,en;q=0.9", "zh-TW,zh;q=0.9"} {
		header := map[string]string{"Accept-Language": language}
		for _, tc := range cases {
			response := f.do(t, &f.bea, tc.method, tc.path, tc.body, header)
			readsAs(t, tc.label, language, refusalIn(t, tc.label, response, tc.status))
		}
	}
}
