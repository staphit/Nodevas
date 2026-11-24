package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/runner/adapter"
	"nodevas/internal/runnerwire"
)

func followUpTestJob(runID, priorID, rootID, prompt string) runnerwire.Job {
	job := globalTestJob(runID, prompt)
	job.ContinueOf = priorID
	job.WorkRoot = rootID
	job.ContinueTask = "Tidy the README"
	job.ContinueSummary = "Added hello.txt"
	return job
}

func TestFollowUpWorksInTheSameFolder(t *testing.T) {
	env := startJobRunner(t, nil)
	env.hub.hello()

	env.hub.offer(globalTestJob("run-f1", "edit: tidy the README"))
	if res := env.hub.wait("run-f1", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
		t.Fatalf("turn 1 = %+v", res)
	}
	firstChanges := env.hub.wait("run-f1", runnerwire.TypeChanges).Changes
	wtPath := filepath.Join(env.client.Config.DataDir, "worktrees", "run-f1")
	branch := "agent/tidy-readme-run-f1"

	env.hub.offer(followUpTestJob("run-f2", "run-f1", "run-f1", "continue: add more.txt"))
	if res := env.hub.wait("run-f2", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
		t.Fatalf("turn 2 = %+v", res)
	}
	accept := env.hub.wait("run-f2", runnerwire.TypeAccept).Accept
	if accept.Branch != branch || accept.Base != "main" {
		t.Fatalf("turn 2 accept = %+v, want branch %s base main", accept, branch)
	}
	if cwd := env.recorded(t, "run-f2", "cwd"); !samePath(cwd, wtPath) {
		t.Fatalf("turn 2 ran in %s, want %s", cwd, wtPath)
	}
	if _, err := os.Stat(filepath.Join(env.client.Config.DataDir, "worktrees", "run-f2")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("turn 2 created a separate worktree folder")
	}
	secondChanges := env.hub.wait("run-f2", runnerwire.TypeChanges).Changes
	if secondChanges.Commit == "" || secondChanges.Commit == firstChanges.Commit {
		t.Fatalf("turn 2 changes = %+v (turn 1 commit %s)", secondChanges, firstChanges.Commit)
	}
	if parent := gitT(t, env.repo, "rev-parse", secondChanges.Commit+"^"); parent != firstChanges.Commit {
		t.Fatalf("turn 2 commit parent = %s, want %s", parent, firstChanges.Commit)
	}
	for _, name := range []string{"hello.txt", "more.txt"} {
		if _, err := os.Stat(filepath.Join(wtPath, name)); err != nil {
			t.Errorf("worktree missing %s: %v", name, err)
		}
	}
}

func TestFollowUpRefusesStaleLastRun(t *testing.T) {
	env := startJobRunner(t, nil)
	env.hub.hello()

	env.hub.offer(globalTestJob("run-st1", "edit: turn 1"))
	env.hub.wait("run-st1", runnerwire.TypeFinish)
	env.hub.offer(followUpTestJob("run-st2", "run-st1", "run-st1", "continue: turn 2"))
	env.hub.wait("run-st2", runnerwire.TypeFinish)

	// Offering another follow-up naming run-st1 instead of run-st2 must be declined.
	env.hub.offer(followUpTestJob("run-st3", "run-st1", "run-st1", "continue: stale"))
	msg := env.hub.wait("run-st3", runnerwire.TypeDecline)
	if !strings.Contains(msg.Reason, "run-st1") && !strings.Contains(msg.Reason, "latest") {
		t.Fatalf("stale last run declined with %q", msg.Reason)
	}

	// Unknown root is also declined.
	env.hub.offer(followUpTestJob("run-st4", "run-unknown", "run-unknown", "continue: unknown"))
	if msg := env.hub.wait("run-st4", runnerwire.TypeDecline); msg.Reason == "" {
		t.Fatal("expected decline for unknown root")
	}
}

func TestFollowUpRefusesHeadMoved(t *testing.T) {
	hermeticGit(t)
	env := startJobRunner(t, nil)
	env.hub.hello()

	env.hub.offer(globalTestJob("run-hm1", "edit: turn 1"))
	env.hub.wait("run-hm1", runnerwire.TypeFinish)

	wtPath := filepath.Join(env.client.Config.DataDir, "worktrees", "run-hm1")
	writeFile(t, wtPath, "extra.txt", []byte("outside commit\n"))
	gitT(t, wtPath, "add", "extra.txt")
	gitT(t, wtPath, "commit", "-q", "-m", "external commit")

	env.hub.offer(followUpTestJob("run-hm2", "run-hm1", "run-hm1", "continue: turn 2"))
	msg := env.hub.wait("run-hm2", runnerwire.TypeDecline)
	if !strings.Contains(strings.ToLower(msg.Reason), "head") && !strings.Contains(strings.ToLower(msg.Reason), "moved") {
		t.Fatalf("HEAD moved declined with %q", msg.Reason)
	}
}

func TestFollowUpRefusesBranchMismatch(t *testing.T) {
	hermeticGit(t)
	env := startJobRunner(t, nil)
	env.hub.hello()

	env.hub.offer(globalTestJob("run-bm1", "edit: turn 1"))
	env.hub.wait("run-bm1", runnerwire.TypeFinish)

	wtPath := filepath.Join(env.client.Config.DataDir, "worktrees", "run-bm1")
	gitT(t, wtPath, "checkout", "-q", "-b", "other-branch")

	env.hub.offer(followUpTestJob("run-bm2", "run-bm1", "run-bm1", "continue: turn 2"))
	msg := env.hub.wait("run-bm2", runnerwire.TypeDecline)
	if !strings.Contains(strings.ToLower(msg.Reason), "branch") {
		t.Fatalf("branch mismatch declined with %q", msg.Reason)
	}
}

func TestFollowUpRefusesInUse(t *testing.T) {
	env := startJobRunner(t, func(c *Client) {
		c.Config.MaxConcurrent = 2
	})
	env.hub.hello()

	env.hub.offer(globalTestJob("run-iu1", "edit: turn 1"))
	env.hub.wait("run-iu1", runnerwire.TypeFinish)

	// Start turn 2 that hangs while running in the worktree.
	env.hub.offer(followUpTestJob("run-iu2", "run-iu1", "run-iu1", "hang: turn 2"))
	env.hub.wait("run-iu2", runnerwire.TypeAccept)
	env.recorded(t, "run-iu2", "child-pid")

	// Offer another turn for the same root while turn 2 is active.
	env.hub.offer(followUpTestJob("run-iu3", "run-iu1", "run-iu1", "continue: turn 3"))
	msg := env.hub.wait("run-iu3", runnerwire.TypeDecline)
	if msg.Reason == "" || strings.Contains(msg.Reason, "runs at most") {
		t.Fatalf("in-use declined with %q, want folder/work in-use reason", msg.Reason)
	}

	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeCancel, RunID: "run-iu2", Reason: "done"})
	env.hub.wait("run-iu2", runnerwire.TypeFinish)
	waitDead(t, env, "run-iu2")
}

func TestFollowUpRefusesForeignWorktree(t *testing.T) {
	env := startJobRunner(t, nil)
	env.hub.hello()

	env.hub.offer(globalTestJob("run-fw1", "edit: turn 1"))
	env.hub.wait("run-fw1", runnerwire.TypeFinish)

	wtPath := filepath.Join(env.client.Config.DataDir, "worktrees", "run-fw1")
	if err := os.WriteFile(filepath.Join(wtPath, ".git"), []byte("gitdir: "+filepath.ToSlash(t.TempDir())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	env.hub.offer(followUpTestJob("run-fw2", "run-fw1", "run-fw1", "continue: turn 2"))
	msg := env.hub.wait("run-fw2", runnerwire.TypeDecline)
	if msg.Reason == "" {
		t.Fatal("expected foreign worktree to be declined")
	}

	// Also test when the worktree folder is swapped for a symlink.
	env.hub.offer(globalTestJob("run-fw3", "edit: turn 1"))
	env.hub.wait("run-fw3", runnerwire.TypeFinish)
	wtPath3 := filepath.Join(env.client.Config.DataDir, "worktrees", "run-fw3")
	if err := os.RemoveAll(wtPath3); err != nil {
		t.Fatal(err)
	}
	link(t, t.TempDir(), wtPath3)
	env.hub.offer(followUpTestJob("run-fw4", "run-fw3", "run-fw3", "continue: turn 2"))
	if msg := env.hub.wait("run-fw4", runnerwire.TypeDecline); msg.Reason == "" {
		t.Fatal("expected symlinked worktree to be declined")
	}
}

func TestFollowUpRefusesRepoMismatch(t *testing.T) {
	repo2 := newTestRepo(t)
	env := startJobRunner(t, func(c *Client) {
		c.Config.Repos = append(c.Config.Repos, runnerwire.Repo{ID: "r2", Name: "other", Path: repo2})
	})
	env.hub.hello()

	env.hub.offer(globalTestJob("run-rm1", "edit: turn 1"))
	env.hub.wait("run-rm1", runnerwire.TypeFinish)

	mismatch := followUpTestJob("run-rm2", "run-rm1", "run-rm1", "continue: turn 2")
	mismatch.RepoID = "r2"
	env.hub.offer(mismatch)
	msg := env.hub.wait("run-rm2", runnerwire.TypeDecline)
	if !strings.Contains(msg.Reason, "r1") && !strings.Contains(msg.Reason, "r2") && !strings.Contains(msg.Reason, "repository") {
		t.Fatalf("repo mismatch declined with %q", msg.Reason)
	}
}

func TestFollowUpRefusesDirtyAfterSuccess(t *testing.T) {
	env := startJobRunner(t, nil)
	env.hub.hello()

	env.hub.offer(globalTestJob("run-ds1", "edit: turn 1"))
	env.hub.wait("run-ds1", runnerwire.TypeFinish)

	wtPath := filepath.Join(env.client.Config.DataDir, "worktrees", "run-ds1")
	writeFile(t, wtPath, "uncommitted.txt", []byte("dirty\n"))

	env.hub.offer(followUpTestJob("run-ds2", "run-ds1", "run-ds1", "continue: turn 2"))
	msg := env.hub.wait("run-ds2", runnerwire.TypeDecline)
	if !strings.Contains(strings.ToLower(msg.Reason), "uncommitted") && !strings.Contains(strings.ToLower(msg.Reason), "dirty") && !strings.Contains(strings.ToLower(msg.Reason), "clean") {
		t.Fatalf("dirty after success declined with %q", msg.Reason)
	}
}

func TestFollowUpAcceptsDirtyAfterCancel(t *testing.T) {
	env := startJobRunner(t, nil)
	env.hub.hello()

	env.hub.offer(globalTestJob("run-dc1", "hang: turn 1"))
	env.hub.wait("run-dc1", runnerwire.TypeAccept)
	env.recorded(t, "run-dc1", "child-pid")

	wtPath := filepath.Join(env.client.Config.DataDir, "worktrees", "run-dc1")
	writeFile(t, wtPath, "partial.txt", []byte("in progress before cancel\n"))

	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeCancel, RunID: "run-dc1", Reason: "stop"})
	if res := env.hub.wait("run-dc1", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunCancelled {
		t.Fatalf("turn 1 = %+v", res)
	}
	waitDead(t, env, "run-dc1")

	// Turn 2 must be accepted even though partial.txt is uncommitted in the worktree.
	env.hub.offer(followUpTestJob("run-dc2", "run-dc1", "run-dc1", "continue: finish it"))
	if res := env.hub.wait("run-dc2", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
		t.Fatalf("turn 2 after cancel = %+v", res)
	}
	changes := env.hub.wait("run-dc2", runnerwire.TypeChanges).Changes
	var paths []string
	for _, f := range changes.Files {
		paths = append(paths, f.Path)
	}
	if !slices.Contains(paths, "partial.txt") || !slices.Contains(paths, "more.txt") {
		t.Fatalf("turn 2 committed files = %v, want partial.txt and more.txt", paths)
	}
}

func TestFollowUpRecreatesANoChangeFolder(t *testing.T) {
	env := startJobRunner(t, nil)
	env.hub.hello()

	// Turn 1 makes no changes, so publish removes the worktree and branch.
	env.hub.offer(globalTestJob("run-nc1", "ok: just looking"))
	if res := env.hub.wait("run-nc1", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
		t.Fatalf("turn 1 = %+v", res)
	}
	wtPath := filepath.Join(env.client.Config.DataDir, "worktrees", "run-nc1")
	if _, err := os.Stat(wtPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("turn 1 no-change worktree still exists: %v", err)
	}

	// Turn 2 recreates the worktree at the same path with the root's branch name.
	env.hub.offer(followUpTestJob("run-nc2", "run-nc1", "run-nc1", "edit: now make changes"))
	if res := env.hub.wait("run-nc2", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
		t.Fatalf("turn 2 = %+v", res)
	}
	accept := env.hub.wait("run-nc2", runnerwire.TypeAccept).Accept
	if accept.Branch != "agent/tidy-readme-run-nc1" {
		t.Fatalf("turn 2 branch = %q, want agent/tidy-readme-run-nc1", accept.Branch)
	}
	if _, err := os.Stat(filepath.Join(wtPath, "hello.txt")); err != nil {
		t.Fatalf("recreated worktree missing hello.txt: %v", err)
	}
}

func TestFollowUpDeclinesWhenPRFolderWasRemoved(t *testing.T) {
	gh := newFollowUpGH("https://github.com/o/r/pull/9", "agent/tidy-readme-run-prgone", "main")
	env := startFollowUpPRRunner(t, gh, nil)
	env.hub.hello()

	first := globalTestJob("run-prgone", "edit: open pr")
	first.NoPR = false
	env.hub.offer(first)
	if res := env.hub.wait("run-prgone", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
		t.Fatalf("turn 1 = %+v", res)
	}
	wtPath := filepath.Join(env.client.Config.DataDir, "worktrees", "run-prgone")
	if err := removeWorktree(context.Background(), env.repo, Worktree{Path: wtPath, Branch: "agent/tidy-readme-run-prgone"}, false); err != nil {
		t.Fatal(err)
	}

	second := followUpTestJob("run-prgone-2", "run-prgone", "run-prgone", "continue: after pr folder removed")
	second.NoPR = false
	env.hub.offer(second)
	msg := env.hub.wait("run-prgone-2", runnerwire.TypeDecline)
	if msg.Code != runnerwire.DeclinePREnded {
		t.Fatalf("decline = %+v, want code %q", msg, runnerwire.DeclinePREnded)
	}
	if _, err := os.Stat(wtPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("worktree was recreated after PR folder was removed")
	}
}

func TestCheckGlobalAllowsContinue(t *testing.T) {
	cfg := &Config{}
	job := globalTestJob("run-2", "continue")
	job.ContinueOf = "run-1"
	job.WorkRoot = "run-1"
	job.ContinueTask = "prior task"
	job.ContinueSummary = "prior summary"
	job.ContinuePR = "https://github.com/o/r/pull/9"
	if reason := checkGlobal(&job, cfg); reason != "" {
		t.Fatalf("checkGlobal refused valid continue job: %q", reason)
	}

	// HandoffBranch and Branch are still refused on global jobs.
	withHandoff := job
	withHandoff.HandoffBranch = "nodevas/handoff/run-1"
	if reason := checkGlobal(&withHandoff, cfg); reason != "malformed global job" {
		t.Errorf("with HandoffBranch = %q", reason)
	}
	withBranch := job
	withBranch.Branch = "agent/tidy-readme-run-1"
	if reason := checkGlobal(&withBranch, cfg); reason != "malformed global job" {
		t.Errorf("with Branch = %q", reason)
	}

	// Bad run IDs in ContinueOf or WorkRoot are refused.
	badContinue := job
	badContinue.ContinueOf = "../escape"
	if reason := checkGlobal(&badContinue, cfg); reason != "malformed global job" {
		t.Errorf("bad ContinueOf = %q", reason)
	}
	badRoot := job
	badRoot.WorkRoot = "../escape"
	if reason := checkGlobal(&badRoot, cfg); reason != "malformed global job" {
		t.Errorf("bad WorkRoot = %q", reason)
	}
	orphanRoot := job
	orphanRoot.ContinueOf = ""
	if reason := checkGlobal(&orphanRoot, cfg); reason != "malformed global job" {
		t.Errorf("WorkRoot without ContinueOf = %q", reason)
	}

	// Non-global job cannot carry ContinueOf or WorkRoot.
	nodeJob := testJob("run-n2", "ok:")
	nodeJob.ContinueOf = "run-n1"
	if reason := checkGlobal(&nodeJob, cfg); reason == "" {
		t.Error("checkGlobal allowed ContinueOf on a node job")
	}
}

func TestWorkspaceActionOnALaterTurn(t *testing.T) {
	hermeticGit(t)
	env := startJobRunner(t, nil)
	env.hub.hello()
	env.hub.setOwner(wsOwner)
	env.hub.kick()
	env.hub.hello()

	env.hub.offer(globalTestJob("run-wa1", "edit: turn 1"))
	env.hub.wait("run-wa1", runnerwire.TypeFinish)
	firstCommit := env.hub.wait("run-wa1", runnerwire.TypeChanges).Changes.Commit

	env.hub.offer(followUpTestJob("run-wa2", "run-wa1", "run-wa1", "continue: turn 2"))
	env.hub.wait("run-wa2", runnerwire.TypeFinish)
	secondCommit := env.hub.wait("run-wa2", runnerwire.TypeChanges).Changes.Commit
	branch := "agent/tidy-readme-run-wa1"

	// Browsing and run_info on the later turn run-wa2 resolve the root folder.
	infoDone := env.client.jobs.browseRun(context.Background(), wsOwner, runnerwire.RepoRequest{
		ID: "q-info", AccountID: wsOwner, RepoID: "r1", Op: runnerwire.RepoOpRunInfo,
		RunID: "run-wa2", Slug: "tidy-readme", Branch: branch, Base: "main",
	})
	if infoDone.Code != "" || infoDone.RunInfo == nil || !infoDone.RunInfo.Exists || !infoDone.RunInfo.OwnBranch || infoDone.RunInfo.Head != secondCommit {
		t.Fatalf("run_info on later turn = %+v (%+v)", infoDone, infoDone.RunInfo)
	}
	readDone := env.client.jobs.browseRun(context.Background(), wsOwner, runnerwire.RepoRequest{
		ID: "q-read", AccountID: wsOwner, RepoID: "r1", Op: runnerwire.RepoOpRunRead,
		RunID: "run-wa2", Path: "more.txt",
	})
	if readDone.Code != "" || readDone.File == nil || readDone.File.Content != "more\n" {
		t.Fatalf("run_read on later turn = %+v", readDone)
	}

	// Workspace action on the earlier turn run-wa1 is refused with gate "continued".
	oldAct := runnerwire.WorkspaceActionJob{
		ID: "act-old", RunID: "run-wa1", WorkRoot: "run-wa1", AccountID: wsOwner,
		Action: runnerwire.WorkspaceMergeLocal, RepoID: "r1", Slug: "tidy-readme",
		Branch: branch, Base: "main", HeadSHA: firstCommit,
	}
	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeWorkspaceAction, RunID: "run-wa1", WorkspaceAction: &oldAct})
	oldRes := env.hub.wait("run-wa1", runnerwire.TypeWorkspaceResult).WorkspaceResult
	if oldRes == nil || oldRes.Code != runnerwire.WorkspaceGateContinued {
		t.Fatalf("earlier turn workspace action = %+v, want code %q", oldRes, runnerwire.WorkspaceGateContinued)
	}

	// Workspace action on the latest turn run-wa2 with WorkRoot="run-wa1" merges and cleans up the root folder.
	latestAct := runnerwire.WorkspaceActionJob{
		ID: "act-latest", RunID: "run-wa2", WorkRoot: "run-wa1", AccountID: wsOwner,
		Action: runnerwire.WorkspaceMergeLocal, RepoID: "r1", Slug: "tidy-readme",
		Branch: branch, Base: "main", HeadSHA: secondCommit,
	}
	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeWorkspaceAction, RunID: "run-wa2", WorkspaceAction: &latestAct})
	latestRes := env.hub.wait("run-wa2", runnerwire.TypeWorkspaceResult).WorkspaceResult
	if latestRes == nil || latestRes.Code != "" || !latestRes.WorktreeRemoved || !latestRes.BranchDeleted {
		t.Fatalf("latest turn workspace action = %+v", latestRes)
	}
	if _, err := os.Stat(filepath.Join(env.client.Config.DataDir, "worktrees", "run-wa1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("root worktree still exists after merge_local on later turn")
	}
	if _, err := os.Stat(filepath.Join(env.repo, "more.txt")); err != nil {
		t.Fatalf("more.txt not merged into main: %v", err)
	}
}

func TestContinueRekeysKeptAndWatched(t *testing.T) {
	gh := newFollowUpGH("https://github.com/o/r/pull/11", "agent/tidy-readme-run-kw1", "main")
	env := startFollowUpPRRunner(t, gh, nil)
	env.hub.hello()
	dataDir := env.client.Config.DataDir

	// Turn 1 writes normal files + withheld agent config ("plant:") and opens a PR.
	first := globalTestJob("run-kw1", "plant: turn 1 with config")
	first.NoPR = false
	env.hub.offer(first)
	if res := env.hub.wait("run-kw1", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
		t.Fatalf("turn 1 = %+v", res)
	}
	if got := env.client.jobs.watchedRuns(); !slices.Equal(got, []string{"run-kw1"}) {
		t.Fatalf("watched after turn 1 = %v, want [run-kw1]", got)
	}
	kept1, err := KeptWorktrees(dataDir)
	if err != nil || len(kept1) != 1 || kept1[0].RunID != "run-kw1" {
		t.Fatalf("kept after turn 1 = %+v, %v", kept1, err)
	}

	// Turn 2 continues in the same folder, writes more changes, and updates the PR.
	second := followUpTestJob("run-kw2", "run-kw1", "run-kw1", "continue: turn 2")
	second.NoPR = false
	env.hub.offer(second)
	if res := env.hub.wait("run-kw2", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
		t.Fatalf("turn 2 = %+v", res)
	}
	if got := env.client.jobs.watchedRuns(); !slices.Equal(got, []string{"run-kw1"}) {
		t.Fatalf("watched after turn 2 = %v, want [run-kw1] (no second watch)", got)
	}
	kept2, err := KeptWorktrees(dataDir)
	if err != nil || len(kept2) != 1 || kept2[0].RunID != "run-kw1" {
		t.Fatalf("kept after turn 2 = %+v, %v", kept2, err)
	}
}

func TestContinueStillNoBoard(t *testing.T) {
	env := startJobRunner(t, nil)
	env.hub.hello()

	env.hub.offer(globalTestJob("run-nb1", "edit: turn 1"))
	env.hub.wait("run-nb1", runnerwire.TypeFinish)

	// Force fallback prompt (no resume) so we also verify the preamble has no board tools.
	entry, _, _ := lookupWork(env.client.Config.DataDir, "run-nb1")
	entry.Session = ""
	_ = registerWork(env.client.Config.DataDir, entry)

	env.hub.offer(followUpTestJob("run-nb2", "run-nb1", "run-nb1", "continue: turn 2"))
	if res := env.hub.wait("run-nb2", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
		t.Fatalf("turn 2 = %+v", res)
	}
	if !hubHasEvent(env, "run-nb2", "no board (a global agent)") {
		t.Errorf("turn 2 missing no-board event: %+v", env.hub.messages("run-nb2"))
	}
	var mcp struct {
		MCPServers map[string]adapter.MCPServer `json:"mcpServers"`
	}
	if raw := env.recorded(t, "run-nb2", "mcp.json"); json.Unmarshal([]byte(raw), &mcp) != nil || len(mcp.MCPServers) != 0 {
		t.Errorf("turn 2 mcp.json = %s", raw)
	}
	if _, err := os.Stat(filepath.Join(env.record, "run-nb2", "mcp-server")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("turn 2 got an MCP proxy door: %v", err)
	}
	stdin := env.recorded(t, "run-nb2", "stdin")
	for _, word := range []string{"claim_task", "set_node_status", "ask_user", "get_node"} {
		if strings.Contains(stdin, word) {
			t.Errorf("turn 2 prompt names %s:\n%s", word, stdin)
		}
	}
	gitDir := filepath.Join(env.repo, ".git", "worktrees", "run-nb1")
	if _, err := os.Stat(filepath.Join(gitDir, runnerwire.NoBoardMarker)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("no-board marker file left behind after turn 2 finished: %v", err)
	}
}

// followUpGH scripts gh for follow-up PR tests.
type followUpGH struct {
	mu       sync.Mutex
	url      string
	state    string
	draft    bool
	headRef  string
	baseRef  string
	headSHA  string
	cross    bool
	creates  [][]string
	bodies   []string
	comments []string
	calls    [][]string
	// endAfterView, when set, is the state the PR takes right after the next
	// gh pr view answers: it was merged or closed while a turn ran.
	endAfterView string
}

func newFollowUpGH(url, branch, base string) *followUpGH {
	return &followUpGH{
		url:     url,
		state:   "OPEN",
		headRef: branch,
		baseRef: base,
		headSHA: "feedface00000000000000000000000000000000",
	}
}

func (g *followUpGH) run(_, stdin string, args []string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, append([]string(nil), args...))
	if len(args) >= 2 && args[0] == "pr" && args[1] == "create" {
		g.creates = append(g.creates, append([]string(nil), args...))
		g.bodies = append(g.bodies, stdin)
		if slices.Contains(args, "--draft") {
			g.draft = true
		}
		return "Creating pull request...\n" + g.url + "\n", nil
	}
	if len(args) >= 2 && args[0] == "pr" && args[1] == "view" {
		raw, _ := json.Marshal(map[string]any{
			"number":            prNumber(g.url),
			"state":             g.state,
			"isDraft":           g.draft,
			"headRefName":       g.headRef,
			"baseRefName":       g.baseRef,
			"isCrossRepository": g.cross,
			"headRefOid":        g.headSHA,
			"statusCheckRollup": []any{},
		})
		if g.endAfterView != "" {
			g.state, g.endAfterView = g.endAfterView, ""
		}
		return string(raw), nil
	}
	if len(args) >= 2 && args[0] == "pr" && args[1] == "comment" {
		g.comments = append(g.comments, stdin)
		return g.url + "#issuecomment-1\n", nil
	}
	return "", errors.New("unexpected gh " + strings.Join(args, " "))
}

type followUpPREnv struct {
	*jobEnv
	gh   *followUpGH
	bare string
}

func startFollowUpPRRunner(t *testing.T, gh *followUpGH, tweak func(*Client)) *followUpPREnv {
	t.Helper()
	hermeticGit(t)
	env := startJobRunner(t, func(c *Client) {
		c.PRPollInterval = time.Hour
		cmds := c.Commands.(*fakeCommands)
		if gh != nil {
			cmds.paths["gh"] = "/bin/gh"
			cmds.gh = gh.run
		}
		if tweak != nil {
			tweak(c)
		}
	})
	bare := withOrigin(t, env.repo)
	return &followUpPREnv{jobEnv: env, gh: gh, bare: bare}
}

func TestTurnPushesFastForwardToOpenPR(t *testing.T) {
	const branch = "agent/tidy-readme-run-ffp"
	gh := newFollowUpGH("https://github.com/o/r/pull/12", branch, "main")
	env := startFollowUpPRRunner(t, gh, nil)
	env.hub.hello()

	first := globalTestJob("run-ffp", "edit: turn 1")
	first.NoPR = false
	env.hub.offer(first)
	if res := env.hub.wait("run-ffp", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
		t.Fatalf("turn 1 = %+v", res)
	}
	c1 := gitT(t, env.bare, "rev-parse", "refs/heads/"+branch)

	second := followUpTestJob("run-ffp-2", "run-ffp", "run-ffp", "continue: turn 2")
	second.NoPR = false
	env.hub.offer(second)
	if res := env.hub.wait("run-ffp-2", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
		t.Fatalf("turn 2 = %+v", res)
	}
	c2 := gitT(t, env.bare, "rev-parse", "refs/heads/"+branch)
	if c2 == c1 || gitT(t, env.bare, "rev-parse", c2+"^") != c1 {
		t.Fatalf("origin branch at %s (parent %s), want fast-forward from %s", c2, gitT(t, env.bare, "rev-parse", c2+"^"), c1)
	}
}

func TestFailedTurnIsNeverPushed(t *testing.T) {
	const branch = "agent/tidy-readme-run-fn"
	gh := newFollowUpGH("https://github.com/o/r/pull/13", branch, "main")
	env := startFollowUpPRRunner(t, gh, nil)
	env.hub.hello()

	first := globalTestJob("run-fn", "edit: turn 1")
	first.NoPR = false
	env.hub.offer(first)
	env.hub.wait("run-fn", runnerwire.TypeFinish)
	c1 := gitT(t, env.bare, "rev-parse", "refs/heads/"+branch)

	second := followUpTestJob("run-fn-2", "run-fn", "run-fn", "continue-fail: broken turn 2")
	second.NoPR = false
	env.hub.offer(second)
	if res := env.hub.wait("run-fn-2", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunFailed {
		t.Fatalf("turn 2 = %+v, want failed", res)
	}

	// Committed locally, but origin is untouched at c1.
	localHead := gitT(t, env.repo, "rev-parse", "refs/heads/"+branch)
	if localHead == c1 {
		t.Fatal("failed turn's local changes were not committed locally")
	}
	if got := gitT(t, env.bare, "rev-parse", "refs/heads/"+branch); got != c1 {
		t.Fatalf("origin moved on a failed turn: %s, want %s", got, c1)
	}
	entry, _, _ := lookupWork(env.client.Config.DataDir, "run-fn")
	if entry.Head != localHead || entry.PushedHead != c1 {
		t.Fatalf("work entry = %+v, want head=%s pushedHead=%s", entry, localHead, c1)
	}
	gh.mu.Lock()
	comments := len(gh.comments)
	gh.mu.Unlock()
	if comments != 0 {
		t.Fatalf("posted %d PR comments on a failed turn", comments)
	}
}

func TestNextGoodTurnPushesEarlierFailedCommits(t *testing.T) {
	const branch = "agent/tidy-readme-run-ng"
	gh := newFollowUpGH("https://github.com/o/r/pull/14", branch, "main")
	env := startFollowUpPRRunner(t, gh, nil)
	env.hub.hello()

	first := globalTestJob("run-ng", "edit: turn 1")
	first.NoPR = false
	env.hub.offer(first)
	env.hub.wait("run-ng", runnerwire.TypeFinish)
	c1 := gitT(t, env.bare, "rev-parse", "refs/heads/"+branch)

	// Turn 2 fails after writing failed-step.txt (committed locally as c2, not pushed).
	second := followUpTestJob("run-ng-2", "run-ng", "run-ng", "continue-fail: turn 2 fails")
	second.NoPR = false
	env.hub.offer(second)
	if res := env.hub.wait("run-ng-2", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunFailed {
		t.Fatalf("turn 2 = %+v", res)
	}
	c2 := gitT(t, env.repo, "rev-parse", "refs/heads/"+branch)
	if c2 == c1 || gitT(t, env.bare, "rev-parse", "refs/heads/"+branch) != c1 {
		t.Fatalf("after turn 2: local=%s origin=%s (c1=%s)", c2, gitT(t, env.bare, "rev-parse", "refs/heads/"+branch), c1)
	}

	// Turn 3 succeeds and pushes both c2 and c3 to origin.
	third := followUpTestJob("run-ng-3", "run-ng-2", "run-ng", "continue: turn 3 fixes it")
	third.NoPR = false
	env.hub.offer(third)
	if res := env.hub.wait("run-ng-3", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
		t.Fatalf("turn 3 = %+v", res)
	}
	c3 := gitT(t, env.bare, "rev-parse", "refs/heads/"+branch)
	if gitT(t, env.bare, "rev-parse", c3+"^") != c2 || gitT(t, env.bare, "rev-parse", c2+"^") != c1 {
		t.Fatalf("origin chain: c3=%s c2=%s c1=%s", c3, c2, c1)
	}
	files := gitT(t, env.bare, "ls-tree", "--name-only", c3)
	for _, want := range []string{"hello.txt", "failed-step.txt", "more.txt"} {
		if !strings.Contains(files, want) {
			t.Errorf("origin tree missing %s:\n%s", want, files)
		}
	}
}

func TestTurnRefusesMergedOrClosedPR(t *testing.T) {
	const branch = "agent/tidy-readme-run-mc"
	gh := newFollowUpGH("https://github.com/o/r/pull/15", branch, "main")
	env := startFollowUpPRRunner(t, gh, nil)
	env.hub.hello()

	first := globalTestJob("run-mc", "edit: turn 1")
	first.NoPR = false
	env.hub.offer(first)
	env.hub.wait("run-mc", runnerwire.TypeFinish)

	for _, state := range []string{"MERGED", "CLOSED"} {
		gh.mu.Lock()
		gh.state = state
		gh.mu.Unlock()
		runID := "run-mc-" + strings.ToLower(state)
		follow := followUpTestJob(runID, "run-mc", "run-mc", "continue: after "+state)
		follow.NoPR = false
		env.hub.offer(follow)
		msg := env.hub.wait(runID, runnerwire.TypeDecline)
		if msg.Code != runnerwire.DeclinePREnded {
			t.Errorf("state %s: decline = %+v, want code %q", state, msg, runnerwire.DeclinePREnded)
		}
	}
}

func TestTurnRefusesRetargetedPR(t *testing.T) {
	const branch = "agent/tidy-readme-run-rt"
	gh := newFollowUpGH("https://github.com/o/r/pull/16", branch, "main")
	env := startFollowUpPRRunner(t, gh, nil)
	env.hub.hello()

	first := globalTestJob("run-rt", "edit: turn 1")
	first.NoPR = false
	env.hub.offer(first)
	env.hub.wait("run-rt", runnerwire.TypeFinish)

	gh.mu.Lock()
	gh.baseRef = "develop"
	gh.mu.Unlock()

	follow := followUpTestJob("run-rt-2", "run-rt", "run-rt", "continue: after retarget")
	follow.NoPR = false
	env.hub.offer(follow)
	msg := env.hub.wait("run-rt-2", runnerwire.TypeDecline)
	if msg.Code != runnerwire.DeclinePRMoved {
		t.Fatalf("decline = %+v, want code %q", msg, runnerwire.DeclinePRMoved)
	}
}

func TestTurnFastForwardsWhenRemoteAhead(t *testing.T) {
	const branch = "agent/tidy-readme-run-ra"
	gh := newFollowUpGH("https://github.com/o/r/pull/17", branch, "main")
	env := startFollowUpPRRunner(t, gh, nil)
	env.hub.hello()

	first := globalTestJob("run-ra", "edit: turn 1")
	first.NoPR = false
	env.hub.offer(first)
	env.hub.wait("run-ra", runnerwire.TypeFinish)
	c1 := gitT(t, env.bare, "rev-parse", "refs/heads/"+branch)

	// Someone pushes a commit to the PR branch on origin.
	clone := filepath.Join(t.TempDir(), "teammate")
	gitT(t, env.repo, "clone", "-q", "-b", branch, env.bare, clone)
	writeFile(t, clone, "teammate.txt", []byte("from github\n"))
	gitT(t, clone, "add", "teammate.txt")
	gitT(t, clone, "commit", "-q", "-m", "teammate commit")
	gitT(t, clone, "push", "-q", "origin", branch)
	cRemote := gitT(t, env.bare, "rev-parse", "refs/heads/"+branch)
	if cRemote == c1 {
		t.Fatal("origin did not advance")
	}

	second := followUpTestJob("run-ra-2", "run-ra", "run-ra", "continue: turn 2 on top of remote")
	second.NoPR = false
	env.hub.offer(second)
	if res := env.hub.wait("run-ra-2", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
		t.Fatalf("turn 2 = %+v", res)
	}
	c2 := gitT(t, env.bare, "rev-parse", "refs/heads/"+branch)
	if gitT(t, env.bare, "rev-parse", c2+"^") != cRemote {
		t.Fatalf("c2 parent = %s, want cRemote %s", gitT(t, env.bare, "rev-parse", c2+"^"), cRemote)
	}
	wtPath := filepath.Join(env.client.Config.DataDir, "worktrees", "run-ra")
	if _, err := os.Stat(filepath.Join(wtPath, "teammate.txt")); err != nil {
		t.Errorf("worktree missing fast-forwarded teammate.txt: %v", err)
	}
}

func TestTurnDeclinesDivergedBranch(t *testing.T) {
	const branch = "agent/tidy-readme-run-dv"
	gh := newFollowUpGH("https://github.com/o/r/pull/18", branch, "main")
	env := startFollowUpPRRunner(t, gh, nil)
	env.hub.hello()

	first := globalTestJob("run-dv", "edit: turn 1")
	first.NoPR = false
	env.hub.offer(first)
	env.hub.wait("run-dv", runnerwire.TypeFinish)

	// Subcase 1: someone pushes to origin WHILE turn 2 runs ("continue-race:").
	// Turn 2's push is rejected (never force-pushed), Turn 2 still succeeds with
	// PushRefused=true, and Turn 3 then meets origin ahead with local commits
	// and is declined with branch_moved.
	second := followUpTestJob("run-dv-2", "run-dv", "run-dv", "continue-race: "+branch)
	second.NoPR = false
	env.hub.offer(second)
	res2 := env.hub.wait("run-dv-2", runnerwire.TypeFinish).Result
	if res2.Status != runnerwire.RunSucceeded || !res2.PushRefused {
		t.Fatalf("turn 2 race = %+v, want Status=succeeded PushRefused=true", res2)
	}
	if subject := gitT(t, env.bare, "log", "-1", "--format=%s", "refs/heads/"+branch); subject != "someone else" {
		t.Fatalf("origin branch was overwritten: head subject = %q", subject)
	}
	gh.mu.Lock()
	comments := len(gh.comments)
	gh.mu.Unlock()
	if comments != 0 {
		t.Fatalf("posted %d comments on an unpushed turn", comments)
	}

	// Turn 3 is declined with branch_moved because origin is ahead and HEAD has unpushed local commits.
	third := followUpTestJob("run-dv-3", "run-dv-2", "run-dv", "continue: turn 3")
	third.NoPR = false
	env.hub.offer(third)
	msg := env.hub.wait("run-dv-3", runnerwire.TypeDecline)
	if msg.Code != runnerwire.DeclineBranchMoved {
		t.Fatalf("turn 3 decline = %+v, want code %q", msg, runnerwire.DeclineBranchMoved)
	}

	// Subcase 2: origin is force-pushed/rewritten to an unrelated commit on main.
	const branch2 = "agent/tidy-readme-run-dvf"
	gh2 := newFollowUpGH("https://github.com/o/r/pull/19", branch2, "main")
	env2 := startFollowUpPRRunner(t, gh2, nil)
	env2.hub.hello()
	first2 := globalTestJob("run-dvf", "edit: turn 1")
	first2.NoPR = false
	env2.hub.offer(first2)
	env2.hub.wait("run-dvf", runnerwire.TypeFinish)

	// Force-reset origin's branch back to main.
	mainSHA := gitT(t, env2.repo, "rev-parse", "main")
	gitT(t, env2.bare, "update-ref", "refs/heads/"+branch2, mainSHA)

	follow2 := followUpTestJob("run-dvf-2", "run-dvf", "run-dvf", "continue: after force push")
	follow2.NoPR = false
	env2.hub.offer(follow2)
	if msg := env2.hub.wait("run-dvf-2", runnerwire.TypeDecline); msg.Code != runnerwire.DeclineBranchMoved {
		t.Fatalf("after force-push decline = %+v, want code %q", msg, runnerwire.DeclineBranchMoved)
	}
}

func TestTurnAttachesPRInsteadOfCreate(t *testing.T) {
	const branch = "agent/tidy-readme-run-att"
	gh := newFollowUpGH("https://github.com/o/r/pull/20", branch, "main")
	env := startFollowUpPRRunner(t, gh, nil)
	env.hub.hello()

	first := globalTestJob("run-att", "edit: turn 1")
	first.NoPR = false
	env.hub.offer(first)
	env.hub.wait("run-att", runnerwire.TypeFinish)

	second := followUpTestJob("run-att-2", "run-att", "run-att", "continue: turn 2")
	second.NoPR = false
	env.hub.offer(second)
	env.hub.wait("run-att-2", runnerwire.TypeFinish)

	pr := env.hub.wait("run-att-2", runnerwire.TypePR).PR
	if pr == nil || pr.URL != gh.url || pr.Number != 20 || pr.State != runnerwire.PROpen {
		t.Fatalf("turn 2 PR = %+v", pr)
	}
	gh.mu.Lock()
	creates := len(gh.creates)
	gh.mu.Unlock()
	if creates != 1 {
		t.Fatalf("gh pr create called %d times, want 1 (only on turn 1)", creates)
	}
}

func TestTurnPostsOneCommentPerPush(t *testing.T) {
	const branch = "agent/tidy-readme-run-cmt"
	gh := newFollowUpGH("https://github.com/o/r/pull/21", branch, "main")
	env := startFollowUpPRRunner(t, gh, nil)
	env.hub.hello()

	first := globalTestJob("run-cmt", "edit: turn 1")
	first.NoPR = false
	env.hub.offer(first)
	env.hub.wait("run-cmt", runnerwire.TypeFinish)

	second := followUpTestJob("run-cmt-2", "run-cmt", "run-cmt", "continue: Make the button bigger\nAnd check mobile")
	second.NoPR = false
	env.hub.offer(second)
	env.hub.wait("run-cmt-2", runnerwire.TypeFinish)

	gh.mu.Lock()
	comments := slices.Clone(gh.comments)
	gh.mu.Unlock()
	if len(comments) != 1 {
		t.Fatalf("comments = %d, want 1", len(comments))
	}
	if !strings.HasPrefix(comments[0], "Turn 2: ") || !strings.Contains(comments[0], "Make the button bigger") ||
		!strings.Contains(comments[0], "hello fixture") {
		t.Errorf("turn 2 comment:\n%s", comments[0])
	}
}

func TestTurnNeverMarksReady(t *testing.T) {
	const branch = "agent/tidy-readme-run-drf"
	gh := newFollowUpGH("https://github.com/o/r/pull/22", branch, "main")
	env := startFollowUpPRRunner(t, gh, func(c *Client) {
		c.Config.PRDraft = true
	})
	env.hub.hello()

	first := globalTestJob("run-drf", "edit: turn 1")
	first.NoPR = false
	env.hub.offer(first)
	env.hub.wait("run-drf", runnerwire.TypeFinish)

	second := followUpTestJob("run-drf-2", "run-drf", "run-drf", "continue: turn 2")
	second.NoPR = false
	env.hub.offer(second)
	env.hub.wait("run-drf-2", runnerwire.TypeFinish)

	pr := env.hub.wait("run-drf-2", runnerwire.TypePR).PR
	if pr == nil || !pr.Draft {
		t.Fatalf("turn 2 PR = %+v, want Draft=true", pr)
	}
	gh.mu.Lock()
	defer gh.mu.Unlock()
	for _, call := range gh.calls {
		if slices.Contains(call, "ready") {
			t.Fatalf("gh pr ready was called: %v", call)
		}
	}
}

func TestNoOpTurnKeepsRootBranch(t *testing.T) {
	const branch = "agent/tidy-readme-run-noop"
	gh := newFollowUpGH("https://github.com/o/r/pull/23", branch, "main")
	env := startFollowUpPRRunner(t, gh, nil)
	env.hub.hello()

	first := globalTestJob("run-noop", "edit: turn 1")
	first.NoPR = false
	env.hub.offer(first)
	env.hub.wait("run-noop", runnerwire.TypeFinish)
	c1 := gitT(t, env.repo, "rev-parse", "refs/heads/"+branch)

	// Turn 2 makes no changes ("ok:").
	second := followUpTestJob("run-noop-2", "run-noop", "run-noop", "ok: nothing to change")
	second.NoPR = false
	env.hub.offer(second)
	if res := env.hub.wait("run-noop-2", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
		t.Fatalf("turn 2 = %+v", res)
	}

	wtPath := filepath.Join(env.client.Config.DataDir, "worktrees", "run-noop")
	if _, err := os.Stat(wtPath); err != nil {
		t.Fatalf("root worktree was dropped on a no-op turn: %v", err)
	}
	if got := gitT(t, env.repo, "rev-parse", "refs/heads/"+branch); got != c1 {
		t.Fatalf("root branch head = %s, want %s", got, c1)
	}
	gh.mu.Lock()
	comments := len(gh.comments)
	gh.mu.Unlock()
	if comments != 0 {
		t.Fatalf("no-op turn posted %d PR comments", comments)
	}
}

func TestTurnReportsOnlyItsOwnChanges(t *testing.T) {
	env := startJobRunner(t, nil)
	env.hub.hello()

	env.hub.offer(globalTestJob("run-own1", "edit: turn 1"))
	env.hub.wait("run-own1", runnerwire.TypeFinish)
	c1 := env.hub.wait("run-own1", runnerwire.TypeChanges).Changes

	env.hub.offer(followUpTestJob("run-own2", "run-own1", "run-own1", "continue: turn 2 adds more.txt only"))
	env.hub.wait("run-own2", runnerwire.TypeFinish)
	c2 := env.hub.wait("run-own2", runnerwire.TypeChanges).Changes

	if c2.Base != c1.Commit || len(c2.Files) != 1 || c2.Files[0].Path != "more.txt" {
		t.Fatalf("turn 2 changes = %+v, want Base=%s and only more.txt", c2, c1.Commit)
	}
	if strings.Contains(c2.Patch, "hello.txt") {
		t.Errorf("turn 2 patch includes turn 1's hello.txt:\n%s", c2.Patch)
	}
}

func TestPRWatchKeepsFolderWhileTurnRuns(t *testing.T) {
	const branch = "agent/tidy-readme-run-pw"
	gh := newFollowUpGH("https://github.com/o/r/pull/24", branch, "main")
	env := startFollowUpPRRunner(t, gh, nil)
	env.hub.hello()

	first := globalTestJob("run-pw", "edit: turn 1")
	first.NoPR = false
	env.hub.offer(first)
	env.hub.wait("run-pw", runnerwire.TypeFinish)
	wtPath := filepath.Join(env.client.Config.DataDir, "worktrees", "run-pw")

	// Start turn 2 that stays running.
	second := followUpTestJob("run-pw-2", "run-pw", "run-pw", "hang: turn 2")
	second.NoPR = false
	env.hub.offer(second)
	env.hub.wait("run-pw-2", runnerwire.TypeAccept)
	env.recorded(t, "run-pw-2", "child-pid")

	// While turn 2 runs, the PR becomes MERGED and the watcher polls.
	gh.mu.Lock()
	gh.state = "MERGED"
	gh.mu.Unlock()
	env.client.jobs.pollPRs()

	if _, err := os.Stat(wtPath); err != nil {
		t.Fatalf("worktree removed while turn 2 was still running: %v", err)
	}

	// Cancel turn 2 so it ends; now the merged PR's worktree is removed.
	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeCancel, RunID: "run-pw-2", Reason: "stop"})
	env.hub.wait("run-pw-2", runnerwire.TypeFinish)
	waitDead(t, env.jobEnv, "run-pw-2")

	deadline := time.Now().Add(5 * time.Second)
	for worktreeExists(wtPath) && time.Now().Before(deadline) {
		env.client.jobs.pollPRs()
		time.Sleep(10 * time.Millisecond)
	}
	if worktreeExists(wtPath) {
		t.Fatal("worktree still exists after turn 2 ended on a merged PR")
	}
}

func TestMergeJobRefusedWhileTurnActive(t *testing.T) {
	env := newMergeEnv(t, nil)
	env.j.mu.Lock()
	env.j.runs["run-turn-2"] = &run{
		job:      runnerwire.Job{RunID: "run-turn-2", ContinueOf: "run-m", WorkRoot: "run-m", Global: true},
		workRoot: "run-m",
	}
	env.j.mu.Unlock()

	msgs := env.answer(t, env.j, "m-active", mergeHead)
	res := mergeResultOf(t, msgs, "m-active")
	if res.Error == "" || !strings.Contains(strings.ToLower(res.Error), "running") {
		t.Fatalf("merge while turn active = %+v, want error mentioning running turn", res)
	}
	if calls := env.gh.mergeCalls(); len(calls) != 0 {
		t.Fatalf("gh pr merge called while turn active: %v", calls)
	}
	if !worktreeExists(env.wt.Path) {
		t.Fatal("worktree removed after refused merge")
	}
}

func TestDescribeOfferNamesFollowUp(t *testing.T) {
	job := followUpTestJob("run-2", "run-1", "run-1", "make the button bigger")
	text := describeOffer(Offer{Job: job, Repo: runnerwire.Repo{Name: "demo", Path: "/src/demo"}})
	if !strings.Contains(text, "Follow-up of run run-1 in the same folder") {
		t.Errorf("describeOffer missing follow-up line:\n%s", text)
	}
	if !strings.Contains(text, "branch:       agent/tidy-readme-run-1") {
		t.Errorf("describeOffer missing root branch:\n%s", text)
	}

	job.ContinuePR = "https://github.com/o/r/pull/9"
	job.NoPR = false
	withPR := describeOffer(Offer{Job: job, Repo: runnerwire.Repo{Name: "demo", Path: "/src/demo"}})
	for _, want := range []string{"Follow-up of run run-1 in the same folder", "updates PR https://github.com/o/r/pull/9"} {
		if !strings.Contains(withPR, want) {
			t.Errorf("describeOffer with PR missing %q:\n%s", want, withPR)
		}
	}
}

func TestAcceptReportsViewOnly(t *testing.T) {
	// Interactive run where remote typing is NOT allowed -> Accept.ViewOnly == true.
	envViewOnly, startedVO := startInteractiveRunner(t, func(c *Client) {
		c.Accepter = acceptFunc(func(context.Context, Offer) (bool, string) { return true, "" })
	})
	envViewOnly.hub.hello()
	jobVO := globalTestJob("run-vo", "ok: view only")
	jobVO.Mode = runnerwire.ModeInteractive
	envViewOnly.hub.offer(jobVO)
	stVO := waitStarted(t, startedVO)
	acceptVO := envViewOnly.hub.wait("run-vo", runnerwire.TypeAccept).Accept
	if !acceptVO.ViewOnly {
		t.Errorf("interactive run without remote typing: Accept = %+v, want ViewOnly=true", acceptVO)
	}
	stVO.term.exit <- 0
	envViewOnly.hub.wait("run-vo", runnerwire.TypeFinish)

	// Interactive run where remote typing IS allowed -> Accept.ViewOnly == false.
	envTyping, startedTy := startInteractiveRunner(t, func(c *Client) {
		c.Config.AutoAccept = &runnerwire.AutoAcceptPolicy{
			Enabled:           true,
			Repos:             []string{"r1"},
			Agents:            []string{"claude"},
			Modes:             []string{runnerwire.ModeInteractive},
			AllowRemoteTyping: true,
		}
	})
	envTyping.hub.hello()
	jobTy := globalTestJob("run-ty", "ok: can type")
	jobTy.Mode = runnerwire.ModeInteractive
	envTyping.hub.offer(jobTy)
	stTy := waitStarted(t, startedTy)
	acceptTy := envTyping.hub.wait("run-ty", runnerwire.TypeAccept).Accept
	if acceptTy.ViewOnly {
		t.Errorf("interactive run with remote typing: Accept = %+v, want ViewOnly=false", acceptTy)
	}
	stTy.term.exit <- 0
	envTyping.hub.wait("run-ty", runnerwire.TypeFinish)
}

// A follow-up whose folder is gone but whose branch still holds the earlier
// turns' commits reuses that branch; a decline, a cancel or a runner stop
// before the turn starts removes only the folder it put back, never the
// branch it did not make.
func TestFollowUpKeepsAnExistingBranchWhenDeclined(t *testing.T) {
	hermeticGit(t)
	env := startJobRunner(t, func(c *Client) {
		c.Accepter = acceptFunc(func(_ context.Context, o Offer) (bool, string) {
			if o.Job.RunID == "run-kb2" {
				return false, ReasonDeclined
			}
			return true, ""
		})
	})
	env.hub.hello()

	env.hub.offer(globalTestJob("run-kb1", "edit: turn 1"))
	if res := env.hub.wait("run-kb1", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
		t.Fatalf("turn 1 = %+v", res)
	}
	const branch = "agent/tidy-readme-run-kb1"
	c1 := gitT(t, env.repo, "rev-parse", "refs/heads/"+branch)
	wtPath := filepath.Join(env.client.Config.DataDir, "worktrees", "run-kb1")
	if err := removeWorktree(context.Background(), env.repo, Worktree{Path: wtPath, Branch: branch}, false); err != nil {
		t.Fatal(err)
	}

	env.hub.offer(followUpTestJob("run-kb2", "run-kb1", "run-kb1", "continue: turn 2"))
	env.hub.wait("run-kb2", runnerwire.TypeDecline)

	if got, err := git(context.Background(), env.repo, "rev-parse", "--verify", "-q", "refs/heads/"+branch); err != nil || strings.TrimSpace(got) != c1 {
		t.Fatalf("branch %s after a declined follow-up = %q, %v; want %s", branch, strings.TrimSpace(got), err, c1)
	}
	if _, err := os.Stat(wtPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the folder put back for the declined turn is still there: %v", err)
	}
}

// A follow-up whose recorded head names commits on a branch that is gone is
// declined (branch_moved), not silently started again from the base.
func TestFollowUpDeclinesWhenItsBranchIsGone(t *testing.T) {
	hermeticGit(t)
	env := startJobRunner(t, nil)
	env.hub.hello()

	env.hub.offer(globalTestJob("run-bg1", "edit: turn 1"))
	if res := env.hub.wait("run-bg1", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
		t.Fatalf("turn 1 = %+v", res)
	}
	const branch = "agent/tidy-readme-run-bg1"
	wtPath := filepath.Join(env.client.Config.DataDir, "worktrees", "run-bg1")
	if err := removeWorktree(context.Background(), env.repo, Worktree{Path: wtPath, Branch: branch}, true); err != nil {
		t.Fatal(err)
	}

	env.hub.offer(followUpTestJob("run-bg2", "run-bg1", "run-bg1", "continue: turn 2"))
	msg := env.hub.wait("run-bg2", runnerwire.TypeDecline)
	if msg.Code != runnerwire.DeclineBranchMoved {
		t.Fatalf("decline = %+v, want code %q", msg, runnerwire.DeclineBranchMoved)
	}
	if _, err := os.Stat(wtPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the folder was re-created from the base")
	}
}

// A PR merged or closed while a turn ran gets no push and no comment: the
// turn's commits stay on the local branch and the turn reports the PR's end.
func TestTurnDoesNotPushToAPRThatEndedDuringTheTurn(t *testing.T) {
	for _, state := range []string{"MERGED", "CLOSED"} {
		t.Run(state, func(t *testing.T) {
			id := "run-end" + strings.ToLower(state[:1])
			branch := "agent/tidy-readme-" + id
			gh := newFollowUpGH("https://github.com/o/r/pull/31", branch, "main")
			env := startFollowUpPRRunner(t, gh, nil)
			env.hub.hello()

			first := globalTestJob(id, "edit: turn 1")
			first.NoPR = false
			env.hub.offer(first)
			if res := env.hub.wait(id, runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
				t.Fatalf("turn 1 = %+v", res)
			}
			c1 := gitT(t, env.bare, "rev-parse", "refs/heads/"+branch)

			// The pre-turn look sees it open; it ends right after.
			gh.mu.Lock()
			gh.endAfterView = state
			gh.mu.Unlock()
			second := followUpTestJob(id+"-2", id, id, "continue: turn 2")
			second.NoPR = false
			env.hub.offer(second)
			res := env.hub.wait(id+"-2", runnerwire.TypeFinish).Result
			if res.Status != runnerwire.RunSucceeded || res.PushRefused {
				t.Fatalf("turn 2 = %+v, want succeeded without pushRefused", res)
			}
			if got := gitT(t, env.bare, "rev-parse", "refs/heads/"+branch); got != c1 {
				t.Fatalf("origin moved to %s after the PR ended; want %s", got, c1)
			}
			local := gitT(t, env.repo, "rev-parse", "refs/heads/"+branch)
			if local == c1 || gitT(t, env.repo, "rev-parse", local+"^") != c1 {
				t.Fatalf("local branch at %s, want turn 2's commit on top of %s", local, c1)
			}
			gh.mu.Lock()
			comments := len(gh.comments)
			gh.mu.Unlock()
			if comments != 0 {
				t.Fatalf("posted %d PR comments on a PR that ended", comments)
			}
			want := runnerwire.PRMerged
			if state == "CLOSED" {
				want = runnerwire.PRClosed
			}
			if pr := env.hub.wait(id+"-2", runnerwire.TypePR).PR; pr == nil || pr.State != want || pr.URL != gh.url {
				t.Fatalf("turn 2 PR = %+v, want state %s", pr, want)
			}
			if !hubHasEvent(env.jobEnv, id+"-2", "not pushed") {
				t.Errorf("no log line says the turn was not pushed: %+v", env.hub.messages(id+"-2"))
			}
			entry, _, _ := lookupWork(env.client.Config.DataDir, id)
			if entry.Head != local || entry.PushedHead != c1 {
				t.Fatalf("work entry = %+v, want head=%s pushedHead=%s", entry, local, c1)
			}
		})
	}
}

// prepareContinue records the turn's work root and job fields under j.mu:
// other goroutines (offers, the PR watcher, merges) read them under it.
//
// Without the race detector: the test holds j.mu while prepareContinue runs
// and lets go only while it is parked waiting for j.mu, taking it straight
// back. A field that changes while the test holds the lock was written
// without it. The first write (the work root) comes before prepareContinue
// takes the lock at all, so code that wrote it unlocked fails every time. A
// later unlocked write is caught only when it lands while the test holds
// the lock again (about half the runs for the last batch of fields, which
// follows a short locked read); go test -race catches those every time.
func TestContinueWritesRunFieldsUnderTheLock(t *testing.T) {
	env := startJobRunner(t, nil)
	env.hub.hello()

	env.hub.offer(globalTestJob("run-mu1", "edit: turn 1"))
	if res := env.hub.wait("run-mu1", runnerwire.TypeFinish).Result; res.Status != runnerwire.RunSucceeded {
		t.Fatalf("turn 1 = %+v", res)
	}

	j := env.client.jobs
	cfg := env.client.cfg()
	job := followUpTestJob("run-mu2", "run-mu1", "run-mu1", "continue: turn 2")
	job.WorkRoot = "" // prepareContinue fills it in
	r := &run{job: job, cfg: cfg}
	for _, repo := range cfg.Repos {
		if repo.ID == job.RepoID {
			r.repo = repo
		}
	}
	type fields struct{ workRoot, jobWorkRoot, base, pr, task, summary, resume string }
	// Only with j.mu held.
	snapshot := func() fields {
		return fields{r.workRoot, r.job.WorkRoot, r.job.Base, r.job.ContinuePR,
			r.job.ContinueTask, r.job.ContinueSummary, r.resumeSession}
	}

	var code, reason string
	done := make(chan struct{})
	j.mu.Lock()
	start := snapshot()
	go func() {
		defer close(done)
		_, _, _, code, reason = j.prepareContinue(context.Background(), r)
	}()
	for {
		before := snapshot()
		finished := waitParkedOnLock(t, "(*jobs).prepareContinue(", done)
		after := snapshot()
		j.mu.Unlock()
		if after != before {
			<-done
			t.Fatalf("prepareContinue changed %+v to %+v while the test held j.mu", before, after)
		}
		if finished {
			break
		}
		j.mu.Lock()
	}
	if reason != "" {
		t.Fatalf("prepareContinue declined: %s %s", code, reason)
	}
	j.mu.Lock()
	end := snapshot()
	j.mu.Unlock()
	if end.workRoot != "run-mu1" || end.jobWorkRoot != "run-mu1" || end.base == start.base {
		t.Fatalf("fields after prepareContinue = %+v (before %+v): nothing to check", end, start)
	}
}

// waitParkedOnLock waits until the goroutine running fn (a function name
// as a stack shows it) is blocked acquiring a sync.Mutex, or done is closed;
// it says which.
func waitParkedOnLock(t *testing.T, fn string, done chan struct{}) (finished bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	buf := make([]byte, 1<<20)
	for {
		select {
		case <-done:
			return true
		default:
		}
		n := runtime.Stack(buf, true)
		for _, g := range strings.Split(string(buf[:n]), "\n\n") {
			header, _, _ := strings.Cut(g, "\n")
			if strings.Contains(header, "[sync.Mutex.Lock") && strings.Contains(g, fn) {
				return false
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s neither waited for a lock nor returned in 20s", fn)
		}
		time.Sleep(100 * time.Microsecond)
	}
}
