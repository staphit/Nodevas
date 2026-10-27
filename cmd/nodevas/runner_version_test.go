package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/runner"
	"nodevas/internal/runnerwire"
)

const testHubCommit = "0123456789abcdef0123456789abcdef01234567"

// fakeVersionHub answers GET VersionPath with status and body, and records
// the headers of the last request.
func fakeVersionHub(t *testing.T, status int, body string) (*httptest.Server, *http.Header) {
	t.Helper()
	var header http.Header
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != runnerwire.VersionPath {
			http.NotFound(w, r)
			return
		}
		header = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(hub.Close)
	return hub, &header
}

func savedRunnerConfig(t *testing.T, cfg *runner.Config) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runner.json")
	if err := runner.SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunnerHubVersionPrintsTheCommit(t *testing.T) {
	answer, _ := json.Marshal(runnerwire.VersionResponse{Commit: testHubCommit})
	hub, header := fakeVersionHub(t, http.StatusOK, string(answer))
	cfgPath := savedRunnerConfig(t, &runner.Config{
		Hub: hub.URL, Token: cliTestToken, AccessClientID: "cid.access", AccessClientSecret: "csecret",
	})

	code, stdout, stderr := runRunner(t, "", "hub-version", "--config", cfgPath)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if stdout != testHubCommit+"\n" {
		t.Fatalf("stdout = %q, want the commit alone", stdout)
	}
	if strings.Contains(stdout+stderr, "csecret") || strings.Contains(stdout+stderr, cliTestToken) {
		t.Fatal("a secret was printed")
	}
	if header.Get("Authorization") != "Bearer "+cliTestToken ||
		header.Get(runnerwire.AccessClientIDHeader) != "cid.access" ||
		header.Get(runnerwire.AccessClientSecretHeader) != "csecret" {
		t.Fatalf("headers = %v", *header)
	}
}

// Whatever the hub says goes into a download URL, so only a full commit is
// passed on.
func TestRunnerHubVersionRefusesAnythingButACommit(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"path":          {http.StatusOK, `{"commit":"../../latest"}`},
		"short":         {http.StatusOK, `{"commit":"0123456"}`},
		"upper case":    {http.StatusOK, `{"commit":"0123456789ABCDEF0123456789ABCDEF01234567"}`},
		"trailing data": {http.StatusOK, `{"commit":"` + testHubCommit + `-linux"}`},
		"unknown":       {http.StatusOK, `{"commit":""}`},
		"not json":      {http.StatusOK, `<html>`},
		"older hub":     {http.StatusNotFound, `{"error":"not found"}`},
		"bad token":     {http.StatusUnauthorized, `{"error":"unauthenticated"}`},
	} {
		t.Run(name, func(t *testing.T) {
			hub, _ := fakeVersionHub(t, tc.status, tc.body)
			cfgPath := savedRunnerConfig(t, &runner.Config{Hub: hub.URL, Token: cliTestToken})
			code, stdout, _ := runRunner(t, "", "hub-version", "--config", cfgPath)
			if code == 0 || stdout != "" {
				t.Fatalf("exit %d, stdout %q: want a failure and nothing on stdout", code, stdout)
			}
		})
	}
}

func TestRunnerHubVersionDoesNotFollowRedirects(t *testing.T) {
	signIn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the redirect was followed")
	}))
	t.Cleanup(signIn.Close)
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, signIn.URL, http.StatusFound)
	}))
	t.Cleanup(hub.Close)
	cfgPath := savedRunnerConfig(t, &runner.Config{Hub: hub.URL, Token: cliTestToken})

	code, _, stderr := runRunner(t, "", "hub-version", "--config", cfgPath)
	if code == 0 || !strings.Contains(stderr, "Cloudflare Access") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}

func TestRunnerHubVersionNeedsALogin(t *testing.T) {
	cfgPath := savedRunnerConfig(t, &runner.Config{})
	code, stdout, stderr := runRunner(t, "", "hub-version", "--config", cfgPath)
	if code == 0 || stdout != "" || !strings.Contains(stderr, "not logged in") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}
