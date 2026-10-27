package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"nodevas/internal/buildinfo"
	"nodevas/internal/runnerwire"
)

// The hub's commit is for its runners: it picks the runner binary the
// installer fetches from GitHub. Nobody without a runner token learns which
// build this is.
func TestRunnerVersionTakesARunnerToken(t *testing.T) {
	s := newRunnerTestServer(t)
	bea := s.signIn(t, "bea@example.test")
	created := s.create(t, bea, "bea-pc")
	bearer := map[string]string{"Authorization": "Bearer " + created.Token}

	response := s.do(t, nil, http.MethodGet, runnerwire.VersionPath, "", bearer)
	if response.Code != http.StatusOK {
		t.Fatalf("runner token: status = %d, body = %s", response.Code, response.Body)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	var out runnerwire.VersionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Commit != buildinfo.Commit() {
		t.Fatalf("commit = %q, want %q", out.Commit, buildinfo.Commit())
	}
	if out.Commit != "" && !buildinfo.IsCommit(out.Commit) {
		t.Fatalf("commit = %q is not a full commit", out.Commit)
	}

	if code := s.do(t, nil, http.MethodGet, runnerwire.VersionPath, "", nil).Code; code != http.StatusUnauthorized {
		t.Errorf("anonymous: status = %d, want 401", code)
	}
	if code := s.do(t, &bea, http.MethodGet, runnerwire.VersionPath, "", nil).Code; code != http.StatusUnauthorized {
		t.Errorf("browser session: status = %d, want 401", code)
	}
	wrong := map[string]string{"Authorization": "Bearer " + runnerwire.TokenPrefix + "not-a-real-token"}
	if code := s.do(t, nil, http.MethodGet, runnerwire.VersionPath, "", wrong).Code; code != http.StatusUnauthorized {
		t.Errorf("unknown runner token: status = %d, want 401", code)
	}

	if code := s.do(t, &bea, http.MethodDelete, "/api/runners/"+created.Runner.ID, "", nil).Code; code != http.StatusNoContent {
		t.Fatalf("revoke: status = %d", code)
	}
	if code := s.do(t, nil, http.MethodGet, runnerwire.VersionPath, "", bearer).Code; code != http.StatusUnauthorized {
		t.Errorf("revoked runner token: status = %d, want 401", code)
	}
}
