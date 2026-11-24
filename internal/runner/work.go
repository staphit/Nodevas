package runner

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"nodevas/internal/redact"
	"nodevas/internal/runner/adapter"
	"nodevas/internal/runnerwire"
)

// Work registry (§6.4): <dataDir>/work.json records each global run's work
// root so follow-up turns (Job.ContinueOf) can reuse the same worktree folder
// and branch in place, resume the CLI's own session when the agent kind
// matches, and fast-forward push updates to an open pull request (§6.5).
//
// The hub names run IDs only. The runner resolves the folder, branch, HEAD,
// pushedHead, PR URL and session ID from work.json and git on disk.

const (
	workFile               = "work.json"
	workKeep               = 30 * 24 * time.Hour
	maxWorks               = 500
	maxTurnCommentSummary  = 2 << 10
	resumeFastFailDuration = 30 * time.Second
	// resumeFailedText is the run event (and runner log line) of a resume
	// that failed fast, headless or interactive: the hub keeps it with the
	// run, so the person sees the agent started again from the carry-over
	// prompt instead of resuming.
	resumeFailedText = "resume failed; starting again without the agent's memory"
)

type workEntry struct {
	Root       string   `json:"root"`
	RepoID     string   `json:"repoID"`
	Branch     string   `json:"branch"`
	Head       string   `json:"head,omitempty"`
	PushedHead string   `json:"pushedHead,omitempty"`
	Base       string   `json:"base"`
	Agent      string   `json:"agent"`
	Session    string   `json:"session,omitempty"`
	PRURL      string   `json:"prURL,omitempty"`
	LastRunID  string   `json:"lastRunID"`
	LastStatus string   `json:"lastStatus"`
	At         string   `json:"at"`
	Turn       int      `json:"turn,omitempty"`
	GitFile    string   `json:"gitFile,omitempty"`
	GitDirs    string   `json:"gitDirs,omitempty"`
	Runs       []string `json:"runs,omitempty"`
}

var workMu sync.Mutex

func workPath(dataDir string) string { return filepath.Join(dataDir, workFile) }

func loadWorks(dataDir string) ([]workEntry, error) {
	if dataDir == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(workPath(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []workEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("%s: %w", workFile, err)
	}
	return entries, nil
}

func saveWorks(dataDir string, entries []workEntry) error {
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dataDir, workFile+".*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(append(raw, '\n'))
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), workPath(dataDir))
}

func registerWork(dataDir string, entry workEntry) error {
	if dataDir == "" || entry.Root == "" {
		return nil
	}
	workMu.Lock()
	defer workMu.Unlock()
	entries, err := loadWorks(dataDir)
	if err != nil {
		return err
	}
	if entry.At == "" {
		entry.At = time.Now().UTC().Format(time.RFC3339)
	}
	cutoff := time.Now().Add(-workKeep).UTC().Format(time.RFC3339)
	var kept []workEntry
	for _, e := range entries {
		if e.Root == entry.Root {
			for _, id := range e.Runs {
				if id != "" && !slices.Contains(entry.Runs, id) {
					entry.Runs = append(entry.Runs, id)
				}
			}
			if entry.Turn == 0 {
				entry.Turn = e.Turn
			}
			continue
		}
		if e.At != "" && e.At < cutoff {
			continue
		}
		kept = append(kept, e)
	}
	if entry.Root != "" && !slices.Contains(entry.Runs, entry.Root) {
		entry.Runs = append(entry.Runs, entry.Root)
	}
	if entry.LastRunID != "" && !slices.Contains(entry.Runs, entry.LastRunID) {
		entry.Runs = append(entry.Runs, entry.LastRunID)
	}
	if entry.Turn <= 0 {
		entry.Turn = 1
	}
	if entry.At >= cutoff {
		kept = append(kept, entry)
	}
	if over := len(kept) - maxWorks; over > 0 {
		kept = kept[over:]
	}
	return saveWorks(dataDir, kept)
}

func lookupWork(dataDir, root string) (workEntry, bool, error) {
	workMu.Lock()
	entries, err := loadWorks(dataDir)
	workMu.Unlock()
	if err != nil {
		return workEntry{}, false, err
	}
	for _, e := range entries {
		if e.Root == root {
			return e, true, nil
		}
	}
	return workEntry{}, false, nil
}

func lookupWorkForRun(dataDir, runID string) (workEntry, bool) {
	if dataDir == "" || runID == "" {
		return workEntry{}, false
	}
	workMu.Lock()
	entries, err := loadWorks(dataDir)
	workMu.Unlock()
	if err != nil {
		return workEntry{}, false
	}
	for _, e := range entries {
		if e.Root == runID || e.LastRunID == runID || slices.Contains(e.Runs, runID) {
			return e, true
		}
	}
	return workEntry{}, false
}

func resolveWorkRoot(dataDir, runID string) string {
	if e, ok := lookupWorkForRun(dataDir, runID); ok && validRunID.MatchString(e.Root) {
		return e.Root
	}
	return runID
}

func dropWork(dataDir, root string) {
	if dataDir == "" || root == "" {
		return
	}
	workMu.Lock()
	defer workMu.Unlock()
	entries, err := loadWorks(dataDir)
	if err != nil || len(entries) == 0 {
		return
	}
	kept := slices.DeleteFunc(slices.Clone(entries), func(e workEntry) bool {
		return e.Root == root || e.LastRunID == root || slices.Contains(e.Runs, root)
	})
	if len(kept) != len(entries) {
		_ = saveWorks(dataDir, kept)
	}
}

func workRootOfJob(job runnerwire.Job) string {
	return workRootOf(job.WorkRoot, job.ContinueOf, job.RunID)
}

// workRootOf is workRootOfJob from the three fields it reads, for callers
// that must not copy a whole job another goroutine may be writing.
func workRootOf(workRoot, continueOf, runID string) string {
	if workRoot != "" {
		return workRoot
	}
	if continueOf != "" {
		return continueOf
	}
	return runID
}

func (j *jobs) workRootActive(root, ignoreRunID string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.workRootActiveLocked(root, ignoreRunID)
}

func (j *jobs) workRootActiveLocked(root, ignoreRunID string) bool {
	if root == "" {
		return false
	}
	for id, r := range j.runs {
		if id == ignoreRunID || r == nil || r.finish != nil {
			continue
		}
		if id == root || r.workRoot == root || workRootOf(r.job.WorkRoot, r.job.ContinueOf, r.job.RunID) == root ||
			r.job.ContinueOf == root {
			return true
		}
	}
	return false
}

// prepareContinue vets a follow-up offer before asking for acceptance (§6.4,
// §6.5). It resolves the worktree, branch, HEAD and session solely from the
// runner's own work.json and git state.
//
// When it had to put the root's folder back, undo removes that folder again
// (and the branch, only when it made the branch too) for a turn that then
// does not start; undo is nil otherwise. The run's fields other goroutines
// read under j.mu (workRoot, the job, resumeSession) are written under it.
func (j *jobs) prepareContinue(ctx context.Context, r *run) (_ workEntry, _ Worktree, undo func(), code, reason string) {
	job := &r.job
	if job.ContinueOf == "" {
		if job.Global {
			j.mu.Lock()
			r.workRoot = job.RunID
			j.mu.Unlock()
		}
		return workEntry{}, Worktree{}, nil, "", ""
	}
	root := workRootOfJob(*job)
	j.mu.Lock()
	r.workRoot = root
	job.WorkRoot = root
	j.mu.Unlock()

	dataDir, err := j.runCfg(r).RunDataDir()
	if err != nil {
		return workEntry{}, Worktree{}, nil, runnerwire.DeclineOther, "cannot read the work record: " + err.Error()
	}
	entry, ok, err := lookupWork(dataDir, root)
	if err != nil {
		return workEntry{}, Worktree{}, nil, runnerwire.DeclineOther, "cannot read the work record: " + err.Error()
	}
	if !ok {
		return workEntry{}, Worktree{}, nil, runnerwire.DeclineOther, fmt.Sprintf("work %s is not on record on this runner", root)
	}
	if entry.LastRunID != job.ContinueOf {
		return workEntry{}, Worktree{}, nil, runnerwire.DeclineOther,
			fmt.Sprintf("run %s is not the latest turn of %s (latest is %s)", job.ContinueOf, root, entry.LastRunID)
	}
	if entry.RepoID != job.RepoID {
		return workEntry{}, Worktree{}, nil, runnerwire.DeclineOther,
			fmt.Sprintf("work %s belongs to repository %s, not %s", root, entry.RepoID, job.RepoID)
	}
	if j.workRootActive(root, job.RunID) {
		return workEntry{}, Worktree{}, nil, runnerwire.DeclineOther,
			fmt.Sprintf("another run on this runner is already working in %s", root)
	}
	if entry.Branch == "" || !strings.HasPrefix(entry.Branch, "agent/") || checkBase(entry.Branch) != nil {
		return workEntry{}, Worktree{}, nil, runnerwire.DeclineOther,
			fmt.Sprintf("recorded branch %q is not a valid agent branch", entry.Branch)
	}

	found, err := findRunWorktreeDirect(ctx, dataDir, r.repo.Path, root)
	if err != nil {
		return workEntry{}, Worktree{}, nil, runnerwire.DeclineOther, err.Error()
	}

	var wt Worktree
	if !found.Exists {
		if entry.PRURL != "" {
			return workEntry{}, Worktree{}, nil, runnerwire.DeclinePREnded,
				fmt.Sprintf("the worktree for %s (%s) was removed after its pull request ended", root, entry.PRURL)
		}
		// A recorded head means an earlier turn committed on the branch: the
		// branch must still be there, at that head. Starting again from the
		// base would quietly lose that work.
		if entry.Head != "" {
			tip, err := revParse(ctx, r.repo.Path, "refs/heads/"+entry.Branch)
			if err != nil || tip == "" {
				return workEntry{}, Worktree{}, nil, runnerwire.DeclineBranchMoved,
					fmt.Sprintf("branch %s, which held this work up to %s, is gone", entry.Branch, short(entry.Head))
			}
			if tip != entry.Head {
				return workEntry{}, Worktree{}, nil, runnerwire.DeclineBranchMoved,
					fmt.Sprintf("branch %s moved (at %s, expected %s)", entry.Branch, short(tip), short(entry.Head))
			}
		}
		var madeBranch bool
		wt, madeBranch, err = recreateWorktree(ctx, r.repo.Path, dataDir, root, entry.Branch, entry.Base, entry.Head)
		if err != nil {
			return workEntry{}, Worktree{}, nil, runnerwire.DeclineOther, err.Error()
		}
		put := wt
		undo = func() { _ = removeWorktree(context.WithoutCancel(j.ctx), r.repo.Path, put, madeBranch) }
		head, err := revParse(ctx, wt.Path, "HEAD")
		if err != nil {
			undo()
			return workEntry{}, Worktree{}, nil, runnerwire.DeclineOther, err.Error()
		}
		entry.Head = head
		entry.GitFile = wt.GitFile
		entry.GitDirs = wt.GitDirs
	} else {
		if !found.Listed || !found.GitFile {
			return workEntry{}, Worktree{}, nil, runnerwire.DeclineOther,
				"the worktree is not a registered git worktree of this repository"
		}
		wt = Worktree{
			Path:    found.Path,
			Branch:  entry.Branch,
			Base:    entry.Base,
			GitFile: entry.GitFile,
			GitDirs: entry.GitDirs,
		}
		if wt.GitFile != "" {
			if err := checkGitDir(ctx, wt); err != nil {
				return workEntry{}, Worktree{}, nil, runnerwire.DeclineOther, err.Error()
			}
		} else if err := recordGitDir(ctx, &wt); err != nil {
			return workEntry{}, Worktree{}, nil, runnerwire.DeclineOther, err.Error()
		} else {
			entry.GitFile, entry.GitDirs = wt.GitFile, wt.GitDirs
		}
		if found.Branch != entry.Branch {
			return workEntry{}, Worktree{}, nil, runnerwire.DeclineOther,
				fmt.Sprintf("worktree is on branch %s, not %s", orDash(found.Branch), entry.Branch)
		}
		head, err := revParse(ctx, wt.Path, "HEAD")
		if err != nil || (entry.Head != "" && head != entry.Head) {
			return workEntry{}, Worktree{}, nil, runnerwire.DeclineOther,
				fmt.Sprintf("worktree HEAD moved (at %s, expected %s)", short(head), short(entry.Head))
		}
		branchTip, err := revParse(ctx, r.repo.Path, "refs/heads/"+entry.Branch)
		if err != nil || branchTip != head {
			return workEntry{}, Worktree{}, nil, runnerwire.DeclineOther,
				fmt.Sprintf("branch %s moved (at %s, expected %s)", entry.Branch, short(branchTip), short(head))
		}
		entry.Head = head
	}

	dirty, dirtyPaths, err := worktreeDirtyBeyondWithheld(ctx, dataDir, wt.Path)
	if err != nil {
		if undo != nil {
			undo()
		}
		return workEntry{}, Worktree{}, nil, runnerwire.DeclineOther, "cannot read the worktree's changes: " + err.Error()
	}
	if dirty && entry.LastStatus != runnerwire.RunCancelled && entry.LastStatus != runnerwire.RunFailed {
		if undo != nil {
			undo()
		}
		return workEntry{}, Worktree{}, nil, runnerwire.DeclineOther,
			fmt.Sprintf("worktree has uncommitted changes after a succeeded run: %s", strings.Join(dirtyPaths, ", "))
	}

	if entry.PRURL != "" {
		if code, reason := j.prepareContinuePR(ctx, r, dataDir, &entry, wt, dirty); reason != "" {
			if undo != nil {
				undo()
			}
			return workEntry{}, Worktree{}, nil, code, reason
		}
	}

	wt.Base = entry.Base
	wt.BaseRef = entry.Head

	resume := ""
	if entry.Session != "" && runnerwire.ValidSessionID(entry.Session) && entry.Agent == job.Agent {
		ad, _ := withoutOpenCode2ACP(j.c.adapterUnder(j.runCfg(r), job.Agent), j.agentVersion(job.Agent))
		if resumer, ok := ad.(adapter.Resumer); ok && resumer.SupportsResume(job.Mode == runnerwire.ModeInteractive) {
			resume = entry.Session
		}
	}
	task := adapter.Truncate(redact.String(job.ContinueTask), runnerwire.MaxPromptBytes)
	summary := adapter.Truncate(redact.String(job.ContinueSummary), runnerwire.MaxSummaryBytes)
	j.mu.Lock()
	job.Base = entry.Base
	job.ContinuePR = entry.PRURL
	job.ContinueTask = task
	job.ContinueSummary = summary
	r.resumeSession = resume
	j.mu.Unlock()
	return entry, wt, undo, "", ""
}

// prepareContinuePR enforces §6.5's pre-turn open-PR checks and fast-forwards
// the worktree when origin is ahead and local HEAD is clean at pushedHead.
func (j *jobs) prepareContinuePR(ctx context.Context, r *run, dataDir string, entry *workEntry, wt Worktree, dirty bool) (string, string) {
	raw, err := git(ctx, r.repo.Path, "config", "--get", "remote.origin.url")
	if err != nil || strings.TrimSpace(raw) == "" {
		return runnerwire.DeclineOther, "the repository has no origin remote"
	}
	if origin := repoKey(strings.TrimSpace(raw)); origin != "" {
		if prRepo := prRepoKey(entry.PRURL); prRepo == "" || origin != prRepo {
			return runnerwire.DeclineOther, fmt.Sprintf("pull request %s is not in this repository (origin is %s)", entry.PRURL, origin)
		}
	}
	gh, err := j.c.gh()
	if err != nil {
		return runnerwire.DeclineOther, err.Error()
	}
	out, err := gh.call(ctx, r.repo.Path, "", "pr", "view", entry.PRURL, "--json", prCheckFields)
	if err != nil {
		return runnerwire.DeclineOther, ghError("gh pr view", err).Error()
	}
	var v ghPRCheck
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &v); err != nil {
		return runnerwire.DeclineOther, "gh pr view printed something other than JSON"
	}
	if !strings.EqualFold(v.State, "OPEN") {
		return runnerwire.DeclinePREnded, fmt.Sprintf("pull request %s is %s, not open", entry.PRURL, strings.ToLower(orDash(v.State)))
	}
	if v.IsCrossRepository {
		return runnerwire.DeclineOther, fmt.Sprintf("pull request %s comes from a fork", entry.PRURL)
	}
	if v.BaseRefName != "" && entry.Base != "" && v.BaseRefName != entry.Base {
		return runnerwire.DeclinePRMoved, fmt.Sprintf("pull request %s now targets %s, not %s", entry.PRURL, v.BaseRefName, entry.Base)
	}
	if v.HeadRefName != "" && v.HeadRefName != entry.Branch {
		return runnerwire.DeclineBranchMoved, fmt.Sprintf("pull request %s is on branch %s, not %s", entry.PRURL, v.HeadRefName, entry.Branch)
	}

	ref, err := fetchBranch(ctx, r.repo.Path, entry.Branch)
	if err != nil {
		return runnerwire.DeclineOther, fmt.Sprintf("could not fetch %s from origin: %v", entry.Branch, err)
	}
	originSHA, err := revParse(ctx, r.repo.Path, ref)
	if err != nil {
		return runnerwire.DeclineBranchMoved, fmt.Sprintf("branch %s is missing on origin", entry.Branch)
	}
	pushedHead := cmp.Or(entry.PushedHead, entry.Head)
	if originSHA == pushedHead {
		return "", ""
	}
	if pushedHead != "" && isAncestor(ctx, r.repo.Path, pushedHead, originSHA) {
		if entry.Head != pushedHead || dirty {
			return runnerwire.DeclineBranchMoved,
				fmt.Sprintf("branch %s moved on origin while local changes exist", entry.Branch)
		}
		filters := append(emptyFilters(ctx, wt.Path), "-c", "submodule.recurse=false")
		if _, err := git(ctx, wt.Path, append(filters, "merge", "--ff-only", "--quiet", originSHA)...); err != nil {
			return runnerwire.DeclineBranchMoved, fmt.Sprintf("could not fast-forward %s to origin: %v", entry.Branch, err)
		}
		entry.Head = originSHA
		entry.PushedHead = originSHA
		_ = registerWork(dataDir, *entry)
		return "", ""
	}
	return runnerwire.DeclineBranchMoved, fmt.Sprintf("branch %s diverged on origin", entry.Branch)
}

// recreateWorktree recreates <dataDir>/worktrees/<root> on branch when its
// folder is gone: on the branch as it is when the branch is still there (at
// expectedHead, when one is given), else on a new branch from base (the prior
// turn changed nothing and publish removed both). madeBranch says which: only
// a branch it made may be deleted again if the turn does not go ahead.
func recreateWorktree(ctx context.Context, repoPath, dataDir, root, branch, base, expectedHead string) (_ Worktree, madeBranch bool, _ error) {
	if !validRunID.MatchString(root) {
		return Worktree{}, false, fmt.Errorf("run id %q is not usable as a directory name", root)
	}
	if base != "" {
		if err := checkBase(base); err != nil {
			return Worktree{}, false, err
		}
	} else {
		var err error
		if base, err = defaultBranch(ctx, repoPath); err != nil {
			return Worktree{}, false, err
		}
	}
	baseRef := base
	fetchCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	_, fetchErr := git(fetchCtx, repoPath, "fetch", "--quiet", "origin", base)
	cancel()
	remoteRef := "origin/" + base
	switch {
	case fetchErr == nil && refExists(ctx, repoPath, remoteRef):
		baseRef = remoteRef
	case refExists(ctx, repoPath, base):
		baseRef = base
	case refExists(ctx, repoPath, remoteRef):
		baseRef = remoteRef
	default:
		return Worktree{}, false, fmt.Errorf("base branch %q exists neither locally nor on origin", base)
	}

	wt := Worktree{
		Path:    filepath.Join(dataDir, "worktrees", root),
		Branch:  branch,
		Base:    base,
		BaseRef: baseRef,
	}
	if err := os.MkdirAll(filepath.Dir(wt.Path), 0o700); err != nil {
		return Worktree{}, false, err
	}
	if refExists(ctx, repoPath, "refs/heads/"+branch) {
		if expectedHead != "" {
			if tip, err := revParse(ctx, repoPath, "refs/heads/"+branch); err != nil || tip != expectedHead {
				return Worktree{}, false, fmt.Errorf("branch %s moved since the last run", branch)
			}
		}
		if _, err := git(ctx, repoPath, "worktree", "add", "--quiet", "--", wt.Path, branch); err != nil {
			return Worktree{}, false, err
		}
	} else {
		if expectedHead != "" {
			return Worktree{}, false, fmt.Errorf("branch %s, which held this work, is gone", branch)
		}
		if _, err := git(ctx, repoPath, "worktree", "add", "--quiet", "-b", branch, "--", wt.Path, baseRef); err != nil {
			return Worktree{}, false, err
		}
		madeBranch = true
	}
	if err := recordGitDir(ctx, &wt); err != nil {
		_ = removeWorktree(ctx, repoPath, wt, madeBranch)
		return Worktree{}, false, err
	}
	return wt, madeBranch, nil
}

// worktreeDirtyBeyondWithheld reports whether dir has uncommitted tracked or
// untracked files beyond any withheld agent config recorded in kept-worktrees.json.
func worktreeDirtyBeyondWithheld(ctx context.Context, dataDir, dir string) (bool, []string, error) {
	out, _, err := repoGit(ctx, dir, 4<<20, "status", "--porcelain=v1", "-z",
		"--untracked-files=all", "--ignore-submodules=all", "--no-renames")
	if err != nil {
		return false, nil, err
	}
	var withheld []string
	if kept, err := KeptWorktrees(dataDir); err == nil {
		for _, k := range kept {
			if sameRepoPath(k.Path, dir) {
				withheld = k.Withheld
				break
			}
		}
	}
	var extra []string
	for _, rec := range strings.Split(out, "\x00") {
		if len(rec) < 4 {
			continue
		}
		path := rec[3:]
		if !slices.Contains(withheld, path) {
			extra = append(extra, path)
		}
	}
	if len(extra) > maxKeptReasonPaths {
		extra = extra[:maxKeptReasonPaths]
	}
	return len(extra) > 0, extra, nil
}

// publishTurn commits and publishes a follow-up turn (§6.5):
//   - measures the turn's own changes from the prior turn's head;
//   - never drops the root folder or branch on a no-op turn;
//   - commits failed turns locally and never pushes them;
//   - on a succeeded turn with an open PR and commits since pushedHead, pushes
//     fast-forward only, attaches the existing PR instead of creating a new one,
//     posts one PR comment per push, and never marks a draft PR ready;
//   - on a rejected push, keeps commits local and pushedHead unchanged and marks
//     RunResult.PushRefused = true;
//   - looks at the PR again right before pushing: one merged or closed while
//     the turn ran gets no push and no comment, the commits stay local, and
//     the turn reports the PR as gh shows it (the hub's pr_ended); one it
//     cannot look at is not pushed either (PushRefused).
func (j *jobs) publishTurn(r *run, wt Worktree, entry *workEntry, result runnerwire.RunResult) (*runnerwire.RunChanges, *runnerwire.PRInfo, runnerwire.RunResult) {
	ctx := j.ctx
	job := r.job
	cfg := j.runCfg(r)
	priorHead := entry.Head
	if priorHead != "" {
		wt.BaseRef = priorHead
	}

	message := commitMessage(result.Summary, job)
	committed := j.commit(ctx, r, wt, message)
	if committed.Err != nil {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "could not commit the changes: " + committed.Err.Error()})
	}
	currentHead, _ := revParse(ctx, wt.Path, "HEAD")
	if priorHead != "" {
		committed.Base = priorHead
		if currentHead == priorHead && committed.Err == nil {
			committed.Commit = ""
			committed.Changed = false
		} else if currentHead != "" && currentHead != priorHead {
			committed.Commit = currentHead
			committed.Changed = true
		}
	}
	if currentHead != "" {
		entry.Head = currentHead
	}

	changes, err := describeChanges(ctx, wt.Path, committed, committed.Base)
	if err != nil {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "could not describe the changes: " + err.Error()})
	}
	fitChanges(job.RunID, &changes)

	if !committed.Changed && committed.Err == nil {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "no changes in this turn; worktree kept at " + wt.Path})
	} else if changes.Commit != "" {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: fmt.Sprintf("committed %s on %s: %d files, +%d −%d",
			short(changes.Commit), wt.Branch, len(changes.Files), changes.Additions, changes.Deletions)})
	}

	// A failed turn is committed locally and never pushed.
	if result.Status != runnerwire.RunSucceeded {
		if entry.PRURL != "" && changes.Commit != "" {
			j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog,
				Text: fmt.Sprintf("the run failed: not pushed to %s; worktree kept at %s", wt.Branch, wt.Path)})
		}
		return &changes, nil, result
	}

	if entry.PRURL != "" {
		hasUnpushed := currentHead != "" && currentHead != entry.PushedHead &&
			(committed.Changed || (entry.PushedHead != "" && !isAncestor(ctx, r.repo.Path, currentHead, entry.PushedHead)))
		if !hasUnpushed {
			return &changes, nil, result
		}
		if !cfg.AutoPROn() {
			j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog,
				Text: fmt.Sprintf("autoPR is off: branch %s stays in %s", wt.Branch, wt.Path)})
			return &changes, nil, result
		}
		// The PR was open before the turn; it may have been merged or closed
		// while the agent worked. Pushing then would put commits on a branch
		// nobody reviews, so look again first.
		if info, ended, why := j.prStillOpen(r, entry.PRURL); why != "" {
			result.PushRefused = true
			j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog,
				Text: fmt.Sprintf("%s; not pushed: commits kept on %s in %s", why, wt.Branch, wt.Path)})
			j.c.logf("run %s: %s; not pushed; commits kept locally at %s", job.RunID, why, wt.Path)
			return &changes, nil, result
		} else if ended {
			text := fmt.Sprintf("pull request %s was %s while this turn ran; not pushed and no comment posted: commits kept on %s in %s",
				entry.PRURL, info.State, wt.Branch, wt.Path)
			j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: text})
			j.c.logf("run %s: %s", job.RunID, text)
			return &changes, &info, result
		}
		pushCtx, cancel := context.WithTimeout(ctx, pushTimeout)
		_, err := git(pushCtx, wt.Path, "push", "--no-verify", "origin", "HEAD:refs/heads/"+wt.Branch)
		cancel()
		if err != nil {
			result.PushRefused = true
			j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog,
				Text: fmt.Sprintf("push to %s refused: %v", wt.Branch, err)})
			j.c.logf("run %s: push to %s refused: %v; commits kept locally at %s", job.RunID, wt.Branch, err, wt.Path)
			return &changes, nil, result
		}
		entry.PushedHead = currentHead
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: fmt.Sprintf("pushed %s to %s", short(currentHead), wt.Branch)})
		j.c.logf("run %s: pushed %s to %s (%s)", job.RunID, short(currentHead), wt.Branch, entry.PRURL)
		info := j.attachTurnPR(r, wt, entry.PRURL, currentHead, len(committed.Withheld) > 0)
		j.postTurnComment(r, entry.PRURL, max(entry.Turn, 1)+1, result.Summary)
		return &changes, &info, result
	}

	if job.NoPR || !cfg.AutoPROn() {
		if changes.Commit != "" {
			text := fmt.Sprintf("autoPR is off: branch %s stays in %s", wt.Branch, wt.Path)
			if job.NoPR {
				text = fmt.Sprintf("kept on the runner: branch %s in %s (no pull request asked)", wt.Branch, wt.Path)
				j.c.logf("run %s: %s", job.RunID, text)
			}
			j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: text})
		}
		return &changes, nil, result
	}
	if changes.Commit == "" {
		return &changes, nil, result
	}
	pr := j.openPR(ctx, r, wt, result, message)
	if pr.Error != "" {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "no pull request: " + pr.Error})
		j.c.logf("run %s: no pull request: %s; worktree kept at %s", job.RunID, firstLine(pr.Error), wt.Path)
	} else {
		entry.PRURL = pr.URL
		entry.PushedHead = currentHead
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "opened " + pr.URL})
		j.c.logf("run %s: opened %s", job.RunID, pr.URL)
		root := cmp.Or(r.workRoot, job.RunID)
		watched := watchedPR{RunID: root, NodeID: job.NodeID, URL: pr.URL, Worktree: wt.Path, Repo: r.repo.Path,
			Branch: wt.Branch, PR: pr}
		if len(committed.Withheld) > 0 {
			watched.Worktree = ""
		}
		j.watch(watched)
	}
	return &changes, &pr, result
}

// prStillOpen looks at a turn's PR right before its push. ended is true, with
// the PR as gh shows it, when it was merged or closed; why is non-empty when
// it could not be looked at, and then nothing is pushed either.
func (j *jobs) prStillOpen(r *run, prURL string) (info runnerwire.PRInfo, ended bool, why string) {
	gh, err := j.c.gh()
	if err != nil {
		return runnerwire.PRInfo{}, false, "could not look at " + prURL + " before pushing: " + err.Error()
	}
	info, err = gh.view(j.ctx, r.repo.Path, prURL)
	if err != nil {
		return runnerwire.PRInfo{}, false, "could not look at " + prURL + " before pushing: " + err.Error()
	}
	return info, info.State == runnerwire.PRMerged || info.State == runnerwire.PRClosed, ""
}

// attachTurnPR reads an open PR back after pushing a follow-up turn and updates
// the root's watch entry in place without adding a second watch.
func (j *jobs) attachTurnPR(r *run, wt Worktree, prURL, head string, withheld bool) runnerwire.PRInfo {
	root := cmp.Or(r.workRoot, r.job.RunID)
	info := runnerwire.PRInfo{URL: prURL, Number: prNumber(prURL), State: runnerwire.PROpen, HeadSHA: head}
	if gh, err := j.c.gh(); err == nil {
		if viewed, err := gh.view(j.ctx, r.repo.Path, prURL); err == nil {
			info = viewed
		} else {
			j.c.logf("run %s: %v", r.job.RunID, err)
		}
	}
	watched := watchedPR{
		RunID:    root,
		NodeID:   r.job.NodeID,
		URL:      prURL,
		Worktree: wt.Path,
		Repo:     r.repo.Path,
		Branch:   wt.Branch,
		PR:       info,
	}
	if withheld {
		watched.Worktree = ""
	}
	j.watch(watched)
	return info
}

// postTurnComment posts one PR comment per pushed follow-up turn (§6.5):
// "Turn {n}: {first line of the person's message}" plus the redacted summary
// (<= 2 KiB).
func (j *jobs) postTurnComment(r *run, prURL string, turn int, summary string) {
	gh, err := j.c.gh()
	if err != nil {
		return
	}
	headline := oneLine(firstLine(r.job.Prompt))
	if headline == "" {
		headline = oneLine(r.job.NodeTitle)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Turn %d: %s", turn, headline)
	if text := truncateUTF8(strings.TrimSpace(redact.Output(summary)), maxTurnCommentSummary); text != "" {
		b.WriteString("\n\n")
		b.WriteString(text)
	}
	body := redact.Output(b.String())
	if _, err := gh.call(j.ctx, r.repo.Path, body, "pr", "comment", prURL, "--body-file", "-"); err != nil {
		err = ghError("gh pr comment", err)
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "could not post the turn comment: " + err.Error()})
		j.c.logf("run %s: could not post the turn comment on %s: %v", r.job.RunID, prURL, err)
	} else {
		j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Text: "posted turn comment on " + prURL})
	}
}

// recordGlobalWork writes or updates the work.json entry for a global run at
// finish, including roots whose folder was dropped because nothing changed.
func (j *jobs) recordGlobalWork(r *run, wt Worktree, prev workEntry, result runnerwire.RunResult, pr *runnerwire.PRInfo) {
	if !r.job.Global || !r.accepted {
		return
	}
	dataDir, err := j.runCfg(r).RunDataDir()
	if err != nil {
		return
	}
	root := cmp.Or(r.workRoot, workRootOfJob(r.job))
	head := prev.Head
	if current, err := revParse(j.ctx, wt.Path, "HEAD"); err == nil && current != "" {
		head = current
	} else if tip, err := revParse(j.ctx, r.repo.Path, "refs/heads/"+wt.Branch); err == nil && tip != "" {
		head = tip
	}
	pushedHead := prev.PushedHead
	prURL := prev.PRURL
	if pr != nil && pr.URL != "" && pr.Error == "" {
		prURL = pr.URL
		if pushedHead == "" && head != "" {
			pushedHead = head
		}
	}
	// The session to resume next turn is one this turn really used: the one
	// the CLI reported or the runner gave it, else the one it was resumed
	// with. A turn that never started the agent with the old session (no
	// resume, or a retry without it) does not carry that session forward.
	j.mu.Lock()
	session := r.session
	resumed, used := r.resumeSession, r.resumeUsed
	j.mu.Unlock()
	if !runnerwire.ValidSessionID(session) {
		session = ""
	}
	if session == "" && used && resumed != "" && prev.Agent == r.job.Agent && runnerwire.ValidSessionID(resumed) {
		session = resumed
	}
	turn := prev.Turn + 1
	if turn <= 0 {
		turn = 1
	}
	entry := workEntry{
		Root:       root,
		RepoID:     r.repo.ID,
		Branch:     cmp.Or(wt.Branch, prev.Branch),
		Head:       head,
		PushedHead: pushedHead,
		Base:       cmp.Or(wt.Base, prev.Base),
		Agent:      r.job.Agent,
		Session:    session,
		PRURL:      prURL,
		LastRunID:  r.job.RunID,
		LastStatus: result.Status,
		At:         time.Now().UTC().Format(time.RFC3339),
		Turn:       turn,
		GitFile:    cmp.Or(wt.GitFile, prev.GitFile),
		GitDirs:    cmp.Or(wt.GitDirs, prev.GitDirs),
		Runs:       prev.Runs,
	}
	if err := registerWork(dataDir, entry); err != nil {
		j.c.logf("run %s: could not record work %s: %v", r.job.RunID, root, err)
	}
}
