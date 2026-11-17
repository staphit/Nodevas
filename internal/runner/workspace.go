package runner

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"nodevas/internal/runnerwire"
)

// The agent workspace (runnerwire/workspace.go): the owner looks into one
// run's worktree from the hub, read-only, with the same rules as repo
// browsing, and merges or discards it (workspace_actions.go). A run's
// worktree is <dataDir>/worktrees/<runId>, named only from a validated run
// id; nothing the hub sends names a path.

// runWorktree is a run's worktree as found on disk and checked.
type runWorktree struct {
	// Path is <dataDir>/worktrees/<runId>, absolute, with the data
	// directory's symlinks resolved.
	Path   string
	Exists bool
	// Link: the name is a symlink or junction, not the run's directory.
	Link bool
	// Listed: the repo's `git worktree list` has it, with this branch
	// ("" detached) and head.
	Listed bool
	Branch string
	Head   string
	// GitFile: its .git is a regular file, as git worktree add made it.
	GitFile bool
}

// gitWorktree is one entry of `git worktree list --porcelain`.
type gitWorktree struct {
	Path, Head, Branch       string
	Bare, Detached, Prunable bool
}

// errForeignWorktree is a run's worktree that is not where the run left it.
func errForeignWorktree(msg string) error {
	return refuseRepo(runnerwire.WorkspaceErrForeign, msg)
}

// findRunWorktree checks a run's worktree before anything reads or changes
// it. It must be a plain directory (no symlink, no junction: an agent could
// swap its own worktree for a link to $HOME) whose resolved path is itself.
// Whether the repo lists it, and on which branch, is reported for the
// actions to judge; browsing needs only the directory checks.
func findRunWorktree(ctx context.Context, dataDir, repoPath, runID string) (runWorktree, error) {
	if !validRunID.MatchString(runID) {
		return runWorktree{}, refuseRepo(runnerwire.RepoErrBadPath, "run id is not usable on this runner")
	}
	base, err := filepath.EvalSymlinks(dataDir)
	if errors.Is(err, fs.ErrNotExist) {
		return runWorktree{Path: filepath.Join(dataDir, "worktrees", runID)}, nil
	}
	if err != nil {
		return runWorktree{}, err
	}
	if base, err = filepath.Abs(base); err != nil {
		return runWorktree{}, err
	}
	wt := runWorktree{Path: filepath.Join(base, "worktrees", runID)}
	info, err := os.Lstat(wt.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return wt, nil
	}
	if err != nil {
		return wt, err
	}
	if info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
		wt.Link = true
		return wt, errForeignWorktree("the run's worktree has been replaced by a link")
	}
	if !info.IsDir() {
		return wt, errForeignWorktree("the run's worktree is not a directory")
	}
	if parent, err := os.Lstat(filepath.Dir(wt.Path)); err != nil || !parent.IsDir() ||
		parent.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
		return wt, errForeignWorktree("the runner's worktrees directory has been replaced by a link")
	}
	// No link above it inside the data directory either.
	real, err := filepath.EvalSymlinks(wt.Path)
	if err != nil || !sameRepoPath(real, wt.Path) {
		return wt, errForeignWorktree("the run's worktree does not resolve to itself")
	}
	wt.Exists = true
	if gi, err := os.Lstat(filepath.Join(wt.Path, ".git")); err == nil && gi.Mode().IsRegular() {
		wt.GitFile = true
	}
	if repoPath == "" {
		return wt, nil
	}
	list, err := listWorktrees(ctx, repoPath)
	if err != nil {
		return wt, nil
	}
	for _, entry := range list {
		if samePlace(entry.Path, wt.Path) {
			wt.Listed = true
			wt.Head = entry.Head
			wt.Branch = strings.TrimPrefix(entry.Branch, "refs/heads/")
			break
		}
	}
	return wt, nil
}

// samePlace compares a path git printed (slash-separated on Windows) with
// one of ours, both with symlinks resolved where they exist.
func samePlace(gitPath, ours string) bool {
	a := filepath.FromSlash(gitPath)
	if real, err := filepath.EvalSymlinks(a); err == nil {
		a = real
	}
	b := ours
	if real, err := filepath.EvalSymlinks(b); err == nil {
		b = real
	}
	return sameRepoPath(a, b)
}

// listWorktrees parses `git -C repo worktree list --porcelain -z`. Reading
// the list runs nothing the repository configures.
func listWorktrees(ctx context.Context, repoPath string) ([]gitWorktree, error) {
	out, err := git(ctx, repoPath, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	var list []gitWorktree
	var cur *gitWorktree
	for _, line := range strings.Split(out, "\x00") {
		if line == "" {
			cur = nil
			continue
		}
		key, value, _ := strings.Cut(line, " ")
		if key == "worktree" {
			list = append(list, gitWorktree{Path: value})
			cur = &list[len(list)-1]
			continue
		}
		if cur == nil {
			continue
		}
		switch key {
		case "HEAD":
			cur.Head = value
		case "branch":
			cur.Branch = value
		case "bare":
			cur.Bare = true
		case "detached":
			cur.Detached = true
		case "prunable":
			cur.Prunable = true
		}
	}
	return list, nil
}

// ownBranch: the branch this run made (implement or test), never a PR branch
// a revise or review run checked out. A global agent's run has no node; its
// branch is named by its slug (global.go).
func ownBranch(job runnerwire.WorkspaceActionJob) bool {
	return job.Branch != "" && job.Branch == BranchName(branchKey(job.NodeID, job.Slug), job.RunID) &&
		(job.Kind == runnerwire.JobImplement || job.Kind == runnerwire.JobTest)
}

// browseRun answers a run-scoped repo_request (runnerwire.RunRepoOp) for the
// runner whose owner is ownerID: the run's worktree browsed like a repo,
// run_info, or run_preview.
func (j *jobs) browseRun(ctx context.Context, ownerID string, req runnerwire.RepoRequest) runnerwire.RepoDone {
	cfg := j.c.cfg()
	done := runnerwire.RepoDone{ID: req.ID}
	fail := func(err error) runnerwire.RepoDone {
		var re *repoErr
		if errors.As(err, &re) {
			done.Code, done.Error = re.code, re.msg
			if re.code == runnerwire.WorkspaceErrForeign {
				// Browsing has no such code: refused, nothing read.
				done.Code = runnerwire.RepoErrRefused
			}
		} else {
			done.Code, done.Error = runnerwire.RepoErrGit, truncate(err.Error(), 500)
		}
		return done
	}
	if ownerID == "" || req.AccountID != ownerID {
		return fail(refuseRepo(runnerwire.RepoErrRefused, "only this runner's owner may look into its runs' worktrees"))
	}
	// A preview is the built-in runner's alone (preview.go): a member's PC
	// never opens a listener for the hub's sake.
	if req.Op == runnerwire.RepoOpRunPreview && (!j.c.BuiltIn() || !cfg.RunPreviewOn()) {
		return fail(refuseRepo(runnerwire.RepoErrPreviewOff, "previews are only for the built-in runner, with runPreview on"))
	}
	// run_info shows no file content, so it answers with browsing off.
	if req.Op != runnerwire.RepoOpRunInfo && !cfg.RepoBrowseOn() {
		return fail(refuseRepo(runnerwire.RepoErrOff, "repo browsing is off on this runner"))
	}
	if !validRunID.MatchString(req.RunID) {
		return fail(refuseRepo(runnerwire.RepoErrBadPath, "run id is not usable on this runner"))
	}
	repo, err := repoRoot(cfg, req.RepoID)
	if err != nil {
		return fail(err)
	}
	dataDir, err := cfg.RunDataDir()
	if err != nil {
		return fail(err)
	}
	if err := acquireRepoSlot(ctx); err != nil {
		return fail(err)
	}
	defer releaseRepoSlot()
	wt, err := findRunWorktree(ctx, dataDir, repo, req.RunID)
	if req.Op == runnerwire.RepoOpRunInfo {
		info := j.runInfo(ctx, dataDir, repo, req, wt, err == nil)
		done.RunInfo = &info
		return done
	}
	if err != nil {
		return fail(err)
	}
	if !wt.Exists {
		return fail(refuseRepo(runnerwire.RepoErrNoWorktree, "this run has no worktree any more"))
	}
	switch req.Op {
	case runnerwire.RepoOpRunList:
		listing, err := listRepo(wt.Path, req.Path)
		if err != nil {
			return fail(err)
		}
		done.Listing = &listing
	case runnerwire.RepoOpRunRead:
		file, err := readRepoFile(wt.Path, req.Path)
		if err != nil {
			return fail(err)
		}
		done.File = &file
	case runnerwire.RepoOpRunStatus:
		// git runs here only while .git is still the file git made.
		if !wt.GitFile {
			return fail(refuseRepo(runnerwire.RepoErrRefused, "the run's worktree is not a git worktree any more"))
		}
		status, err := repoStatus(ctx, wt.Path)
		if err != nil {
			return fail(err)
		}
		done.Status = &status
	case runnerwire.RepoOpRunPreview:
		preview, err := j.c.previewRun(ctx, dataDir, req.RepoID, wt, req.RunID)
		if err != nil {
			return fail(err)
		}
		done.Preview = &preview
	default:
		return fail(refuseRepo(runnerwire.RepoErrBadPath, "unknown repo operation"))
	}
	return done
}

// runInfo is run_info's answer: reads only. ok is false when the worktree
// failed its checks; it is then reported as not there.
func (j *jobs) runInfo(ctx context.Context, dataDir, repo string, req runnerwire.RepoRequest, wt runWorktree, ok bool) runnerwire.RunWorkspaceInfo {
	info := runnerwire.RunWorkspaceInfo{BaseCheckout: runnerwire.BaseCheckoutMissing}
	if ok && wt.Exists {
		info.Exists, info.Path, info.Branch, info.Head = true, wt.Path, wt.Branch, wt.Head
		// -1 until read: a count the runner could not take is never 0, or
		// a discard would say nothing uncommitted goes with it.
		info.Uncommitted = runnerwire.UncommittedUnknown
		if wt.GitFile {
			if out, _, err := repoGit(ctx, wt.Path, 4<<20, "status", "--porcelain=v1", "-z", "--untracked-files=all",
				"--ignore-submodules=all", "--no-renames"); err == nil {
				info.Uncommitted = countStatus(out)
			}
		}
	}
	branch := info.Branch
	if branch == "" && !info.Exists {
		// The worktree is gone; its branch may not be.
		branch = req.Branch
	}
	if branch != "" && checkBase(branch) == nil {
		info.Branch = branch
		info.OwnBranch = branch == BranchName(branchKey(req.NodeID, req.Slug), req.RunID)
		if head, err := revParse(ctx, repo, "refs/heads/"+branch); err == nil {
			info.BranchExists, info.BranchHead = true, head
		}
	}
	j.mu.Lock()
	if r := j.runs[req.RunID]; r != nil && r.finish == nil {
		info.Running = true
	}
	if w := j.watched[req.RunID]; w != nil && !w.closing {
		info.WatchedPR = w.URL
	}
	j.mu.Unlock()
	info.Kept = keptListed(dataDir, filepath.Join(dataDir, "worktrees", req.RunID)) || (wt.Path != "" && keptListed(dataDir, wt.Path))
	if req.Base != "" && checkBase(req.Base) == nil && refExists(ctx, repo, "refs/heads/"+req.Base) {
		where, _ := baseCheckout(ctx, repo, req.Base)
		info.BaseCheckout = where
		if where == runnerwire.BaseCheckoutRepo {
			// Only a look: nothing the repo's config names runs (the merge,
			// which the owner confirms, checks again with plain git).
			info.BaseClean = baseCleanRead(ctx, repo) == ""
		}
	}
	return info
}

// countStatus counts the records of `git status --porcelain=v1 -z
// --no-renames`.
func countStatus(out string) int {
	n := 0
	for _, rec := range strings.Split(out, "\x00") {
		if len(rec) >= 4 {
			n++
		}
	}
	return n
}

// statusPaths are the first few paths of `git status --porcelain=v1 -z
// --no-renames`, and how many there are.
func statusPaths(out string, limit int) ([]string, int) {
	var paths []string
	n := 0
	for _, rec := range strings.Split(out, "\x00") {
		if len(rec) < 4 {
			continue
		}
		n++
		if len(paths) < limit {
			paths = append(paths, rec[3:])
		}
	}
	return paths, n
}

// baseCheckout is where base is checked out: in the registered repo itself,
// in another worktree (path names it), or nowhere.
func baseCheckout(ctx context.Context, repo, base string) (where, path string) {
	list, err := listWorktrees(ctx, repo)
	if err != nil {
		return runnerwire.BaseCheckoutMissing, ""
	}
	for _, entry := range list {
		if entry.Branch != "refs/heads/"+base {
			continue
		}
		if samePlace(entry.Path, repo) {
			return runnerwire.BaseCheckoutRepo, entry.Path
		}
		return runnerwire.BaseCheckoutElsewhere, filepath.FromSlash(entry.Path)
	}
	return runnerwire.BaseCheckoutNone, ""
}

// inProgress are the files git leaves while a merge, cherry-pick, revert,
// rebase or bisect waits for a person.
var inProgress = []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "BISECT_LOG"}

// baseClean says why the owner's checkout at dir is not clean enough to
// merge in, or "": no change to a tracked file and nothing in progress.
// Untracked and ignored files are allowed: the merge refuses to overwrite
// either (git the untracked, mergeAt the ignored), with nothing changed. It
// is the owner's own
// checkout, about to be merged in at their word, so git runs as it always
// does there.
func baseClean(ctx context.Context, dir string) string {
	out, err := git(ctx, dir, "status", "--porcelain=v1", "-z", "--untracked-files=no", "--ignore-submodules=none", "--no-renames")
	if err != nil {
		return "cannot read its changes: " + firstLine(err.Error())
	}
	return baseState(ctx, dir, out)
}

// baseCleanRead is baseClean for a look nobody asked to act on (run_info):
// the repository's filters emptied and submodules left alone, so nothing its
// config names runs. An agent can write that config (.git/config is shared,
// and so are .git/modules/*), and opening the panel is no consent to run it.
func baseCleanRead(ctx context.Context, dir string) string {
	out, _, err := repoGit(ctx, dir, 4<<20, "status", "--porcelain=v1", "-z", "--untracked-files=no",
		"--ignore-submodules=all", "--no-renames")
	if err != nil {
		return "cannot read its changes: " + firstLine(err.Error())
	}
	return baseState(ctx, dir, out)
}

// baseState is baseClean's verdict from the status read at dir.
func baseState(ctx context.Context, dir, out string) string {
	if paths, n := statusPaths(out, maxKeptReasonPaths); n > 0 {
		return "it has uncommitted changes: " + listPaths(paths, n)
	}
	args := []string{"rev-parse", "--path-format=absolute"}
	for _, name := range inProgress {
		args = append(args, "--git-path", name)
	}
	out, err := git(ctx, dir, args...)
	if err != nil {
		return "cannot read its state: " + firstLine(err.Error())
	}
	for i, path := range strings.Split(strings.TrimSpace(out), "\n") {
		path = strings.TrimSpace(path)
		if path == "" || i >= len(inProgress) {
			continue
		}
		if _, err := os.Lstat(filepath.FromSlash(path)); err == nil {
			return "a " + strings.ToLower(strings.ReplaceAll(strings.TrimSuffix(inProgress[i], "_HEAD"), "_", "-")) +
				" is in progress there"
		}
	}
	return ""
}

// listPaths names a few paths and how many more there are.
func listPaths(paths []string, n int) string {
	text := strings.Join(paths, ", ")
	if more := n - len(paths); more > 0 {
		text += " and " + strconv.Itoa(more) + " more"
	}
	return text
}
