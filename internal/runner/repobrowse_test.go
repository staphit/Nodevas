package runner

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

const browseOwner = "u-owner"

func writeFile(t *testing.T, root, rel string, data []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// browseRepo is a plain directory registered as repo "r1".
func browseRepo(t *testing.T) (*Config, string) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "src/math.js", []byte("export const add = (a, b) => a + b;\n"))
	writeFile(t, dir, "README.md", []byte("# Repo\n"))
	writeFile(t, dir, ".env", []byte("TOKEN=secret\n"))
	writeFile(t, dir, ".env.example", []byte("TOKEN=\n"))
	writeFile(t, dir, ".git/HEAD", []byte("ref: refs/heads/main\n"))
	return &Config{Repos: []runnerwire.Repo{{ID: "r1", Name: "repo", Path: dir}}}, dir
}

func browse(cfg *Config, op, path string) runnerwire.RepoDone {
	return BrowseRepo(context.Background(), cfg, browseOwner, runnerwire.RepoRequest{
		ID: "q", AccountID: browseOwner, RepoID: "r1", Op: op, Path: path,
	})
}

// B-42: listing, the secret deny list, and who may browse at all.
func TestRepoBrowseList(t *testing.T) {
	cfg, _ := browseRepo(t)
	done := browse(cfg, runnerwire.RepoOpList, "")
	if done.Code != "" || done.Listing == nil {
		t.Fatalf("list = %+v", done)
	}
	var names []string
	for _, e := range done.Listing.Entries {
		names = append(names, e.Name+":"+e.Type)
	}
	if got := strings.Join(names, ","); got != "src:dir,.env.example:file,README.md:file" {
		t.Fatalf("entries = %s, want src first, no .git, no .env", got)
	}
	if done.Listing.Hidden != 1 || done.Listing.Truncated {
		t.Fatalf("hidden %d truncated %v, want 1 false", done.Listing.Hidden, done.Listing.Truncated)
	}
	if got := browse(cfg, runnerwire.RepoOpRead, ".env"); got.Code != runnerwire.RepoErrHidden || got.File != nil {
		t.Fatalf("read .env = %+v, want hidden", got)
	}

	other := BrowseRepo(context.Background(), cfg, browseOwner, runnerwire.RepoRequest{ID: "q", AccountID: browseOwner, RepoID: "nope", Op: runnerwire.RepoOpList})
	if other.Code != runnerwire.RepoErrNotFound {
		t.Fatalf("unregistered repo = %+v, want not_found", other)
	}
	stranger := BrowseRepo(context.Background(), cfg, browseOwner, runnerwire.RepoRequest{ID: "q", AccountID: "u-else", RepoID: "r1", Op: runnerwire.RepoOpList})
	if stranger.Code != runnerwire.RepoErrRefused || stranger.Listing != nil {
		t.Fatalf("another account = %+v, want refused", stranger)
	}

	if !slices.Contains(capabilities(cfg), runnerwire.CapRepoBrowse) {
		t.Fatal("repo browsing is on by default but not in the capabilities")
	}
	off := false
	cfg.RepoBrowse = &off
	if got := browse(cfg, runnerwire.RepoOpList, ""); got.Code != runnerwire.RepoErrOff {
		t.Fatalf("browse off = %+v, want off", got)
	}
	if slices.Contains(capabilities(cfg), runnerwire.CapRepoBrowse) {
		t.Fatal("repo browsing off but still in the capabilities")
	}
}

// B-43: paths that try to leave the repo.
func TestRepoBrowseBadPaths(t *testing.T) {
	cfg, _ := browseRepo(t)
	for _, bad := range []string{"../x", "/etc/passwd", `C:\x`, "C:/x", "a/../../b", "a\x00b", "a:b", `a\b`, `\\server\share`,
		"src/../../x", strings.Repeat("a/", runnerwire.MaxRepoPathBytes)} {
		for _, op := range []string{runnerwire.RepoOpList, runnerwire.RepoOpRead} {
			if got := browse(cfg, op, bad); got.Code != runnerwire.RepoErrBadPath {
				t.Errorf("%s %q = code %q, want bad_path", op, bad, got.Code)
			}
		}
	}
}

// B-44: a symlink out of the repo is listed as an escape and never
// followed; one inside the repo is readable.
func TestRepoBrowseSymlinks(t *testing.T) {
	cfg, dir := browseRepo(t)
	outside := t.TempDir()
	writeFile(t, outside, "secret.txt", []byte("outside\n"))
	if err := os.Symlink(outside, filepath.Join(dir, "out")); err != nil {
		t.Skipf("no symlink privilege: %v", err)
	}
	if err := os.Symlink(filepath.Join(dir, "README.md"), filepath.Join(dir, "readme-link")); err != nil {
		t.Skipf("no symlink privilege: %v", err)
	}
	done := browse(cfg, runnerwire.RepoOpList, "")
	var escape *runnerwire.RepoEntry
	for i, e := range done.Listing.Entries {
		if e.Name == "out" {
			escape = &done.Listing.Entries[i]
		}
	}
	if escape == nil || escape.Type != "symlink" || !escape.Escape {
		t.Fatalf("escaping symlink entry = %+v", escape)
	}
	for _, op := range []string{runnerwire.RepoOpList, runnerwire.RepoOpRead} {
		path := "out"
		if op == runnerwire.RepoOpRead {
			path = "out/secret.txt"
		}
		if got := browse(cfg, op, path); got.Code != runnerwire.RepoErrBadPath {
			t.Errorf("%s through the escaping symlink = %+v, want bad_path", op, got)
		}
	}
	if got := browse(cfg, runnerwire.RepoOpRead, "readme-link"); got.Code != "" || got.File == nil || got.File.Content != "# Repo\n" {
		t.Fatalf("symlink inside the repo = %+v", got)
	}
}

// B-45: reads are capped, binaries are not shown, keys are hidden.
func TestRepoBrowseRead(t *testing.T) {
	cfg, dir := browseRepo(t)
	writeFile(t, dir, "big.txt", []byte(strings.Repeat("x", 600<<10)))
	writeFile(t, dir, "logo.bin", append([]byte("PNG"), 0, 1, 2, 3))
	writeFile(t, dir, "config/id_rsa", []byte("-----BEGIN KEY-----\n"))
	got := browse(cfg, runnerwire.RepoOpRead, "big.txt")
	if got.File == nil || !got.File.Truncated || len(got.File.Content) != runnerwire.MaxRepoFileBytes || got.File.Size != 600<<10 {
		t.Fatalf("big file = code %q truncated %v len %d", got.Code, got.File != nil && got.File.Truncated, len(fileContent(got)))
	}
	if got := browse(cfg, runnerwire.RepoOpRead, "logo.bin"); got.File == nil || !got.File.Binary || got.File.Content != "" {
		t.Fatalf("binary = %+v", got)
	}
	if got := browse(cfg, runnerwire.RepoOpRead, "config/id_rsa"); got.Code != runnerwire.RepoErrHidden {
		t.Fatalf("config/id_rsa = %+v, want hidden", got)
	}
	if got := browse(cfg, runnerwire.RepoOpRead, "src/math.js"); got.File == nil || !strings.Contains(got.File.Content, "add") {
		t.Fatalf("src/math.js = %+v", got)
	}
}

func fileContent(d runnerwire.RepoDone) string {
	if d.File == nil {
		return ""
	}
	return d.File.Content
}

// B-46: git runs hardened: neither a textconv nor an external diff driver
// the repo configures ever runs; patches are capped; secrets are dropped
// from status and diffs.
func TestRepoBrowseGitIsHardened(t *testing.T) {
	repo := newTestRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	script := filepath.Join(t.TempDir(), "evil.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho ran > '"+filepath.ToSlash(marker)+"'\ncat \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "config", "diff.evil.textconv", filepath.ToSlash(script))
	gitT(t, repo, "config", "diff.external", filepath.ToSlash(script))
	writeFile(t, repo, ".gitattributes", []byte("*.txt diff=evil\n"))
	writeFile(t, repo, "a.txt", []byte("one\n"))
	writeFile(t, repo, ".env", []byte("TOKEN=one\n"))
	gitT(t, repo, "add", "-f", ".gitattributes", "a.txt", ".env")
	gitT(t, repo, "commit", "-q", "-m", "second")
	sha := gitT(t, repo, "rev-parse", "HEAD")
	writeFile(t, repo, "a.txt", []byte("two\n"))
	writeFile(t, repo, ".env", []byte("TOKEN=two\n"))
	writeFile(t, repo, "big.go", []byte(strings.Repeat("// filler line\n", 30000)))

	cfg := &Config{Repos: []runnerwire.Repo{{ID: "r1", Name: "repo", Path: repo}}}
	status := browse(cfg, runnerwire.RepoOpStatus, "")
	if status.Status == nil || status.Status.Branch != "main" || status.Status.Hidden != 1 {
		t.Fatalf("status = %+v", status)
	}
	for _, e := range status.Status.Entries {
		if e.Path == ".env" {
			t.Fatal(".env listed in status")
		}
	}
	log := BrowseRepo(context.Background(), cfg, browseOwner, runnerwire.RepoRequest{ID: "q", AccountID: browseOwner, RepoID: "r1", Op: runnerwire.RepoOpLog, Limit: 5})
	if len(log.Log) != 2 || log.Log[0].SHA != sha || log.Log[0].Subject != "second" {
		t.Fatalf("log = %+v", log)
	}
	worktree := browse(cfg, runnerwire.RepoOpDiff, "")
	if worktree.Diff == nil || !worktree.Diff.Truncated || len(worktree.Diff.Patch) > runnerwire.MaxRepoPatchBytes ||
		strings.Contains(worktree.Diff.Patch, "TOKEN") || worktree.Diff.Hidden != 1 {
		t.Fatalf("worktree diff = code %q truncated %v hidden %d", worktree.Code, worktree.Diff != nil && worktree.Diff.Truncated, hiddenOf(worktree))
	}
	commit := BrowseRepo(context.Background(), cfg, browseOwner, runnerwire.RepoRequest{ID: "q", AccountID: browseOwner, RepoID: "r1", Op: runnerwire.RepoOpDiff, Ref: sha})
	if commit.Diff == nil || !strings.Contains(commit.Diff.Patch, "a.txt") || strings.Contains(commit.Diff.Patch, "TOKEN") {
		t.Fatalf("commit diff = %+v", commit)
	}
	if bad := BrowseRepo(context.Background(), cfg, browseOwner, runnerwire.RepoRequest{ID: "q", AccountID: browseOwner, RepoID: "r1", Op: runnerwire.RepoOpDiff, Ref: "HEAD~1"}); bad.Code != runnerwire.RepoErrBadPath {
		t.Fatalf("diff ref HEAD~1 = %+v, want bad_path", bad)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a textconv or external diff driver the repo configured ran")
	}

	// Control: plain git does run the driver, so the test proves something.
	gitT(t, repo, "diff", "--", "a.txt")
	if _, err := os.Stat(marker); err != nil {
		t.Skipf("the driver never runs here, so this proves nothing: %v", err)
	}
}

func hiddenOf(d runnerwire.RepoDone) int {
	if d.Diff == nil {
		return -1
	}
	return d.Diff.Hidden
}

// A filter driver whose name has capitals (git keeps a config subsection's
// case) never runs either: blanking it under a lowercased name would miss it.
func TestRepoBrowseMixedCaseFilterNeverRuns(t *testing.T) {
	repo := newTestRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	script := filepath.Join(t.TempDir(), "evil.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho ran > '"+filepath.ToSlash(marker)+"'\ncat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, ".gitattributes", []byte("*.txt filter=EvilFilter\n"))
	writeFile(t, repo, "a.txt", []byte("one\n"))
	gitT(t, repo, "add", ".gitattributes", "a.txt")
	gitT(t, repo, "commit", "-q", "-m", "filtered")
	gitT(t, repo, "config", "filter.EvilFilter.clean", "sh '"+filepath.ToSlash(script)+"'")
	writeFile(t, repo, "a.txt", []byte("two\n"))

	cfg := &Config{Repos: []runnerwire.Repo{{ID: "r1", Name: "repo", Path: repo}}}
	if got := browse(cfg, runnerwire.RepoOpStatus, ""); got.Code != "" {
		t.Fatalf("status = %+v", got)
	}
	if got := browse(cfg, runnerwire.RepoOpDiff, ""); got.Code != "" {
		t.Fatalf("diff = %+v", got)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a mixed-case filter driver the repo configured ran")
	}
	// Control: plain git runs it, so the test proves something.
	gitT(t, repo, "diff", "--", "a.txt")
	if _, err := os.Stat(marker); err != nil {
		t.Skipf("the filter never runs here, so this proves nothing: %v", err)
	}
}

// A nested repository's .git (a vendored or submodule checkout) is never
// listed or read, like the root's.
func TestRepoBrowseNestedGitHidden(t *testing.T) {
	cfg, dir := browseRepo(t)
	writeFile(t, dir, "vendor/lib/.git/config", []byte("[remote \"origin\"]\nurl = https://user:token@example.test/x\n"))
	writeFile(t, dir, "vendor/lib/main.go", []byte("package lib\n"))
	done := browse(cfg, runnerwire.RepoOpList, "vendor/lib")
	if done.Listing == nil {
		t.Fatalf("list = %+v", done)
	}
	for _, e := range done.Listing.Entries {
		if e.Name == ".git" {
			t.Fatal("a nested .git is listed")
		}
	}
	if got := browse(cfg, runnerwire.RepoOpRead, "vendor/lib/.git/config"); got.Code == "" || got.File != nil {
		t.Fatalf("read nested .git/config = %+v, want refused", got)
	}
}

// An untracked file in a worktree diff is read only up to the patch cap,
// never whole.
func TestRepoBrowseUntrackedReadCapped(t *testing.T) {
	if got := len(untrackedRead(t)); got > runnerwire.MaxRepoPatchBytes+1 {
		t.Fatalf("read %d bytes of an untracked file, want at most the patch cap", got)
	}
}

func untrackedRead(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "huge.txt", []byte(strings.Repeat("y", runnerwire.MaxRepoPatchBytes*3)))
	data, err := readCapped(filepath.Join(dir, "huge.txt"), runnerwire.MaxRepoPatchBytes)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
