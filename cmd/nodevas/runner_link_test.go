package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/runner"
	"nodevas/internal/runnerwire"
)

func fakeLinkHub(t *testing.T, status int, answer any) (*httptest.Server, *[]runnerwire.RedeemLinkRequest, *http.Header) {
	t.Helper()
	var got []runnerwire.RedeemLinkRequest
	var header http.Header
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != runnerwire.LinkPath {
			http.NotFound(w, r)
			return
		}
		header = r.Header.Clone()
		var body runnerwire.RedeemLinkRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = append(got, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(answer)
	}))
	t.Cleanup(hub.Close)
	return hub, &got, &header
}

func encodeTestLink(t *testing.T, p runnerwire.LinkPayload) string {
	t.Helper()
	link, err := runnerwire.EncodeLink(p)
	if err != nil {
		t.Fatal(err)
	}
	return link
}

func TestRunnerLoginLinkRedeemsAndSaves(t *testing.T) {
	hub, got, header := fakeLinkHub(t, http.StatusCreated,
		runnerwire.RedeemLinkResponse{RunnerID: "r1", Name: "laptop", Token: cliTestToken})
	cfgPath := filepath.Join(t.TempDir(), "runner.json")
	link := encodeTestLink(t, runnerwire.LinkPayload{
		Hub: hub.URL + "/", Code: "the-code", AccessClientID: "cid.access", AccessClientSecret: "csecret",
	})

	code, stdout, stderr := runRunner(t, link+"\n", "login", "--link", "--name", "lab-pc", "--config", cfgPath)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if strings.Contains(stdout+stderr, "csecret") || strings.Contains(stdout+stderr, cliTestToken) {
		t.Fatal("a secret was printed")
	}
	if len(*got) != 1 || (*got)[0].Code != "the-code" || (*got)[0].Name != "lab-pc" || (*got)[0].OS == "" {
		t.Fatalf("redeem body = %+v", *got)
	}
	if header.Get(runnerwire.AccessClientIDHeader) != "cid.access" || header.Get(runnerwire.AccessClientSecretHeader) != "csecret" {
		t.Fatalf("access headers = %v", *header)
	}
	cfg, err := runner.LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hub != hub.URL || cfg.Token != cliTestToken || cfg.AccessClientID != "cid.access" ||
		cfg.AccessClientSecret != "csecret" || cfg.Name != "lab-pc" {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestRunnerLoginLinkFailureKeepsTheOldLogin(t *testing.T) {
	hub, _, _ := fakeLinkHub(t, http.StatusNotFound, map[string]string{"error": "gone", "code": "NOT_FOUND"})
	cfgPath := filepath.Join(t.TempDir(), "runner.json")
	before := &runner.Config{Hub: "https://old.example", Token: cliTestToken}
	if err := runner.SaveConfig(cfgPath, before); err != nil {
		t.Fatal(err)
	}
	link := encodeTestLink(t, runnerwire.LinkPayload{Hub: hub.URL, Code: "used"})
	code, _, stderr := runRunner(t, link+"\n", "login", "--link", "--config", cfgPath)
	if code == 0 || !strings.Contains(stderr, "expired or already used") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if cfg, _ := runner.LoadConfig(cfgPath); cfg.Hub != before.Hub || cfg.Token != before.Token {
		t.Fatalf("a failed link changed the config: %+v", cfg)
	}
}

func TestRunnerLoginLinkRefusesArgumentsAndMismatches(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "runner.json")
	link := encodeTestLink(t, runnerwire.LinkPayload{Hub: "https://hub.example", Code: "c"})
	for _, args := range [][]string{
		{"login", "--link", link},
		{"login", "--link=" + link},
		{"login", link},
		{"login", "--hub", "https://hub.example", "--name", link},
	} {
		code, _, stderr := runRunner(t, "", append(args, "--config", cfgPath)...)
		if code == 0 || !strings.Contains(stderr, "never taken as an argument") {
			t.Errorf("%v: exit %d: %s", args[:2], code, stderr)
		}
		if strings.Contains(stderr, link) {
			t.Error("the refused link was echoed back")
		}
	}
	cases := []struct {
		stdin string
		args  []string
		want  string
	}{
		{link + "\n", []string{"--hub", "https://other.example"}, "is for https://hub.example"},
		{link + "\n", []string{"--access-client-id", "cid"}, "drop --access-client-id"},
		{"nvr_not-a-link\n", nil, "not a Nodevas device link"},
		{encodeTestLink(t, runnerwire.LinkPayload{Hub: "http://hub.example", Code: "c"}) + "\n", nil, "plain http"},
		{"\n", nil, "nothing was given"},
	}
	for _, tc := range cases {
		args := append([]string{"login", "--link", "--config", cfgPath}, tc.args...)
		if code, _, stderr := runRunner(t, tc.stdin, args...); code == 0 || !strings.Contains(stderr, tc.want) {
			t.Errorf("%v: exit %d: %s", tc.args, code, stderr)
		}
	}
	if _, err := os.Stat(cfgPath); err == nil {
		t.Fatal("a refused link wrote a config")
	}
}
