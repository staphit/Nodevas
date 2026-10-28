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
	"sync"
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
	repoBrowseConcurrent = 4
	repoBrowsePerMinute  = 120
	// binarySniffBytes is how much of a file is checked for a NUL byte.
	binarySniffBytes = 8 << 10
)

// repoLimiter bounds repo requests: a few at once, and a rate per minute.
var repoLimiter = struct {
	sync.Mutex
	active int
	recent []time.Time
}{}

func acquireRepoSlot(context.Context) error {
	if !tryRepoSlot() {
		return refuseRepo(runnerwire.RepoErrBusy, "too many repo requests; try again shortly")
	}
	return nil
}

func tryRepoSlot() bool {
	repoLimiter.Lock()
	defer repoLimiter.Unlock()
	now := time.Now()
	kept := repoLimiter.recent[:0]
	for _, t := range repoLimiter.recent {
		if now.Sub(t) < time.Minute {
			kept = append(kept, t)
		}
	}
	repoLimiter.recent = kept
	if repoLimiter.active >= repoBrowseConcurrent || len(repoLimiter.recent) >= repoBrowsePerMinute {
		return false
	}
	repoLimiter.active++
	repoLimiter.recent = append(repoLimiter.recent, now)
	return true
}

func releaseRepoSlot() {
	repoLimiter.Lock()
	repoLimiter.active--
	repoLimiter.Unlock()
}

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
			return "", refuseRepo(runnerwire.RepoErrNotFound, "no such file")
		}
		return "", refuseRepo(runnerwire.RepoErrBadPath, "the path is not inside the repo")
	}
	if !inside(root, real) {
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
		case e.Type()&fs.ModeSymlink != 0:
			entry.Type = "symlink"
			target, err := filepath.EvalSymlinks(filepath.Join(dir, name))
			entry.Escape = err != nil || !inside(root, target)
		case e.IsDir():
			entry.Type = "dir"
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
	f, err := os.Open(real)
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
