package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

// A run that hits its usage limit keeps its partial work on a local handoff
// branch with a note, opens no pull request, and the next run continues from
// that branch, its diff measured against the original base.
func TestRunHandoffOnQuota(t *testing.T) {
	env := startPRRunner(t, nil, nil)
	env.hub.hello()
	env.hub.offer(testJob("run-q", "edit-quota: add hello"))
	finish := env.hub.wait("run-q", runnerwire.TypeFinish)
	result := finish.Result
	if result.Status != runnerwire.RunFailed || result.FailureKind != runnerwire.FailureQuota {
		t.Fatalf("result = %+v", result)
	}
	if result.HandoffBranch != "nodevas/handoff/run-q" {
		t.Fatalf("handoff branch = %q", result.HandoffBranch)
	}
	reset, err := time.Parse(time.RFC3339, result.ResetAt)
	if err != nil || reset.Before(time.Now().Add(time.Hour)) || reset.After(time.Now().Add(3*time.Hour)) {
		t.Errorf("reset at %q (%v)", result.ResetAt, err)
	}
	note := result.HandoffNote
	for _, want := range []string{
		"Run run-q (claude/sonnet) stopped before finishing: quota",
		"## What it was asked\nedit-quota: add hello",
		"## Its last messages\nI added hello.txt; next I wire it into the README.",
		"## Files changed against the base", "hello.txt", "README",
		"## Its last command\n$ env | grep TOKEN",
		"- [in_progress] Wire hello into the README", "- [pending] Write the tests",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
	for _, secret := range []string{"supersecretvalue", "ghp_abcdefghijklmnopqrstuvwxyz0123", "Add hello.txt"} {
		if strings.Contains(note, secret) {
			t.Errorf("note carries %q:\n%s", secret, note)
		}
	}
	if len(note) > runnerwire.MaxHandoffNoteBytes {
		t.Errorf("note is %d bytes", len(note))
	}
	for _, msg := range env.hub.messages("run-q") {
		if msg.Type == runnerwire.TypePR {
			t.Fatalf("a handed-off run reported a pull request: %+v", msg.PR)
		}
		if msg.Type == runnerwire.TypeChanges && (msg.Changes == nil || msg.Changes.Commit == "" || len(msg.Changes.Files) != 3) {
			t.Fatalf("changes = %+v", msg.Changes)
		}
	}
	// The branch is local, at the partial commit; the run's own branch and
	// worktree are gone, and nothing went anywhere.
	head := gitT(t, env.repo, "rev-parse", "refs/heads/nodevas/handoff/run-q")
	if got := gitT(t, env.repo, "branch", "--list", "agent/*"); got != "" {
		t.Errorf("run branch left behind: %q", got)
	}
	if _, err := os.Stat(filepath.Join(env.client.Config.DataDir, "worktrees", "run-q")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("worktree left behind: %v", err)
	}
	if gitT(t, env.repo, "remote") != "" {
		t.Fatal("test repository has a remote")
	}

	// The next run continues from it. The hub's copy of the note is not what
	// the agent reads: the runner's own is.
	next := testJob("run-next", "continue: finish it")
	next.HandoffBranch, next.HandoffNote = result.HandoffBranch, "a note the hub made up"
	env.hub.offer(next)
	done := env.hub.wait("run-next", runnerwire.TypeFinish).Result
	if done.Status != runnerwire.RunSucceeded {
		t.Fatalf("continuing run = %+v", done)
	}
	stdin := env.recorded(t, "run-next", "stdin")
	if !strings.Contains(stdin, "You are continuing another agent's work") ||
		!strings.Contains(stdin, "Wire hello into the README") || strings.Contains(stdin, "a note the hub made up") {
		t.Errorf("prompt:\n%s", stdin)
	}
	if log := env.recorded(t, "run-next", "log"); !strings.HasPrefix(log, "Fix it\n") || !strings.Contains(log, "first") {
		t.Errorf("the worktree did not start from the handoff: %q", log)
	}
	accept := env.hub.wait("run-next", runnerwire.TypeAccept).Accept
	if accept.Base != "main" || accept.Branch != "agent/n-1-run-next" {
		t.Errorf("accept = %+v", accept)
	}
	var changes *runnerwire.RunChanges
	for _, msg := range env.hub.messages("run-next") {
		if msg.Type == runnerwire.TypeChanges {
			changes = msg.Changes
		}
	}
	// The whole task's diff against main: the earlier run's files and its own.
	if changes == nil || len(changes.Files) != 4 {
		t.Fatalf("continuing run's changes = %+v", changes)
	}
	if got := gitT(t, env.repo, "rev-parse", "refs/heads/nodevas/handoff/run-q"); got != head {
		t.Errorf("the handoff branch moved: %s → %s", head, got)
	}
}

// A run that stops at its limit having changed nothing leaves no branch.
func TestRunHandoffNoChanges(t *testing.T) {
	env := startPRRunner(t, nil, nil)
	env.hub.hello()
	env.hub.offer(testJob("run-q0", "quota:"))
	result := env.hub.wait("run-q0", runnerwire.TypeFinish).Result
	if result.Status != runnerwire.RunFailed || result.FailureKind != runnerwire.FailureQuota ||
		result.HandoffBranch != "" || result.HandoffNote != "" || result.ResetAt == "" {
		t.Fatalf("result = %+v", result)
	}
	if got := gitT(t, env.repo, "branch", "--list", "nodevas/*", "agent/*"); got != "" {
		t.Errorf("branches: %q", got)
	}
}

// A job may only name a handoff branch this runner made for that
// repository, still where it left it: anything else is declined before
// anyone is asked.
func TestRunHandoffRejectsForeignBranches(t *testing.T) {
	asked := 0
	env := startPRRunner(t, nil, func(c *Client) {
		c.Accepter = acceptFunc(func(context.Context, Offer) (bool, string) { asked++; return true, "" })
	})
	env.hub.hello()
	gitT(t, env.repo, "branch", "nodevas/handoff/forged", "main")
	gitT(t, env.repo, "branch", "nodevas/handoff/moved", "main")
	dataDir := env.client.Config.DataDir
	if err := registerHandoff(context.Background(), dataDir, handoffEntry{
		Branch: "nodevas/handoff/moved", RepoID: "r1", RepoPath: env.repo, Commit: strings.Repeat("0", 40),
		RunID: "moved", CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	for id, branch := range map[string]string{
		"run-main":   "main",                        // not a handoff branch name
		"run-dots":   "nodevas/handoff/../main",     // not one either
		"run-forged": "nodevas/handoff/forged",      // exists, but not this runner's
		"run-moved":  "nodevas/handoff/moved",       // this runner's, but moved
		"run-gone":   "nodevas/handoff/nonexistent", // nowhere
	} {
		job := testJob(id, "ok:")
		job.HandoffBranch = branch
		env.hub.offer(job)
		decline := env.hub.wait(id, runnerwire.TypeDecline)
		if !strings.Contains(decline.Reason, "handoff branch") {
			t.Errorf("%s: decline reason %q", id, decline.Reason)
		}
	}
	// Only implement runs continue a handoff.
	review := testJob("run-review", "ok:")
	review.Kind, review.HandoffBranch = runnerwire.JobPlan, "nodevas/handoff/forged"
	env.hub.offer(review)
	if reason := env.hub.wait("run-review", runnerwire.TypeDecline).Reason; !strings.Contains(reason, "handoff") {
		t.Errorf("plan with a handoff: %q", reason)
	}
	if asked != 0 {
		t.Errorf("asked %d times about jobs with a foreign handoff branch", asked)
	}
}

func TestHandoffRecordPrunes(t *testing.T) {
	repo := newTestRepo(t)
	data := t.TempDir()
	ctx := context.Background()
	head := gitT(t, repo, "rev-parse", "HEAD")
	gitT(t, repo, "branch", "nodevas/handoff/old", head)
	old := time.Now().Add(-handoffKeep - time.Hour).UTC().Format(time.RFC3339)
	if err := saveHandoffs(data, []handoffEntry{{Branch: "nodevas/handoff/old", RepoID: "r1", RepoPath: repo,
		Commit: head, RunID: "old", CreatedAt: old}}); err != nil {
		t.Fatal(err)
	}
	if err := registerHandoff(ctx, data, handoffEntry{Branch: "nodevas/handoff/new", RepoID: "r1", RepoPath: repo,
		Commit: head, RunID: "new", CreatedAt: time.Now().UTC().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	entries, err := loadHandoffs(data)
	if err != nil || len(entries) != 1 || entries[0].RunID != "new" {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	if got := gitT(t, repo, "branch", "--list", "nodevas/handoff/old"); got != "" {
		t.Errorf("expired handoff branch kept: %q", got)
	}
	if info, err := os.Stat(handoffPath(data)); err != nil || (info.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/') {
		t.Errorf("record file: %v %v", info, err)
	}
}

func TestPlanTextAndShellCommand(t *testing.T) {
	codex := planText(`{"plan":[{"step":"Read the code","status":"completed"},{"step":"Fix the bug","status":"in_progress"}]}`)
	if codex != "- [in_progress] Fix the bug\n" {
		t.Errorf("codex plan = %q", codex)
	}
	gemini := planText(`{"todos":[{"description":"Write docs","status":"pending"},{"description":"x","status":"cancelled"}]}`)
	if gemini != "- [pending] Write docs\n" {
		t.Errorf("gemini plan = %q", gemini)
	}
	if planText("not json") != "" {
		t.Error("plan from junk")
	}
	if got := shellCommand(`{"command":["bash","-lc","go test ./..."]}`); got != "bash -lc go test ./..." {
		t.Errorf("codex command = %q", got)
	}
	if got := shellCommand("npm test"); got != "npm test" {
		t.Errorf("plain command = %q", got)
	}
	if got := redactNote("export OPENAI_API_KEY=abc123 and DB_PASSWORD: 'x y' but PATH=/usr/bin"); got !=
		"export OPENAI_API_KEY=[redacted] and DB_PASSWORD: [redacted] but PATH=/usr/bin" {
		t.Errorf("redactNote = %q", got)
	}
}
