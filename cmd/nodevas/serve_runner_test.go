package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"nodevas/internal/runner"
)

// gitCommands answers `git rev-parse --show-toplevel` with the directory it
// was asked about, as git would inside a work tree.
type gitCommands struct{}

func (gitCommands) LookPath(name string) (string, error) { return "/usr/bin/" + name, nil }

func (gitCommands) Run(_ context.Context, _ string, args ...string) (string, error) {
	if len(args) >= 3 && args[0] == "-C" && args[2] == "rev-parse" {
		return args[1] + "\n", nil
	}
	return "", errors.New("no remote")
}

// A standalone hub starts its runner unasked; a networked one never does,
// and refuses one asked for.
func TestPlanLocalRunner(t *testing.T) {
	r := []string{"/r"}
	cases := []struct {
		name                string
		with, no            bool
		repos               []string
		remote, tls, access bool
		want                localRunnerMode
		fails, noted        bool
	}{
		{name: "loopback, no flags", want: defaultLocalRunner},
		{name: "--no-runner", no: true, want: noLocalRunner},
		{name: "--with-runner", with: true, want: explicitLocalRunner},
		{name: "--repo alone", repos: r, want: explicitLocalRunner},
		{name: "--with-runner --repo", with: true, repos: r, want: explicitLocalRunner},
		{name: "--no-runner --with-runner", no: true, with: true, fails: true},
		{name: "--no-runner --repo", no: true, repos: r, fails: true},
		{name: "networked, no flags", remote: true, want: noLocalRunner},
		{name: "networked --no-runner", remote: true, no: true, want: noLocalRunner},
		{name: "networked --with-runner", remote: true, with: true, fails: true},
		{name: "networked --repo", remote: true, repos: r, fails: true},
		{name: "loopback TLS, no flags", tls: true, want: noLocalRunner, noted: true},
		{name: "loopback TLS --with-runner", tls: true, with: true, fails: true},
		{name: "loopback TLS --repo", tls: true, repos: r, fails: true},
		{name: "Cloudflare Access, no flags", access: true, want: noLocalRunner, noted: true},
		{name: "networked TLS, no flags", remote: true, tls: true, want: noLocalRunner},
		{name: "networked Access, no flags", remote: true, access: true, want: noLocalRunner},
	}
	for _, tc := range cases {
		mode, note, err := planLocalRunner(tc.with, tc.no, tc.repos, tc.remote, tc.tls, tc.access)
		if (err != nil) != tc.fails {
			t.Errorf("%s: err = %v", tc.name, err)
			continue
		}
		if err != nil {
			if mode != noLocalRunner {
				t.Errorf("%s: refused, but mode %d", tc.name, mode)
			}
			continue
		}
		if mode != tc.want || (note != "") != tc.noted {
			t.Errorf("%s: mode %d note %q, want mode %d noted %t", tc.name, mode, note, tc.want, tc.noted)
		}
	}
	// Today's refusal of --with-runner on a networked hub, word for word.
	if _, _, err := planLocalRunner(true, false, nil, true, false, false); err == nil ||
		err.Error() != "--with-runner is for a server on this machine only (a loopback --listen, no proxy); "+
			"on a networked hub, link each runner from the Runners page" {
		t.Fatalf("networked --with-runner: %v", err)
	}
}

func TestLocalRunnerConfigPathIsPerWorkspaceAndOutsideIt(t *testing.T) {
	catalog := filepath.Join("cfg", "nodevas")
	a := localRunnerConfigPath(catalog, "/work/a")
	if a != localRunnerConfigPath(catalog, "/work/a") || a == localRunnerConfigPath(catalog, "/work/b") {
		t.Fatalf("paths not one per workspace: %s", a)
	}
	if filepath.Dir(a) != filepath.Join(catalog, "local-runners") {
		t.Fatalf("path %s is not under the catalog", a)
	}
	if got := localHubURL("::1", 5666); got != "http://[::1]:5666" {
		t.Fatalf("hub url = %s", got)
	}
	if got := localHubURL("127.0.0.1", 5666); got != "http://127.0.0.1:5666" {
		t.Fatalf("hub url = %s", got)
	}
}

func TestPrepareLocalRunnerWritesTokenAndReposOnce(t *testing.T) {
	saved := runnerCommands
	runnerCommands = gitCommands{}
	t.Cleanup(func() { runnerCommands = saved })

	path := filepath.Join(t.TempDir(), "local-runners", "w.json")
	repo := t.TempDir()
	var seen []string
	mint := func(_ context.Context, existing, name string) (string, bool, error) {
		seen = append(seen, existing)
		if existing != "" {
			return existing, false, nil
		}
		return cliTestToken, true, nil
	}
	var log bytes.Buffer
	for range 2 {
		if err := prepareLocalRunner(context.Background(), path, "http://127.0.0.1:5666", []string{repo}, mint, &log); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 2 || seen[0] != "" || seen[1] != cliTestToken {
		t.Fatalf("mint saw %q: the stored token is not offered back", seen)
	}
	cfg, err := runner.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hub != "http://127.0.0.1:5666" || cfg.Token != cliTestToken || len(cfg.Repos) != 1 || cfg.Repos[0].Path != repo {
		t.Fatalf("config = %+v", cfg)
	}
	// Whatever the runner's auto-accept was, --with-runner does not turn it on.
	if cfg.AutoAcceptOn() {
		t.Fatal("--with-runner turned auto-accept on")
	}
	if strings.Contains(log.String(), cliTestToken) {
		t.Fatal("the runner token was written to the log")
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("settings mode = %v, %v; want 0600", info.Mode().Perm(), err)
		}
	}

	// A hub that cannot mint leaves the settings as they were.
	refuse := func(context.Context, string, string) (string, bool, error) { return "", false, errors.New("no") }
	if err := prepareLocalRunner(context.Background(), path, "http://127.0.0.1:5667", nil, refuse, &log); err == nil {
		t.Fatal("prepared without a token")
	}
	if cfg, _ = runner.LoadConfig(path); cfg.Hub != "http://127.0.0.1:5666" {
		t.Fatalf("a failed mint rewrote the hub: %s", cfg.Hub)
	}
	// Plain http off this machine is refused before anything is minted.
	if err := prepareLocalRunner(context.Background(), path, "http://hub.example:5666", nil, mint, &log); err == nil {
		t.Fatal("prepared a runner for a non-loopback http hub")
	}
}

// Without --repo the local runner keeps the repositories its settings allow,
// and adds none for itself, the current directory included.
func TestPrepareLocalRunnerWithoutRepoAddsNone(t *testing.T) {
	saved := runnerCommands
	runnerCommands = gitCommands{}
	t.Cleanup(func() { runnerCommands = saved })
	mint := func(_ context.Context, existing, _ string) (string, bool, error) {
		if existing != "" {
			return existing, false, nil
		}
		return cliTestToken, true, nil
	}
	path := filepath.Join(t.TempDir(), "local-runners", "w.json")
	var log bytes.Buffer
	if err := prepareLocalRunner(context.Background(), path, "http://127.0.0.1:5666", nil, mint, &log); err != nil {
		t.Fatal(err)
	}
	cfg, err := runner.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Repos) != 0 {
		t.Fatalf("repos added unasked: %+v", cfg.Repos)
	}
	for _, want := range []string{"no repositories yet", "--repo <dir>", "nodevas runner repo add <dir> --config"} {
		if !strings.Contains(log.String(), want) {
			t.Fatalf("log lacks %q:\n%s", want, log.String())
		}
	}

	// One allowed earlier (`runner repo add --config`) stays allowed.
	repo := t.TempDir()
	if err := prepareLocalRunner(context.Background(), path, "http://127.0.0.1:5666", []string{repo}, mint, &log); err != nil {
		t.Fatal(err)
	}
	log.Reset()
	if err := prepareLocalRunner(context.Background(), path, "http://127.0.0.1:5666", nil, mint, &log); err != nil {
		t.Fatal(err)
	}
	if cfg, _ = runner.LoadConfig(path); len(cfg.Repos) != 1 || cfg.Repos[0].Path != repo {
		t.Fatalf("repos = %+v", cfg.Repos)
	}
	if strings.Contains(log.String(), "no repositories yet") {
		t.Fatalf("hint printed with a repository allowed:\n%s", log.String())
	}
}
