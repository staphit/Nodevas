package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

// adminEnv is a built-in runner's client for repo admin, with a made-up
// home whose rules apply instead of this machine's.
type adminEnv struct {
	c    *Client
	home string
	path string
	log  *bytes.Buffer
}

func newAdminEnv(t *testing.T) *adminEnv {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home, err := resolveFolder(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	path := filepath.Join(t.TempDir(), "runner.json")
	cfg := &Config{Hub: "http://127.0.0.1:1", Token: testToken, DataDir: data}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	c := &Client{Config: cfg, ConfigPath: path, Commands: SystemCommands{}, Log: &log,
		guard: &repoGuard{fold: runtime.GOOS != "linux", home: home, isRoot: isFilesystemRoot, nodevas: []string{data}}}
	c.builtIn.Store(true)
	return &adminEnv{c: c, home: home, path: path, log: &log}
}

func (e *adminEnv) do(t *testing.T, req runnerwire.RepoAdminRequest) repoAdminAnswer {
	t.Helper()
	if req.AccountID == "" {
		req.AccountID = wsOwner
	}
	if req.ID == "" {
		req.ID = "a1"
	}
	return e.c.repoAdmin(context.Background(), wsOwner, req)
}

// folder makes a folder under the made-up home with files (name → content;
// a name ending in / is a folder).
func (e *adminEnv) folder(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(e.home, "code", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for file, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(file))
		if strings.HasSuffix(file, "/") {
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return string(out)
}

// Owner only, the built-in runner only, known ops only.
func TestRepoAdminRefusals(t *testing.T) {
	e := newAdminEnv(t)
	if a := e.c.repoAdmin(context.Background(), wsOwner, runnerwire.RepoAdminRequest{ID: "x", AccountID: "u-other",
		Op: runnerwire.RepoAdminInspect, Path: e.home}); a.done.Code != runnerwire.RepoAdminErrRefused || a.changed {
		t.Fatalf("someone else: %+v", a)
	}
	if a := e.c.repoAdmin(context.Background(), "", runnerwire.RepoAdminRequest{ID: "x", Op: runnerwire.RepoAdminInspect}); a.done.Code != runnerwire.RepoAdminErrRefused {
		t.Fatalf("no owner known: %+v", a)
	}
	if a := e.do(t, runnerwire.RepoAdminRequest{Op: "format"}); a.done.Code != runnerwire.RepoAdminErrBadRequest || a.done.ID != "a1" {
		t.Fatalf("unknown op: %+v", a)
	}
	e.c.builtIn.Store(false)
	if a := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminInspect, Path: e.home}); a.done.Code != runnerwire.RepoAdminErrOff {
		t.Fatalf("classic: %+v", a)
	}
	e.c.builtIn.Store(true)
	// One at a time; the next waits a while, then says busy.
	defer func(wait time.Duration) { adminBusyWait = wait }(adminBusyWait)
	adminBusyWait = 50 * time.Millisecond
	e.c.adminSlot() <- struct{}{}
	if a := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminInspect, Path: e.home}); a.done.Code != runnerwire.RepoAdminErrBusy {
		t.Fatalf("busy: %+v", a)
	}
	<-e.c.adminSlot()
}

// What inspect says about each kind of folder.
func TestRepoAdminInspect(t *testing.T) {
	e := newAdminEnv(t)
	inspect := func(path string) (runnerwire.RepoInspect, string) {
		t.Helper()
		a := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminInspect, Path: path})
		if a.changed {
			t.Fatalf("inspect changed something: %+v", a)
		}
		if a.done.Inspect == nil {
			return runnerwire.RepoInspect{}, a.done.Code
		}
		return *a.done.Inspect, a.done.Code
	}
	repo := e.folder(t, "app", map[string]string{"README": "hi\n"})
	gitOut(t, repo, "init", "-q")
	gitOut(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.test", "commit", "-q", "--allow-empty", "-m", "x")
	sub := filepath.Join(repo, "web")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, code := inspect(repo); code != "" || got.State != runnerwire.RepoStateRepo || got.Repo == nil || got.Already ||
		!samePath(got.Repo.Path, repo) || got.Repo.Name != "app" || got.Init != nil {
		t.Fatalf("repo: %+v %q", got, code)
	}
	if got, code := inspect(sub); code != "" || got.State != runnerwire.RepoStateInsideRepo || !samePath(got.Top, repo) || got.Repo == nil {
		t.Fatalf("inside: %+v %q", got, code)
	}
	plain := e.folder(t, "plain", map[string]string{"main.go": "package main\n", "node_modules/x/index.js": "x", ".env": "K=1\n", "app.log": "x"})
	got, code := inspect(plain)
	if code != "" || got.State != runnerwire.RepoStateNotRepo || got.Init == nil || got.Init.Files != 1 || got.Init.Bytes != int64(len("package main\n")) ||
		!got.Init.WritesGitignore || got.Init.TooBig || len(got.Init.Hash) != 32 ||
		!slices.Contains(got.Init.Ignored, "node_modules/") || !slices.Contains(got.Init.Ignored, ".env") || !slices.Contains(got.Init.Ignored, "app.log") {
		t.Fatalf("not a repo: %+v %+v %q", got, got.Init, code)
	}
	if _, err := os.Stat(filepath.Join(plain, ".git")); err == nil {
		t.Fatal("inspect made a .git")
	}
	for path, want := range map[string]string{
		e.home:                        runnerwire.RepoRefusedHome,
		filepath.Join(e.home, ".ssh"): runnerwire.RepoRefusedSecret,
		e.c.guard.nodevas[0]:          runnerwire.RepoRefusedNodevas,
		filepath.VolumeName(e.home) + string(filepath.Separator): runnerwire.RepoRefusedRoot,
	} {
		_ = os.MkdirAll(path, 0o700)
		if got, code := inspect(path); code != "" || got.State != runnerwire.RepoStateRefused || got.Refused != want {
			t.Errorf("%s: %+v %q, want refused %s", path, got, code, want)
		}
	}
	file := filepath.Join(plain, "main.go")
	for path, want := range map[string]string{
		filepath.Join(e.home, "code", "missing"): runnerwire.RepoAdminErrNotFound,
		file:                                     runnerwire.RepoAdminErrNotDir,
		"relative/path":                          runnerwire.RepoAdminErrBadRequest,
		"":                                       runnerwire.RepoAdminErrBadRequest,
	} {
		if _, code := inspect(path); code != want {
			t.Errorf("%q: code %q, want %q", path, code, want)
		}
	}
	// No git: said so, for a folder the guard lets through.
	e.c.Commands = &fakeCommands{}
	if _, code := inspect(plain); code != runnerwire.RepoAdminErrGitMissing {
		t.Fatalf("git missing: %q", code)
	}
}

// A home folder that is itself a git repo (dotfiles): a project folder in it
// that is no repo of its own belongs to the home repo, which is refused.
func TestRepoAdminDotfilesHome(t *testing.T) {
	e := newAdminEnv(t)
	gitOut(t, e.home, "init", "-q")
	app := e.folder(t, "app", map[string]string{"main.go": "package main\n"})
	a := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminInspect, Path: app})
	if a.done.Inspect == nil || a.done.Inspect.State != runnerwire.RepoStateRefused || a.done.Inspect.Refused != runnerwire.RepoRefusedHome {
		t.Fatalf("inspect = %+v", a.done)
	}
	if a := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminAdd, Path: app}); a.done.Code != runnerwire.RepoAdminErrSensitive || a.changed {
		t.Fatalf("add = %+v", a.done)
	}
}

// Adding a repo: saved, in effect, once; a subfolder adds its project; the
// hard limit holds.
func TestRepoAdminAdd(t *testing.T) {
	e := newAdminEnv(t)
	repo := e.folder(t, "app", map[string]string{"README": "hi\n"})
	gitOut(t, repo, "init", "-q")
	a := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminAdd, Path: filepath.Join(repo)})
	if a.done.Code != "" || a.done.Error != "" || !a.changed || a.done.Repo == nil || !samePath(a.done.Repo.Path, repo) {
		t.Fatalf("add = %+v", a.done)
	}
	id := a.done.Repo.ID
	if len(e.c.cfg().Repos) != 1 || e.c.cfg().Repos[0].ID != id || len(e.c.Config.Repos) != 0 {
		t.Fatalf("in effect %+v; started with %+v", e.c.cfg().Repos, e.c.Config.Repos)
	}
	if saved, err := LoadConfig(e.path); err != nil || len(saved.Repos) != 1 || saved.Repos[0].ID != id {
		t.Fatalf("saved %+v, %v", saved, err)
	}
	again := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminAdd, Path: filepath.Join(repo, ".")})
	if again.done.Code != runnerwire.RepoAdminErrAlready || again.changed || again.done.Repo == nil || again.done.Repo.ID != id {
		t.Fatalf("again = %+v", again.done)
	}
	if insp := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminInspect, Path: repo}).done.Inspect; insp == nil || !insp.Already {
		t.Fatalf("inspect after add = %+v", insp)
	}
	other := e.folder(t, "other", map[string]string{"web/index.html": "x"})
	gitOut(t, other, "init", "-q")
	sub := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminAdd, Path: filepath.Join(other, "web")})
	if sub.done.Code != "" || sub.done.Repo == nil || !samePath(sub.done.Repo.Path, other) {
		t.Fatalf("subfolder = %+v", sub.done)
	}
	plain := e.folder(t, "plain", map[string]string{"a.txt": "a"})
	if a := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminAdd, Path: plain}); a.done.Code != runnerwire.RepoAdminErrNotRepo || a.changed {
		t.Fatalf("plain = %+v", a.done)
	}
	// Full.
	e.c.settingsMu.Lock()
	_, err := e.c.updateConfig(func(cfg *Config) error {
		for i := len(cfg.Repos); i < runnerwire.MaxRepos; i++ {
			cfg.Repos = append(cfg.Repos, runnerwire.Repo{ID: fmt.Sprintf("fill%03d", i), Name: "fill", Path: "/fill"})
		}
		return nil
	})
	e.c.settingsMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	third := e.folder(t, "third", nil)
	gitOut(t, third, "init", "-q")
	if a := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminAdd, Path: third}); a.done.Code != runnerwire.RepoAdminErrTooMany {
		t.Fatalf("too many = %+v", a.done)
	}
	if a := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminInitAdd, Path: plain,
		InitPlan: e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminInspect, Path: plain}).done.Inspect.Init.Hash}); a.done.Code != runnerwire.RepoAdminErrTooMany {
		t.Fatalf("too many for init = %+v", a.done)
	}
	if _, err := os.Stat(filepath.Join(plain, ".git")); err == nil {
		t.Fatal("init ran although the repo could not be added")
	}
}

// Making a folder a git project: by the plan the owner confirmed only, with
// a .gitignore only where there is none, everything else as it was.
func TestRepoAdminInitAdd(t *testing.T) {
	e := newAdminEnv(t)
	plan := func(dir string) *runnerwire.InitPlan {
		t.Helper()
		a := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminInspect, Path: dir})
		if a.done.Inspect == nil || a.done.Inspect.Init == nil {
			t.Fatalf("no plan for %s: %+v", dir, a.done)
		}
		return a.done.Inspect.Init
	}
	app := e.folder(t, "app", map[string]string{"main.go": "package main\n", "node_modules/x/i.js": "x", ".env": "K=1\n", "dist/out.js": "x",
		"id_ed25519": "key", ".npmrc": "//registry.npmjs.org/:_authToken=t\n"})
	p := plan(app)
	// The folder changes while the owner decides.
	if err := os.WriteFile(filepath.Join(app, "new.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if a := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminInitAdd, Path: app, InitPlan: p.Hash}); a.done.Code != runnerwire.RepoAdminErrPlanChanged || a.changed {
		t.Fatalf("changed folder = %+v", a.done)
	}
	if a := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminInitAdd, Path: app}); a.done.Code != runnerwire.RepoAdminErrPlanChanged {
		t.Fatalf("no plan named = %+v", a.done)
	}
	if _, err := os.Stat(filepath.Join(app, ".git")); err == nil {
		t.Fatal("init ran on a changed plan")
	}
	p = plan(app)
	if p.Files != 2 || !p.WritesGitignore || !slices.Contains(p.Ignored, "id_ed25519") || !slices.Contains(p.Ignored, ".npmrc") {
		t.Fatalf("plan = %+v", p)
	}
	a := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminInitAdd, Path: app, InitPlan: p.Hash})
	if a.done.Code != "" || !a.changed || a.done.Repo == nil || !samePath(a.done.Repo.Path, app) {
		t.Fatalf("init_add = %+v", a.done)
	}
	ignore, err := os.ReadFile(filepath.Join(app, ".gitignore"))
	if err != nil || !strings.Contains(string(ignore), "node_modules/") || !strings.Contains(string(ignore), "id_rsa*") {
		t.Fatalf(".gitignore %q, %v", ignore, err)
	}
	tracked := strings.Fields(gitOut(t, app, "ls-files"))
	if !slices.Equal(tracked, []string{".gitignore", "main.go", "new.go"}) {
		t.Fatalf("first version holds %v", tracked)
	}
	if msg := strings.TrimSpace(gitOut(t, app, "log", "-1", "--format=%s")); msg != initCommitMessage {
		t.Fatalf("commit message %q", msg)
	}
	if hooks, _ := os.ReadDir(filepath.Join(app, ".git", "hooks")); len(hooks) > 0 {
		t.Fatalf("sample hooks were installed: %v", hooks)
	}
	if env, _ := os.ReadFile(filepath.Join(app, ".env")); string(env) != "K=1\n" {
		t.Fatal("a file was changed")
	}
	if len(e.c.cfg().Repos) != 1 {
		t.Fatalf("repos %+v", e.c.cfg().Repos)
	}

	// Its own .gitignore stays as it is.
	own := e.folder(t, "own", map[string]string{".gitignore": "secret.txt\n", "secret.txt": "s", "a.txt": "a"})
	p = plan(own)
	if p.WritesGitignore || p.Files != 2 || !slices.Contains(p.Ignored, "secret.txt") {
		t.Fatalf("own plan = %+v", p)
	}
	if a := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminInitAdd, Path: own, InitPlan: p.Hash}); a.done.Code != "" {
		t.Fatalf("own init = %+v", a.done)
	}
	if ignore, _ := os.ReadFile(filepath.Join(own, ".gitignore")); string(ignore) != "secret.txt\n" {
		t.Fatalf("its .gitignore became %q", ignore)
	}

	// An empty folder gets an empty first commit: a worktree needs a HEAD.
	empty := e.folder(t, "empty", nil)
	p = plan(empty)
	if p.Files != 0 || p.TooBig {
		t.Fatalf("empty plan = %+v", p)
	}
	if a := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminInitAdd, Path: empty, InitPlan: p.Hash}); a.done.Code != "" {
		t.Fatalf("empty init = %+v", a.done)
	}
	gitOut(t, empty, "rev-parse", "--verify", "HEAD")

	// Already a project now: plan_changed, not a second init.
	if a := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminInitAdd, Path: empty, InitPlan: p.Hash}); a.done.Code != runnerwire.RepoAdminErrPlanChanged {
		t.Fatalf("init twice = %+v", a.done)
	}
}

// Past the limits a first version is not made here.
func TestRepoAdminInitTooBig(t *testing.T) {
	e := newAdminEnv(t)
	defer func(files int, bytes int64) { initMaxFiles, initMaxBytes = files, bytes }(initMaxFiles, initMaxBytes)
	initMaxFiles = 2
	big := e.folder(t, "big", map[string]string{"a": "1", "b": "2", "c": "3"})
	a := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminInspect, Path: big})
	if a.done.Inspect == nil || a.done.Inspect.Init == nil || !a.done.Inspect.Init.TooBig {
		t.Fatalf("plan = %+v", a.done.Inspect)
	}
	add := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminInitAdd, Path: big, InitPlan: a.done.Inspect.Init.Hash})
	if add.done.Code != runnerwire.RepoAdminErrTooBig || add.changed {
		t.Fatalf("init = %+v", add.done)
	}
	initMaxFiles, initMaxBytes = 100, 3
	heavy := e.folder(t, "heavy", map[string]string{"a": "1234"})
	if a := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminInspect, Path: heavy}); !a.done.Inspect.Init.TooBig {
		t.Fatalf("bytes plan = %+v", a.done.Inspect.Init)
	}
	if _, err := os.Stat(filepath.Join(big, ".git")); err == nil {
		t.Fatal("init ran on a folder too big")
	}
}

// A failure after the init (here the runner filled up meanwhile) takes the
// init back: no .git and no .gitignore of its making stay in the folder.
func TestRepoAdminInitTakenBackOnAFailedAdd(t *testing.T) {
	e := newAdminEnv(t)
	app := e.folder(t, "app", map[string]string{"main.go": "package main\n"})
	a := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminInspect, Path: app})
	if a.done.Inspect == nil || a.done.Inspect.Init == nil {
		t.Fatalf("no plan: %+v", a.done)
	}
	defer func() { afterInit = nil }()
	afterInit = func(c *Client) {
		c.settingsMu.Lock()
		defer c.settingsMu.Unlock()
		if _, err := c.updateConfig(func(cfg *Config) error {
			for i := len(cfg.Repos); i < runnerwire.MaxRepos; i++ {
				cfg.Repos = append(cfg.Repos, runnerwire.Repo{ID: fmt.Sprintf("other-%d", i), Name: "other", Path: e.home})
			}
			return nil
		}); err != nil {
			t.Error(err)
		}
	}
	add := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminInitAdd, Path: app, InitPlan: a.done.Inspect.Init.Hash})
	if add.done.Code != runnerwire.RepoAdminErrTooMany || add.changed {
		t.Fatalf("init_add = %+v", add.done)
	}
	for _, name := range []string{".git", ".gitignore"} {
		if _, err := os.Lstat(filepath.Join(app, name)); err == nil {
			t.Errorf("%s stayed after the add failed", name)
		}
	}
	if main, err := os.ReadFile(filepath.Join(app, "main.go")); err != nil || string(main) != "package main\n" {
		t.Fatalf("main.go = %q, %v", main, err)
	}
}

// The first version runs no filter driver: a .gitattributes in the folder
// names one the owner's git config defines, and git add leaves it alone.
func TestRepoAdminInitRunsNoFilter(t *testing.T) {
	e := newAdminEnv(t)
	marker := filepath.Join(t.TempDir(), "ran")
	global := filepath.Join(t.TempDir(), "gitconfig")
	config := "[filter \"evil\"]\n\tclean = touch '" + filepath.ToSlash(marker) + "' && cat\n\trequired = true\n"
	if err := os.WriteFile(global, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	files := map[string]string{".gitattributes": "* filter=evil\n", "main.go": "package main\n"}
	// The driver is real: a plain git add runs it.
	control := e.folder(t, "control", files)
	gitOut(t, control, "init", "-q")
	gitOut(t, control, "add", "-A")
	if _, err := os.Stat(marker); err != nil {
		t.Skipf("the filter driver did not run under a plain git add (%v); nothing to show", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	app := e.folder(t, "app", files)
	a := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminInspect, Path: app})
	if a.done.Inspect == nil || a.done.Inspect.Init == nil {
		t.Fatalf("no plan: %+v", a.done)
	}
	if add := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminInitAdd, Path: app, InitPlan: a.done.Inspect.Init.Hash}); add.done.Code != "" {
		t.Fatalf("init_add = %+v", add.done)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the init ran the folder's filter driver")
	}
	if tracked := strings.Fields(gitOut(t, app, "ls-files")); !slices.Contains(tracked, "main.go") {
		t.Fatalf("first version holds %v", tracked)
	}
}

// Repo admin's git works on the picked folder: nothing in the environment
// serve was started in points it at another repository or adds config.
func TestAdminGitEnv(t *testing.T) {
	for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT",
		"GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0"} {
		t.Setenv(name, "x")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", "kept")
	env := adminGitEnv()
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if name = strings.ToUpper(name); strings.HasPrefix(name, "GIT_CONFIG_KEY") || slices.Contains([]string{"GIT_DIR", "GIT_WORK_TREE",
			"GIT_INDEX_FILE", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT", "GIT_CONFIG_VALUE_0"}, name) {
			t.Errorf("%s reached repo admin's git", entry)
		}
	}
	if !slices.Contains(env, "GIT_CONFIG_GLOBAL=kept") || !slices.Contains(env, "GIT_TERMINAL_PROMPT=0") {
		t.Fatal("the rest of the environment is gone")
	}
}

// Removing a repo forgets its auto-accept entry and leaves its files; the
// test command is set, cleared and checked.
func TestRepoAdminRemoveAndTestCommand(t *testing.T) {
	e := newAdminEnv(t)
	repo := e.folder(t, "app", map[string]string{"README": "x"})
	gitOut(t, repo, "init", "-q")
	added := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminAdd, Path: repo})
	id := added.done.Repo.ID
	e.c.settingsMu.Lock()
	if _, err := e.c.updateConfig(func(cfg *Config) error {
		cfg.AutoAccept = &runnerwire.AutoAcceptPolicy{Enabled: true, Repos: []string{id, "other"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	e.c.settingsMu.Unlock()

	set := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminSetTest, RepoID: id, TestCommand: `go test "./..."`, TestTimeoutMinutes: 5})
	if set.done.Code != "" || !set.changed || set.done.Repo == nil || !set.done.Repo.Verify {
		t.Fatalf("set_test = %+v", set.done)
	}
	if command, timeout := e.c.cfg().RepoTestCommand(id); !slices.Equal(command, []string{"go", "test", "./..."}) || timeout != 5*time.Minute {
		t.Fatalf("in effect %q %s", command, timeout)
	}
	if saved, _ := LoadConfig(e.path); !slices.Equal(firstOf(saved.RepoTestCommand(id)), []string{"go", "test", "./..."}) {
		t.Fatal("test command not saved")
	}
	for name, req := range map[string]runnerwire.RepoAdminRequest{
		"open quote": {Op: runnerwire.RepoAdminSetTest, RepoID: id, TestCommand: `go test "./...`},
		"too long":   {Op: runnerwire.RepoAdminSetTest, RepoID: id, TestCommand: strings.Repeat("x", runnerwire.MaxTestCommandBytes+1)},
		"timeout":    {Op: runnerwire.RepoAdminSetTest, RepoID: id, TestCommand: "make", TestTimeoutMinutes: runnerwire.MaxTestTimeoutMinutes + 1},
		"line break": {Op: runnerwire.RepoAdminSetTest, RepoID: id, TestCommand: "make\nrm -rf /"},
	} {
		if a := e.do(t, req); a.done.Code != runnerwire.RepoAdminErrBadRequest || a.changed {
			t.Errorf("%s: %+v", name, a.done)
		}
	}
	if a := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminSetTest, RepoID: "nope", TestCommand: "make"}); a.done.Code != runnerwire.RepoAdminErrNotFound {
		t.Fatalf("unknown repo: %+v", a.done)
	}
	if command, _ := e.c.cfg().RepoTestCommand(id); len(command) != 3 {
		t.Fatalf("a refused change changed the command to %q", command)
	}
	if clear := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminSetTest, RepoID: id}); clear.done.Code != "" || clear.done.Repo.Verify {
		t.Fatalf("clear = %+v", clear.done)
	}

	removed := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminRemove, RepoID: id})
	if removed.done.Code != "" || !removed.changed || removed.done.Repo == nil || removed.done.Repo.ID != id {
		t.Fatalf("remove = %+v", removed.done)
	}
	now := e.c.cfg()
	if len(now.Repos) != 0 || !slices.Equal(now.AutoAccept.Repos, []string{"other"}) {
		t.Fatalf("after remove %+v %+v", now.Repos, now.AutoAccept)
	}
	if saved, _ := LoadConfig(e.path); len(saved.Repos) != 0 || !slices.Equal(saved.AutoAccept.Repos, []string{"other"}) {
		t.Fatalf("saved after remove %+v", saved)
	}
	if _, err := os.Stat(filepath.Join(repo, "README")); err != nil {
		t.Fatal("remove touched the files")
	}
	if a := e.do(t, runnerwire.RepoAdminRequest{Op: runnerwire.RepoAdminRemove, RepoID: id}); a.done.Code != runnerwire.RepoAdminErrNotFound {
		t.Fatalf("remove twice = %+v", a.done)
	}
}

func firstOf(command []string, _ time.Duration) []string { return command }

// Over the socket: the new repo is in a hello before repo_admin_done, so the
// app can preselect it.
func TestRepoAdminOverTheSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runner.json")
	extra := newTestRepo(t)
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	env := startBuiltInRunner(t, func(c *Client) {
		c.ConfigPath = path
		c.guard = &repoGuard{isRoot: isFilesystemRoot}
		c.Commands.(*fakeCommands).paths["git"] = gitBin

		if err := SaveConfig(path, c.Config); err != nil {
			t.Fatal(err)
		}
	})
	env.hub.hello()
	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeRepoAdmin, RepoAdmin: &runnerwire.RepoAdminRequest{ID: "add-1",
		AccountID: wsOwner, Op: runnerwire.RepoAdminAdd, Path: extra}})
	var done *runnerwire.RepoAdminDone
	deadline := time.Now().Add(20 * time.Second)
	for done == nil && time.Now().Before(deadline) {
		for _, msg := range env.hub.messages("") {
			if msg.Type == runnerwire.TypeRepoAdminDone && msg.RepoAdminDone != nil && msg.RepoAdminDone.ID == "add-1" {
				done = msg.RepoAdminDone
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if done == nil || done.Code != "" || done.Repo == nil {
		t.Fatalf("done = %+v", done)
	}
	var hello runnerwire.Envelope
	for len(env.hub.hellos) > 0 {
		hello = <-env.hub.hellos
	}
	if hello.Hello == nil || !slices.ContainsFunc(hello.Hello.Repos, func(r runnerwire.Repo) bool { return r.ID == done.Repo.ID }) {
		t.Fatalf("no hello with the new repo before the answer: %+v", hello.Hello)
	}
	// Someone else is refused over the socket too.
	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeRepoAdmin, RepoAdmin: &runnerwire.RepoAdminRequest{ID: "add-2",
		AccountID: "u-other", Op: runnerwire.RepoAdminRemove, RepoID: done.Repo.ID}})
	deadline = time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, msg := range env.hub.messages("") {
			if msg.Type == runnerwire.TypeRepoAdminDone && msg.RepoAdminDone != nil && msg.RepoAdminDone.ID == "add-2" {
				if msg.RepoAdminDone.Code != runnerwire.RepoAdminErrRefused {
					t.Fatalf("someone else: %+v", msg.RepoAdminDone)
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no answer for someone else")
}
