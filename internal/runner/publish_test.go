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

func TestCommitMessage(t *testing.T) {
	job := runnerwire.Job{RunID: "0123456789abcdef", NodeID: "n-42", NodeTitle: "Fix  the\nlogin page"}
	got := commitMessage("\n\n## Fixed the login redirect\n\nThe callback now keeps ?next=.\r\nTests added.\n", job)
	want := "Fixed the login redirect\n\nThe callback now keeps ?next=.\nTests added.\n\nNodevas-Node: n-42\nNodevas-Run: 0123456789abcdef\n"
	if got != want {
		t.Errorf("message:\n%q\nwant\n%q", got, want)
	}

	// No summary: the run and the node name it.
	if got := commitMessage("  ", job); got != "Nodevas run 01234567: Fix the login page\n\nNodevas-Node: n-42\nNodevas-Run: 0123456789abcdef\n" {
		t.Errorf("fallback message %q", got)
	}

	// A long first line is cut at a word, and the body keeps all of it.
	long := strings.Repeat("word ", 30) + "end"
	got = commitMessage(long, job)
	subject, body, _ := strings.Cut(got, "\n\n")
	if len([]rune(subject)) > maxSubject || strings.HasSuffix(subject, " ") || !strings.HasPrefix(body, long) {
		t.Errorf("long subject %q, body %q", subject, body)
	}

	// The body is capped.
	got = commitMessage("Subject\n"+strings.Repeat("é", 3000), job)
	if len(got) > maxCommitBody+200 || !strings.HasSuffix(got, "Nodevas-Run: 0123456789abcdef\n") || !strings.Contains(got, "é") {
		t.Errorf("capped body: %d bytes", len(got))
	}
}

func TestParseDiffOutput(t *testing.T) {
	statuses := "M\x00README\x00R087\x00old/name.go\x00new/name.go\x00A\x00img.png\x00D\x00gone.txt\x00C100\x00a.txt\x00b.txt\x00T\x00link\x00"
	numstats := "2\t1\tREADME\x0010\t3\t\x00old/name.go\x00new/name.go\x00-\t-\timg.png\x000\t4\tgone.txt\x001\t0\t\x00a.txt\x00b.txt\x00"
	files := parseNameStatus(statuses)
	applyNumstat(files, parseNumstat(numstats))
	want := []runnerwire.FileChange{
		{Path: "README", Status: runnerwire.FileModified, Additions: 2, Deletions: 1},
		{Path: "new/name.go", OldPath: "old/name.go", Status: runnerwire.FileRenamed, Additions: 10, Deletions: 3},
		{Path: "img.png", Status: runnerwire.FileAdded, Binary: true},
		{Path: "gone.txt", Status: runnerwire.FileDeleted, Deletions: 4},
		{Path: "b.txt", Status: runnerwire.FileAdded, Additions: 1},
		{Path: "link", Status: runnerwire.FileModified},
	}
	if !slices.Equal(files, want) {
		t.Fatalf("files:\n%+v\nwant\n%+v", files, want)
	}
}

func TestCapWriter(t *testing.T) {
	w := &capWriter{limit: 5}
	_, _ = w.Write([]byte("abc"))
	_, _ = w.Write([]byte("de"))
	if string(w.buf) != "abcde" || w.over {
		t.Fatalf("exact fit: %q %v", w.buf, w.over)
	}
	_, _ = w.Write([]byte("f"))
	if string(w.buf) != "abcde" || !w.over {
		t.Fatalf("over: %q %v", w.buf, w.over)
	}
}

// hermeticGit points git at an empty global config: no identity, no signing,
// no hooks path from the machine running the tests.
func hermeticGit(t *testing.T) {
	t.Helper()
	empty := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func TestCommitAndDescribeChanges(t *testing.T) {
	hermeticGit(t)
	repo := newTestRepo(t)
	write := func(dir, name, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	body := strings.Repeat("line of the file that will move\n", 20)
	write(repo, "old.txt", body)
	write(repo, "gone.txt", "bye\n")
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "more")
	ctx := context.Background()
	wt, err := CreateWorktree(ctx, repo, t.TempDir(), "run-c", "n-1", "")
	if err != nil {
		t.Fatal(err)
	}

	// Nothing changed: no commit, nothing to describe.
	res := commitRun(ctx, wt, "unused")
	if res.Err != nil || res.Changed || res.Commit != "" {
		t.Fatalf("clean worktree: %+v", res)
	}

	if err := os.MkdirAll(filepath.Join(wt.Path, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	gitT(t, wt.Path, "mv", "old.txt", "dir/new.txt")
	write(wt.Path, "dir/new.txt", body+"one more\n")
	if err := os.Remove(filepath.Join(wt.Path, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	write(wt.Path, "blob.bin", "\x00\x01\x02")
	write(wt.Path, "README", "hi\nthere\n")
	message := commitMessage("Moved things\n\nAnd more.", runnerwire.Job{RunID: "run-c", NodeID: "n-1"})
	res = commitRun(ctx, wt, message)
	if res.Err != nil || !res.Changed || len(res.Commit) != 40 {
		t.Fatalf("commit: %+v", res)
	}
	// No identity anywhere: the fallback one. The trailers are real trailers.
	if author := gitT(t, wt.Path, "log", "-1", "--format=%an <%ae>"); author != "Nodevas runner <runner@nodevas.invalid>" {
		t.Errorf("author %q", author)
	}
	if trailers := gitT(t, wt.Path, "log", "-1", "--format=%(trailers:key=Nodevas-Node,valueonly)"); trailers != "n-1" {
		t.Errorf("trailers %q", trailers)
	}

	changes, err := describeChanges(ctx, wt.Path, res, res.Base)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]runnerwire.FileChange{}
	for _, f := range changes.Files {
		byPath[f.Path] = f
	}
	if f := byPath["dir/new.txt"]; f.Status != runnerwire.FileRenamed || f.OldPath != "old.txt" || f.Additions != 1 {
		t.Errorf("rename: %+v", f)
	}
	if f := byPath["blob.bin"]; f.Status != runnerwire.FileAdded || !f.Binary {
		t.Errorf("binary: %+v", f)
	}
	if f := byPath["gone.txt"]; f.Status != runnerwire.FileDeleted || f.Deletions != 1 {
		t.Errorf("delete: %+v", f)
	}
	if f := byPath["README"]; f.Status != runnerwire.FileModified || f.Additions != 1 {
		t.Errorf("modify: %+v", f)
	}
	if len(changes.Files) != 4 || changes.Commit != res.Commit || changes.Base != res.Base || changes.Additions != 2 ||
		changes.Deletions != 1 || !strings.Contains(changes.Patch, "rename to dir/new.txt") || changes.PatchTruncated {
		t.Errorf("changes = %+v", changes)
	}

	// The repository's own identity wins over the fallback.
	gitT(t, repo, "config", "user.name", "Owner Name")
	gitT(t, repo, "config", "user.email", "owner@example.test")
	big := strings.Repeat("0123456789abcdef<>&\n", runnerwire.MaxPatchBytes/20+10)
	write(wt.Path, "big.txt", big)
	res = commitRun(ctx, wt, message)
	if res.Err != nil || res.Commit == "" {
		t.Fatalf("second commit: %+v", res)
	}
	if author := gitT(t, wt.Path, "log", "-1", "--format=%an"); author != "Owner Name" {
		t.Errorf("author %q", author)
	}
	changes, err = describeChanges(ctx, wt.Path, res, res.Base)
	if err != nil {
		t.Fatal(err)
	}
	if changes.Patch != "" || !changes.PatchTruncated || len(changes.Files) != 5 {
		t.Fatalf("over the patch cap: truncated %v, %d patch bytes, %d files", changes.PatchTruncated, len(changes.Patch), len(changes.Files))
	}
}

func TestFitChanges(t *testing.T) {
	changes := runnerwire.RunChanges{Commit: "abc",
		Files: []runnerwire.FileChange{{Path: "ok.go"}, {Path: "bad\xffname"}, {Path: strings.Repeat("a", 2000)}, {Path: ""}},
		// Every < escapes to six bytes: this is over the limit as JSON.
		Patch: strings.Repeat("<", 2*runnerwire.MaxPatchBytes)}
	fitChanges("r", &changes)
	raw, _ := json.Marshal(runnerwire.Envelope{Type: runnerwire.TypeChanges, RunID: "r", Changes: &changes})
	if len(raw) > runnerwire.MaxChangesMessageBytes {
		t.Fatalf("message of %d bytes", len(raw))
	}
	if changes.Patch != "" || !changes.PatchTruncated || len(changes.Files) != 3 || changes.Files[1].Path != "bad\uFFFDname" ||
		len(changes.Files[2].Path) != maxFilePath {
		t.Fatalf("fitted: truncated %v, files %d", changes.PatchTruncated, len(changes.Files))
	}

	// A patch that fits once escaped is kept, even over MaxMessageBytes.
	changes = runnerwire.RunChanges{Files: []runnerwire.FileChange{}, Patch: strings.Repeat("<", runnerwire.MaxPatchBytes/2)}
	fitChanges("r", &changes)
	if changes.Patch == "" || changes.PatchTruncated {
		t.Fatal("dropped a patch that fits")
	}
}

func TestCIRollup(t *testing.T) {
	run := func(status, conclusion string) ghCheck { return ghCheck{Status: status, Conclusion: conclusion} }
	ctx := func(state string) ghCheck { return ghCheck{State: state} }
	cases := []struct {
		checks []ghCheck
		want   string
	}{
		{nil, runnerwire.CINone},
		{[]ghCheck{run("COMPLETED", "SUCCESS"), run("COMPLETED", "SKIPPED"), run("COMPLETED", "NEUTRAL"), ctx("SUCCESS")}, runnerwire.CISuccess},
		{[]ghCheck{run("COMPLETED", "SUCCESS"), run("IN_PROGRESS", "")}, runnerwire.CIPending},
		{[]ghCheck{run("QUEUED", ""), ctx("SUCCESS")}, runnerwire.CIPending},
		{[]ghCheck{ctx("PENDING")}, runnerwire.CIPending},
		{[]ghCheck{ctx("EXPECTED"), run("COMPLETED", "SUCCESS")}, runnerwire.CIPending},
		{[]ghCheck{run("IN_PROGRESS", ""), run("COMPLETED", "FAILURE")}, runnerwire.CIFailure},
		{[]ghCheck{run("COMPLETED", "TIMED_OUT")}, runnerwire.CIFailure},
		{[]ghCheck{run("COMPLETED", "CANCELLED")}, runnerwire.CIFailure},
		{[]ghCheck{run("COMPLETED", "ACTION_REQUIRED")}, runnerwire.CIFailure},
		{[]ghCheck{ctx("ERROR"), run("COMPLETED", "SUCCESS")}, runnerwire.CIFailure},
		{[]ghCheck{ctx("FAILURE")}, runnerwire.CIFailure},
	}
	for i, c := range cases {
		if got := ciRollup(c.checks); got != c.want {
			t.Errorf("case %d: %q, want %q", i, got, c.want)
		}
	}
}

func TestParsePRView(t *testing.T) {
	const prURL = "https://github.com/o/r/pull/7"
	info, err := parsePRView(prURL, `{"number":7,"state":"MERGED","isDraft":true,"headRefOid":"abc",
		"statusCheckRollup":[{"__typename":"CheckRun","status":"COMPLETED","conclusion":"SUCCESS"}]}`)
	if err != nil || info != (runnerwire.PRInfo{URL: prURL, Number: 7, State: runnerwire.PRMerged, Draft: true,
		CI: runnerwire.CISuccess, HeadSHA: "abc"}) {
		t.Fatalf("info = %+v, %v", info, err)
	}
	if _, err := parsePRView(prURL, "not json"); err == nil {
		t.Fatal("no error for garbage")
	}
	if prNumber("https://github.com/o/r/pull/123") != 123 || lastPRURL("x http://github.com/o/r/pull/1") != "" ||
		lastPRURL("Creating...\nhttps://github.com/o/r/pull/9\n") != "https://github.com/o/r/pull/9" {
		t.Fatal("URL parsing")
	}
}

func TestPRBodyAndLabels(t *testing.T) {
	job := runnerwire.Job{RunID: "run-1", NodeID: "n-1", NodeTitle: "Fix it", Project: "demo", Agent: "claude", Model: "sonnet",
		RequestedBy: "patrick"}
	body := prBody(prInfo{job: job, result: runnerwire.RunResult{Status: runnerwire.RunSucceeded, Summary: "Did it.",
		Usage: &runnerwire.Usage{InputTokens: 10, OutputTokens: 20, CostUSD: 1.5}}, runnerName: "pc", hub: "https://hub.example/"})
	for _, want := range []string{"Did it.", "https://hub.example/popout?node=n-1&project=demo", "model `sonnet`",
		"**Runner:** pc", "`run-1`", "10 input tokens", "$1.50", "\nNodevas-Node: n-1\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
	failed := prBody(prInfo{job: job, result: runnerwire.RunResult{Status: runnerwire.RunFailed, Error: "timed out"}})
	if !strings.Contains(failed, "did not finish cleanly**: timed out") || !strings.Contains(failed, "no summary") {
		t.Errorf("failed body:\n%s", failed)
	}

	cfg := &Config{}
	if got := cfg.PRLabelsFor("claude", ""); !slices.Equal(got, []string{"nodevas", "model:default"}) {
		t.Errorf("default labels %v", got)
	}
	if err := cfg.Set("prLabels", []string{"ai, {agent}/{model}", "nodevas"}); err != nil {
		t.Fatal(err)
	}
	if got := cfg.PRLabelsFor("codex", "gpt-5.5"); !slices.Equal(got, []string{"ai", "codex/gpt-5.5", "nodevas"}) {
		t.Errorf("labels %v", got)
	}
	if err := cfg.Set("prLabels", []string{"none"}); err != nil || len(cfg.PRLabelsFor("a", "b")) != 0 || cfg.PRLabels == nil {
		t.Errorf("none: %v %v", err, cfg.PRLabels)
	}
	if err := cfg.Set("prLabels", []string{"default"}); err != nil || cfg.PRLabels != nil {
		t.Errorf("default: %v", err)
	}
	for key, value := range map[string]string{"autoPR": "false", "prDraft": "true", "prPollSeconds": "45"} {
		if err := cfg.Set(key, []string{value}); err != nil {
			t.Fatal(err)
		}
	}
	if cfg.AutoPROn() || !cfg.PRDraft || cfg.PRPollInterval() != 45*time.Second {
		t.Errorf("config %+v", cfg)
	}
	if err := cfg.Set("prPollSeconds", []string{"5"}); err == nil {
		t.Error("prPollSeconds 5 accepted")
	}
	if (&Config{}).PRPollInterval() != 2*time.Minute || !(&Config{}).AutoPROn() {
		t.Error("defaults")
	}
}

// ghFake is gh: pr create and pr view, scripted.
type ghFake struct {
	mu        sync.Mutex
	creates   [][]string
	bodies    []string
	createErr []error // consumed one per create
	url       string
	view      string
	viewErr   error
	views     int
}

func (g *ghFake) run(dir, stdin string, args []string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(args) >= 2 && args[0] == "pr" && args[1] == "create" {
		g.creates = append(g.creates, args)
		g.bodies = append(g.bodies, stdin)
		if len(g.createErr) > 0 {
			err := g.createErr[0]
			g.createErr = g.createErr[1:]
			if err != nil {
				return "", err
			}
		}
		return "Creating pull request...\n" + g.url + "\n", nil
	}
	if len(args) >= 2 && args[0] == "pr" && args[1] == "view" {
		g.views++
		if g.viewErr != nil {
			return "", g.viewErr
		}
		return g.view, nil
	}
	return "", errors.New("unexpected gh " + strings.Join(args, " "))
}

func (g *ghFake) setView(state, ci string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	checks := "[]"
	switch ci {
	case "pending":
		checks = `[{"status":"IN_PROGRESS","conclusion":""}]`
	case "success":
		checks = `[{"status":"COMPLETED","conclusion":"SUCCESS"}]`
	}
	g.view = `{"number":7,"state":"` + state + `","isDraft":false,"headRefOid":"feedface","statusCheckRollup":` + checks + `}`
}

func (g *ghFake) createCalls() ([][]string, []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([][]string(nil), g.creates...), append([]string(nil), g.bodies...)
}

// withOrigin gives the env's repository a bare origin holding main.
func withOrigin(t *testing.T, repo string) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "origin.git")
	gitT(t, repo, "init", "-q", "--bare", bare)
	gitT(t, repo, "remote", "add", "origin", bare)
	gitT(t, repo, "push", "-q", "origin", "main")
	return bare
}

func startPRRunner(t *testing.T, gh *ghFake, tweak func(*Client)) *jobEnv {
	t.Helper()
	hermeticGit(t)
	return startJobRunner(t, func(c *Client) {
		cmds := c.Commands.(*fakeCommands)
		if gh != nil {
			cmds.paths["gh"] = "/bin/gh"
			cmds.gh = gh.run
		}
		if tweak != nil {
			tweak(c)
		}
	})
}

func typesOf(msgs []runnerwire.Envelope) []string {
	var types []string
	for _, msg := range msgs {
		if len(types) == 0 || types[len(types)-1] != msg.Type {
			types = append(types, msg.Type)
		}
	}
	return types
}

func TestPipelineOpensPR(t *testing.T) {
	gh := &ghFake{url: "https://github.com/o/r/pull/7"}
	gh.setView("OPEN", "pending")

	env := startPRRunner(t, gh, func(c *Client) { c.PRPollInterval = time.Hour })
	bare := withOrigin(t, env.repo)
	env.hub.hello()
	env.hub.offer(testJob("run-pr", "edit: change things"))
	env.hub.wait("run-pr", runnerwire.TypeFinish)
	msgs := env.hub.messages("run-pr")

	want := []string{runnerwire.TypeAccept, runnerwire.TypeEvents, runnerwire.TypeChanges, runnerwire.TypePR, runnerwire.TypeFinish}
	if got := typesOf(msgs); !slices.Equal(got, want) {
		t.Fatalf("message order %v, want %v", got, want)
	}
	var changes *runnerwire.RunChanges
	var pr *runnerwire.PRInfo
	for _, msg := range msgs {
		if msg.Changes != nil {
			changes = msg.Changes
		}
		if msg.PR != nil {
			pr = msg.PR
		}
	}
	if len(changes.Files) != 3 || len(changes.Commit) != 40 || changes.Additions != 2 || !strings.Contains(changes.Patch, "+more") {
		t.Fatalf("changes = %+v", changes)
	}
	if *pr != (runnerwire.PRInfo{URL: gh.url, Number: 7, State: runnerwire.PROpen, CI: runnerwire.CIPending, HeadSHA: "feedface"}) {
		t.Fatalf("pr = %+v", pr)
	}

	// Really pushed, with the run's commit.
	if got := gitT(t, bare, "rev-parse", "refs/heads/agent/n-1-run-pr"); got != changes.Commit {
		t.Errorf("origin has %q, want %q", got, changes.Commit)
	}
	if subject := gitT(t, bare, "log", "-1", "--format=%s", "agent/n-1-run-pr"); subject != `Content: "hello fixture"` {
		t.Errorf("commit subject %q", subject)
	}
	creates, bodies := gh.createCalls()
	if len(creates) != 1 {
		t.Fatalf("creates %v", creates)
	}
	args := strings.Join(creates[0], " ")
	for _, want := range []string{"--base main", "--head agent/n-1-run-pr", "--title Fix it", "--body-file -",
		"--label nodevas", "--label model:sonnet"} {
		if !strings.Contains(args, want) {
			t.Errorf("gh pr create %s: missing %q", args, want)
		}
	}
	if strings.Contains(args, "--draft") {
		t.Errorf("a succeeded run's PR is a draft: %s", args)
	}
	if !strings.Contains(bodies[0], "hello fixture") || !strings.Contains(bodies[0], "Nodevas-Node: n-1") ||
		!strings.Contains(bodies[0], "popout?node=n-1") || !strings.Contains(bodies[0], "**Runner:** pc") {
		t.Errorf("body:\n%s", bodies[0])
	}

	// The worktree stays for the PR, which is watched and saved.
	wtPath := filepath.Join(env.client.Config.DataDir, "worktrees", "run-pr")
	if _, err := os.Stat(wtPath); err != nil {
		t.Errorf("worktree: %v", err)
	}
	if got := env.client.jobs.watchedRuns(); !slices.Equal(got, []string{"run-pr"}) {
		t.Errorf("watched %v", got)
	}
	raw, err := os.ReadFile(filepath.Join(env.client.Config.DataDir, prsFileName))
	if err != nil || !strings.Contains(string(raw), gh.url) {
		t.Errorf("prs.json: %s %v", raw, err)
	}
}

func TestPipelineFailedRunOpensDraft(t *testing.T) {
	gh := &ghFake{url: "https://github.com/o/r/pull/8"}
	gh.setView("OPEN", "")
	env := startPRRunner(t, gh, nil)
	withOrigin(t, env.repo)
	env.hub.hello()
	env.hub.offer(testJob("run-pf", "edit-fail:"))
	if r := env.hub.wait("run-pf", runnerwire.TypeFinish).Result; r.Status != runnerwire.RunFailed {
		t.Fatalf("result = %+v", r)
	}
	creates, bodies := gh.createCalls()
	if len(creates) != 1 || !slices.Contains(creates[0], "--draft") || !strings.Contains(bodies[0], "did not finish cleanly") {
		t.Fatalf("creates %v\n%s", creates, bodies)
	}
}

func prOf(t *testing.T, env *jobEnv, runID string) runnerwire.PRInfo {
	t.Helper()
	msg := env.hub.wait(runID, runnerwire.TypePR)
	if types := typesOf(env.hub.messages(runID)); !slices.Equal(types[len(types)-3:],
		[]string{runnerwire.TypeChanges, runnerwire.TypePR, runnerwire.TypeFinish}) {
		t.Fatalf("message order %v", types)
	}
	return *msg.PR
}

func TestPipelinePRFailures(t *testing.T) {
	t.Run("no origin", func(t *testing.T) {
		gh := &ghFake{url: "https://github.com/o/r/pull/1"}
		env := startPRRunner(t, gh, nil)
		env.hub.hello()
		env.hub.offer(testJob("run-no", "edit:"))
		env.hub.wait("run-no", runnerwire.TypeFinish)
		if pr := prOf(t, env, "run-no"); pr.URL != "" || !strings.Contains(pr.Error, "no origin remote") {
			t.Fatalf("pr = %+v", pr)
		}
		if creates, _ := gh.createCalls(); len(creates) != 0 {
			t.Fatal("gh called without an origin")
		}
		// Kept for the owner, not watched.
		if _, err := os.Stat(filepath.Join(env.client.Config.DataDir, "worktrees", "run-no")); err != nil {
			t.Errorf("worktree: %v", err)
		}
		if len(env.client.jobs.watchedRuns()) != 0 {
			t.Error("watching a PR that does not exist")
		}
	})
	t.Run("gh missing", func(t *testing.T) {
		env := startPRRunner(t, nil, nil)
		bare := withOrigin(t, env.repo)
		env.hub.hello()
		env.hub.offer(testJob("run-nogh", "edit:"))
		env.hub.wait("run-nogh", runnerwire.TypeFinish)
		if pr := prOf(t, env, "run-nogh"); pr.URL != "" || !strings.Contains(pr.Error, "gh (GitHub CLI) is not installed") {
			t.Fatalf("pr = %+v", pr)
		}
		if refs := gitT(t, bare, "for-each-ref", "refs/heads/agent"); refs != "" {
			t.Errorf("pushed without gh: %s", refs)
		}
	})
	t.Run("gh not signed in", func(t *testing.T) {
		gh := &ghFake{createErr: []error{errors.New("To get started with GitHub CLI, please run:  gh auth login")}}
		env := startPRRunner(t, gh, nil)
		withOrigin(t, env.repo)
		env.hub.hello()
		env.hub.offer(testJob("run-auth", "edit:"))
		env.hub.wait("run-auth", runnerwire.TypeFinish)
		if pr := prOf(t, env, "run-auth"); pr.URL != "" || !strings.Contains(pr.Error, "gh is not signed in") {
			t.Fatalf("pr = %+v", pr)
		}
	})
	t.Run("push refused", func(t *testing.T) {
		gh := &ghFake{url: "https://github.com/o/r/pull/1"}
		env := startPRRunner(t, gh, nil)
		bare := withOrigin(t, env.repo)
		hook := filepath.Join(bare, "hooks", "pre-receive")
		if err := os.WriteFile(hook, []byte("#!/bin/sh\necho no pushes here >&2\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		env.hub.hello()
		env.hub.offer(testJob("run-push", "edit:"))
		env.hub.wait("run-push", runnerwire.TypeFinish)
		if pr := prOf(t, env, "run-push"); pr.URL != "" || !strings.Contains(pr.Error, "push to origin refused") {
			t.Fatalf("pr = %+v", pr)
		}
	})
	t.Run("label missing", func(t *testing.T) {
		gh := &ghFake{url: "https://github.com/o/r/pull/3",
			createErr: []error{errors.New("could not add label: 'model:sonnet' not found")}}
		gh.setView("OPEN", "")
		env := startPRRunner(t, gh, nil)
		withOrigin(t, env.repo)
		env.hub.hello()
		env.hub.offer(testJob("run-label", "edit:"))
		env.hub.wait("run-label", runnerwire.TypeFinish)
		if pr := prOf(t, env, "run-label"); pr.URL != gh.url || pr.Error != "" {
			t.Fatalf("pr = %+v", pr)
		}
		creates, _ := gh.createCalls()
		if len(creates) != 2 || !slices.Contains(creates[0], "--label") || slices.Contains(creates[1], "--label") {
			t.Fatalf("creates %v", creates)
		}
	})
	t.Run("commit hook refuses", func(t *testing.T) {
		gh := &ghFake{url: "https://github.com/o/r/pull/1"}
		env := startPRRunner(t, gh, nil)
		withOrigin(t, env.repo)
		hook := filepath.Join(env.repo, ".git", "hooks", "pre-commit")
		if err := os.WriteFile(hook, []byte("#!/bin/sh\necho lint failed >&2\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		env.hub.hello()
		env.hub.offer(testJob("run-hook", "edit:"))
		env.hub.wait("run-hook", runnerwire.TypeFinish)
		changes := env.hub.wait("run-hook", runnerwire.TypeChanges).Changes
		if changes.Commit != "" || len(changes.Files) != 3 {
			t.Fatalf("changes = %+v", changes)
		}
		if pr := prOf(t, env, "run-hook"); pr.URL != "" || !strings.Contains(pr.Error, "lint failed") {
			t.Fatalf("pr = %+v", pr)
		}
		if _, err := os.Stat(filepath.Join(env.client.Config.DataDir, "worktrees", "run-hook", "hello.txt")); err != nil {
			t.Errorf("uncommitted work not kept: %v", err)
		}
	})
	t.Run("autoPR off", func(t *testing.T) {
		gh := &ghFake{url: "https://github.com/o/r/pull/1"}
		env := startPRRunner(t, gh, func(c *Client) { off := false; c.Config.AutoPR = &off })
		withOrigin(t, env.repo)
		env.hub.hello()
		env.hub.offer(testJob("run-off", "edit:"))
		env.hub.wait("run-off", runnerwire.TypeFinish)
		types := typesOf(env.hub.messages("run-off"))
		if !slices.Equal(types[len(types)-2:], []string{runnerwire.TypeChanges, runnerwire.TypeFinish}) || slices.Contains(types, runnerwire.TypePR) {
			t.Fatalf("types %v", types)
		}
		if creates, _ := gh.createCalls(); len(creates) != 0 {
			t.Fatal("gh called with autoPR off")
		}
	})
}

// The watcher: a PR from a previous runner process (prs.json), resent on
// connect, sent again on each change, and on merge its worktree goes.
func TestPRWatcher(t *testing.T) {
	gh := &ghFake{url: "https://github.com/o/r/pull/7"}
	gh.setView("OPEN", "")
	dataDir := t.TempDir()
	repo := ""
	var wtPath string
	env := startPRRunner(t, gh, func(c *Client) {
		c.Config.DataDir = dataDir
		c.PRPollInterval = 30 * time.Millisecond
		repo = c.Config.Repos[0].Path
		wt, err := CreateWorktree(context.Background(), repo, dataDir, "run-old", "n-9", "")
		if err != nil {
			t.Fatal(err)
		}
		wtPath = wt.Path
		saved := prsFile{PRs: []watchedPR{{RunID: "run-old", URL: gh.url, Worktree: wt.Path, Repo: repo, Branch: wt.Branch,
			PR: runnerwire.PRInfo{URL: gh.url, Number: 7, State: runnerwire.PROpen, HeadSHA: "feedface"}}}}
		if err := writeFileAtomic(filepath.Join(dataDir, prsFileName), saved); err != nil {
			t.Fatal(err)
		}
	})
	if hello := env.hub.hello(); len(hello.Active) != 0 {
		t.Fatalf("a watched PR's run listed as active: %v", hello.Active)
	}
	prs := func() []runnerwire.PRInfo {
		var out []runnerwire.PRInfo
		for _, msg := range env.hub.messages("run-old") {
			if msg.Type != runnerwire.TypePR {
				t.Fatalf("unexpected %s", msg.Type)
			}
			out = append(out, *msg.PR)
		}
		return out
	}
	waitPRs := func(n int) []runnerwire.PRInfo {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if got := prs(); len(got) >= n {
				return got
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("wanted %d PR messages, got %+v", n, prs())
		return nil
	}
	// Resent once on connect; unchanged polls send nothing.
	got := waitPRs(1)
	if got[0].State != runnerwire.PROpen || got[0].Number != 7 {
		t.Fatalf("resent %+v", got[0])
	}
	time.Sleep(200 * time.Millisecond)
	if n := len(prs()); n != 1 {
		t.Fatalf("%d messages for an unchanged PR", n)
	}

	gh.setView("OPEN", "pending")
	if got := waitPRs(2); got[1].CI != runnerwire.CIPending {
		t.Fatalf("after CI started: %+v", got[1])
	}
	// A reconnect resends the latest once.
	env.hub.kick()
	env.hub.hello()
	if got := waitPRs(3); got[2] != got[1] {
		t.Fatalf("resent %+v, want %+v", got[2], got[1])
	}

	gh.setView("MERGED", "success")
	got = waitPRs(4)
	if got[3].State != runnerwire.PRMerged || got[3].CI != runnerwire.CISuccess {
		t.Fatalf("merged: %+v", got[3])
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(env.client.jobs.watchedRuns()) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(wtPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("merged PR's worktree still there: %v", err)
	}
	if got := gitT(t, repo, "branch", "--list", "agent/n-9-run-old"); got == "" {
		t.Error("the branch went with the worktree; GitHub owns it")
	}
	var file prsFile
	raw, _ := os.ReadFile(filepath.Join(dataDir, prsFileName))
	if err := json.Unmarshal(raw, &file); err != nil || len(file.PRs) != 0 || len(env.client.jobs.watchedRuns()) != 0 {
		t.Fatalf("still watching: %s %v", raw, err)
	}
	gh.mu.Lock()
	views := gh.views
	gh.mu.Unlock()
	time.Sleep(150 * time.Millisecond)
	gh.mu.Lock()
	defer gh.mu.Unlock()
	if gh.views != views {
		t.Errorf("still polling a merged PR: %d views, then %d", views, gh.views)
	}
}
