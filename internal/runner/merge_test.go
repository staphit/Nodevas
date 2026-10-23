package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

const (
	mergePRURL = "https://github.com/o/r/pull/7"
	mergeHead  = "feedface00000000000000000000000000000000"
)

// mergeGH is gh for remote merges: pr view answers from its fields, pr merge
// is recorded and, unless told to fail, turns the PR merged. Never real gh.
type mergeGH struct {
	mu         sync.Mutex
	state      string // OPEN, MERGED, CLOSED
	draft      bool
	head       string
	ci         string // success, pending, failure, none
	mergeable  string
	mergeState string
	mergeErr   error
	merges     [][]string
	mergeDirs  []string
	views      int
}

func newMergeGH() *mergeGH {
	return &mergeGH{state: "OPEN", head: mergeHead, ci: "success", mergeable: "MERGEABLE", mergeState: "CLEAN"}
}

func (g *mergeGH) run(dir, _ string, args []string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(args) >= 3 && args[0] == "pr" && args[1] == "view" && args[2] == mergePRURL {
		g.views++
		checks := "[]"
		switch g.ci {
		case "success":
			checks = `[{"status":"COMPLETED","conclusion":"SUCCESS"}]`
		case "pending":
			checks = `[{"status":"IN_PROGRESS","conclusion":""}]`
		case "failure":
			checks = `[{"status":"COMPLETED","conclusion":"FAILURE"}]`
		}
		view := map[string]any{"number": 7, "state": g.state, "isDraft": g.draft, "headRefOid": g.head,
			"statusCheckRollup": json.RawMessage(checks), "mergeable": g.mergeable, "mergeStateStatus": g.mergeState}
		raw, _ := json.Marshal(view)
		return string(raw), nil
	}
	if len(args) >= 3 && args[0] == "pr" && args[1] == "merge" && args[2] == mergePRURL {
		g.merges = append(g.merges, append([]string(nil), args...))
		g.mergeDirs = append(g.mergeDirs, dir)
		if g.mergeErr != nil {
			return "", g.mergeErr
		}
		g.state = "MERGED"
		return "", nil
	}
	return "", errors.New("unexpected gh " + strings.Join(args, " "))
}

func (g *mergeGH) set(fn func(g *mergeGH)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	fn(g)
}

func (g *mergeGH) mergeCalls() [][]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([][]string(nil), g.merges...)
}

func (g *mergeGH) viewCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.views
}

// mergeEnv is a runner's jobs with one watched PR and its real worktree,
// without a hub: answers are read with flush.
type mergeEnv struct {
	j       *jobs
	gh      *mergeGH
	cfg     *Config
	repo    string
	wt      Worktree
	dataDir string
	log     *safeLog
}

type safeLog struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *safeLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *safeLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func newMergeEnv(t *testing.T, tweak func(*Config)) *mergeEnv {
	t.Helper()
	hermeticGit(t)
	repo := newTestRepo(t)
	dataDir := t.TempDir()
	wt, err := CreateWorktree(context.Background(), repo, dataDir, "run-m", "n-1", "")
	if err != nil {
		t.Fatal(err)
	}
	env := &mergeEnv{gh: newMergeGH(), repo: repo, wt: wt, dataDir: dataDir, log: &safeLog{},
		cfg: &Config{DataDir: dataDir, Repos: []runnerwire.Repo{{ID: "r1", Name: "demo", Path: repo}}}}
	if tweak != nil {
		tweak(env.cfg)
	}
	env.j = env.start(t)
	env.j.watch(watchedPR{RunID: "run-m", NodeID: "n-1", URL: mergePRURL, Worktree: wt.Path, Repo: repo, Branch: wt.Branch,
		PR: runnerwire.PRInfo{URL: mergePRURL, Number: 7, State: runnerwire.PROpen, CI: runnerwire.CISuccess, HeadSHA: mergeHead}})
	return env
}

// start makes a fresh jobs over the same data directory, as a restarted
// runner would.
func (e *mergeEnv) start(t *testing.T) *jobs {
	t.Helper()
	c := &Client{Config: e.cfg, Log: e.log, Commands: &fakeCommands{paths: map[string]string{"gh": "/bin/gh"}, gh: e.gh.run}}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	j := newJobs(ctx, c)
	j.loadPRs()
	j.loadMerges()
	return j
}

func (e *mergeEnv) answer(t *testing.T, j *jobs, id, head string) []runnerwire.Envelope {
	t.Helper()
	j.runMerge(runnerwire.MergeJob{ID: id, RunID: "run-m", HeadSHA: head})
	return drain(t, j)
}

func drain(t *testing.T, j *jobs) []runnerwire.Envelope {
	t.Helper()
	var out []runnerwire.Envelope
	if err := j.flush(func(msg runnerwire.Envelope) error { out = append(out, msg); return nil }); err != nil {
		t.Fatal(err)
	}
	return out
}

func mergeResultOf(t *testing.T, msgs []runnerwire.Envelope, id string) runnerwire.MergeOutcome {
	t.Helper()
	for _, msg := range msgs {
		if msg.Type == runnerwire.TypeMergeResult && msg.MergeResult != nil && msg.MergeResult.ID == id {
			if msg.RunID != "run-m" || msg.MergeResult.RunID != "run-m" {
				t.Fatalf("result for the wrong run: %+v", msg)
			}
			return *msg.MergeResult
		}
	}
	t.Fatalf("no merge_result for %s in %+v", id, msgs)
	return runnerwire.MergeOutcome{}
}

func worktreeExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestMergeSucceeds(t *testing.T) {
	env := newMergeEnv(t, nil)
	msgs := env.answer(t, env.j, "m-1", mergeHead)
	if len(msgs) != 2 || msgs[0].Type != runnerwire.TypeMergeResult || msgs[1].Type != runnerwire.TypePR {
		t.Fatalf("messages %+v", msgs)
	}
	if res := mergeResultOf(t, msgs, "m-1"); res.Error != "" {
		t.Fatalf("result %+v", res)
	}
	if pr := msgs[1].PR; pr.State != runnerwire.PRMerged || pr.URL != mergePRURL || msgs[1].RunID != "run-m" {
		t.Fatalf("pr %+v", pr)
	}
	calls := env.gh.mergeCalls()
	if len(calls) != 1 {
		t.Fatalf("merge calls %v", calls)
	}
	want := []string{"pr", "merge", mergePRURL, "--squash", "--match-head-commit", mergeHead}
	if !slices.Equal(calls[0], want) {
		t.Fatalf("gh %v, want %v", calls[0], want)
	}
	for _, banned := range []string{"--admin", "--auto", "--delete-branch"} {
		if slices.Contains(calls[0], banned) {
			t.Errorf("gh pr merge with %s", banned)
		}
	}
	// gh merges outside the repository, so it never touches a local branch.
	if dir := env.gh.mergeDirs[0]; dir != env.dataDir {
		t.Errorf("merged from %q", dir)
	}
	// Merged: the worktree goes (the branch stays, GitHub's), the watch ends.
	if worktreeExists(env.wt.Path) {
		t.Error("worktree still there after the merge")
	}
	if got := gitT(t, env.repo, "branch", "--list", env.wt.Branch); got == "" {
		t.Error("local branch deleted")
	}
	if len(env.j.watchedRuns()) != 0 {
		t.Error("merged PR still watched")
	}
	log := env.log.String()
	for _, want := range []string{"merge m-1 approved at head feedfac", "merging " + mergePRURL + " (squash", "merge m-1: merged"} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q:\n%s", want, log)
		}
	}
}

func TestMergeMethodAndDeleteBranch(t *testing.T) {
	env := newMergeEnv(t, func(c *Config) {
		if err := c.Set("mergeMethod", []string{"rebase"}); err != nil {
			t.Fatal(err)
		}
		c.DeleteBranchOnMerge = true
	})
	if res := mergeResultOf(t, env.answer(t, env.j, "m-1", mergeHead), "m-1"); res.Error != "" {
		t.Fatalf("result %+v", res)
	}
	want := []string{"pr", "merge", mergePRURL, "--rebase", "--match-head-commit", mergeHead, "--delete-branch"}
	if calls := env.gh.mergeCalls(); len(calls) != 1 || !slices.Equal(calls[0], want) {
		t.Fatalf("gh %v, want %v", calls, want)
	}
}

func TestMergeRefusals(t *testing.T) {
	cases := []struct {
		name   string
		cfg    func(*Config)
		gh     func(*mergeGH)
		runID  string
		head   string
		want   string
		noView bool
	}{
		{name: "remote merge off", cfg: func(c *Config) { off := false; c.RemoteMerge = &off },
			want: ErrRemoteMergeOff, noView: true},
		{name: "unknown run", runID: "run-other", want: "no open pull request for run run-other", noView: true},
		{name: "no head", head: "-", want: "names no head commit", noView: true},
		{name: "draft", gh: func(g *mergeGH) { g.draft = true }, want: "is a draft"},
		{name: "head moved", gh: func(g *mergeGH) { g.head = "0123456789abcdef" }, want: "PR head moved since approval"},
		{name: "CI pending", gh: func(g *mergeGH) { g.ci = "pending" }, want: "CI is still running"},
		{name: "CI failed", gh: func(g *mergeGH) { g.ci = "failure" }, want: "CI failed"},
		{name: "no CI", gh: func(g *mergeGH) { g.ci = "none" }, want: "no CI checks"},
		{name: "conflicting", gh: func(g *mergeGH) { g.mergeable, g.mergeState = "CONFLICTING", "DIRTY" },
			want: "merge conflicts (mergeable CONFLICTING, mergeStateStatus DIRTY)"},
		{name: "blocked", gh: func(g *mergeGH) { g.mergeState = "BLOCKED" }, want: "mergeStateStatus BLOCKED"},
		{name: "closed", gh: func(g *mergeGH) { g.state = "CLOSED" }, want: "is closed"},
		{name: "merged elsewhere", gh: func(g *mergeGH) { g.state, g.head = "MERGED", "0123456789abcdef" },
			want: "already merged, at a different head"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newMergeEnv(t, c.cfg)
			if c.gh != nil {
				env.gh.set(c.gh)
			}
			req := runnerwire.MergeJob{ID: "m-1", RunID: "run-m", HeadSHA: mergeHead}
			if c.runID != "" {
				req.RunID = c.runID
			}
			if c.head == "-" {
				req.HeadSHA = ""
			}
			env.j.runMerge(req)
			msgs := drain(t, env.j)
			var res *runnerwire.MergeOutcome
			for _, msg := range msgs {
				if msg.Type == runnerwire.TypeMergeResult {
					res = msg.MergeResult
				}
			}
			if res == nil || res.ID != "m-1" || !strings.Contains(res.Error, c.want) {
				t.Fatalf("result %+v, want error containing %q", res, c.want)
			}
			if calls := env.gh.mergeCalls(); len(calls) != 0 {
				t.Fatalf("merged anyway: %v", calls)
			}
			if c.noView && env.gh.viewCount() != 0 {
				t.Error("asked gh before refusing on the runner's own say")
			}
			ended := c.name == "closed" || c.name == "merged elsewhere"
			if worktreeExists(env.wt.Path) == ended {
				t.Errorf("worktree exists %v for %s", !ended, c.name)
			}
			if !strings.Contains(env.log.String(), "merge m-1 refused: ") {
				t.Errorf("log:\n%s", env.log.String())
			}
		})
	}
}

func TestMergeGHFails(t *testing.T) {
	env := newMergeEnv(t, nil)
	env.gh.set(func(g *mergeGH) {
		g.mergeErr = errors.New("  GraphQL: Pull Request is not mergeable (mergePullRequest)\n")
	})
	msgs := env.answer(t, env.j, "m-1", mergeHead)
	if res := mergeResultOf(t, msgs, "m-1"); res.Error != "GraphQL: Pull Request is not mergeable (mergePullRequest)" {
		t.Fatalf("result %q", res.Error)
	}
	if len(msgs) != 1 || !worktreeExists(env.wt.Path) || len(env.j.watchedRuns()) != 1 {
		t.Fatalf("after a failed merge: %+v, watched %v", msgs, env.j.watchedRuns())
	}

	// gh's complaint is capped at 1 KB.
	env.gh.set(func(g *mergeGH) { g.mergeErr = errors.New(strings.Repeat("x", 5000)) })
	if res := mergeResultOf(t, env.answer(t, env.j, "m-2", mergeHead), "m-2"); len(res.Error) != maxMergeError {
		t.Fatalf("error of %d bytes", len(res.Error))
	}
}

func TestMergeDedupe(t *testing.T) {
	env := newMergeEnv(t, nil)
	// Refused, then sent again after the reason went away: the same answer,
	// no merge. A new approval is a new question.
	env.gh.set(func(g *mergeGH) { g.ci = "pending" })
	first := mergeResultOf(t, env.answer(t, env.j, "m-1", mergeHead), "m-1")
	env.gh.set(func(g *mergeGH) { g.ci = "success" })
	if again := mergeResultOf(t, env.answer(t, env.j, "m-1", mergeHead), "m-1"); again != first || first.Error == "" {
		t.Fatalf("resent: %+v, first %+v", again, first)
	}
	if len(env.gh.mergeCalls()) != 0 {
		t.Fatal("a refused request merged when sent again")
	}

	// Merged, then sent again: success, one merge.
	if res := mergeResultOf(t, env.answer(t, env.j, "m-2", mergeHead), "m-2"); res.Error != "" {
		t.Fatalf("m-2 %+v", res)
	}
	msgs := env.answer(t, env.j, "m-2", mergeHead)
	if res := mergeResultOf(t, msgs, "m-2"); res.Error != "" || len(msgs) != 1 {
		t.Fatalf("resent m-2: %+v", msgs)
	}
	// Another approval of the same head after the merge: merged, too.
	if res := mergeResultOf(t, env.answer(t, env.j, "m-3", mergeHead), "m-3"); res.Error != "" {
		t.Fatalf("m-3 %+v", res)
	}
	if calls := env.gh.mergeCalls(); len(calls) != 1 {
		t.Fatalf("merged %d times", len(calls))
	}

	// Sent twice while the first is still being worked on: one answer.
	env2 := newMergeEnv(t, nil)
	env2.j.mergeMu.Lock()
	req := &runnerwire.MergeJob{ID: "m-9", HeadSHA: mergeHead}
	env2.j.merge("run-m", req)
	env2.j.merge("run-m", req)
	env2.j.mergeMu.Unlock()
	deadline := time.Now().Add(10 * time.Second)
	for env2.j.pendingMergeResults() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	env2.j.wg.Wait()
	if n := env2.j.pendingMergeResults(); n != 1 {
		t.Fatalf("%d answers", n)
	}
	if res := mergeResultOf(t, drain(t, env2.j), "m-9"); res.Error != "" {
		t.Fatalf("m-9 %+v", res)
	}
	if calls := env2.gh.mergeCalls(); len(calls) != 1 {
		t.Fatalf("merged %d times", len(calls))
	}
	if !strings.Contains(env2.log.String(), "sent again while in hand") {
		t.Errorf("log:\n%s", env2.log.String())
	}
}

func TestMergeAnswersSurviveRestart(t *testing.T) {
	env := newMergeEnv(t, nil)
	env.gh.set(func(g *mergeGH) { g.draft = true })
	refused := mergeResultOf(t, env.answer(t, env.j, "m-1", mergeHead), "m-1")
	env.gh.set(func(g *mergeGH) { g.draft = false })
	if res := mergeResultOf(t, env.answer(t, env.j, "m-2", mergeHead), "m-2"); res.Error != "" {
		t.Fatalf("m-2 %+v", res)
	}

	// An old answer in the file is forgotten on load.
	raw, err := os.ReadFile(filepath.Join(env.dataDir, mergesFileName))
	if err != nil {
		t.Fatal(err)
	}
	var file mergesFile
	if err := json.Unmarshal(raw, &file); err != nil || len(file.Merges) != 2 {
		t.Fatalf("merges.json: %s %v", raw, err)
	}
	file.Merges = append(file.Merges, mergeRecord{ID: "m-old", RunID: "run-m", HeadSHA: mergeHead,
		At: time.Now().Add(-25 * time.Hour)})
	if err := writeFileAtomic(filepath.Join(env.dataDir, mergesFileName), file); err != nil {
		t.Fatal(err)
	}

	restarted := env.start(t)
	if len(restarted.watchedRuns()) != 0 {
		t.Fatalf("merged PR watched after restart: %v", restarted.watchedRuns())
	}
	if res := mergeResultOf(t, env.answer(t, restarted, "m-1", mergeHead), "m-1"); res != refused {
		t.Fatalf("m-1 after restart: %+v, want %+v", res, refused)
	}
	if res := mergeResultOf(t, env.answer(t, restarted, "m-2", mergeHead), "m-2"); res.Error != "" {
		t.Fatalf("m-2 after restart: %+v", res)
	}
	if calls := env.gh.mergeCalls(); len(calls) != 1 {
		t.Fatalf("merged %d times", len(calls))
	}
	restarted.mu.Lock()
	_, old := restarted.merges["m-old"]
	restarted.mu.Unlock()
	if old {
		t.Error("a two-day-old answer survived the load")
	}
}

// gh merged it but the runner died before it could record the answer: the
// re-sent request finds the PR merged at the approved head and says so.
func TestMergeAfterCrashMidMerge(t *testing.T) {
	env := newMergeEnv(t, nil)
	env.gh.set(func(g *mergeGH) { g.state = "MERGED" })
	msgs := env.answer(t, env.j, "m-1", mergeHead)
	if res := mergeResultOf(t, msgs, "m-1"); res.Error != "" {
		t.Fatalf("result %+v", res)
	}
	if len(env.gh.mergeCalls()) != 0 || worktreeExists(env.wt.Path) {
		t.Fatal("merged twice or left the worktree")
	}
}

func TestMergeResultWaitsForConnection(t *testing.T) {
	env := newMergeEnv(t, func(c *Config) { off := false; c.RemoteMerge = &off })
	env.j.runMerge(runnerwire.MergeJob{ID: "m-1", RunID: "run-m", HeadSHA: mergeHead})
	broken := errors.New("socket closed")
	if err := env.j.flush(func(runnerwire.Envelope) error { return broken }); !errors.Is(err, broken) {
		t.Fatalf("flush %v", err)
	}
	if env.j.pendingMergeResults() != 1 {
		t.Fatal("an unwritten answer was dropped")
	}
	if res := mergeResultOf(t, drain(t, env.j), "m-1"); res.Error != ErrRemoteMergeOff {
		t.Fatalf("result %+v", res)
	}
	if env.j.pendingMergeResults() != 0 {
		t.Fatal("a written answer is still queued")
	}
}

func TestMergeConfig(t *testing.T) {
	cfg := &Config{}
	if !cfg.RemoteMergeOn() || cfg.MergeMethodOrDefault() != "squash" || cfg.DeleteBranchOnMerge {
		t.Fatalf("defaults %+v", cfg)
	}
	for key, value := range map[string]string{"remoteMerge": "false", "mergeMethod": "MERGE", "deleteBranchOnMerge": "true"} {
		if err := cfg.Set(key, []string{value}); err != nil {
			t.Fatal(err)
		}
	}
	if cfg.RemoteMergeOn() || cfg.MergeMethodOrDefault() != "merge" || !cfg.DeleteBranchOnMerge {
		t.Fatalf("set %+v", cfg)
	}
	if err := cfg.Set("mergeMethod", []string{"fast-forward"}); err == nil {
		t.Error("mergeMethod fast-forward accepted")
	}
	for _, key := range []string{"remoteMerge", "mergeMethod", "deleteBranchOnMerge"} {
		if err := cfg.Set(key, []string{"default"}); err != nil {
			t.Fatal(err)
		}
	}
	if cfg.RemoteMerge != nil || cfg.MergeMethod != "" || cfg.DeleteBranchOnMerge {
		t.Fatalf("reset %+v", cfg)
	}
	if (&Config{MergeMethod: "octopus"}).MergeMethodOrDefault() != "squash" {
		t.Error("a hand-edited method is used")
	}
}

// Over the socket: the hub's merge, its answer, then the PR merged.
func TestMergeOverTheSocket(t *testing.T) {
	gh := newMergeGH()
	dataDir := t.TempDir()
	var wtPath string
	env := startPRRunner(t, nil, func(c *Client) {
		c.Config.DataDir = dataDir
		c.PRPollInterval = time.Hour
		cmds := c.Commands.(*fakeCommands)
		cmds.paths["gh"] = "/bin/gh"
		cmds.gh = gh.run
		repo := c.Config.Repos[0].Path
		wt, err := CreateWorktree(context.Background(), repo, dataDir, "run-m", "n-1", "")
		if err != nil {
			t.Fatal(err)
		}
		wtPath = wt.Path
		saved := prsFile{PRs: []watchedPR{{RunID: "run-m", URL: mergePRURL, Worktree: wt.Path, Repo: repo, Branch: wt.Branch,
			PR: runnerwire.PRInfo{URL: mergePRURL, Number: 7, State: runnerwire.PROpen, CI: runnerwire.CISuccess, HeadSHA: mergeHead}}}}
		if err := writeFileAtomic(filepath.Join(dataDir, prsFileName), saved); err != nil {
			t.Fatal(err)
		}
	})
	env.hub.hello()
	env.hub.wait("run-m", runnerwire.TypePR) // the resend on connect
	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeMerge, RunID: "run-m",
		Merge: &runnerwire.MergeJob{ID: "m-1", RunID: "run-m", HeadSHA: mergeHead}})
	if res := env.hub.wait("run-m", runnerwire.TypeMergeResult).MergeResult; res == nil || res.ID != "m-1" || res.Error != "" {
		t.Fatalf("result %+v", res)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var last runnerwire.PRInfo
		for _, msg := range env.hub.messages("run-m") {
			if msg.Type == runnerwire.TypePR {
				last = *msg.PR
			}
		}
		if last.State == runnerwire.PRMerged {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no merged PR: %+v", env.hub.messages("run-m"))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if worktreeExists(wtPath) {
		t.Error("worktree still there")
	}

	// Sent again after a reconnect: answered from memory, not merged again.
	env.hub.kick()
	env.hub.hello()
	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeMerge, RunID: "run-m",
		Merge: &runnerwire.MergeJob{ID: "m-1", RunID: "run-m", HeadSHA: mergeHead}})
	deadline = time.Now().Add(10 * time.Second)
	for {
		n := 0
		for _, msg := range env.hub.messages("run-m") {
			if msg.Type == runnerwire.TypeMergeResult {
				n++
				if msg.MergeResult.Error != "" {
					t.Fatalf("resent answer %+v", msg.MergeResult)
				}
			}
		}
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d answers", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if calls := gh.mergeCalls(); len(calls) != 1 {
		t.Fatalf("merged %d times", len(calls))
	}
}
