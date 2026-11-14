package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"nodevas/internal/runnerwire"
)

// Workspace actions (runnerwire/workspace.go): the runner's owner, from the
// hub's agent workspace, merges a run's branch into its base on this machine
// or discards the run's worktree and branch. The runner decides from its own
// checks, never from the request: only its owner may ask, only while
// workspaceActions is on, never for a run it still holds, only on the
// branch this run made, at exactly the commit the owner saw.
//
// A local merge never touches a checkout with uncommitted changes. When the
// base is checked out in the registered repo and that checkout is clean, it
// merges there with --no-ff; when the base is checked out nowhere, it merges
// in a temporary worktree it then removes; anywhere else it refuses. On a
// conflict the merge is aborted and everything is left as it was. Nothing is
// ever pushed.
//
// Each request is answered once with workspace_result; the answer is kept a
// day in <dataDir>/workspace-actions.json, so a request the hub sends again
// after a reconnect (or a runner restart) gets the same answer and never a
// second action.

// workspaceRecord is one answered workspace action.
type workspaceRecord struct {
	ID      string                      `json:"id"`
	RunID   string                      `json:"runId"`
	Action  string                      `json:"action"`
	Outcome runnerwire.WorkspaceOutcome `json:"outcome"`
	At      time.Time                   `json:"at"`
}

type workspaceActionsFile struct {
	Actions []workspaceRecord `json:"actions"`
}

const (
	workspaceActionsFileName = "workspace-actions.json"
	// workspaceMergeTimeout and workspaceDiscardTimeout bound one action: a
	// merge may wait on a signing prompt, a discard on gh.
	workspaceMergeTimeout   = 5 * time.Minute
	workspaceDiscardTimeout = 3 * time.Minute
	// maxConflictPaths is how many conflicting files a refusal names.
	maxConflictPaths = 20
)

// loadWorkspaceActions reads workspace-actions.json, dropping answers older
// than a day.
func (j *jobs) loadWorkspaceActions() {
	dir, err := j.c.cfg().RunDataDir()
	if err != nil {
		return
	}
	path := filepath.Join(dir, workspaceActionsFileName)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	var file workspaceActionsFile
	if err == nil {
		err = json.Unmarshal(raw, &file)
	}
	if err != nil {
		j.c.logf("workspace actions: cannot read %s: %v", path, err)
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, rec := range file.Actions {
		if rec.ID != "" {
			j.workspaceRecords[rec.ID] = rec
		}
	}
	j.pruneWorkspaceLocked(time.Now())
}

// pruneWorkspaceLocked forgets answers older than mergeMemory, and the
// oldest beyond maxMergeRecords.
func (j *jobs) pruneWorkspaceLocked(now time.Time) {
	for id, rec := range j.workspaceRecords {
		if now.Sub(rec.At) > mergeMemory {
			delete(j.workspaceRecords, id)
		}
	}
	if over := len(j.workspaceRecords) - maxMergeRecords; over > 0 {
		for _, rec := range j.sortedWorkspaceLocked()[:over] {
			delete(j.workspaceRecords, rec.ID)
		}
	}
}

func (j *jobs) sortedWorkspaceLocked() []workspaceRecord {
	list := make([]workspaceRecord, 0, len(j.workspaceRecords))
	for _, rec := range j.workspaceRecords {
		list = append(list, rec)
	}
	sort.Slice(list, func(a, b int) bool {
		if !list[a].At.Equal(list[b].At) {
			return list[a].At.Before(list[b].At)
		}
		return list[a].ID < list[b].ID
	})
	return list
}

func (j *jobs) saveWorkspaceLocked() {
	dir, err := j.c.cfg().RunDataDir()
	if err != nil {
		return
	}
	file := workspaceActionsFile{Actions: j.sortedWorkspaceLocked()}
	if err := writeFileAtomic(filepath.Join(dir, workspaceActionsFileName), file); err != nil {
		j.c.logf("workspace actions: cannot save %s: %v", workspaceActionsFileName, err)
	}
}

// workspaceAction handles a TypeWorkspaceAction from the hub, for the runner
// whose owner is ownerID. It never blocks the connection: the work is on its
// own goroutine, and the answer is queued like every other message.
func (j *jobs) workspaceAction(ownerID string, m *runnerwire.WorkspaceActionJob) {
	if m == nil {
		return
	}
	job := *m
	if job.ID == "" || len(job.ID) > maxMergeIDLen || job.RunID == "" {
		j.c.logf("workspace action without a usable id or run; ignored")
		return
	}
	j.mu.Lock()
	if j.workspaceInFlight[job.ID] {
		j.mu.Unlock()
		j.c.logf("run %s: workspace action %s sent again while in hand; ignored", job.RunID, job.ID)
		return
	}
	j.workspaceInFlight[job.ID] = true
	j.wg.Add(1)
	j.mu.Unlock()
	go func() {
		defer j.wg.Done()
		j.runWorkspaceAction(ownerID, job)
	}()
}

// runWorkspaceAction answers one request: from memory when it was answered
// before, otherwise by checking and acting.
func (j *jobs) runWorkspaceAction(ownerID string, job runnerwire.WorkspaceActionJob) {
	defer func() {
		j.mu.Lock()
		delete(j.workspaceInFlight, job.ID)
		j.mu.Unlock()
	}()
	// PR merges and workspace actions never overlap on one runner.
	j.mergeMu.Lock()
	defer j.mergeMu.Unlock()

	j.mu.Lock()
	rec, answered := j.workspaceRecords[job.ID]
	j.mu.Unlock()
	if answered {
		j.c.logf("run %s: workspace action %s sent again; answering as before (%s)", job.RunID, job.ID, workspaceText(rec.Outcome))
		j.queueWorkspaceResult(rec.Outcome)
		return
	}
	j.c.logf("run %s: workspace action %s: %s asked for", job.RunID, job.ID, oneLine(job.Action))
	outcome := j.doWorkspaceAction(ownerID, job)
	if j.ctx.Err() != nil {
		// The runner is stopping; git may have been killed half way. Not
		// answered, so the hub sends it again and a fresh look decides.
		j.c.logf("run %s: workspace action %s interrupted; not answered", job.RunID, job.ID)
		return
	}
	outcome.ID, outcome.RunID, outcome.Action = job.ID, job.RunID, job.Action
	outcome.Error = workspaceReason(outcome.Error)
	outcome.Note = workspaceReason(outcome.Note)
	j.mu.Lock()
	j.workspaceRecords[job.ID] = workspaceRecord{ID: job.ID, RunID: job.RunID, Action: job.Action, Outcome: outcome, At: time.Now().UTC()}
	j.pruneWorkspaceLocked(time.Now())
	j.saveWorkspaceLocked()
	j.mu.Unlock()
	j.queueWorkspaceResult(outcome)
	j.c.logf("run %s: workspace action %s: %s", job.RunID, job.ID, workspaceText(outcome))
}

func workspaceText(o runnerwire.WorkspaceOutcome) string {
	if o.Code == "" {
		if o.Note != "" {
			return "done; " + firstLine(o.Note)
		}
		return "done"
	}
	return "refused (" + o.Code + "): " + firstLine(o.Error)
}

// workspaceReason is text for the hub: valid UTF-8, at most
// MaxWorkspaceReason bytes.
func workspaceReason(s string) string {
	s = strings.TrimSpace(strings.ToValidUTF8(s, ""))
	if len(s) > runnerwire.MaxWorkspaceReason {
		s = strings.ToValidUTF8(s[:runnerwire.MaxWorkspaceReason], "")
	}
	return s
}

func (j *jobs) queueWorkspaceResult(outcome runnerwire.WorkspaceOutcome) {
	j.mu.Lock()
	j.workspaceResults = append(j.workspaceResults, runnerwire.Envelope{Type: runnerwire.TypeWorkspaceResult,
		RunID: outcome.RunID, WorkspaceResult: &outcome})
	j.mu.Unlock()
	j.wake()
}

// pendingWorkspaceResults is how many answers wait for the connection, for
// tests.
func (j *jobs) pendingWorkspaceResults() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.workspaceResults)
}

func refusedWith(code, format string, args ...any) runnerwire.WorkspaceOutcome {
	return runnerwire.WorkspaceOutcome{Code: code, Error: fmt.Sprintf(format, args...)}
}

// doWorkspaceAction runs the checks every action shares, then the action.
func (j *jobs) doWorkspaceAction(ownerID string, job runnerwire.WorkspaceActionJob) runnerwire.WorkspaceOutcome {
	cfg := j.c.cfg()
	if ownerID == "" || job.AccountID != ownerID {
		return refusedWith(runnerwire.WorkspaceErrRefused, "only this runner's owner may merge or discard its runs")
	}
	if !cfg.WorkspaceActionsOn() {
		return refusedWith(runnerwire.WorkspaceErrOff, "workspace actions are off on this runner (workspaceActions)")
	}
	if !validRunID.MatchString(job.RunID) {
		return refusedWith(runnerwire.WorkspaceErrRefused, "run id is not usable on this runner")
	}
	if job.Action != runnerwire.WorkspaceMergeLocal && job.Action != runnerwire.WorkspaceDiscard {
		return refusedWith(runnerwire.WorkspaceErrRefused, "unknown workspace action %q", job.Action)
	}
	repo, err := repoRoot(cfg, job.RepoID)
	if err != nil {
		return refusedWith(runnerwire.WorkspaceErrRefused, "repository %q is not on this runner", job.RepoID)
	}
	j.mu.Lock()
	r := j.runs[job.RunID]
	running := r != nil && r.finish == nil
	j.mu.Unlock()
	if running {
		return refusedWith(runnerwire.WorkspaceErrRunning, "this runner is still running the run")
	}
	if other := j.sharedWith(job); other != "" {
		return refusedWith(runnerwire.WorkspaceErrRunning, "run %s on this runner is still working on %s or its pull request", other, job.Branch)
	}
	dataDir, err := cfg.RunDataDir()
	if err != nil {
		return refusedWith(runnerwire.WorkspaceErrGit, "%v", err)
	}
	if job.Action == runnerwire.WorkspaceMergeLocal {
		ctx, cancel := context.WithTimeout(j.ctx, workspaceMergeTimeout)
		defer cancel()
		return j.mergeRunLocal(ctx, job, repo, dataDir)
	}
	ctx, cancel := context.WithTimeout(j.ctx, workspaceDiscardTimeout)
	defer cancel()
	return j.discardRun(ctx, job, repo, dataDir)
}

// sharedWith names another run this runner still holds that works on the
// job's branch or on the PR it watches for the run (a revise or review run),
// or "": closing that PR or deleting that branch would pull it out from
// under that run.
func (j *jobs) sharedWith(job runnerwire.WorkspaceActionJob) string {
	j.mu.Lock()
	defer j.mu.Unlock()
	pr := ""
	if w := j.watched[job.RunID]; w != nil {
		pr = w.URL
	}
	for id, r := range j.runs {
		if id == job.RunID || r.finish != nil {
			continue
		}
		if (job.Branch != "" && r.job.Branch == job.Branch) || (pr != "" && r.job.PRURL == pr) {
			return id
		}
	}
	return ""
}

// openWatch is the run's watched PR while it is open, or nil.
func (j *jobs) openWatch(runID string) *watchedPR {
	j.mu.Lock()
	defer j.mu.Unlock()
	w := j.watched[runID]
	if w == nil || w.closing || (w.PR.State != "" && w.PR.State != runnerwire.PROpen) {
		return nil
	}
	copied := *w
	return &copied
}

// discardRun removes a run's worktree and its own branch, closing its PR
// first. Everything that could refuse without changing anything is checked
// before the PR is closed; if gh fails, nothing is removed and asking again
// is safe. A run with nothing left is done.
func (j *jobs) discardRun(ctx context.Context, job runnerwire.WorkspaceActionJob, repo, dataDir string) runnerwire.WorkspaceOutcome {
	var out runnerwire.WorkspaceOutcome
	// After the PR was closed a refusal still says so: the hub records what
	// happened on GitHub too.
	refuse := func(o runnerwire.WorkspaceOutcome) runnerwire.WorkspaceOutcome {
		if out.PRClosed {
			o.PRClosed = true
			o.Error = "the pull request was closed and its GitHub branch deleted, but the worktree was not removed: " + o.Error
		}
		return o
	}
	plain := filepath.Join(dataDir, "worktrees", job.RunID)
	_, statErr := os.Lstat(plain)
	hadWorktree := statErr == nil

	wt, err := findRunWorktree(ctx, dataDir, repo, job.RunID)
	if err != nil && !wt.Link {
		return refusedWith(runnerwire.WorkspaceErrForeign, "%v; nothing was removed", err)
	}
	dropBranch := ownBranch(job) && checkBase(job.Branch) == nil
	if dropBranch {
		if why := unsavedWork(ctx, repo, job, wt); why != "" {
			return refusedWith(runnerwire.WorkspaceErrUnsaved, "%s; nothing was removed", why)
		}
	}

	if w := j.openWatch(job.RunID); w != nil {
		if ownBranch(job) && w.Branch == job.Branch {
			gh, err := j.c.gh()
			if err != nil {
				return refusedWith(runnerwire.WorkspaceErrPRClose, "%v; nothing was removed", err)
			}
			// In the data directory, outside any checkout: gh touches only
			// GitHub.
			if err := gh.closePR(ctx, dataDir, w.URL, true); err != nil {
				return refusedWith(runnerwire.WorkspaceErrPRClose, "%s; nothing was removed", mergeError(err))
			}
			info := w.PR
			info.State = runnerwire.PRClosed
			// The watcher ends the watch (and its siblings') and tells the
			// hub the PR is closed; it removes the worktree it knows of.
			j.updatePR(*w, info)
			out.PRClosed = true
		} else {
			// A revise or review run's entry: the PR is the implement
			// run's, never closed from here.
			j.unwatch(job.RunID)
		}
	}

	// Looked at again: the watcher may have removed it meanwhile.
	wt, err = findRunWorktree(ctx, dataDir, repo, job.RunID)
	switch {
	case wt.Link:
		// Only the link goes; whatever it points at is not the run's.
		if rmErr := os.Remove(wt.Path); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			return refuse(busyOr(rmErr, "removing the link where the run's worktree was"))
		}
	case err != nil:
		return refuse(refusedWith(runnerwire.WorkspaceErrForeign, "%v", err))
	case wt.Exists:
		// Not registered with the repo, it is still a plain directory under
		// this runner's worktrees, named by a validated run id.
		if rmErr := removeRunTree(ctx, repo, wt.Path, wt.Listed); rmErr != nil {
			return refuse(busyOr(rmErr, "removing the run's worktree"))
		}
	}
	_, _ = git(ctx, repo, "worktree", "prune")
	if _, statErr := os.Lstat(wt.Path); statErr == nil {
		return refuse(refusedWith(runnerwire.WorkspaceErrFilesBusy, "the run's worktree is still there after removing it"))
	}
	out.WorktreeRemoved = hadWorktree

	if dropBranch && refExists(ctx, repo, "refs/heads/"+job.Branch) {
		// Once more, right before: the owner may have committed meanwhile.
		if why := unsavedWork(ctx, repo, job, runWorktree{}); why != "" {
			out.Code, out.Error = runnerwire.WorkspaceErrUnsaved, why+"; the worktree is gone but the branch stays"
			return out
		}
		if _, err := git(ctx, repo, "branch", "-D", job.Branch); err != nil {
			out.Code, out.Error = runnerwire.WorkspaceErrGit, "the worktree is gone but its branch is not: "+err.Error()
			return out
		}
		out.BranchDeleted = true
	}
	if err := DropKept(ctx, dataDir, plain); err != nil {
		out.Note = "could not update " + keptFile + ": " + err.Error()
	}
	// A handoff branch (nodevas/handoff/<run>) stays: a queued fallback run
	// may continue from it, and it expires on its own.
	if !out.PRClosed && !out.WorktreeRemoved && !out.BranchDeleted && out.Note == "" {
		out.Note = "nothing left to remove"
	}
	return out
}

// unsavedWork says why discarding the run's own branch (and its worktree's
// HEAD) would lose commits it did not make, or "". A commit may go when it
// is the run's commit as the hub has it (job.HeadSHA), or when everything up
// to it is on some other ref: commits the owner added on top and kept
// nowhere else stop the discard, since branch -D would leave them reachable
// by id alone.
func unsavedWork(ctx context.Context, repo string, job runnerwire.WorkspaceActionJob, wt runWorktree) string {
	tip, err := revParse(ctx, repo, "refs/heads/"+job.Branch)
	if err == nil && !droppable(ctx, repo, job, tip) {
		return fmt.Sprintf("%s is at %s, past the run's commit %s, with commits found on no other branch; "+
			"merge or delete them yourself first", job.Branch, short(tip), shortOr(job.HeadSHA))
	}
	if wt.Exists && wt.Listed && wt.Head != "" && !strings.EqualFold(wt.Head, tip) && !droppable(ctx, repo, job, wt.Head) {
		return fmt.Sprintf("the run's worktree is at %s, with commits found on no branch; "+
			"keep them on a branch first", short(wt.Head))
	}
	return ""
}

// droppable reports whether commit may go with the run's branch: it is the
// run's commit, or no commit up to it is only on that branch. Other
// worktrees' HEADs do not count (--single-worktree), the run's own among
// them.
func droppable(ctx context.Context, repo string, job runnerwire.WorkspaceActionJob, commit string) bool {
	if job.HeadSHA != "" && strings.EqualFold(commit, job.HeadSHA) {
		return true
	}
	if strings.ContainsAny(job.Branch, "*?[\\") {
		// --exclude takes a glob; such a name never comes from BranchName.
		return false
	}
	out, err := git(ctx, repo, "rev-list", "-n1", commit, "--not", "--single-worktree",
		"--exclude=refs/heads/"+job.Branch, "--all")
	return err == nil && strings.TrimSpace(out) == ""
}

func shortOr(sha string) string {
	if sha == "" {
		return "(none)"
	}
	return short(sha)
}

// removeRunTree removes a worktree of this runner's: its links first
// (unlinkTree, so nothing they point at goes with it), then git's own
// removal when the repo lists it, else the directory.
func removeRunTree(ctx context.Context, repo, path string, listed bool) error {
	if err := unlinkTree(path); err != nil {
		return err
	}
	if listed {
		if _, err := git(ctx, repo, "worktree", "remove", "--force", path); err == nil {
			return nil
		}
	}
	return os.RemoveAll(path)
}

// busyOr is a removal that failed: files_busy when another program holds
// files there (Windows), otherwise git.
func busyOr(err error, what string) runnerwire.WorkspaceOutcome {
	msg := strings.ToLower(err.Error())
	for _, sign := range []string{"used by another process", "access is denied", "permission denied", "resource busy", "sharing violation"} {
		if strings.Contains(msg, sign) {
			return refusedWith(runnerwire.WorkspaceErrFilesBusy, "%s: %v", what, err)
		}
	}
	return refusedWith(runnerwire.WorkspaceErrGit, "%s: %v", what, err)
}

// mergeRunLocal merges the run's own branch into its local base on this
// machine, then removes the run's worktree and branch. See the top of the
// file for where it merges and when it refuses.
func (j *jobs) mergeRunLocal(ctx context.Context, job runnerwire.WorkspaceActionJob, repo, dataDir string) runnerwire.WorkspaceOutcome {
	if !ownBranch(job) || checkBase(job.Branch) != nil {
		return refusedWith(runnerwire.WorkspaceErrNotOwnBranch, "%s is not the branch run %s made", job.Branch, job.RunID)
	}
	if w := j.openWatch(job.RunID); w != nil {
		return refusedWith(runnerwire.WorkspaceErrHasPR, "the run has an open pull request (%s); merge it there", w.URL)
	}
	if job.Base == "" || checkBase(job.Base) != nil || !refExists(ctx, repo, "refs/heads/"+job.Base) {
		// A local base is never made from origin's: that is not a merge
		// the owner looked at.
		return refusedWith(runnerwire.WorkspaceErrNoBase, "there is no local branch %q", job.Base)
	}
	tip, err := revParse(ctx, repo, "refs/heads/"+job.Branch)
	if err != nil {
		return refusedWith(runnerwire.WorkspaceErrNoBranch, "the run's branch %s is gone", job.Branch)
	}
	if job.HeadSHA == "" || !strings.EqualFold(tip, job.HeadSHA) {
		return refusedWith(runnerwire.WorkspaceErrHeadMoved, "%s is at %s, not %s", job.Branch, short(tip), short(job.HeadSHA))
	}

	wt, err := findRunWorktree(ctx, dataDir, repo, job.RunID)
	if err != nil {
		return refusedWith(runnerwire.WorkspaceErrForeign, "%v", err)
	}
	if wt.Exists {
		if !wt.Listed || !wt.GitFile || wt.Branch != job.Branch {
			return refusedWith(runnerwire.WorkspaceErrForeign, "the run's worktree is not on %s as the run left it", job.Branch)
		}
		if !strings.EqualFold(wt.Head, tip) {
			return refusedWith(runnerwire.WorkspaceErrHeadMoved, "the run's worktree is at %s, not %s", short(wt.Head), short(tip))
		}
		// The agent's tree: read with its filters emptied. Withheld agent
		// config counts: removing the worktree would lose it.
		out, _, err := repoGit(ctx, wt.Path, 4<<20, "status", "--porcelain=v1", "-z", "--untracked-files=all",
			"--ignore-submodules=all", "--no-renames")
		if err != nil {
			return refusedWith(runnerwire.WorkspaceErrGit, "reading the run's worktree: %v", err)
		}
		if paths, n := statusPaths(out, maxKeptReasonPaths); n > 0 {
			return refusedWith(runnerwire.WorkspaceErrDirtyWorktree, "the run's worktree has uncommitted files: %s", listPaths(paths, n))
		}
	}

	if !isAncestor(ctx, repo, tip, "refs/heads/"+job.Base) {
		if code, why := j.mergeInto(ctx, job, repo, dataDir, tip); code != "" {
			return refusedWith(code, "%s", why)
		}
		if !isAncestor(ctx, repo, tip, "refs/heads/"+job.Base) {
			return refusedWith(runnerwire.WorkspaceErrGit, "the merge finished but %s does not contain %s", job.Base, short(tip))
		}
	}
	var out runnerwire.WorkspaceOutcome
	out.MergeCommit, _ = revParse(ctx, repo, "refs/heads/"+job.Base)

	// Merged: the run's worktree and branch go. A failure here keeps the
	// outcome done, with a note: the merge happened and must be recorded.
	var notes []string
	if wt.Exists {
		if err := removeRunTree(ctx, repo, wt.Path, true); err != nil {
			notes = append(notes, "could not remove the worktree: "+err.Error())
		}
		_, _ = git(ctx, repo, "worktree", "prune")
		if _, statErr := os.Lstat(wt.Path); errors.Is(statErr, fs.ErrNotExist) {
			out.WorktreeRemoved = true
		}
	}
	if refExists(ctx, repo, "refs/heads/"+job.Branch) {
		if _, err := git(ctx, repo, "branch", "-D", job.Branch); err != nil {
			notes = append(notes, "could not delete the branch: "+err.Error())
		} else {
			out.BranchDeleted = true
		}
	}
	if err := DropKept(ctx, dataDir, filepath.Join(dataDir, "worktrees", job.RunID)); err != nil {
		notes = append(notes, "could not update "+keptFile+": "+err.Error())
	}
	if len(notes) > 0 {
		out.Note = "merged; " + strings.Join(notes, "; ")
	}
	return out
}

// isAncestor reports whether commit is already in ref.
func isAncestor(ctx context.Context, repo, commit, ref string) bool {
	_, code, _, err := gitExit(ctx, repo, "merge-base", "--is-ancestor", commit, ref)
	return err == nil && code == 0
}

// mergeInto merges tip into the base where it is safe to: the repo's own
// checkout when it holds the base and is clean, else a temporary worktree
// when the base is checked out nowhere. code is "" once merged.
func (j *jobs) mergeInto(ctx context.Context, job runnerwire.WorkspaceActionJob, repo, dataDir, tip string) (code, why string) {
	if code, why := mergeDriverAt(ctx, repo); code != "" {
		return code, why
	}
	where, path := baseCheckout(ctx, repo, job.Base)
	switch where {
	case runnerwire.BaseCheckoutRepo:
		if reason := baseClean(ctx, repo); reason != "" {
			return runnerwire.WorkspaceErrDirtyBase, "your checkout of " + job.Base + " is not clean: " + reason
		}
		// The owner's own checkout: git runs as it always does there.
		return mergeAt(ctx, repo, job, tip, nil)
	case runnerwire.BaseCheckoutElsewhere:
		return runnerwire.WorkspaceErrBaseElsewhere, job.Base + " is checked out at " + path + "; merge there"
	case runnerwire.BaseCheckoutNone:
	default:
		return runnerwire.WorkspaceErrGit, "cannot list the repository's worktrees"
	}

	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return runnerwire.WorkspaceErrGit, err.Error()
	}
	tmp := filepath.Join(dataDir, "merge-tmp", job.RunID+"-"+hex.EncodeToString(suffix[:]))
	if err := os.MkdirAll(filepath.Dir(tmp), 0o700); err != nil {
		return runnerwire.WorkspaceErrGit, err.Error()
	}
	// Nobody's checkout: made without checking anything out, so no filter
	// runs before the overrides are known.
	if _, err := git(ctx, repo, "worktree", "add", "--quiet", "--no-checkout", "--", tmp, job.Base); err != nil {
		_ = os.RemoveAll(tmp)
		return runnerwire.WorkspaceErrGit, "making a temporary worktree: " + err.Error()
	}
	defer func() {
		cleanup := context.WithoutCancel(ctx)
		_ = removeRunTree(cleanup, repo, tmp, true)
		_, _ = git(cleanup, repo, "worktree", "prune")
	}()
	// The config is read where the checkout and the merge run: an include
	// for the base branch (includeIf onbranch:) or for this path (gitdir:)
	// applies there and not in the repo. With every filter it names emptied,
	// no merge driver, no signing program of its own and no recursion into
	// submodules (whose config an agent can write as well), nothing that
	// config (which an agent can write) names runs.
	if code, why := mergeDriverAt(ctx, tmp); code != "" {
		return code, why
	}
	if code, why := signingProgramAt(ctx, tmp); code != "" {
		return code, why
	}
	filters := append(emptyFilters(ctx, tmp), "-c", "submodule.recurse=false")
	if _, err := git(ctx, tmp, append(filters, "reset", "--hard", "--quiet")...); err != nil {
		return runnerwire.WorkspaceErrGit, "checking out the temporary worktree: " + err.Error()
	}
	return mergeAt(ctx, tmp, job, tip, filters)
}

// mergeDriverAt refuses a merge where git's config at dir names a merge
// driver: a driver is a command, and the repository's config is reachable
// from an agent's worktree (.git/config is shared).
func mergeDriverAt(ctx context.Context, dir string) (code, why string) {
	out, exit, _, err := gitExit(ctx, dir, "config", "--get-regexp", `^merge\..*\.driver$`)
	if err != nil {
		return runnerwire.WorkspaceErrGit, err.Error()
	}
	if exit == 0 && strings.TrimSpace(out) != "" {
		return runnerwire.WorkspaceErrMergeDriver, "the repository configures a merge driver; merge it yourself"
	}
	return "", ""
}

// signingProgramAt refuses a merge at dir that would sign its commit
// (commit.gpgSign) or check the branch's signatures (merge.verifySignatures)
// with a program the repository's own config names (gpg.program,
// gpg.<format>.program, gpg.ssh.defaultKeyCommand): like a merge driver,
// that is a command, and the config is reachable from an agent's worktree.
// A program set in the owner's global or system config is theirs, and is
// used as it is.
func signingProgramAt(ctx context.Context, dir string) (code, why string) {
	signs := false
	for _, key := range []string{"commit.gpgSign", "merge.verifySignatures"} {
		out, exit, _, err := gitExit(ctx, dir, "config", "--type=bool", "--get", key)
		if err != nil {
			return runnerwire.WorkspaceErrGit, err.Error()
		}
		signs = signs || (exit == 0 && strings.TrimSpace(out) == "true")
	}
	if !signs {
		return "", ""
	}
	out, exit, _, err := gitExit(ctx, dir, "config", "--show-scope", "--name-only", "-z", "--get-regexp", `^gpg\.`)
	if err != nil {
		return runnerwire.WorkspaceErrGit, err.Error()
	}
	if exit != 0 {
		return "", ""
	}
	fields := strings.Split(out, "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		scope, key := fields[i], strings.ToLower(fields[i+1])
		if scope == "global" || scope == "system" || scope == "command" {
			continue
		}
		if strings.HasSuffix(key, ".program") || key == "gpg.ssh.defaultkeycommand" {
			return runnerwire.WorkspaceErrSigning, "the repository's own config names the program that signs the merge (" +
				key + "); merge it yourself"
		}
	}
	return "", ""
}

// emptyFilters are -c overrides that empty every filter driver the
// repository's config names (repoGitArgs' own).
func emptyFilters(ctx context.Context, repo string) []string {
	var out []string
	for _, name := range repoFilterDrivers(ctx, repo) {
		out = append(out, "-c", "filter."+name+".clean=", "-c", "filter."+name+".smudge=",
			"-c", "filter."+name+".process=", "-c", "filter."+name+".required=false")
	}
	return out
}

// mergeAt merges tip with --no-ff in dir, whose HEAD must be the base. On
// any failure the merge is aborted and HEAD and the tree are checked to be
// as they were.
func mergeAt(ctx context.Context, dir string, job runnerwire.WorkspaceActionJob, tip string, extra []string) (code, why string) {
	baseRef := "refs/heads/" + job.Base
	// Checked right before merging: in the owner's checkout they may have
	// switched branches (or committed) since baseCheckout looked, and a
	// merge lands on whatever HEAD is.
	if head, err := git(ctx, dir, "symbolic-ref", "-q", "HEAD"); err != nil || strings.TrimSpace(head) != baseRef {
		return runnerwire.WorkspaceErrDirtyBase, "the checkout at " + dir + " is no longer on " + job.Base + "; nothing was merged"
	}
	before, err := revParse(ctx, dir, "HEAD")
	if err != nil {
		return runnerwire.WorkspaceErrGit, err.Error()
	}
	msg := fmt.Sprintf("Merge branch '%s' into %s\n\n", job.Branch, job.Base) + trailers(job.NodeID, job.RunID)
	// git's merge replaces an ignored file (the owner's own .env or local
	// config) with the branch's without asking, --no-overwrite-ignore or
	// not (ort honours it only for a fast-forward), and an abort would then
	// delete it. Untracked files that are not ignored it refuses itself.
	if paths, err := ignoredInTheWay(ctx, dir, before, tip, extra); err != nil {
		return runnerwire.WorkspaceErrGit, "reading the checkout's ignored files: " + err.Error()
	} else if len(paths) > 0 {
		n := len(paths)
		if n > maxConflictPaths {
			paths = paths[:maxConflictPaths]
		}
		return runnerwire.WorkspaceErrDirtyBase, "the merge would overwrite " + listPaths(paths, n) +
			" (ignored there); move them away and merge again. Nothing changed"
	}
	args := append(append(append([]string{}, extra...), identityArgs(ctx, dir)...),
		"merge", "--no-ff", "--no-edit", "-m", msg, tip)
	_, mergeErr := git(ctx, dir, args...)
	if mergeErr == nil {
		// The merge commit must sit on the base, on top of where it was.
		after, _ := revParse(ctx, dir, "HEAD")
		parent, _ := revParse(ctx, dir, "HEAD^1")
		onBase, _ := revParse(ctx, dir, baseRef)
		if after == "" || parent != before || onBase != after {
			return runnerwire.WorkspaceErrGit, fmt.Sprintf("the merge made %s, which is not on %s right after %s; check %s yourself",
				shortOr(after), job.Base, short(before), dir)
		}
		return "", ""
	}
	cleanup := context.WithoutCancel(ctx)
	conflicts, _ := git(cleanup, dir, append(append([]string{}, extra...), "diff", "--name-only", "--diff-filter=U", "-z")...)
	if refExists(cleanup, dir, "MERGE_HEAD") {
		if _, err := git(cleanup, dir, append(append([]string{}, extra...), "merge", "--abort")...); err != nil {
			return runnerwire.WorkspaceErrGit, "the merge failed and could not be aborted: " + err.Error() + "; " + mergeErr.Error()
		}
	}
	if after, err := revParse(cleanup, dir, "HEAD"); err != nil || after != before {
		return runnerwire.WorkspaceErrGit, "the merge failed and HEAD moved: " + mergeErr.Error()
	}
	if out, err := git(cleanup, dir, append(append([]string{}, extra...), "status", "--porcelain=v1", "-z", "--untracked-files=no", "--no-renames")...); err != nil || countStatus(out) > 0 {
		return runnerwire.WorkspaceErrGit, "the merge failed and left changes behind: " + mergeErr.Error()
	}
	var paths []string
	for _, p := range strings.Split(conflicts, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) > 0 {
		n := len(paths)
		if n > maxConflictPaths {
			paths = paths[:maxConflictPaths]
		}
		return runnerwire.WorkspaceErrConflict, "the merge has conflicts in " + listPaths(paths, n) + "; it was aborted and nothing changed"
	}
	if strings.Contains(mergeErr.Error(), "would be overwritten by merge") {
		what := "files in the checkout"
		if paths := overwritten(mergeErr.Error()); len(paths) > 0 {
			n := len(paths)
			if n > maxConflictPaths {
				paths = paths[:maxConflictPaths]
			}
			what = listPaths(paths, n)
		}
		return runnerwire.WorkspaceErrDirtyBase, "the merge would overwrite " + what +
			" (untracked there); move them away and merge again. Nothing changed"
	}
	return runnerwire.WorkspaceErrGit, mergeErr.Error()
}

// ignoredInTheWay names the ignored files (or directories) in dir's checkout
// at a path the merge of tip writes: one the branch changed since the merge
// base, at it, under it, or in its way as a parent.
func ignoredInTheWay(ctx context.Context, dir, before, tip string, extra []string) ([]string, error) {
	with := func(args ...string) []string { return append(append([]string{}, extra...), args...) }
	base, err := git(ctx, dir, with("merge-base", before, tip)...)
	if err != nil {
		// No common history: git's own merge refuses that.
		return nil, nil
	}
	changed, err := git(ctx, dir, with("diff", "--name-only", "-z", "--no-renames", strings.TrimSpace(base), tip)...)
	if err != nil {
		return nil, err
	}
	if changed == "" {
		return nil, nil
	}
	listed, err := git(ctx, dir, with("ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory")...)
	if err != nil {
		return nil, err
	}
	ignored := map[string]bool{}
	for _, p := range strings.Split(listed, "\x00") {
		if p != "" {
			ignored[p] = true
		}
	}
	if len(ignored) == 0 {
		return nil, nil
	}
	var hits []string
	for _, p := range strings.Split(changed, "\x00") {
		if p == "" {
			continue
		}
		hit := ignored[p] || ignored[p+"/"]
		for i := strings.IndexByte(p, '/'); i >= 0 && !hit; {
			parent := p[:i]
			hit = ignored[parent] || ignored[parent+"/"]
			next := strings.IndexByte(p[i+1:], '/')
			if next < 0 {
				break
			}
			i += 1 + next
		}
		if hit {
			hits = append(hits, p)
		}
	}
	return hits, nil
}

// overwritten are the paths git's refusal names: the untracked files a
// merge would have overwritten, one per tab-indented line.
func overwritten(msg string) []string {
	var paths []string
	in := false
	for _, line := range strings.Split(msg, "\n") {
		switch {
		case strings.Contains(line, "would be overwritten by merge"):
			in = true
		case in && strings.HasPrefix(line, "\t"):
			if p := strings.TrimSpace(line); p != "" {
				paths = append(paths, p)
			}
		default:
			in = false
		}
	}
	return paths
}
