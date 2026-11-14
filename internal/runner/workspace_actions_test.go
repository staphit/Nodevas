package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

func (e *wsEnv) mergeJob(id string) runnerwire.WorkspaceActionJob {
	return runnerwire.WorkspaceActionJob{ID: id, RunID: wsRun, AccountID: wsOwner, Action: runnerwire.WorkspaceMergeLocal,
		RepoID: "r1", NodeID: wsNode, Branch: e.wt.Branch, Base: "main", HeadSHA: e.tip}
}

func (e *wsEnv) discardJob(id string) runnerwire.WorkspaceActionJob {
	return runnerwire.WorkspaceActionJob{ID: id, RunID: wsRun, AccountID: wsOwner, Action: runnerwire.WorkspaceDiscard,
		RepoID: "r1", NodeID: wsNode, Branch: e.wt.Branch, Base: "main", HeadSHA: e.tip}
}

// act answers one request and returns its outcome, as the hub would read it.
func (e *wsEnv) act(t *testing.T, job runnerwire.WorkspaceActionJob) runnerwire.WorkspaceOutcome {
	t.Helper()
	return actOn(t, e.j, job)
}

func actOn(t *testing.T, j *jobs, job runnerwire.WorkspaceActionJob) runnerwire.WorkspaceOutcome {
	t.Helper()
	j.runWorkspaceAction(wsOwner, job)
	return workspaceResultOf(t, drain(t, j), job.ID)
}

func workspaceResultOf(t *testing.T, msgs []runnerwire.Envelope, id string) runnerwire.WorkspaceOutcome {
	t.Helper()
	for _, msg := range msgs {
		if msg.Type == runnerwire.TypeWorkspaceResult && msg.WorkspaceResult != nil && msg.WorkspaceResult.ID == id {
			if msg.RunID != msg.WorkspaceResult.RunID {
				t.Fatalf("result for the wrong run: %+v", msg)
			}
			return *msg.WorkspaceResult
		}
	}
	t.Fatalf("no workspace_result for %s in %+v", id, msgs)
	return runnerwire.WorkspaceOutcome{}
}

func (e *wsEnv) branchExists(t *testing.T) bool {
	t.Helper()
	return gitT(t, e.repo, "branch", "--list", e.wt.Branch) != ""
}

// worktreeListed reports whether git lists a worktree whose path ends in
// suffix.
func worktreeListed(t *testing.T, repo, suffix string) bool {
	t.Helper()
	for _, line := range strings.Split(gitT(t, repo, "worktree", "list", "--porcelain"), "\n") {
		if strings.HasPrefix(line, "worktree ") && strings.Contains(filepath.ToSlash(line), suffix) {
			return true
		}
	}
	return false
}

func parents(t *testing.T, repo, ref string) []string {
	t.Helper()
	return strings.Fields(gitT(t, repo, "rev-list", "--parents", "-n", "1", ref))[1:]
}

func TestMergeLocalInCleanCheckout(t *testing.T) {
	env := newWSEnv(t, nil)
	before := gitT(t, env.repo, "rev-parse", "main")
	out := env.act(t, env.mergeJob("w-1"))
	if out.Code != "" || out.Note != "" || !out.WorktreeRemoved || !out.BranchDeleted || out.Action != runnerwire.WorkspaceMergeLocal {
		t.Fatalf("outcome = %+v", out)
	}
	main := gitT(t, env.repo, "rev-parse", "main")
	if out.MergeCommit != main {
		t.Fatalf("merge commit %s, main at %s", out.MergeCommit, main)
	}
	if got := parents(t, env.repo, "main"); len(got) != 2 || got[0] != before || got[1] != env.tip {
		t.Fatalf("parents %v, want %s %s", got, before, env.tip)
	}
	msg := gitT(t, env.repo, "log", "-1", "--format=%B", "main")
	for _, want := range []string{"Merge branch '" + env.wt.Branch + "' into main", "Nodevas-Node: " + wsNode, "Nodevas-Run: " + wsRun} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
	// The repo's checkout moved with its branch: the merged files are there.
	if raw, err := os.ReadFile(filepath.Join(env.repo, "feature.txt")); err != nil || string(raw) != "feature\n" {
		t.Fatalf("checkout after the merge: %q %v", raw, err)
	}
	if gitT(t, env.repo, "status", "--porcelain") != "" {
		t.Fatal("the checkout is not clean after the merge")
	}
	if worktreeExists(env.wt.Path) || env.branchExists(t) {
		t.Fatal("the run's worktree or branch is still there")
	}
}

func TestMergeLocalInTempWorktree(t *testing.T) {
	env := newWSEnv(t, nil)
	gitT(t, env.repo, "checkout", "-q", "-b", "other")
	head := gitT(t, env.repo, "rev-parse", "HEAD")
	out := env.act(t, env.mergeJob("w-1"))
	if out.Code != "" || out.MergeCommit == "" {
		t.Fatalf("outcome = %+v", out)
	}
	if got := parents(t, env.repo, "main"); len(got) != 2 || got[1] != env.tip {
		t.Fatalf("main's parents %v", got)
	}
	if gitT(t, env.repo, "rev-parse", "HEAD") != head || gitT(t, env.repo, "branch", "--show-current") != "other" {
		t.Fatal("the repo's own checkout moved")
	}
	if worktreeListed(t, env.repo, "/merge-tmp/") || worktreeListed(t, env.repo, "/worktrees/"+wsRun) {
		t.Fatalf("worktrees left: %s", gitT(t, env.repo, "worktree", "list"))
	}
	if entries, _ := os.ReadDir(filepath.Join(env.dataDir, "merge-tmp")); len(entries) != 0 {
		t.Fatalf("temporary worktree left on disk: %v", entries)
	}
}

func TestMergeLocalRefusesDirtyCheckout(t *testing.T) {
	env := newWSEnv(t, nil)
	main := gitT(t, env.repo, "rev-parse", "main")
	writeFile(t, env.repo, "README", []byte("my own edit\n"))
	out := env.act(t, env.mergeJob("w-1"))
	if out.Code != runnerwire.WorkspaceErrDirtyBase || !strings.Contains(out.Error, "README") {
		t.Fatalf("outcome = %+v", out)
	}
	if raw, _ := os.ReadFile(filepath.Join(env.repo, "README")); string(raw) != "my own edit\n" {
		t.Fatalf("the person's edit changed: %q", raw)
	}
	if gitT(t, env.repo, "rev-parse", "main") != main || !worktreeExists(env.wt.Path) || !env.branchExists(t) {
		t.Fatal("something moved after a refusal")
	}

	// A merge in progress there: refused too.
	gitT(t, env.repo, "checkout", "-q", "--", "README")
	if err := os.WriteFile(filepath.Join(env.repo, ".git", "MERGE_HEAD"), []byte(main+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := env.act(t, env.mergeJob("w-2")); out.Code != runnerwire.WorkspaceErrDirtyBase || !strings.Contains(out.Error, "merge") {
		t.Fatalf("merge in progress = %+v", out)
	}
	if gitT(t, env.repo, "rev-parse", "main") != main {
		t.Fatal("main moved")
	}
}

func TestMergeLocalRefusesDirtyRunWorktree(t *testing.T) {
	env := newWSEnv(t, nil)
	main := gitT(t, env.repo, "rev-parse", "main")
	writeFile(t, env.wt.Path, ".claude/settings.json", []byte("{}\n"))
	out := env.act(t, env.mergeJob("w-1"))
	if out.Code != runnerwire.WorkspaceErrDirtyWorktree || !strings.Contains(out.Error, ".claude") {
		t.Fatalf("outcome = %+v", out)
	}
	if gitT(t, env.repo, "rev-parse", "main") != main || !worktreeExists(filepath.Join(env.wt.Path, ".claude", "settings.json")) {
		t.Fatal("something changed after a refusal")
	}
}

func TestMergeLocalConflictAborts(t *testing.T) {
	for _, temp := range []bool{false, true} {
		name := "in the repo"
		if temp {
			name = "in a temporary worktree"
		}
		t.Run(name, func(t *testing.T) {
			env := newWSEnv(t, nil)
			writeFile(t, env.repo, "README", []byte("main's own change\n"))
			gitT(t, env.repo, "commit", "-q", "-am", "main moves on")
			if temp {
				gitT(t, env.repo, "checkout", "-q", "-b", "other")
			}
			main := gitT(t, env.repo, "rev-parse", "main")
			head := gitT(t, env.repo, "rev-parse", "HEAD")
			out := env.act(t, env.mergeJob("w-1"))
			if out.Code != runnerwire.WorkspaceErrConflict || !strings.Contains(out.Error, "README") {
				t.Fatalf("outcome = %+v", out)
			}
			if gitT(t, env.repo, "rev-parse", "main") != main || gitT(t, env.repo, "rev-parse", "HEAD") != head {
				t.Fatal("a ref moved after the conflict")
			}
			if gitT(t, env.repo, "status", "--porcelain") != "" {
				t.Fatalf("the checkout is not as it was: %s", gitT(t, env.repo, "status", "--porcelain"))
			}
			if _, err := os.Stat(filepath.Join(env.repo, ".git", "MERGE_HEAD")); err == nil {
				t.Fatal("MERGE_HEAD left behind")
			}
			if raw, _ := os.ReadFile(filepath.Join(env.repo, "README")); string(raw) != "main's own change\n" {
				t.Fatalf("README = %q", raw)
			}
			if worktreeListed(t, env.repo, "/merge-tmp/") {
				t.Fatal("temporary worktree left")
			}
			if !worktreeExists(env.wt.Path) || !env.branchExists(t) {
				t.Fatal("the run's worktree or branch went after a conflict")
			}
		})
	}
}

func TestMergeLocalBaseElsewhere(t *testing.T) {
	env := newWSEnv(t, nil)
	gitT(t, env.repo, "checkout", "-q", "-b", "other")
	elsewhere := filepath.Join(t.TempDir(), "main-wt")
	gitT(t, env.repo, "worktree", "add", "-q", elsewhere, "main")
	main := gitT(t, env.repo, "rev-parse", "main")
	out := env.act(t, env.mergeJob("w-1"))
	if out.Code != runnerwire.WorkspaceErrBaseElsewhere || !strings.Contains(out.Error, "main-wt") {
		t.Fatalf("outcome = %+v", out)
	}
	if gitT(t, env.repo, "rev-parse", "main") != main {
		t.Fatal("main moved")
	}
}

func TestMergeLocalHeadMoved(t *testing.T) {
	env := newWSEnv(t, nil)
	job := env.mergeJob("w-1")
	job.HeadSHA = strings.Repeat("0", 40)
	if out := env.act(t, job); out.Code != runnerwire.WorkspaceErrHeadMoved {
		t.Fatalf("stale head = %+v", out)
	}
	// The worktree moved on past the branch the owner saw.
	writeFile(t, env.wt.Path, "more.txt", []byte("more\n"))
	gitT(t, env.wt.Path, "add", "-A")
	gitT(t, env.wt.Path, "commit", "-q", "-m", "more")
	if out := env.act(t, env.mergeJob("w-2")); out.Code != runnerwire.WorkspaceErrHeadMoved {
		t.Fatalf("branch moved = %+v", out)
	}
}

func TestMergeLocalHasPR(t *testing.T) {
	env := newWSEnv(t, nil)
	env.j.watch(watchedPR{RunID: wsRun, NodeID: wsNode, URL: mergePRURL, Worktree: env.wt.Path, Repo: env.repo, Branch: env.wt.Branch,
		PR: runnerwire.PRInfo{URL: mergePRURL, Number: 7, State: runnerwire.PROpen}})
	if out := env.act(t, env.mergeJob("w-1")); out.Code != runnerwire.WorkspaceErrHasPR || !strings.Contains(out.Error, mergePRURL) {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestMergeLocalNotOwnBranch(t *testing.T) {
	env := newWSEnv(t, nil)
	for name, edit := range map[string]func(*runnerwire.WorkspaceActionJob){
		"another branch": func(j *runnerwire.WorkspaceActionJob) { j.Branch = "main" },
		"another node":   func(j *runnerwire.WorkspaceActionJob) { j.NodeID = "n-2" },
		"a revise run":   func(j *runnerwire.WorkspaceActionJob) { j.Kind = runnerwire.JobRevise },
	} {
		job := env.mergeJob("w-" + name)
		edit(&job)
		if out := env.act(t, job); out.Code != runnerwire.WorkspaceErrNotOwnBranch {
			t.Errorf("%s = %+v", name, out)
		}
	}
}

func TestMergeLocalNoBase(t *testing.T) {
	env := newWSEnv(t, nil)
	for _, base := range []string{"trunk", "", "-x", "a..b"} {
		job := env.mergeJob("w-" + base)
		job.Base = base
		if out := env.act(t, job); out.Code != runnerwire.WorkspaceErrNoBase {
			t.Errorf("base %q = %+v", base, out)
		}
	}
}

func TestMergeLocalMergeDriver(t *testing.T) {
	env := newWSEnv(t, nil)
	gitT(t, env.repo, "config", "merge.custom.driver", "sh evil.sh %O %A %B")
	main := gitT(t, env.repo, "rev-parse", "main")
	if out := env.act(t, env.mergeJob("w-1")); out.Code != runnerwire.WorkspaceErrMergeDriver {
		t.Fatalf("outcome = %+v", out)
	}
	if gitT(t, env.repo, "rev-parse", "main") != main {
		t.Fatal("main moved")
	}
}

// A branch already in its base (merged by hand, or a merge whose cleanup
// failed) is done without a second merge commit.
func TestMergeLocalAlreadyMerged(t *testing.T) {
	env := newWSEnv(t, nil)
	gitT(t, env.repo, "merge", "-q", "--no-ff", "--no-edit", env.wt.Branch)
	main := gitT(t, env.repo, "rev-parse", "main")
	out := env.act(t, env.mergeJob("w-1"))
	if out.Code != "" || out.MergeCommit != main || !out.BranchDeleted {
		t.Fatalf("outcome = %+v", out)
	}
	if gitT(t, env.repo, "rev-parse", "main") != main {
		t.Fatal("merged a second time")
	}
}

func TestDiscardRemovesWorktreeBranchAndKept(t *testing.T) {
	env := newWSEnv(t, nil)
	writeFile(t, env.wt.Path, "wip.txt", []byte("never committed\n"))
	// A link the agent left in its worktree: removing the worktree removes
	// the link, never what it points at.
	outside := t.TempDir()
	writeFile(t, outside, "keep.txt", []byte("not the run's\n"))
	link(t, outside, filepath.Join(env.wt.Path, "escape"))
	if err := RecordKept(context.Background(), env.dataDir, env.repo, env.wt, []string{"wip.txt"}); err != nil {
		t.Fatal(err)
	}
	main := gitT(t, env.repo, "rev-parse", "main")
	out := env.act(t, env.discardJob("d-1"))
	if out.Code != "" || !out.WorktreeRemoved || !out.BranchDeleted || out.PRClosed || out.Note != "" {
		t.Fatalf("outcome = %+v", out)
	}
	if worktreeExists(env.wt.Path) || env.branchExists(t) || worktreeListed(t, env.repo, "/worktrees/"+wsRun) {
		t.Fatal("worktree or branch left")
	}
	kept, err := KeptWorktrees(env.dataDir)
	if err != nil || len(kept) != 0 {
		t.Fatalf("kept = %+v %v", kept, err)
	}
	raw, _ := os.ReadFile(keptPath(env.dataDir))
	if strings.Contains(string(raw), wsRun) {
		t.Fatalf("%s still names the run: %s", keptFile, raw)
	}
	if gitT(t, env.repo, "rev-parse", "main") != main {
		t.Fatal("main moved")
	}
	if _, err := os.Stat(filepath.Join(outside, "keep.txt")); err != nil {
		t.Fatalf("removing the worktree went through the link: %v", err)
	}
}

func TestDiscardIdempotent(t *testing.T) {
	env := newWSEnv(t, nil)
	if out := env.act(t, env.discardJob("d-1")); out.Code != "" || !out.WorktreeRemoved {
		t.Fatalf("first = %+v", out)
	}
	again := env.act(t, env.discardJob("d-2"))
	if again.Code != "" || again.WorktreeRemoved || again.BranchDeleted || again.Note != "nothing left to remove" {
		t.Fatalf("second = %+v", again)
	}

	// The worktree removed by hand beforehand: the branch still goes.
	env2 := newWSEnv(t, nil)
	if err := os.RemoveAll(env2.wt.Path); err != nil {
		t.Fatal(err)
	}
	out := env2.act(t, env2.discardJob("d-1"))
	if out.Code != "" || out.WorktreeRemoved || !out.BranchDeleted {
		t.Fatalf("worktree already gone = %+v", out)
	}
	if worktreeListed(t, env2.repo, "/worktrees/"+wsRun) {
		t.Fatal("git still lists the worktree")
	}
}

func TestDiscardClosesPR(t *testing.T) {
	env := newWSEnv(t, nil)
	watch := func() {
		env.j.watch(watchedPR{RunID: wsRun, NodeID: wsNode, URL: mergePRURL, Worktree: env.wt.Path, Repo: env.repo,
			Branch: env.wt.Branch, PR: runnerwire.PRInfo{URL: mergePRURL, Number: 7, State: runnerwire.PROpen, HeadSHA: env.tip}})
	}
	watch()
	// gh fails: nothing local is touched.
	env.gh.fail = errors.New("HTTP 502")
	out := env.act(t, env.discardJob("d-1"))
	if out.Code != runnerwire.WorkspaceErrPRClose || !worktreeExists(env.wt.Path) || !env.branchExists(t) ||
		len(env.j.watchedRuns()) != 1 {
		t.Fatalf("gh failing = %+v", out)
	}
	env.gh.fail = nil
	env.j.runWorkspaceAction(wsOwner, env.discardJob("d-2"))
	msgs := drain(t, env.j)
	out = workspaceResultOf(t, msgs, "d-2")
	if out.Code != "" || !out.PRClosed || !out.WorktreeRemoved || !out.BranchDeleted {
		t.Fatalf("outcome = %+v", out)
	}
	calls, dirs := env.gh.recorded()
	want := []string{"pr", "close", mergePRURL, "--delete-branch"}
	if len(calls) != 2 || !slices.Equal(calls[1], want) || dirs[1] != env.dataDir {
		t.Fatalf("gh calls %v in %v, want %v in the data dir", calls, dirs, want)
	}
	if len(env.j.watchedRuns()) != 0 {
		t.Fatal("still watching the closed PR")
	}
	var closed *runnerwire.PRInfo
	for i, msg := range msgs {
		if msg.Type == runnerwire.TypePR && msg.RunID == wsRun {
			closed = msgs[i].PR
		}
	}
	if closed == nil || closed.State != runnerwire.PRClosed {
		t.Fatalf("no closed PR queued: %+v", msgs)
	}
	if worktreeExists(env.wt.Path) || env.branchExists(t) {
		t.Fatal("worktree or branch left")
	}
}

// A revise run's entry watches the implement run's PR: discarding the
// revise run never closes it, nor deletes its branch.
func TestDiscardReviseKeepsPRBranch(t *testing.T) {
	env := newWSEnv(t, nil)
	env.j.watch(watchedPR{RunID: wsRun, NodeID: wsNode, URL: mergePRURL, Repo: env.repo, Branch: env.wt.Branch,
		PR: runnerwire.PRInfo{URL: mergePRURL, Number: 7, State: runnerwire.PROpen}})
	job := env.discardJob("d-1")
	job.Kind = runnerwire.JobRevise
	out := env.act(t, job)
	if out.Code != "" || out.PRClosed || out.BranchDeleted || !out.WorktreeRemoved {
		t.Fatalf("outcome = %+v", out)
	}
	if calls, _ := env.gh.recorded(); len(calls) != 0 {
		t.Fatalf("gh called: %v", calls)
	}
	if !env.branchExists(t) || worktreeExists(env.wt.Path) || len(env.j.watchedRuns()) != 0 {
		t.Fatal("branch gone, worktree left, or still watched")
	}
}

func TestDiscardRefusesRunning(t *testing.T) {
	env := newWSEnv(t, nil)
	env.j.mu.Lock()
	env.j.runs[wsRun] = &run{}
	env.j.mu.Unlock()
	for _, job := range []runnerwire.WorkspaceActionJob{env.discardJob("d-1"), env.mergeJob("w-1")} {
		if out := env.act(t, job); out.Code != runnerwire.WorkspaceErrRunning {
			t.Errorf("%s while running = %+v", job.Action, out)
		}
	}
	if !worktreeExists(env.wt.Path) || !env.branchExists(t) {
		t.Fatal("something removed while running")
	}
}

func TestWorkspaceActionOwnerOnly(t *testing.T) {
	env := newWSEnv(t, nil)
	for _, who := range []string{"", "u-else"} {
		job := env.discardJob("d-" + who)
		job.AccountID = who
		if out := env.act(t, job); out.Code != runnerwire.WorkspaceErrRefused {
			t.Errorf("account %q = %+v", who, out)
		}
	}
	// No owner known on this connection: refused whoever is named.
	env.j.runWorkspaceAction("", env.discardJob("d-x"))
	if out := workspaceResultOf(t, drain(t, env.j), "d-x"); out.Code != runnerwire.WorkspaceErrRefused {
		t.Errorf("no owner = %+v", out)
	}
	for name, edit := range map[string]func(*runnerwire.WorkspaceActionJob){
		"unknown action": func(j *runnerwire.WorkspaceActionJob) { j.Action = "push" },
		"unknown repo":   func(j *runnerwire.WorkspaceActionJob) { j.RepoID = "nope" },
		"bad run id":     func(j *runnerwire.WorkspaceActionJob) { j.RunID = "../x" },
	} {
		job := env.discardJob("d-" + name)
		edit(&job)
		if out := env.act(t, job); out.Code != runnerwire.WorkspaceErrRefused {
			t.Errorf("%s = %+v", name, out)
		}
	}
	if !worktreeExists(env.wt.Path) || !env.branchExists(t) {
		t.Fatal("a refused request removed something")
	}
}

func TestWorkspaceActionsOff(t *testing.T) {
	off := false
	env := newWSEnv(t, func(c *Config) { c.WorkspaceActions = &off })
	for _, job := range []runnerwire.WorkspaceActionJob{env.discardJob("d-1"), env.mergeJob("w-1")} {
		if out := env.act(t, job); out.Code != runnerwire.WorkspaceErrOff {
			t.Errorf("%s with actions off = %+v", job.Action, out)
		}
	}
	if !worktreeExists(env.wt.Path) {
		t.Fatal("removed with actions off")
	}
}

func TestWorkspaceActionDedupe(t *testing.T) {
	env := newWSEnv(t, nil)
	// Refused, then sent again after the reason went away: the same answer.
	writeFile(t, env.wt.Path, "wip.txt", []byte("x\n"))
	first := env.act(t, env.mergeJob("w-1"))
	if first.Code != runnerwire.WorkspaceErrDirtyWorktree {
		t.Fatalf("first = %+v", first)
	}
	if err := os.Remove(filepath.Join(env.wt.Path, "wip.txt")); err != nil {
		t.Fatal(err)
	}
	if again := env.act(t, env.mergeJob("w-1")); again != first {
		t.Fatalf("resent = %+v, first %+v", again, first)
	}
	main := gitT(t, env.repo, "rev-parse", "main")
	if gitT(t, env.repo, "rev-parse", "main") != main {
		t.Fatal("a refused request merged when sent again")
	}

	// Sent twice while the first is in hand: one answer.
	env.j.mergeMu.Lock()
	job := env.discardJob("d-9")
	env.j.workspaceAction(wsOwner, &job)
	env.j.workspaceAction(wsOwner, &job)
	env.j.mergeMu.Unlock()
	deadline := time.Now().Add(10 * time.Second)
	for env.j.pendingWorkspaceResults() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	env.j.wg.Wait()
	if n := env.j.pendingWorkspaceResults(); n != 1 {
		t.Fatalf("%d answers", n)
	}
	if out := workspaceResultOf(t, drain(t, env.j), "d-9"); out.Code != "" || !out.WorktreeRemoved {
		t.Fatalf("d-9 = %+v", out)
	}
	if !strings.Contains(env.log.String(), "sent again while in hand") {
		t.Errorf("log:\n%s", env.log.String())
	}
}

func TestWorkspaceAnswersSurviveRestart(t *testing.T) {
	env := newWSEnv(t, nil)
	done := env.act(t, env.discardJob("d-1"))
	if done.Code != "" {
		t.Fatalf("discard = %+v", done)
	}
	raw, err := os.ReadFile(filepath.Join(env.dataDir, workspaceActionsFileName))
	if err != nil {
		t.Fatal(err)
	}
	var file workspaceActionsFile
	if err := json.Unmarshal(raw, &file); err != nil || len(file.Actions) != 1 {
		t.Fatalf("%s: %s %v", workspaceActionsFileName, raw, err)
	}
	file.Actions = append(file.Actions, workspaceRecord{ID: "d-old", RunID: wsRun, Action: runnerwire.WorkspaceDiscard,
		At: time.Now().Add(-25 * time.Hour)})
	if err := writeFileAtomic(filepath.Join(env.dataDir, workspaceActionsFileName), file); err != nil {
		t.Fatal(err)
	}
	restarted := env.start(t)
	if again := actOn(t, restarted, env.discardJob("d-1")); again != done {
		t.Fatalf("after restart = %+v, want %+v", again, done)
	}
	restarted.mu.Lock()
	_, old := restarted.workspaceRecords["d-old"]
	restarted.mu.Unlock()
	if old {
		t.Error("a day-old answer survived the load")
	}
}

func TestWorkspaceResultWaitsForConnection(t *testing.T) {
	off := false
	env := newWSEnv(t, func(c *Config) { c.WorkspaceActions = &off })
	env.j.runWorkspaceAction(wsOwner, env.discardJob("d-1"))
	broken := errors.New("socket closed")
	if err := env.j.flush(func(runnerwire.Envelope) error { return broken }); !errors.Is(err, broken) {
		t.Fatalf("flush %v", err)
	}
	if env.j.pendingWorkspaceResults() != 1 {
		t.Fatal("an unwritten answer was dropped")
	}
	if out := workspaceResultOf(t, drain(t, env.j), "d-1"); out.Code != runnerwire.WorkspaceErrOff {
		t.Fatalf("result %+v", out)
	}
	if env.j.pendingWorkspaceResults() != 0 {
		t.Fatal("a written answer is still queued")
	}
}

// Over the socket: the owner's discard and its answer; another account's
// is refused.
func TestWorkspaceActionOverTheSocket(t *testing.T) {
	hermeticGit(t)
	dataDir := t.TempDir()
	var wt Worktree
	env := startJobRunner(t, func(c *Client) {
		c.Config.DataDir = dataDir
		var err error
		wt, err = CreateWorktree(context.Background(), c.Config.Repos[0].Path, dataDir, wsRun, wsNode, "")
		if err != nil {
			t.Fatal(err)
		}
	})
	env.hub.hello()
	// Reconnect, welcomed as wsOwner's runner this time.
	env.hub.setOwner(wsOwner)
	env.hub.kick()
	env.hub.hello()
	job := runnerwire.WorkspaceActionJob{ID: "d-1", RunID: wsRun, AccountID: "u-else", Action: runnerwire.WorkspaceDiscard,
		RepoID: "r1", NodeID: wsNode, Branch: wt.Branch, Base: "main"}
	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeWorkspaceAction, RunID: wsRun, WorkspaceAction: &job})
	if res := env.hub.wait(wsRun, runnerwire.TypeWorkspaceResult).WorkspaceResult; res == nil || res.Code != runnerwire.WorkspaceErrRefused {
		t.Fatalf("another account's discard = %+v", res)
	}
	if !worktreeExists(wt.Path) {
		t.Fatal("removed for another account")
	}
	job.ID, job.AccountID = "d-2", wsOwner
	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeWorkspaceAction, RunID: wsRun, WorkspaceAction: &job})
	deadline := time.Now().Add(20 * time.Second)
	for {
		var got *runnerwire.WorkspaceOutcome
		for _, msg := range env.hub.messages(wsRun) {
			if msg.Type == runnerwire.TypeWorkspaceResult && msg.WorkspaceResult.ID == "d-2" {
				got = msg.WorkspaceResult
			}
		}
		if got != nil {
			if got.Code != "" || !got.WorktreeRemoved || !got.BranchDeleted {
				t.Fatalf("owner's discard = %+v", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no answer: %+v", env.hub.messages(wsRun))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if worktreeExists(wt.Path) {
		t.Fatal("worktree still there")
	}
}

// The owner switched their checkout to another branch after the checks: the
// merge refuses instead of landing there.
func TestMergeAtRefusesAnotherBranch(t *testing.T) {
	env := newWSEnv(t, nil)
	gitT(t, env.repo, "checkout", "-q", "-b", "feature")
	head := gitT(t, env.repo, "rev-parse", "HEAD")
	main := gitT(t, env.repo, "rev-parse", "main")
	code, why := mergeAt(context.Background(), env.repo, env.mergeJob("w-1"), env.tip, nil)
	if code != runnerwire.WorkspaceErrDirtyBase || !strings.Contains(why, "no longer on main") {
		t.Fatalf("merge on another branch = %s %s", code, why)
	}
	if gitT(t, env.repo, "rev-parse", "HEAD") != head || gitT(t, env.repo, "rev-parse", "main") != main {
		t.Fatal("a branch moved")
	}
}

// A signing program refuses a merge only when the repository's own config
// names it and the merge would sign or check signatures; the owner's global
// one is theirs.
func TestSigningProgramAt(t *testing.T) {
	env := newWSEnv(t, nil)
	ctx := context.Background()
	if code, why := signingProgramAt(ctx, env.repo); code != "" {
		t.Fatalf("no signing = %s %s", code, why)
	}
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[commit]\n\tgpgSign = true\n[gpg \"ssh\"]\n\tprogram = owner-sign\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	if code, why := signingProgramAt(ctx, env.repo); code != "" {
		t.Fatalf("the owner's own program = %s %s", code, why)
	}
	gitT(t, env.repo, "config", "gpg.ssh.defaultKeyCommand", "agent-key")
	if code, why := signingProgramAt(ctx, env.repo); code != runnerwire.WorkspaceErrSigning || !strings.Contains(why, "defaultkeycommand") {
		t.Fatalf("a key command of the repo's = %s %s", code, why)
	}
	// Not signing and not checking signatures: nothing to run.
	gitT(t, env.repo, "config", "commit.gpgSign", "false")
	if code, why := signingProgramAt(ctx, env.repo); code != "" {
		t.Fatalf("signing off = %s %s", code, why)
	}
	gitT(t, env.repo, "config", "merge.verifySignatures", "true")
	if code, _ := signingProgramAt(ctx, env.repo); code != runnerwire.WorkspaceErrSigning {
		t.Fatalf("checking signatures = %s", code)
	}
}

// A file the owner's checkout ignores (their own config) is never replaced
// by the branch's, nor deleted by an abort.
func TestMergeLocalKeepsIgnoredFiles(t *testing.T) {
	env := newWSEnv(t, nil)
	writeFile(t, env.repo, ".gitignore", []byte("local.yml\n"))
	gitT(t, env.repo, "add", ".gitignore")
	gitT(t, env.repo, "commit", "-q", "-m", "ignore local config")
	writeFile(t, env.repo, "local.yml", []byte("the owner's own\n"))
	writeFile(t, env.wt.Path, "local.yml", []byte("the agent's\n"))
	gitT(t, env.wt.Path, "add", "-f", "local.yml")
	gitT(t, env.wt.Path, "commit", "-q", "-m", "commit an ignored path")
	main := gitT(t, env.repo, "rev-parse", "main")
	job := env.mergeJob("w-1")
	job.HeadSHA = gitT(t, env.wt.Path, "rev-parse", "HEAD")
	out := env.act(t, job)
	if out.Code != runnerwire.WorkspaceErrDirtyBase || !strings.Contains(out.Error, "local.yml") {
		t.Fatalf("outcome = %+v", out)
	}
	if raw, _ := os.ReadFile(filepath.Join(env.repo, "local.yml")); string(raw) != "the owner's own\n" {
		t.Fatalf("the owner's ignored file = %q", raw)
	}
	if gitT(t, env.repo, "rev-parse", "main") != main || !worktreeExists(env.wt.Path) || !env.branchExists(t) {
		t.Fatal("something moved after a refusal")
	}
	// An ignored directory in the way, too.
	writeFile(t, env.repo, ".gitignore", []byte("local.yml\nfeature.txt/\n"))
	gitT(t, env.repo, "commit", "-q", "-am", "ignore a directory")
	if err := os.Remove(filepath.Join(env.repo, "local.yml")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, env.repo, "feature.txt/mine", []byte("the owner's\n"))
	job.ID = "w-2"
	if out := env.act(t, job); out.Code != runnerwire.WorkspaceErrDirtyBase || !strings.Contains(out.Error, "feature.txt") {
		t.Fatalf("ignored directory = %+v", out)
	}

	// An untracked (not ignored) file: git refuses, said as dirty_base.
	env2 := newWSEnv(t, nil)
	writeFile(t, env2.repo, "feature.txt", []byte("the owner's draft\n"))
	if out := env2.act(t, env2.mergeJob("w-1")); out.Code != runnerwire.WorkspaceErrDirtyBase || !strings.Contains(out.Error, "feature.txt") {
		t.Fatalf("untracked = %+v", out)
	}
	if raw, _ := os.ReadFile(filepath.Join(env2.repo, "feature.txt")); string(raw) != "the owner's draft\n" {
		t.Fatalf("the owner's untracked file = %q", raw)
	}
}

// Commits the owner added on top of the run's, kept on no other branch, stop
// a discard before anything goes; kept elsewhere, the discard goes ahead.
func TestDiscardKeepsCommitsAddedAfterTheRun(t *testing.T) {
	env := newWSEnv(t, nil)
	writeFile(t, env.wt.Path, "fix.txt", []byte("the owner's fix\n"))
	gitT(t, env.wt.Path, "add", "fix.txt")
	gitT(t, env.wt.Path, "commit", "-q", "-m", "the owner's fix")
	for _, head := range []string{env.tip, ""} {
		job := env.discardJob("d-" + head)
		job.HeadSHA = head
		out := env.act(t, job)
		if out.Code != runnerwire.WorkspaceErrUnsaved || !strings.Contains(out.Error, "nothing was removed") {
			t.Fatalf("head %q = %+v", head, out)
		}
	}
	if !worktreeExists(env.wt.Path) || !env.branchExists(t) {
		t.Fatal("removed with the owner's commits on it")
	}
	gitT(t, env.repo, "branch", "keep-fix", env.wt.Branch)
	out := env.act(t, env.discardJob("d-2"))
	if out.Code != "" || !out.BranchDeleted || !out.WorktreeRemoved {
		t.Fatalf("kept elsewhere = %+v", out)
	}
	if gitT(t, env.repo, "branch", "--list", "keep-fix") == "" {
		t.Fatal("the owner's branch went")
	}

	// A commit made on a detached HEAD in the run's worktree counts too.
	env2 := newWSEnv(t, nil)
	gitT(t, env2.wt.Path, "checkout", "-q", "--detach")
	writeFile(t, env2.wt.Path, "fix.txt", []byte("detached\n"))
	gitT(t, env2.wt.Path, "add", "fix.txt")
	gitT(t, env2.wt.Path, "commit", "-q", "-m", "detached fix")
	if out := env2.act(t, env2.discardJob("d-1")); out.Code != runnerwire.WorkspaceErrUnsaved || !strings.Contains(out.Error, "worktree") {
		t.Fatalf("detached = %+v", out)
	}
	if !worktreeExists(env2.wt.Path) || !env2.branchExists(t) {
		t.Fatal("removed with a detached commit in it")
	}
}

// Another run the runner still holds on the same branch or PR (a revise or
// review run) keeps both actions away.
func TestWorkspaceActionRefusesSharedBranch(t *testing.T) {
	env := newWSEnv(t, nil)
	env.j.mu.Lock()
	env.j.runs["run-revise"] = &run{job: runnerwire.Job{RunID: "run-revise", Branch: env.wt.Branch}}
	env.j.mu.Unlock()
	for _, job := range []runnerwire.WorkspaceActionJob{env.discardJob("d-1"), env.mergeJob("w-1")} {
		if out := env.act(t, job); out.Code != runnerwire.WorkspaceErrRunning || !strings.Contains(out.Error, "run-revise") {
			t.Errorf("%s = %+v", job.Action, out)
		}
	}
	env.j.mu.Lock()
	delete(env.j.runs, "run-revise")
	env.j.runs["run-review"] = &run{job: runnerwire.Job{RunID: "run-review", PRURL: mergePRURL}}
	env.j.mu.Unlock()
	env.j.watch(watchedPR{RunID: wsRun, NodeID: wsNode, URL: mergePRURL, Repo: env.repo, Branch: env.wt.Branch,
		PR: runnerwire.PRInfo{URL: mergePRURL, Number: 7, State: runnerwire.PROpen}})
	if out := env.act(t, env.discardJob("d-2")); out.Code != runnerwire.WorkspaceErrRunning || !strings.Contains(out.Error, "run-review") {
		t.Fatalf("PR in use = %+v", out)
	}
	if calls, _ := env.gh.recorded(); len(calls) != 0 {
		t.Fatalf("gh called: %v", calls)
	}
	if !worktreeExists(env.wt.Path) || !env.branchExists(t) {
		t.Fatal("removed while another run works on it")
	}
}

// A refusal after the PR was closed still says the PR was closed.
func TestDiscardRefusalAfterPRClosedSaysSo(t *testing.T) {
	env := newWSEnv(t, nil)
	env.j.watch(watchedPR{RunID: wsRun, NodeID: wsNode, URL: mergePRURL, Repo: env.repo, Branch: env.wt.Branch,
		PR: runnerwire.PRInfo{URL: mergePRURL, Number: 7, State: runnerwire.PROpen}})
	// Once gh closed it, the worktree is no longer the run's directory.
	env.gh.after = func() {
		if err := os.RemoveAll(env.wt.Path); err != nil {
			t.Error(err)
		}
		if err := os.WriteFile(env.wt.Path, []byte("not a directory\n"), 0o644); err != nil {
			t.Error(err)
		}
	}
	out := env.act(t, env.discardJob("d-1"))
	if out.Code != runnerwire.WorkspaceErrForeign || !out.PRClosed || !strings.Contains(out.Error, "pull request was closed") ||
		strings.Contains(out.Error, "nothing was removed") {
		t.Fatalf("outcome = %+v", out)
	}
}

// The temporary worktree reads the config the merge will run under: a
// filter or merge driver included only on the base branch never runs.
func TestMergeLocalTempWorktreeOnBranchConfig(t *testing.T) {
	env := newWSEnv(t, nil)
	gitT(t, env.repo, "checkout", "-q", "-b", "other")
	marker := filepath.Join(t.TempDir(), "ran")
	script := filepath.Join(t.TempDir(), "evil.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho ran > '"+filepath.ToSlash(marker)+"'\ncat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	command := "sh '" + filepath.ToSlash(script) + "'"
	gitDir := filepath.Join(env.repo, ".git")
	writeFile(t, gitDir, "x.inc", []byte("[filter \"Zed\"]\n\tsmudge = "+command+"\n\tclean = "+command+"\n"))
	writeFile(t, gitDir, "info/attributes", []byte("* filter=Zed\n"))
	gitT(t, env.repo, "config", "includeIf.onbranch:main.path", "x.inc")
	out := env.act(t, env.mergeJob("w-1"))
	if out.Code != "" || out.MergeCommit == "" {
		t.Fatalf("outcome = %+v", out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a filter included on the base branch ran")
	}

	// A merge driver included the same way is refused.
	env2 := newWSEnv(t, nil)
	gitT(t, env2.repo, "checkout", "-q", "-b", "other")
	writeFile(t, filepath.Join(env2.repo, ".git"), "y.inc", []byte("[merge \"d\"]\n\tdriver = "+command+"\n"))
	gitT(t, env2.repo, "config", "includeIf.onbranch:main.path", "y.inc")
	main := gitT(t, env2.repo, "rev-parse", "main")
	if out := env2.act(t, env2.mergeJob("w-1")); out.Code != runnerwire.WorkspaceErrMergeDriver {
		t.Fatalf("hidden merge driver = %+v", out)
	}
	if gitT(t, env2.repo, "rev-parse", "main") != main {
		t.Fatal("main moved")
	}

	// So is a signing program included the same way.
	env3 := newWSEnv(t, nil)
	gitT(t, env3.repo, "checkout", "-q", "-b", "other")
	writeFile(t, filepath.Join(env3.repo, ".git"), "z.inc", []byte("[commit]\n\tgpgSign = true\n[gpg]\n\tprogram = "+filepath.ToSlash(script)+"\n"))
	gitT(t, env3.repo, "config", "includeIf.onbranch:main.path", "z.inc")
	main = gitT(t, env3.repo, "rev-parse", "main")
	if out := env3.act(t, env3.mergeJob("w-1")); out.Code != runnerwire.WorkspaceErrSigning || !strings.Contains(out.Error, "gpg.program") {
		t.Fatalf("hidden signing program = %+v", out)
	}
	if gitT(t, env3.repo, "rev-parse", "main") != main || !worktreeExists(env3.wt.Path) {
		t.Fatal("something moved after a refusal")
	}

	// Control: a plain checkout of main runs the filter, so the test proves
	// something.
	gitT(t, env.repo, "worktree", "add", "-q", filepath.Join(t.TempDir(), "plain"), "main")
	if _, err := os.Stat(marker); err != nil {
		t.Skipf("the filter never runs here, so this proves nothing: %v", err)
	}
}
