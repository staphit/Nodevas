package runner

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

const (
	wsRun   = "run-ws"
	wsNode  = "n-1"
	wsOwner = "u-owner"
)

// wsGH is gh for the workspace tests: it records every call and its
// directory, and fails when told to. Never real gh.
type wsGH struct {
	mu    sync.Mutex
	calls [][]string
	dirs  []string
	fail  error
	// after, when set, runs after each call that succeeds.
	after func()
}

func (g *wsGH) run(dir, _ string, args []string) (string, error) {
	g.mu.Lock()
	g.calls = append(g.calls, append([]string(nil), args...))
	g.dirs = append(g.dirs, dir)
	fail, after := g.fail, g.after
	g.mu.Unlock()
	if fail != nil {
		return "", fail
	}
	if after != nil {
		after()
	}
	return "", nil
}

func (g *wsGH) recorded() ([][]string, []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([][]string(nil), g.calls...), append([]string(nil), g.dirs...)
}

// wsEnv is a runner's jobs over a repo whose run left a worktree with one
// commit on its own branch, without a hub: answers are read with drain.
type wsEnv struct {
	j       *jobs
	cfg     *Config
	gh      *wsGH
	repo    string
	dataDir string
	wt      Worktree
	tip     string
	log     *safeLog
}

func newWSEnv(t *testing.T, tweak func(*Config)) *wsEnv {
	t.Helper()
	hermeticGit(t)
	repo := newTestRepo(t)
	dataDir := t.TempDir()
	wt, err := CreateWorktree(context.Background(), repo, dataDir, wsRun, wsNode, "main")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, wt.Path, "feature.txt", []byte("feature\n"))
	writeFile(t, wt.Path, "README", []byte("hi from the run\n"))
	gitT(t, wt.Path, "add", "-A")
	gitT(t, wt.Path, "commit", "-q", "-m", "the run's work")
	env := &wsEnv{gh: &wsGH{}, repo: repo, dataDir: dataDir, wt: wt, log: &safeLog{},
		tip: gitT(t, wt.Path, "rev-parse", "HEAD"),
		cfg: &Config{DataDir: dataDir, Repos: []runnerwire.Repo{{ID: "r1", Name: "demo", Path: repo}}}}
	if tweak != nil {
		tweak(env.cfg)
	}
	env.j = env.start(t)
	return env
}

// start makes a fresh jobs over the same data directory, as a restarted
// runner would.
func (e *wsEnv) start(t *testing.T) *jobs {
	t.Helper()
	c := &Client{Config: e.cfg, Log: e.log, Commands: &fakeCommands{paths: map[string]string{"gh": "/bin/gh"}, gh: e.gh.run}}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	j := newJobs(ctx, c)
	j.loadPRs()
	j.loadMerges()
	j.loadWorkspaceActions()
	return j
}

func (e *wsEnv) browse(op, path string) runnerwire.RepoDone {
	return e.j.browseRun(context.Background(), wsOwner, runnerwire.RepoRequest{ID: "q", AccountID: wsOwner, RepoID: "r1",
		Op: op, Path: path, RunID: wsRun, NodeID: wsNode, Branch: e.wt.Branch, Base: "main"})
}

func (e *wsEnv) info(t *testing.T) runnerwire.RunWorkspaceInfo {
	t.Helper()
	done := e.browse(runnerwire.RepoOpRunInfo, "")
	if done.Code != "" || done.RunInfo == nil {
		t.Fatalf("run_info = %+v", done)
	}
	return *done.RunInfo
}

// link makes a symlink, or on Windows without the privilege a junction,
// at name pointing to target.
func link(t *testing.T, target, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, name); err == nil {
		return
	} else if runtime.GOOS != "windows" {
		t.Skipf("no symlink: %v", err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", name, target).CombinedOutput(); err != nil {
		t.Skipf("no symlink or junction: %v %s", err, out)
	}
}

// symlinkOrSkip makes a real symlink (file targets need one).
func symlinkOrSkip(t *testing.T, target, name string) {
	t.Helper()
	if err := os.Symlink(target, name); err != nil {
		t.Skipf("no symlink privilege: %v", err)
	}
}

// Listing and reading are rooted at the run's worktree: its files, never
// the repo root's.
func TestRunBrowseScopedToWorktree(t *testing.T) {
	env := newWSEnv(t, nil)
	writeFile(t, env.repo, "rootonly.txt", []byte("only in the repo root\n"))
	done := env.browse(runnerwire.RepoOpRunList, "")
	if done.Code != "" || done.Listing == nil {
		t.Fatalf("list = %+v", done)
	}
	var names []string
	for _, e := range done.Listing.Entries {
		names = append(names, e.Name)
	}
	if !slices.Contains(names, "feature.txt") || slices.Contains(names, "rootonly.txt") || slices.Contains(names, ".git") {
		t.Fatalf("entries = %v", names)
	}
	if got := env.browse(runnerwire.RepoOpRunRead, "feature.txt"); got.File == nil || got.File.Content != "feature\n" {
		t.Fatalf("read feature.txt = %+v", got)
	}
	if got := env.browse(runnerwire.RepoOpRunRead, "README"); got.File == nil || got.File.Content != "hi from the run\n" {
		t.Fatalf("read README = %+v, want the run's", got)
	}
	if got := env.browse(runnerwire.RepoOpRunRead, "rootonly.txt"); got.Code != runnerwire.RepoErrNotFound {
		t.Fatalf("a repo root file through the run = %+v", got)
	}
	writeFile(t, env.wt.Path, "wip.txt", []byte("not committed\n"))
	status := env.browse(runnerwire.RepoOpRunStatus, "")
	if status.Status == nil || status.Status.Branch != env.wt.Branch || status.Status.Head != env.tip ||
		len(status.Status.Entries) != 1 || status.Status.Entries[0].Path != "wip.txt" {
		t.Fatalf("status = %+v", status)
	}

	// Gone: a coded answer, not the repo root.
	if err := os.RemoveAll(env.wt.Path); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{runnerwire.RepoOpRunList, runnerwire.RepoOpRunRead, runnerwire.RepoOpRunStatus} {
		if got := env.browse(op, "feature.txt"); got.Code != runnerwire.RepoErrNoWorktree {
			t.Errorf("%s with no worktree = %+v", op, got)
		}
	}
}

func TestRunBrowseBadPaths(t *testing.T) {
	env := newWSEnv(t, nil)
	for _, bad := range []string{"..", "../x", "a/../../x", "/etc", `C:\x`, "C:/x", `a\b`, "x:stream", "a\x00b",
		strings.Repeat("a/", runnerwire.MaxRepoPathBytes)} {
		for _, op := range []string{runnerwire.RepoOpRunList, runnerwire.RepoOpRunRead} {
			if got := env.browse(op, bad); got.Code != runnerwire.RepoErrBadPath {
				t.Errorf("%s %q = %+v, want bad_path", op, bad, got.Code)
			}
		}
	}
	// A run id that is not a plain name is refused before any path.
	got := env.j.browseRun(context.Background(), wsOwner, runnerwire.RepoRequest{ID: "q", AccountID: wsOwner, RepoID: "r1",
		Op: runnerwire.RepoOpRunList, RunID: "../" + wsRun})
	if got.Code != runnerwire.RepoErrBadPath {
		t.Fatalf("run id ../ = %+v", got)
	}
}

func TestRunBrowseSymlinkEscape(t *testing.T) {
	env := newWSEnv(t, nil)
	outside := t.TempDir()
	root := "/"
	if runtime.GOOS == "windows" {
		root = os.Getenv("SystemDrive") + `\`
	}
	// Directory links: symlinks, or junctions where symlinks need a
	// privilege.
	link(t, env.repo, filepath.Join(env.wt.Path, "torepo"))
	link(t, env.dataDir, filepath.Join(env.wt.Path, "todata"))
	link(t, root, filepath.Join(env.wt.Path, "toroot"))
	link(t, filepath.Join(outside, "missing"), filepath.Join(env.wt.Path, "dangle"))
	done := env.browse(runnerwire.RepoOpRunList, "")
	escapes := map[string]bool{}
	for _, e := range done.Listing.Entries {
		if e.Type == "symlink" {
			escapes[e.Name] = e.Escape
		}
	}
	for _, name := range []string{"torepo", "todata", "toroot", "dangle"} {
		if !escapes[name] {
			t.Errorf("%s not listed as an escaping link: %+v", name, done.Listing.Entries)
		}
		if got := env.browse(runnerwire.RepoOpRunList, name); got.Code != runnerwire.RepoErrBadPath {
			t.Errorf("list through %s = %+v", name, got)
		}
		if got := env.browse(runnerwire.RepoOpRunRead, name+"/README"); got.Code != runnerwire.RepoErrBadPath {
			t.Errorf("read through %s = %+v", name, got)
		}
	}
	if got := env.browse(runnerwire.RepoOpRunRead, "dangle"); got.Code != runnerwire.RepoErrBadPath {
		t.Errorf("dangling link out = %+v", got)
	}
	// A link to a file inside stays readable (a file link needs a real
	// symlink).
	symlinkOrSkip(t, filepath.Join(env.wt.Path, "feature.txt"), filepath.Join(env.wt.Path, "inside"))
	done = env.browse(runnerwire.RepoOpRunList, "")
	for _, e := range done.Listing.Entries {
		if e.Name == "inside" && e.Escape {
			t.Error("a link to a file inside the worktree is marked escape")
		}
	}
	if got := env.browse(runnerwire.RepoOpRunRead, "inside"); got.File == nil || got.File.Content != "feature\n" {
		t.Errorf("link inside = %+v", got)
	}
}

// An agent that swaps its own worktree for a link to somewhere else gets
// nothing read through it.
func TestRunBrowseWorktreeSwappedForLink(t *testing.T) {
	env := newWSEnv(t, nil)
	elsewhere := t.TempDir()
	writeFile(t, elsewhere, "id_ed25519.pub", []byte("ssh-ed25519 AAAA\n"))
	writeFile(t, elsewhere, "notes.txt", []byte("private\n"))
	if err := os.RemoveAll(env.wt.Path); err != nil {
		t.Fatal(err)
	}
	link(t, elsewhere, env.wt.Path)
	for _, op := range []string{runnerwire.RepoOpRunList, runnerwire.RepoOpRunRead, runnerwire.RepoOpRunStatus} {
		got := env.browse(op, "notes.txt")
		if got.Code != runnerwire.RepoErrRefused || got.Listing != nil || got.File != nil || got.Status != nil {
			t.Errorf("%s through a swapped worktree = %+v", op, got)
		}
	}
	if info := env.info(t); info.Exists {
		t.Errorf("info calls a link the worktree: %+v", info)
	}
	// Discarding removes the link only, never what it points at.
	out := env.act(t, env.discardJob("d-1"))
	if out.Code != "" {
		t.Fatalf("discard = %+v", out)
	}
	if _, err := os.Lstat(env.wt.Path); err == nil {
		t.Error("the link is still there")
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "notes.txt")); err != nil {
		t.Errorf("discard followed the link: %v", err)
	}
}

func TestRunBrowseHidesSecretsAndGit(t *testing.T) {
	env := newWSEnv(t, nil)
	writeFile(t, env.wt.Path, ".env", []byte("TOKEN=secret\n"))
	writeFile(t, env.wt.Path, "vendor/lib/.git/config", []byte("[remote]\nurl = https://u:t@example.test\n"))
	writeFile(t, env.wt.Path, "vendor/lib/x.go", []byte("package lib\n"))
	done := env.browse(runnerwire.RepoOpRunList, "")
	for _, e := range done.Listing.Entries {
		if e.Name == ".git" || e.Name == ".env" {
			t.Errorf("listed %s", e.Name)
		}
	}
	if done.Listing.Hidden != 1 {
		t.Errorf("hidden = %d, want 1", done.Listing.Hidden)
	}
	for _, path := range []string{".env", ".git", "vendor/lib/.git/config"} {
		if got := env.browse(runnerwire.RepoOpRunRead, path); got.File != nil || got.Code == "" {
			t.Errorf("read %s = %+v", path, got)
		}
	}
	nested := env.browse(runnerwire.RepoOpRunList, "vendor/lib")
	for _, e := range nested.Listing.Entries {
		if e.Name == ".git" {
			t.Error("a nested .git is listed")
		}
	}
}

// The plain ops still browse the repo root, and an op this runner does not
// know is refused, never served from the root: what a runner from before
// run workspaces does with run_list.
func TestRunBrowseOldOpOnRepoRootUnchanged(t *testing.T) {
	env := newWSEnv(t, nil)
	writeFile(t, env.repo, "rootonly.txt", []byte("root\n"))
	root := BrowseRepo(context.Background(), env.cfg, wsOwner, runnerwire.RepoRequest{ID: "q", AccountID: wsOwner, RepoID: "r1", Op: runnerwire.RepoOpList})
	if root.Listing == nil || !slices.ContainsFunc(root.Listing.Entries, func(e runnerwire.RepoEntry) bool { return e.Name == "rootonly.txt" }) {
		t.Fatalf("repo root list = %+v", root)
	}
	old := BrowseRepo(context.Background(), env.cfg, wsOwner, runnerwire.RepoRequest{ID: "q", AccountID: wsOwner, RepoID: "r1",
		Op: runnerwire.RepoOpRunList, RunID: wsRun})
	if old.Code != runnerwire.RepoErrBadPath || old.Listing != nil {
		t.Fatalf("run_list through the plain browse = %+v", old)
	}
	for op, want := range map[string]bool{runnerwire.RepoOpRunList: true, runnerwire.RepoOpRunRead: true,
		runnerwire.RepoOpRunStatus: true, runnerwire.RepoOpRunInfo: true, runnerwire.RepoOpList: false, "": false} {
		if runnerwire.RunRepoOp(op) != want {
			t.Errorf("RunRepoOp(%q) != %v", op, want)
		}
	}
	if got := env.browse(runnerwire.RepoOpList, ""); got.Code != runnerwire.RepoErrBadPath {
		t.Fatalf("a plain op through the run browse = %+v", got)
	}
}

func TestRunBrowseOwnerOnly(t *testing.T) {
	env := newWSEnv(t, nil)
	for _, owner := range []string{"", "u-else"} {
		got := env.j.browseRun(context.Background(), wsOwner, runnerwire.RepoRequest{ID: "q", AccountID: owner, RepoID: "r1",
			Op: runnerwire.RepoOpRunList, RunID: wsRun})
		if got.Code != runnerwire.RepoErrRefused || got.Listing != nil {
			t.Errorf("account %q = %+v", owner, got)
		}
	}
	if got := env.j.browseRun(context.Background(), "", runnerwire.RepoRequest{ID: "q", RepoID: "r1",
		Op: runnerwire.RepoOpRunList, RunID: wsRun}); got.Code != runnerwire.RepoErrRefused {
		t.Errorf("no owner known = %+v", got)
	}
	got := env.j.browseRun(context.Background(), wsOwner, runnerwire.RepoRequest{ID: "q", AccountID: wsOwner, RepoID: "nope",
		Op: runnerwire.RepoOpRunList, RunID: wsRun})
	if got.Code != runnerwire.RepoErrNotFound {
		t.Errorf("unregistered repo = %+v", got)
	}
}

func TestRunBrowseOff(t *testing.T) {
	off := false
	env := newWSEnv(t, func(c *Config) { c.RepoBrowse = &off })
	for _, op := range []string{runnerwire.RepoOpRunList, runnerwire.RepoOpRunRead, runnerwire.RepoOpRunStatus} {
		if got := env.browse(op, "feature.txt"); got.Code != runnerwire.RepoErrOff {
			t.Errorf("%s with browsing off = %+v", op, got)
		}
	}
	// The info shows no file content, so it still answers.
	if info := env.info(t); !info.Exists {
		t.Fatalf("info = %+v", info)
	}
}

func TestRunInfo(t *testing.T) {
	env := newWSEnv(t, nil)
	writeFile(t, env.wt.Path, "wip.txt", []byte("x\n"))
	writeFile(t, env.wt.Path, "feature.txt", []byte("changed\n"))
	info := env.info(t)
	if !info.Exists || info.Branch != env.wt.Branch || !info.OwnBranch || info.Head != env.tip || !info.BranchExists ||
		info.BranchHead != env.tip || info.Uncommitted != 2 || info.Running || info.Kept || info.WatchedPR != "" ||
		info.BaseCheckout != runnerwire.BaseCheckoutRepo || !info.BaseClean || info.Path == "" {
		t.Fatalf("info = %+v", info)
	}
	// The repo's checkout of main gets a change: not clean.
	writeFile(t, env.repo, "README", []byte("edited\n"))
	if info := env.info(t); info.BaseCheckout != runnerwire.BaseCheckoutRepo || info.BaseClean {
		t.Fatalf("dirty base = %+v", info)
	}
	gitT(t, env.repo, "checkout", "-q", "--", "README")
	// main checked out nowhere, then elsewhere.
	gitT(t, env.repo, "checkout", "-q", "-b", "other")
	if info := env.info(t); info.BaseCheckout != runnerwire.BaseCheckoutNone {
		t.Fatalf("base nowhere = %+v", info)
	}
	elsewhere := filepath.Join(t.TempDir(), "main-wt")
	gitT(t, env.repo, "worktree", "add", "-q", elsewhere, "main")
	if info := env.info(t); info.BaseCheckout != runnerwire.BaseCheckoutElsewhere {
		t.Fatalf("base elsewhere = %+v", info)
	}
	missing := env.j.browseRun(context.Background(), wsOwner, runnerwire.RepoRequest{ID: "q", AccountID: wsOwner, RepoID: "r1",
		Op: runnerwire.RepoOpRunInfo, RunID: wsRun, NodeID: wsNode, Base: "trunk"})
	if missing.RunInfo == nil || missing.RunInfo.BaseCheckout != runnerwire.BaseCheckoutMissing {
		t.Fatalf("no local base = %+v", missing)
	}

	// Still in the runner's hands, watched, kept.
	env.j.mu.Lock()
	env.j.runs[wsRun] = &run{}
	env.j.mu.Unlock()
	env.j.watch(watchedPR{RunID: wsRun, NodeID: wsNode, URL: mergePRURL, Repo: env.repo, Branch: env.wt.Branch,
		PR: runnerwire.PRInfo{URL: mergePRURL, State: runnerwire.PROpen}})
	if err := RecordKept(context.Background(), env.dataDir, env.repo, env.wt, []string{".claude/settings.json"}); err != nil {
		t.Fatal(err)
	}
	if info := env.info(t); !info.Running || info.WatchedPR != mergePRURL || !info.Kept {
		t.Fatalf("running, watched, kept = %+v", info)
	}

	// Gone: not there, but the branch it left is still named.
	gitT(t, env.repo, "worktree", "remove", "--force", env.wt.Path)
	if info := env.info(t); info.Exists || info.Branch != env.wt.Branch || !info.BranchExists || info.Uncommitted != 0 {
		t.Fatalf("gone = %+v", info)
	}
}

// A filter driver written into the repo's config (shared with the agent's
// worktree) never runs when the owner looks at the worktree's changes.
func TestRunBrowseFilterNeverRuns(t *testing.T) {
	env := newWSEnv(t, nil)
	marker := filepath.Join(t.TempDir(), "ran")
	script := filepath.Join(t.TempDir(), "evil.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho ran > '"+filepath.ToSlash(marker)+"'\ncat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, env.wt.Path, ".gitattributes", []byte("*.txt filter=EvilFilter\n"))
	gitT(t, env.wt.Path, "add", ".gitattributes")
	gitT(t, env.wt.Path, "commit", "-q", "-m", "attributes")
	gitT(t, env.wt.Path, "config", "filter.EvilFilter.clean", "sh '"+filepath.ToSlash(script)+"'")
	writeFile(t, env.wt.Path, "feature.txt", []byte("changed\n"))
	if got := env.browse(runnerwire.RepoOpRunStatus, ""); got.Code != "" {
		t.Fatalf("status = %+v", got)
	}
	if info := env.info(t); info.Uncommitted != 1 {
		t.Fatalf("info = %+v", info)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a filter driver in the repo's config ran")
	}
	// Control: plain git runs it, so the test proves something.
	gitT(t, env.wt.Path, "diff", "--", "feature.txt")
	if _, err := os.Stat(marker); err != nil {
		t.Skipf("the filter never runs here, so this proves nothing: %v", err)
	}
}

func TestWorkspaceCapabilities(t *testing.T) {
	cfg := &Config{}
	caps := capabilities(cfg)
	if !slices.Contains(caps, runnerwire.CapRunWorkspace) || !slices.Contains(caps, runnerwire.CapWorkspaceActions) {
		t.Fatalf("default caps = %v", caps)
	}
	if err := cfg.Set("workspaceActions", []string{"false"}); err != nil {
		t.Fatal(err)
	}
	caps = capabilities(cfg)
	if !slices.Contains(caps, runnerwire.CapRunWorkspace) || slices.Contains(caps, runnerwire.CapWorkspaceActions) || cfg.WorkspaceActionsOn() {
		t.Fatalf("actions off: caps = %v", caps)
	}
	if err := cfg.Set("workspaceActions", []string{"maybe"}); err == nil {
		t.Error("workspaceActions maybe accepted")
	}
	if err := cfg.Set("workspaceActions", []string{"default"}); err != nil || cfg.WorkspaceActions != nil || !cfg.WorkspaceActionsOn() {
		t.Fatalf("reset: %v %+v", err, cfg.WorkspaceActions)
	}
}

// A link an agent can make without any privilege (a Windows junction, or a
// symlink elsewhere) never takes the repo browse out of the repo either, nor
// says what exists past it.
func TestRepoBrowseLinkNeverFollowed(t *testing.T) {
	cfg, dir := browseRepo(t)
	outside := t.TempDir()
	writeFile(t, outside, "secret.txt", []byte("outside\n"))
	link(t, outside, filepath.Join(dir, "out"))
	done := browse(cfg, runnerwire.RepoOpList, "")
	escaped := false
	for _, e := range done.Listing.Entries {
		if e.Name == "out" {
			escaped = e.Type == "symlink" && e.Escape
		}
	}
	if !escaped {
		t.Fatalf("link out not listed as an escape: %+v", done.Listing.Entries)
	}
	for _, c := range [][2]string{{runnerwire.RepoOpList, "out"}, {runnerwire.RepoOpRead, "out/secret.txt"},
		{runnerwire.RepoOpRead, "out/missing.txt"}} {
		if got := browse(cfg, c[0], c[1]); got.Code != runnerwire.RepoErrBadPath {
			t.Errorf("%s %s = %+v, want bad_path", c[0], c[1], got)
		}
	}
	if got := browse(cfg, runnerwire.RepoOpRead, "src/missing.txt"); got.Code != runnerwire.RepoErrNotFound {
		t.Errorf("a missing file inside = %+v, want not_found", got)
	}
}

// Removing a run's worktree (a merged PR, prune-kept, a run that changed
// nothing) removes the links an agent left there, never what they point at;
// a worktree swapped for a link loses only the link.
func TestRemoveWorktreeNeverFollowsLinks(t *testing.T) {
	env := newWSEnv(t, nil)
	outside := t.TempDir()
	writeFile(t, outside, "keep.txt", []byte("not the run's\n"))
	link(t, outside, filepath.Join(env.wt.Path, "sub", "escape"))
	if err := removeWorktree(context.Background(), env.repo, env.wt, false); err != nil {
		t.Fatal(err)
	}
	if worktreeExists(env.wt.Path) {
		t.Fatal("worktree still there")
	}
	if _, err := os.Stat(filepath.Join(outside, "keep.txt")); err != nil {
		t.Fatalf("removal went through the link: %v", err)
	}

	elsewhere := t.TempDir()
	writeFile(t, elsewhere, "keep.txt", []byte("mine\n"))
	link(t, elsewhere, env.wt.Path)
	if err := removeWorktree(context.Background(), env.repo, env.wt, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(env.wt.Path); err == nil {
		t.Fatal("the link is still there")
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "keep.txt")); err != nil {
		t.Fatalf("removal went through the swapped worktree: %v", err)
	}
}

// Opening the panel runs nothing the repo's config names in the owner's
// checkout: run_info reads the base with filters emptied.
func TestRunInfoBaseRunsNoFilter(t *testing.T) {
	env := newWSEnv(t, nil)
	marker := filepath.Join(t.TempDir(), "ran")
	script := filepath.Join(t.TempDir(), "evil.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho ran > '"+filepath.ToSlash(marker)+"'\ncat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, env.repo, "config", "filter.Evil.clean", "sh '"+filepath.ToSlash(script)+"'")
	writeFile(t, filepath.Join(env.repo, ".git"), "info/attributes", []byte("* filter=Evil\n"))
	readme := filepath.Join(env.repo, "README")
	raw, err := os.ReadFile(readme)
	if err != nil {
		t.Fatal(err)
	}
	// The same bytes with a new time: git must look at the content again.
	touch := func(at time.Time) {
		writeFile(t, env.repo, "README", raw)
		if err := os.Chtimes(readme, at, at); err != nil {
			t.Fatal(err)
		}
	}
	touch(time.Now().Add(time.Hour))
	if info := env.info(t); info.BaseCheckout != runnerwire.BaseCheckoutRepo {
		t.Fatalf("info = %+v", info)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a filter in the repo's config ran when the panel looked")
	}
	// Control: plain git status runs it.
	touch(time.Now().Add(2 * time.Hour))
	gitT(t, env.repo, "status", "--porcelain")
	if _, err := os.Stat(marker); err != nil {
		t.Skipf("the filter never runs here, so this proves nothing: %v", err)
	}
}

// A worktree whose status cannot be read reports an unknown count, never 0.
func TestRunInfoUncommittedUnknown(t *testing.T) {
	env := newWSEnv(t, nil)
	if err := os.Remove(filepath.Join(env.wt.Path, ".git")); err != nil {
		t.Fatal(err)
	}
	if info := env.info(t); !info.Exists || info.Uncommitted != runnerwire.UncommittedUnknown {
		t.Fatalf("info = %+v", info)
	}
}

// asBuiltIn makes the env's runner the built-in one, whose preview listener
// (preview.go) stops with the test.
func (e *wsEnv) asBuiltIn(t *testing.T) {
	t.Helper()
	e.j.c.builtIn.Store(true)
	t.Cleanup(e.j.c.preview.close)
}

// preview mints a preview of the run's folder, as its owner.
func (e *wsEnv) preview(t *testing.T) runnerwire.RunPreview {
	t.Helper()
	done := e.browse(runnerwire.RepoOpRunPreview, "")
	if done.Code != "" || done.Preview == nil {
		t.Fatalf("run_preview = %+v", done)
	}
	return *done.Preview
}

// previewGet fetches a minted preview's page over its loopback listener.
func previewGet(t *testing.T, p runnerwire.RunPreview, rel string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(p.Port)+"/p/"+p.Token+"/"+rel, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "localhost:" + strconv.Itoa(p.Port)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestBrowseRunPreview(t *testing.T) {
	// The built-in runner, its owner asking, a page at the top of the folder.
	env := newWSEnv(t, nil)
	env.asBuiltIn(t)
	writeFile(t, env.wt.Path, "index.html", []byte("<p>the run's page</p>"))
	before := time.Now()
	p := env.preview(t)
	real, err := filepath.EvalSymlinks(env.wt.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Web || p.Entry != "index.html" || p.Port <= 0 || len(p.Token) != 32 || !sameRepoPath(p.Path, real) ||
		p.ExpiresAt.Before(before.Add(previewTTL-time.Minute)) || p.ExpiresAt.After(time.Now().Add(previewTTL+time.Minute)) {
		t.Fatalf("preview = %+v", p)
	}
	if code, body := previewGet(t, p, p.Entry); code != http.StatusOK || body != "<p>the run's page</p>" {
		t.Fatalf("GET entry = %d %q", code, body)
	}
	// The repo root's files are not the run's.
	writeFile(t, env.repo, "rootonly.html", []byte("root"))
	if code, _ := previewGet(t, p, "rootonly.html"); code != http.StatusNotFound {
		t.Fatalf("a repo root file through the preview = %d", code)
	}

	// No page: the folder to open instead, and no listener for it.
	plain := newWSEnv(t, nil)
	plain.asBuiltIn(t)
	if p := plain.preview(t); p.Web || p.Token != "" || p.Port != 0 || p.Entry != "" || p.Path == "" {
		t.Fatalf("no page = %+v", p)
	}
	if plain.j.c.preview.peek() != nil {
		t.Fatal("a listener started for a folder with no page")
	}

	// A runner that is not built in: off, and never a listener.
	classic := newWSEnv(t, nil)
	t.Cleanup(classic.j.c.preview.close)
	writeFile(t, classic.wt.Path, "index.html", []byte("x"))
	if got := classic.browse(runnerwire.RepoOpRunPreview, ""); got.Code != runnerwire.RepoErrPreviewOff || got.Preview != nil {
		t.Fatalf("classic runner = %+v", got)
	}
	if classic.j.c.preview.peek() != nil {
		t.Fatal("a classic runner started a preview listener")
	}

	// The owner turned it off.
	off := newWSEnv(t, func(cfg *Config) {
		no := false
		cfg.RunPreview = &no
	})
	off.asBuiltIn(t)
	writeFile(t, off.wt.Path, "index.html", []byte("x"))
	if got := off.browse(runnerwire.RepoOpRunPreview, ""); got.Code != runnerwire.RepoErrPreviewOff || got.Preview != nil {
		t.Fatalf("runPreview off = %+v", got)
	}
	if off.j.c.preview.peek() != nil {
		t.Fatal("runPreview off started a listener")
	}

	// Someone else's account.
	got := env.j.browseRun(context.Background(), wsOwner, runnerwire.RepoRequest{ID: "q", AccountID: "u-other", RepoID: "r1",
		Op: runnerwire.RepoOpRunPreview, RunID: wsRun, NodeID: wsNode, Branch: env.wt.Branch, Base: "main"})
	if got.Code != runnerwire.RepoErrRefused || got.Preview != nil {
		t.Fatalf("another account = %+v", got)
	}

	// The worktree is gone.
	if err := os.RemoveAll(env.wt.Path); err != nil {
		t.Fatal(err)
	}
	if got := env.browse(runnerwire.RepoOpRunPreview, ""); got.Code != runnerwire.RepoErrNoWorktree {
		t.Fatalf("no worktree = %+v", got)
	}
	if code, _ := previewGet(t, p, p.Entry); code != http.StatusNotFound {
		t.Fatalf("the page outlived its folder: %d", code)
	}
}

// A page minted for a run ends once its folder is removed from Nodevas (the
// run's worktree stays on disk), however often an open tab asks.
func TestPreviewEndsWithRemovedFolder(t *testing.T) {
	env := newWSEnv(t, nil)
	env.asBuiltIn(t)
	writeFile(t, env.wt.Path, "index.html", []byte("<p>the run's page</p>"))
	p := env.preview(t)
	if code, _ := previewGet(t, p, p.Entry); code != http.StatusOK {
		t.Fatalf("GET entry = %d", code)
	}
	next := env.j.c.cfg().Clone()
	next.Repos = nil
	env.j.c.live.Store(next)
	if code, body := previewGet(t, p, p.Entry); code != http.StatusNotFound {
		t.Fatalf("after the folder was removed = %d %q", code, body)
	}
}
