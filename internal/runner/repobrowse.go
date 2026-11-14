package runner

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"nodevas/internal/runnerwire"
)

// Read-only repo browsing for the hub's Explorer (runnerwire/repobrowse.go):
// the runner's owner may list, read and see git state of a registered repo,
// never anything outside it, never a secret, never a write.

// RepoBrowseOn is RepoBrowse with its default (on) applied.
func (c *Config) RepoBrowseOn() bool {
	return c == nil || c.RepoBrowse == nil || *c.RepoBrowse
}

// Repo browsing limits on this runner.
const (
	// repoBrowseConcurrent reads run at once; the rest wait their turn.
	repoBrowseConcurrent = 4
	// binarySniffBytes is how much of a file is checked for a NUL byte.
	binarySniffBytes = 8 << 10
)

// repoSlotWait is how long a read waits for its turn: past the hub's own
// wait (runners.DefaultRepoTimeout), nobody is listening any more. A var only
// so tests can pin it.
var repoSlotWait = 15 * time.Second

// repoSlots bounds the reads in flight. Clicking through folders fast is
// normal use, so a read past the bound queues instead of being refused; the
// owner is the only one who may browse, and every read is size-capped.
var repoSlots = make(chan struct{}, repoBrowseConcurrent)

// acquireRepoSlot waits for a turn until ctx ends or repoSlotWait passes.
func acquireRepoSlot(ctx context.Context) error {
	timer := time.NewTimer(repoSlotWait)
	defer timer.Stop()
	select {
	case repoSlots <- struct{}{}:
		return nil
	case <-ctx.Done():
	case <-timer.C:
	}
	return refuseRepo(runnerwire.RepoErrBusy, "too many repo requests; try again shortly")
}

func releaseRepoSlot() { <-repoSlots }

// repoErr is a refusal with its wire code.
type repoErr struct{ code, msg string }

func (e *repoErr) Error() string { return e.msg }

func refuseRepo(code, msg string) error { return &repoErr{code: code, msg: msg} }

// BrowseRepo answers one repo_request for the runner whose owner is
// ownerID.
func BrowseRepo(ctx context.Context, cfg *Config, ownerID string, req runnerwire.RepoRequest) runnerwire.RepoDone {
	done := runnerwire.RepoDone{ID: req.ID}
	fail := func(err error) runnerwire.RepoDone {
		var re *repoErr
		if errors.As(err, &re) {
			done.Code, done.Error = re.code, re.msg
		} else {
			done.Code, done.Error = runnerwire.RepoErrGit, truncate(err.Error(), 500)
		}
		return done
	}
	if ownerID == "" || req.AccountID != ownerID {
		return fail(refuseRepo(runnerwire.RepoErrRefused, "only this runner's owner may browse its repos"))
	}
	if !cfg.RepoBrowseOn() {
		return fail(refuseRepo(runnerwire.RepoErrOff, "repo browsing is off on this runner"))
	}
	root, err := repoRoot(cfg, req.RepoID)
	if err != nil {
		return fail(err)
	}
	if err := acquireRepoSlot(ctx); err != nil {
		return fail(err)
	}
	defer releaseRepoSlot()
	switch req.Op {
	case runnerwire.RepoOpList:
		listing, err := listRepo(root, req.Path)
		if err != nil {
			return fail(err)
		}
		done.Listing = &listing
	case runnerwire.RepoOpRead:
		file, err := readRepoFile(root, req.Path)
		if err != nil {
			return fail(err)
		}
		done.File = &file
	case runnerwire.RepoOpStatus:
		status, err := repoStatus(ctx, root)
		if err != nil {
			return fail(err)
		}
		done.Status = &status
	case runnerwire.RepoOpLog:
		log, err := repoLog(ctx, root, req.Limit)
		if err != nil {
			return fail(err)
		}
		done.Log = log
	case runnerwire.RepoOpDiff:
		if !runnerwire.ValidRepoRef(req.Ref) {
			return fail(refuseRepo(runnerwire.RepoErrBadPath, "a diff's ref is a full commit id or empty"))
		}
		diff, err := repoDiff(ctx, root, req.Ref)
		if err != nil {
			return fail(err)
		}
		done.Diff = &diff
	default:
		return fail(refuseRepo(runnerwire.RepoErrBadPath, "unknown repo operation"))
	}
	return done
}

// repoRoot is a registered repo's directory, symlinks resolved.
func repoRoot(cfg *Config, repoID string) (string, error) {
	if cfg == nil || repoID == "" {
		return "", refuseRepo(runnerwire.RepoErrNotFound, "no such repo on this runner")
	}
	for _, r := range cfg.Repos {
		if r.ID != repoID {
			continue
		}
		root, err := filepath.EvalSymlinks(r.Path)
		if err != nil {
			return "", refuseRepo(runnerwire.RepoErrNotFound, "the repo's directory is gone")
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return "", refuseRepo(runnerwire.RepoErrNotFound, "the repo's directory is gone")
		}
		return abs, nil
	}
	return "", refuseRepo(runnerwire.RepoErrNotFound, "no such repo on this runner")
}

// cleanRepoPath checks a request path and returns it slash-separated and
// clean ("" for the root).
func cleanRepoPath(p string) (string, error) {
	bad := refuseRepo(runnerwire.RepoErrBadPath, "the path is not inside the repo")
	if len(p) > runnerwire.MaxRepoPathBytes || !utf8.ValidString(p) ||
		strings.ContainsAny(p, "\x00\\:") || strings.HasPrefix(p, "/") {
		return "", bad
	}
	var segs []string
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "", ".":
			continue
		case "..":
			return "", bad
		}
		segs = append(segs, seg)
	}
	return strings.Join(segs, "/"), nil
}

// inside reports whether target is root or under it.
func inside(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

// resolveRepoPath is rel's real location under root; a path through a
// symlink out of the repo is bad, a hidden one hidden.
func resolveRepoPath(root, rel string) (string, error) {
	clean, err := cleanRepoPath(rel)
	if err != nil {
		return "", err
	}
	if clean != "" && (secretPath(clean) || gitPath(clean)) {
		return "", refuseRepo(runnerwire.RepoErrHidden, "this file is hidden because it may hold secrets")
	}
	full := filepath.Join(root, filepath.FromSlash(clean))
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// A dangling link out of the repo is still out of the repo.
			if _, lerr := os.Lstat(full); lerr == nil {
				return "", refuseRepo(runnerwire.RepoErrBadPath, "the path is not inside the repo")
			}
			// So is a missing file under a link out: "no such file" there
			// would tell what exists outside the repo.
			if !existingInside(root, full) {
				return "", refuseRepo(runnerwire.RepoErrBadPath, "the path is not inside the repo")
			}
			return "", refuseRepo(runnerwire.RepoErrNotFound, "no such file")
		}
		return "", refuseRepo(runnerwire.RepoErrBadPath, "the path is not inside the repo")
	}
	if !inside(root, real) || throughMountPoint(root, real) {
		return "", refuseRepo(runnerwire.RepoErrBadPath, "the path is not inside the repo")
	}
	if relReal, err := filepath.Rel(root, real); err == nil && relReal != "." {
		slashed := filepath.ToSlash(relReal)
		if secretPath(slashed) || gitPath(slashed) {
			return "", refuseRepo(runnerwire.RepoErrHidden, "this file is hidden because it may hold secrets")
		}
	}
	return real, nil
}

// existingInside reports whether the deepest existing directory above full
// is inside root, links and junctions included. A dangling link on the way
// is not: where it points is outside what can be checked, and skipping it
// would answer "no such file" for a path under a link out of the repo (on
// Linux EvalSymlinks reports a missing link target as not existing).
func existingInside(root, full string) bool {
	for p := filepath.Dir(full); inside(root, p); p = filepath.Dir(p) {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			return inside(root, real) && !throughMountPoint(root, real)
		}
		if _, err := os.Lstat(p); err == nil {
			return false
		}
		if p == root {
			break
		}
	}
	return true
}

// throughMountPoint reports whether any part of target below root is a
// Windows junction (or another mount point). EvalSymlinks does not follow
// those, so where one leads is unknown, and an agent needs no privilege to
// make one: it is never gone through, whatever it points at.
func throughMountPoint(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." {
		return false
	}
	cur := root
	for _, seg := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, seg)
		info, err := os.Lstat(cur)
		if err != nil {
			return false
		}
		if info.Mode()&fs.ModeIrregular != 0 {
			return true
		}
	}
	return false
}

// gitPath reports whether rel is in a .git directory: the repo's own or a
// nested checkout's.
func gitPath(rel string) bool {
	for _, seg := range strings.Split(rel, "/") {
		if strings.EqualFold(seg, ".git") {
			return true
		}
	}
	return false
}

func listRepo(root, rel string) (runnerwire.RepoListing, error) {
	clean, err := cleanRepoPath(rel)
	if err != nil {
		return runnerwire.RepoListing{}, err
	}
	dir, err := resolveRepoPath(root, clean)
	if err != nil {
		return runnerwire.RepoListing{}, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return runnerwire.RepoListing{}, refuseRepo(runnerwire.RepoErrNotFound, "no such directory")
	}
	if !info.IsDir() {
		return runnerwire.RepoListing{}, refuseRepo(runnerwire.RepoErrBadPath, "not a directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return runnerwire.RepoListing{}, refuseRepo(runnerwire.RepoErrNotFound, "the directory cannot be read")
	}
	out := runnerwire.RepoListing{Path: clean, Entries: []runnerwire.RepoEntry{}}
	for _, e := range entries {
		name := e.Name()
		if strings.EqualFold(name, ".git") {
			continue
		}
		if secretName(name) {
			out.Hidden++
			continue
		}
		entry := runnerwire.RepoEntry{Name: name, Type: "file"}
		switch {
		// A Windows junction reads as irregular, not as a symlink: it is a
		// link all the same.
		case e.Type()&(fs.ModeSymlink|fs.ModeIrregular) != 0:
			entry.Type = "symlink"
			target, err := filepath.EvalSymlinks(filepath.Join(dir, name))
			entry.Escape = err != nil || !inside(root, target) || throughMountPoint(root, target)
		case e.IsDir():
			entry.Type = "dir"
		case !e.Type().IsRegular():
			// A FIFO, socket or device: named, never read.
			entry.Type = "other"
		default:
			if info, err := e.Info(); err == nil {
				entry.Size = info.Size()
			}
		}
		out.Entries = append(out.Entries, entry)
	}
	sort.SliceStable(out.Entries, func(i, j int) bool {
		a, b := out.Entries[i], out.Entries[j]
		if (a.Type == "dir") != (b.Type == "dir") {
			return a.Type == "dir"
		}
		return a.Name < b.Name
	})
	if len(out.Entries) > runnerwire.MaxRepoEntries {
		out.Entries, out.Truncated = out.Entries[:runnerwire.MaxRepoEntries], true
	}
	return out, nil
}

func readRepoFile(root, rel string) (runnerwire.RepoFile, error) {
	clean, err := cleanRepoPath(rel)
	if err != nil {
		return runnerwire.RepoFile{}, err
	}
	if clean == "" {
		return runnerwire.RepoFile{}, refuseRepo(runnerwire.RepoErrBadPath, "not a file")
	}
	real, err := resolveRepoPath(root, clean)
	if err != nil {
		return runnerwire.RepoFile{}, err
	}
	// Only a regular file is opened, and without waiting: a run's worktree
	// is the agent's, and a FIFO there would hold this read (and a repo slot)
	// for good. The mode is checked again once open, in case it was swapped.
	if info, err := os.Stat(real); err != nil {
		return runnerwire.RepoFile{}, refuseRepo(runnerwire.RepoErrNotFound, "the file cannot be read")
	} else if !info.Mode().IsRegular() {
		return runnerwire.RepoFile{}, refuseRepo(runnerwire.RepoErrBadPath, "not a file")
	}
	f, err := openNoBlock(real)
	if err != nil {
		return runnerwire.RepoFile{}, refuseRepo(runnerwire.RepoErrNotFound, "the file cannot be read")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return runnerwire.RepoFile{}, refuseRepo(runnerwire.RepoErrBadPath, "not a file")
	}
	buf, err := io.ReadAll(io.LimitReader(f, runnerwire.MaxRepoFileBytes+1))
	if err != nil {
		return runnerwire.RepoFile{}, refuseRepo(runnerwire.RepoErrNotFound, "the file cannot be read")
	}
	out := runnerwire.RepoFile{Path: clean, Size: info.Size()}
	if len(buf) > runnerwire.MaxRepoFileBytes || info.Size() > runnerwire.MaxRepoFileBytes {
		out.Truncated = true
		buf = buf[:min(len(buf), runnerwire.MaxRepoFileBytes)]
	}
	text, binary := textOf(buf, out.Truncated)
	out.Binary = binary
	if !binary {
		out.Content = text
	}
	return out, nil
}

// textOf is buf as text, or binary when it holds a NUL early on or is not
// UTF-8 (a rune cut by truncation is dropped first).
func textOf(buf []byte, truncated bool) (string, bool) {
	if strings.IndexByte(string(buf[:min(len(buf), binarySniffBytes)]), 0) >= 0 {
		return "", true
	}
	if truncated {
		for cut := 0; cut < utf8.UTFMax && len(buf) > 0; cut++ {
			if utf8.Valid(buf) {
				break
			}
			buf = buf[:len(buf)-1]
		}
	}
	if !utf8.Valid(buf) {
		return "", true
	}
	return string(buf), false
}
