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

	"nodevas/internal/runnerwire"
)

const (
	reviewPRURL  = "https://github.com/o/r/pull/7"
	reviewBranch = "agent/n-1-run-impl"
	reviewHead   = "cafe000000000000000000000000000000000000"
)

// reviewGH is gh for review and revise jobs: pr view answers for one PR, pr
// comment is recorded. Never real gh.
type reviewGH struct {
	mu         sync.Mutex
	state      string
	headRef    string
	baseRef    string
	cross      bool
	comments   []string
	commentErr error
	calls      []string
}

func newReviewGH() *reviewGH {
	return &reviewGH{state: "OPEN", headRef: reviewBranch, baseRef: "main"}
}

func (g *reviewGH) run(_, stdin string, args []string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, strings.Join(args, " "))
	if len(args) >= 3 && args[0] == "pr" && args[1] == "view" && args[2] == reviewPRURL {
		raw, _ := json.Marshal(map[string]any{"number": 7, "state": g.state, "isDraft": false, "headRefName": g.headRef,
			"baseRefName": g.baseRef, "isCrossRepository": g.cross, "headRefOid": reviewHead,
			"statusCheckRollup": []any{}})
		return string(raw), nil
	}
	if len(args) == 5 && args[0] == "pr" && args[1] == "comment" && args[2] == reviewPRURL &&
		args[3] == "--body-file" && args[4] == "-" {
		if g.commentErr != nil {
			return "", g.commentErr
		}
		g.comments = append(g.comments, stdin)
		return reviewPRURL + "#issuecomment-1\n", nil
	}
	return "", errors.New("unexpected gh " + strings.Join(args, " "))
}

func (g *reviewGH) snapshot() (comments, calls []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.comments...), append([]string(nil), g.calls...)
}

type reviewEnv struct {
	*jobEnv
	gh   *reviewGH
	bare string
	// head is the PR branch's head on origin before the job.
	head string
}

// startReviewRunner is a runner whose repository's origin is "GitHub's"
// o/r (a bare repository behind an insteadOf), with a PR branch on it.
func startReviewRunner(t *testing.T, tweak func(*Client)) *reviewEnv {
	t.Helper()
	hermeticGit(t)
	gh := newReviewGH()
	env := startJobRunner(t, func(c *Client) {
		cmds := c.Commands.(*fakeCommands)
		cmds.paths["gh"] = "/bin/gh"
		cmds.gh = gh.run
		if tweak != nil {
			tweak(c)
		}
	})
	bare := withOrigin(t, env.repo)
	gitT(t, env.repo, "remote", "set-url", "origin", "git@github.com:O/r.git")
	gitT(t, env.repo, "config", "url."+filepath.ToSlash(bare)+".insteadOf", "git@github.com:O/r.git")
	gitT(t, env.repo, "checkout", "-q", "-b", reviewBranch)
	if err := os.WriteFile(filepath.Join(env.repo, "feature.txt"), []byte("the feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, env.repo, "add", "feature.txt")
	gitT(t, env.repo, "commit", "-q", "-m", "implement")
	gitT(t, env.repo, "push", "-q", "origin", reviewBranch)
	gitT(t, env.repo, "checkout", "-q", "main")
	// The implement run's worktree still holds the branch, as while its PR
	// is watched.
	gitT(t, env.repo, "worktree", "add", "-q", filepath.Join(t.TempDir(), "impl"), reviewBranch)
	return &reviewEnv{jobEnv: env, gh: gh, bare: bare, head: gitT(t, bare, "rev-parse", "refs/heads/"+reviewBranch)}
}

func prJob(runID, kind, prompt string) runnerwire.Job {
	job := testJob(runID, prompt)
	job.Kind, job.Branch, job.PRURL = kind, reviewBranch, reviewPRURL
	if kind == runnerwire.JobRevise {
		job.Review = "Please handle the empty case.\nVERDICT: changes"
	}
	return job
}

func eventsOf(msgs []runnerwire.Envelope) []runnerwire.RunEvent {
	var events []runnerwire.RunEvent
	for _, msg := range msgs {
		events = append(events, msg.Events...)
	}
	return events
}

func hasEvent(events []runnerwire.RunEvent, text string) bool {
	return slices.ContainsFunc(events, func(ev runnerwire.RunEvent) bool { return strings.Contains(ev.Text, text) })
}

func TestReviewJob(t *testing.T) {
	env := startReviewRunner(t, nil)
	env.hub.hello()
	env.hub.offer(prJob("run-rev", runnerwire.JobReview, "review-approve: Fix it, with tests"))
	result := env.hub.wait("run-rev", runnerwire.TypeFinish).Result
	if result.Status != runnerwire.RunSucceeded || result.Verdict != runnerwire.VerdictApprove || result.FailureKind != "" ||
		!strings.HasPrefix(result.Summary, "The change does what the node asks.") {
		t.Fatalf("result = %+v", result)
	}
	msgs := env.hub.messages("run-rev")
	if got := typesOf(msgs); !slices.Equal(got, []string{runnerwire.TypeAccept, runnerwire.TypeEvents, runnerwire.TypeFinish}) {
		t.Fatalf("message order %v: a review sends no changes and no PR", got)
	}
	if a := msgs[0].Accept; a.Branch != reviewBranch || a.Base != "main" {
		t.Errorf("accept = %+v", a)
	}

	// The prompt: a review of the PR, with its diff against main.
	stdin := env.recorded(t, "run-rev", "stdin")
	for _, want := range []string{"code review of pull request " + reviewPRURL, "review-approve: Fix it, with tests",
		"git diff origin/main...HEAD", "feature.txt | 1 +", "----- BEGIN DIFF -----", "+the feature", "----- END DIFF -----"} {
		if !strings.Contains(stdin, want) {
			t.Errorf("prompt missing %q:\n%s", want, stdin)
		}
	}

	comments, calls := env.gh.snapshot()
	if len(comments) != 1 || !strings.HasPrefix(comments[0], "**Nodevas review** (claude/sonnet) — verdict: approve\n\n") ||
		!strings.Contains(comments[0], "**VERDICT: approve**") || !strings.Contains(comments[0], "Nodevas-Run: run-rev") {
		t.Fatalf("comments %q", comments)
	}
	if slices.ContainsFunc(calls, func(c string) bool { return strings.HasPrefix(c, "pr create") }) {
		t.Errorf("a review opened a PR: %v", calls)
	}

	// Nothing committed or pushed; the scribbles and the worktree are gone.
	if got := gitT(t, env.bare, "rev-parse", "refs/heads/"+reviewBranch); got != env.head {
		t.Errorf("origin's branch moved: %s, was %s", got, env.head)
	}
	if refs := gitT(t, env.bare, "for-each-ref", "--format=%(refname)"); strings.Count(refs, "\n") != 1 {
		t.Errorf("origin refs:\n%s", refs)
	}
	if _, err := os.Stat(filepath.Join(env.client.Config.DataDir, "worktrees", "run-rev")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("review worktree still there: %v", err)
	}
	if !hasEvent(eventsOf(msgs), "the reviewer changed files; discarded") {
		t.Error("no note that the reviewer's changes were discarded")
	}
	if len(env.client.jobs.watchedRuns()) != 0 {
		t.Error("a review watches the PR")
	}
}

func TestReviewVerdicts(t *testing.T) {
	env := startReviewRunner(t, nil)
	env.hub.hello()
	env.hub.offer(prJob("run-rc", runnerwire.JobReview, "review-changes:"))
	if r := env.hub.wait("run-rc", runnerwire.TypeFinish).Result; r.Status != runnerwire.RunSucceeded ||
		r.Verdict != runnerwire.VerdictChanges {
		t.Fatalf("changes: %+v", r)
	}

	// No verdict: the review is still posted, the run fails as the task's.
	env.hub.offer(prJob("run-rs", runnerwire.JobReview, "review-silent:"))
	r := env.hub.wait("run-rs", runnerwire.TypeFinish).Result
	if r.Status != runnerwire.RunFailed || r.Verdict != "" || r.FailureKind != runnerwire.FailureTask ||
		!strings.Contains(r.Error, "review gave no verdict") || r.Summary != "Looks fine to me." {
		t.Fatalf("silent: %+v", r)
	}
	comments, _ := env.gh.snapshot()
	if len(comments) != 2 || !strings.Contains(comments[0], "verdict: changes") || !strings.Contains(comments[1], "verdict: none given") {
		t.Fatalf("comments %q", comments)
	}

	// A comment gh refuses does not change the verdict.
	env.gh.mu.Lock()
	env.gh.commentErr = errors.New("HTTP 403: Resource not accessible")
	env.gh.mu.Unlock()
	env.hub.offer(prJob("run-rf", runnerwire.JobReview, "review-approve:"))
	if r := env.hub.wait("run-rf", runnerwire.TypeFinish).Result; r.Status != runnerwire.RunSucceeded ||
		r.Verdict != runnerwire.VerdictApprove {
		t.Fatalf("comment refused: %+v", r)
	}
	if !hasEvent(eventsOf(env.hub.messages("run-rf")), "could not post the review") {
		t.Error("no note that the comment failed")
	}
}

func TestParseVerdictAndSummary(t *testing.T) {
	cases := map[string]string{
		"ok\nVERDICT: approve":                       "approve",
		"ok\n**Verdict:** Changes\n":                 "changes",
		"> verdict： approve":                         "approve",
		"VERDICT: changes\nmore\nVERDICT: approve\n": "approve",
		"VERDICT: approve\nthen VERDICT: changes":    "approve", // only whole lines count
		"I would approve this.":                      "",
		"VERDICT: approved-ish":                      "",
		"":                                           "",
	}
	for text, want := range cases {
		if got := parseVerdict(text); got != want {
			t.Errorf("parseVerdict(%q) = %q, want %q", text, got, want)
		}
	}
	long := strings.Repeat("a finding. ", 1000) + "\nVERDICT: changes"
	if got := reviewSummary(long, "changes"); len(got) > runnerwire.MaxSummaryBytes || !strings.HasSuffix(got, "\nVERDICT: changes") ||
		parseVerdict(got) != "changes" {
		t.Errorf("long summary: %d bytes, ends %q", len(got), got[len(got)-30:])
	}
	if got := reviewSummary(" short\n", "approve"); got != "short" {
		t.Errorf("short summary %q", got)
	}
}

func TestReviseJob(t *testing.T) {
	env := startReviewRunner(t, nil)
	env.hub.hello()
	env.hub.offer(prJob("run-rvs", runnerwire.JobRevise, "edit: Fix it"))
	result := env.hub.wait("run-rvs", runnerwire.TypeFinish).Result
	if result.Status != runnerwire.RunSucceeded || result.FailureKind != "" {
		t.Fatalf("result = %+v", result)
	}
	msgs := env.hub.messages("run-rvs")
	want := []string{runnerwire.TypeAccept, runnerwire.TypeEvents, runnerwire.TypeChanges, runnerwire.TypePR, runnerwire.TypeFinish}
	if got := typesOf(msgs); !slices.Equal(got, want) {
		t.Fatalf("message order %v, want %v", got, want)
	}
	if a := msgs[0].Accept; a.Branch != reviewBranch || a.Base != "main" {
		t.Errorf("accept = %+v", a)
	}
	changes := env.hub.wait("run-rvs", runnerwire.TypeChanges).Changes
	pr := env.hub.wait("run-rvs", runnerwire.TypePR).PR

	// Pushed to the PR's branch as a fast-forward of what was there.
	newHead := gitT(t, env.bare, "rev-parse", "refs/heads/"+reviewBranch)
	if newHead != changes.Commit || gitT(t, env.bare, "rev-parse", newHead+"^") != env.head {
		t.Fatalf("origin's branch at %s (parent %s), changes %+v, old head %s", newHead,
			gitT(t, env.bare, "rev-parse", newHead+"^"), changes, env.head)
	}
	// The changes are this revise's own, not the whole PR's.
	if changes.Base != env.head || len(changes.Files) != 3 || strings.Contains(changes.Patch, "feature.txt") {
		t.Errorf("changes = %+v", changes)
	}
	if subject := gitT(t, env.bare, "log", "-1", "--format=%s", newHead); subject != "Address review: Fix it" {
		t.Errorf("subject %q", subject)
	}
	if pr.URL != reviewPRURL || pr.Number != 7 || pr.State != runnerwire.PROpen || pr.HeadSHA != reviewHead {
		t.Errorf("pr = %+v", pr)
	}

	stdin := env.recorded(t, "run-rvs", "stdin")
	for _, want := range []string{"on branch " + reviewBranch, "open pull request, " + reviewPRURL,
		"Review to address:\n\nPlease handle the empty case."} {
		if !strings.Contains(stdin, want) {
			t.Errorf("prompt missing %q:\n%s", want, stdin)
		}
	}
	_, calls := env.gh.snapshot()
	if slices.ContainsFunc(calls, func(c string) bool { return strings.HasPrefix(c, "pr create") }) {
		t.Errorf("a revise opened a PR: %v", calls)
	}
	// The worktree is gone (the commit lives on the branch); the PR is
	// watched under this run, so its merge can be approved by it.
	if _, err := os.Stat(filepath.Join(env.client.Config.DataDir, "worktrees", "run-rvs")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("revise worktree still there: %v", err)
	}
	if got := env.client.jobs.watchedRuns(); !slices.Equal(got, []string{"run-rvs"}) {
		t.Errorf("watched %v", got)
	}
}

func TestReviseRejectedPush(t *testing.T) {
	env := startReviewRunner(t, nil)
	env.hub.hello()
	env.hub.offer(prJob("run-race", runnerwire.JobRevise, "race: "+reviewBranch))
	result := env.hub.wait("run-race", runnerwire.TypeFinish).Result
	if result.Status != runnerwire.RunFailed || result.FailureKind != runnerwire.FailureTask ||
		!strings.Contains(result.Error, "moved on origin") {
		t.Fatalf("result = %+v", result)
	}
	if slices.Contains(typesOf(env.hub.messages("run-race")), runnerwire.TypePR) {
		t.Error("a PR message for a revise that pushed nothing")
	}
	// Origin keeps the other push; the run's work stays for the owner.
	if subject := gitT(t, env.bare, "log", "-1", "--format=%s", "refs/heads/"+reviewBranch); subject != "someone else" {
		t.Errorf("origin's head %q", subject)
	}
	if _, err := os.Stat(filepath.Join(env.client.Config.DataDir, "worktrees", "run-race", "hello.txt")); err != nil {
		t.Errorf("the run's work was not kept: %v", err)
	}
	if len(env.client.jobs.watchedRuns()) != 0 {
		t.Error("watching after a refused push")
	}
}

// Review and revise offers are checked against the PR before anyone is asked.
func TestPRJobDeclines(t *testing.T) {
	env := startReviewRunner(t, nil)
	env.hub.hello()
	cases := map[string]struct {
		mutate func(*runnerwire.Job)
		gh     func(*reviewGH)
		reason string
	}{
		"d-kind":   {func(j *runnerwire.Job) { j.Kind = "deploy" }, nil, `job kind "deploy"`},
		"d-branch": {func(j *runnerwire.Job) { j.Branch = "-x" }, nil, "not a plain branch name"},
		"d-url":    {func(j *runnerwire.Job) { j.PRURL = "https://github.com/o/r/issues/7" }, nil, "not a pull request URL"},
		"d-repo": {func(j *runnerwire.Job) { j.PRURL = "https://github.com/o/other/pull/7" }, nil,
			"is not in this repository (origin is github.com/o/r)"},
		"d-merged":   {nil, func(g *reviewGH) { g.state = "MERGED" }, "is merged, not open"},
		"d-fork":     {nil, func(g *reviewGH) { g.cross = true }, "comes from a fork"},
		"d-head":     {nil, func(g *reviewGH) { g.headRef = "agent/other" }, "is on branch agent/other, not " + reviewBranch},
		"d-main":     {func(j *runnerwire.Job) { j.Kind, j.Branch, j.Review = runnerwire.JobRevise, "main", "do" }, nil, "only to its own agent/*"},
		"d-noreview": {func(j *runnerwire.Job) { j.Kind, j.Review = runnerwire.JobRevise, " " }, nil, "needs the review"},
	}
	for id, c := range cases {
		env.gh.mu.Lock()
		env.gh.state, env.gh.cross, env.gh.headRef = "OPEN", false, reviewBranch
		if c.gh != nil {
			c.gh(env.gh)
		}
		env.gh.mu.Unlock()
		job := prJob(id, runnerwire.JobReview, "review-approve:")
		if c.mutate != nil {
			c.mutate(&job)
		}
		env.hub.offer(job)
		msg := env.hub.wait(id, runnerwire.TypeDecline)
		if !strings.Contains(msg.Reason, c.reason) {
			t.Errorf("%s: reason %q, want %q", id, msg.Reason, c.reason)
		}
	}
}

func TestCheckKind(t *testing.T) {
	on := &Config{}
	off := false
	cases := []struct {
		job    runnerwire.Job
		cfg    *Config
		reason string
	}{
		{runnerwire.Job{}, on, ""},
		{runnerwire.Job{Kind: "review", Branch: "feature/x", PRURL: "https://github.com/o/r/pull/1"}, on, ""},
		{runnerwire.Job{Kind: "review", Branch: "feature/x", PRURL: "https://ghe.example:8443/team/o/r/pull/12"}, on, ""},
		{runnerwire.Job{Kind: "review", PRURL: "https://github.com/o/r/pull/1"}, on, "branch"},
		{runnerwire.Job{Kind: "review", Branch: "a..b", PRURL: "https://github.com/o/r/pull/1"}, on, "branch"},
		{runnerwire.Job{Kind: "review", Branch: "x", PRURL: "http://github.com/o/r/pull/1"}, on, "not a pull request URL"},
		{runnerwire.Job{Kind: "review", Branch: "x", PRURL: "https://github.com/o/r/pull/1 --admin"}, on, "not a pull request URL"},
		{runnerwire.Job{Kind: "review", Branch: "x", PRURL: "https://github.com/pull/1"}, on, "not a pull request URL"},
		{runnerwire.Job{Kind: "revise", Branch: "agent/x", PRURL: "https://github.com/o/r/pull/1", Review: "do"}, on, ""},
		{runnerwire.Job{Kind: "revise", Branch: "agent/x", PRURL: "https://github.com/o/r/pull/1", Review: "do"},
			&Config{AutoPR: &off}, "autoPR is off"},
		{runnerwire.Job{Kind: "revise", Branch: "agent/x", PRURL: "https://github.com/o/r/pull/1",
			Review: strings.Repeat("x", runnerwire.MaxPromptBytes+1)}, on, "too long"},
		{runnerwire.Job{Kind: "Review"}, on, "kind"},
	}
	for i, c := range cases {
		got := checkKind(&c.job, c.cfg)
		if (c.reason == "") != (got == "") || !strings.Contains(got, c.reason) {
			t.Errorf("case %d: %q, want %q", i, got, c.reason)
		}
	}
}

func TestRepoKeys(t *testing.T) {
	for remote, want := range map[string]string{
		"https://github.com/O/R.git":           "github.com/o/r",
		"https://user:tok@github.com/o/r/":     "github.com/o/r",
		"git@github.com:o/r.git":               "github.com/o/r",
		"ssh://git@ssh.github.com:443/o/r.git": "github.com/o/r",
		"ssh://git@ghe.example:2222/team/o/r":  "ghe.example/team/o/r",
		"C:/Users/x/origin.git":                "",
		"/srv/git/r.git":                       "",
		"https://github.com/onlyowner":         "",
	} {
		if got := repoKey(remote); got != want {
			t.Errorf("repoKey(%q) = %q, want %q", remote, got, want)
		}
	}
	if got := prRepoKey("https://GitHub.com/o/r/pull/9"); got != "github.com/o/r" {
		t.Errorf("prRepoKey = %q", got)
	}
}

// A revised PR is watched by its implement run and its revise run. A merge
// approved through the revise run ends both: the implement run's worktree is
// removed once, and the hub hears the merge on each run.
func TestMergeSharedPR(t *testing.T) {
	env := newMergeEnv(t, nil)
	env.j.watch(watchedPR{RunID: "run-v", NodeID: "n-1", URL: mergePRURL, Repo: env.repo, Branch: env.wt.Branch,
		PR: runnerwire.PRInfo{URL: mergePRURL, Number: 7, State: runnerwire.PROpen, CI: runnerwire.CISuccess, HeadSHA: mergeHead}})
	env.j.runMerge(runnerwire.MergeJob{ID: "m-v", RunID: "run-v", HeadSHA: mergeHead})
	msgs := drain(t, env.j)
	merged := map[string]bool{}
	for _, msg := range msgs {
		switch {
		case msg.Type == runnerwire.TypeMergeResult && msg.MergeResult.Error != "":
			t.Fatalf("refused: %+v", msg.MergeResult)
		case msg.Type == runnerwire.TypePR && msg.PR.State == runnerwire.PRMerged:
			merged[msg.RunID] = true
		}
	}
	if !merged["run-m"] || !merged["run-v"] || len(env.gh.mergeCalls()) != 1 {
		t.Fatalf("merged %v, merges %v, messages %+v", merged, env.gh.mergeCalls(), msgs)
	}
	if worktreeExists(env.wt.Path) || len(env.j.watchedRuns()) != 0 {
		t.Fatalf("worktree %v, watched %v", worktreeExists(env.wt.Path), env.j.watchedRuns())
	}
	if strings.Contains(env.log.String(), "removing its worktree") {
		t.Errorf("a worktree removed twice or failed:\n%s", env.log.String())
	}
}

// A failure before the agent starts is the agent's absence, not the task's.
func TestRunAgentMissingBinary(t *testing.T) {
	c := &Client{Config: &Config{}, Commands: &fakeCommands{paths: map[string]string{}}}
	j := newJobs(context.Background(), c)
	r := &run{job: testJob("run-x", "ok:")}
	result, _ := j.runAgent(context.Background(), r, Worktree{Path: t.TempDir()}, r.job)
	if result.Status != runnerwire.RunFailed || result.FailureKind != runnerwire.FailureAgentAbsent ||
		!strings.Contains(result.Error, "no longer on PATH") {
		t.Fatalf("result = %+v", result)
	}
}

func TestLoginFailureKind(t *testing.T) {
	env := startJobRunner(t, nil)
	env.hub.hello()
	env.hub.offer(testJob("run-login", "login-fail:"))
	result := env.hub.wait("run-login", runnerwire.TypeFinish).Result
	if result.Status != runnerwire.RunFailed || result.FailureKind != runnerwire.FailureAgentAbsent ||
		!runnerwire.FallbackWorthy(result.FailureKind) {
		t.Fatalf("result = %+v", result)
	}
}
