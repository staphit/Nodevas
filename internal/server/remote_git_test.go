package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nodevas/internal/identity"
	"nodevas/internal/realtime"
)

// The config endpoint says whether a git token is stored, never what it is.
func TestGitRemoteConfigHidesTheToken(t *testing.T) {
	server := serverForTest(t, projectManagerForTest(t), realtime.NewHub(), nil)
	handler := server.Handler()
	// A literal public address: no DNS in the test.
	rec := doJSON(t, handler, http.MethodPut, "/api/remote/config", map[string]any{
		"kind": "git", "gitUrl": "https://140.82.112.3/org/backup.git", "gitToken": "ghp_doNotLeakThis",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("save = %d, %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, handler, http.MethodGet, "/api/remote/config", nil)
	if strings.Contains(rec.Body.String(), "ghp_doNotLeakThis") {
		t.Fatalf("the endpoint returned the token: %s", rec.Body)
	}
	var config struct {
		Kind        string `json:"kind"`
		GitURL      string `json:"gitUrl"`
		HasGitToken bool   `json:"hasGitToken"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &config); err != nil {
		t.Fatal(err)
	}
	if config.Kind != "git" || config.GitURL != "https://140.82.112.3/org/backup.git" || !config.HasGitToken {
		t.Fatalf("config = %+v", config)
	}

	// Moving the token to another host has to be asked for.
	rec = doJSON(t, handler, http.MethodPut, "/api/remote/config", map[string]any{
		"kind": "git", "gitUrl": "https://140.82.112.4/org/backup.git",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("URL change without the token = %d, want 400", rec.Code)
	}

	// The git backend has no bundles to list or restore.
	rec = doJSON(t, handler, http.MethodGet, "/api/remote/list", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"backend":"git"`) {
		t.Fatalf("list = %d, %s", rec.Code, rec.Body)
	}
}

func TestGitRemoteConfigRefusesUnsafeURLs(t *testing.T) {
	pm := projectManagerForTest(t)
	pm.SetRemote(true)
	server := serverForTest(t, pm, realtime.NewHub(), nil)
	for _, url := range []string{
		"http://140.82.112.3/backup.git",
		"ssh://git@140.82.112.3/backup.git",
		"file:///srv/backup.git",
		"https://user:pw@140.82.112.3/backup.git",
		// Private, loopback and link-local destinations on a networked server.
		"https://10.0.0.5/backup.git",
		"https://127.0.0.1/backup.git",
		"https://169.254.169.254/latest",
	} {
		rec := doJSON(t, server.Handler(), http.MethodPut, "/api/remote/config",
			map[string]any{"kind": "git", "gitUrl": url})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", url, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "pw@") {
			t.Errorf("the error repeats the URL's password: %s", rec.Body)
		}
	}
}

// Only an administrator may test, push or keep-local, as with the rest of
// the backup settings.
func TestGitRemoteActionsNeedAnAdministrator(t *testing.T) {
	srv, _ := twoProjectServer(t)
	srv.auth = staticActorAuth{actor: identity.Actor{ID: "member-1", Name: "member", Role: identity.RoleMember}}
	for _, path := range []string{"/api/remote/git/test", "/api/remote/git/keep-local", "/api/remote/push"} {
		request := httptest.NewRequest(http.MethodPost, path, nil)
		response := httptest.NewRecorder()
		srv.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Errorf("POST %s = %d, want 403", path, response.Code)
		}
	}
}
