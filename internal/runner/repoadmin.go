package runner

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"nodevas/internal/runnerwire"
)

// Repo admin (single-user; runnerwire/repoadmin.go): the owner adds a repo
// of the built-in runner from the app with a folder picker, makes a folder a
// git project first when it is not one yet, sets a repo's test command or
// removes it, where a classic runner needs `nodevas runner repo …` on its
// own machine. The runner decides: its owner only, the built-in runner only,
// one operation at a time, and every add looks at the folder again and
// re-checks the sensitive-folder guard (repoguard.go) on the folder and its
// git top level, never trusting an earlier inspect. Nothing here deletes or
// changes the owner's files: an init adds a .git folder and, when there is
// none, a .gitignore, and takes both away again if the first commit fails.

// repoAdminAnswer is a repo_admin_done on its way out; changed sends a hello
// with the new repos first.
type repoAdminAnswer struct {
	done    runnerwire.RepoAdminDone
	changed bool
}

// Limits a test shortens.
var (
	// adminBusyWait is how long an operation waits for the one before it
	// rather than failing at once.
	adminBusyWait = 5 * time.Second
	// initMaxFiles and initMaxBytes are how much a first version may hold.
	initMaxFiles       = runnerwire.MaxInitFiles
	initMaxBytes int64 = runnerwire.MaxInitBytes
	// afterInit runs between a successful init and the add; a test makes the
	// add fail there. nil in use.
	afterInit func(*Client)
)

const (
	// initPlanTimeout bounds counting a folder's files for an init, and
	// initIgnoredTimeout listing what it leaves out, which only informs.
	initPlanTimeout    = 5 * time.Second
	initIgnoredTimeout = 2 * time.Second
	// initGitTimeout bounds each git command of an init (adding 20 000 files
	// takes a while on a slow disk), and initTotalTimeout the whole init,
	// well within the hub's wait for it (RepoAdminInitTimeoutSeconds).
	initGitTimeout   = 2 * time.Minute
	initTotalTimeout = 4 * time.Minute
	// maxInitIgnored is how many left-out files and folders a plan lists.
	maxInitIgnored = 20
	// initCommitMessage is the first commit's message.
	initCommitMessage = "Nodevas: first version"
)

// defaultGitignore is the .gitignore an init writes when the folder has
// none: dependencies, build output, logs, environments, keys and the files
// that hold credentials, none of which belongs in a first version that every
// worktree, branch and pull request carries.
var defaultGitignore = []string{"node_modules/", ".env", ".env.*", "*.log", "dist/", "build/", "out/", "target/",
	".venv/", "venv/", "__pycache__/", ".DS_Store", "Thumbs.db", "*.pem", "*.key", "id_rsa*", "id_dsa*", "id_ecdsa*",
	"id_ed25519*", "*.ppk", "*.p12", "*.pfx", "*.jks", "*.keystore", ".netrc", ".npmrc", ".pypirc"}

// adminSlot is the one-operation-at-a-time slot.
func (c *Client) adminSlot() chan struct{} {
	c.adminOnce.Do(func() { c.admin = make(chan struct{}, 1) })
	return c.admin
}

// repoAdmin answers a repo_admin from the hub of the runner whose owner is
// ownerID.
func (c *Client) repoAdmin(ctx context.Context, ownerID string, req runnerwire.RepoAdminRequest) repoAdminAnswer {
	done := runnerwire.RepoAdminDone{ID: req.ID}
	if ownerID == "" || req.AccountID != ownerID {
		return c.adminFailed(done, runnerwire.RepoAdminErrRefused, "only this runner's owner may change its repos")
	}
	if !c.BuiltIn() {
		return c.adminFailed(done, runnerwire.RepoAdminErrOff, "repos of this runner are added on its own machine "+
			"(nodevas runner repo add)")
	}
	slot := c.adminSlot()
	wait := time.NewTimer(adminBusyWait)
	defer wait.Stop()
	select {
	case slot <- struct{}{}:
		defer func() { <-slot }()
	case <-wait.C:
		return c.adminFailed(done, runnerwire.RepoAdminErrBusy, "another repo change is still going on; try again")
	case <-ctx.Done():
		return c.adminFailed(done, runnerwire.RepoAdminErrBusy, "the runner is stopping")
	}
	switch req.Op {
	case runnerwire.RepoAdminInspect:
		inspect, code, err := c.inspectFolder(ctx, req.Path)
		if code != "" {
			return c.adminFailed(done, code, "%v", err)
		}
		done.Inspect = &inspect
		return repoAdminAnswer{done: done}
	case runnerwire.RepoAdminAdd, runnerwire.RepoAdminInitAdd:
		return c.addRepo(ctx, done, req)
	case runnerwire.RepoAdminRemove:
		return c.removeRepo(done, req.RepoID)
	case runnerwire.RepoAdminSetTest:
		return c.setRepoTest(done, req)
	}
	return c.adminFailed(done, runnerwire.RepoAdminErrBadRequest, "unknown repo operation %q", truncate(req.Op, 40))
}

// adminFailed is done with code and the error, logged.
func (c *Client) adminFailed(done runnerwire.RepoAdminDone, code, format string, args ...any) repoAdminAnswer {
	done.Code, done.Error = code, truncate(fmt.Sprintf(format, args...), runnerwire.MaxRepoAdminError)
	c.logf("repo change from Nodevas refused: %s", done.Error)
	return repoAdminAnswer{done: done}
}

// inspectFolder looks at the folder at path: refused, a repo, inside one, or
// not one yet (with the plan for making it one). A code is an error
// (runnerwire.RepoAdminErr*) that is not one of those answers.
func (c *Client) inspectFolder(ctx context.Context, path string) (runnerwire.RepoInspect, string, error) {
	path = strings.TrimSpace(path)
	if path == "" || len(path) > runnerwire.MaxRepoPathBytes || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) {
		return runnerwire.RepoInspect{}, runnerwire.RepoAdminErrBadRequest, errors.New("the folder must be a full path")
	}
	abs := filepath.Clean(path)
	inspect := runnerwire.RepoInspect{Path: abs}
	refused := func(class string) (runnerwire.RepoInspect, string, error) {
		inspect.State, inspect.Refused = runnerwire.RepoStateRefused, class
		return inspect, "", nil
	}
	guard := c.repoGuard()
	if class := guard.check(abs); class != "" {
		return refused(class)
	}
	info, err := os.Stat(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return inspect, runnerwire.RepoAdminErrNotFound, fmt.Errorf("%s does not exist", abs)
	case err != nil:
		return refused(runnerwire.RepoRefusedUnreadable)
	case !info.IsDir():
		return inspect, runnerwire.RepoAdminErrNotDir, fmt.Errorf("%s is not a folder", abs)
	}
	resolved, err := resolveFolder(abs)
	if err != nil {
		return refused(runnerwire.RepoRefusedUnreadable)
	}
	if class := guard.check(resolved); class != "" {
		return refused(class)
	}
	gitBin, err := c.Commands.LookPath("git")
	if err != nil {
		return inspect, runnerwire.RepoAdminErrGitMissing, errors.New("git is not installed on this computer")
	}
	if out, err := adminGit(ctx, gitBin, abs, repoGitTimeout, "rev-parse", "--show-toplevel"); err == nil {
		top := filepath.Clean(filepath.FromSlash(firstLine(out)))
		if top == "" || top == "." || !filepath.IsAbs(top) {
			return inspect, runnerwire.RepoAdminErrGit, fmt.Errorf("git named no top folder for %s", abs)
		}
		inspect.Top = top
		if class := guard.check(top); class != "" {
			return refused(class)
		}
		resolvedTop, err := resolveFolder(top)
		if err != nil {
			return refused(runnerwire.RepoRefusedUnreadable)
		}
		if class := guard.check(resolvedTop); class != "" {
			return refused(class)
		}
		inspect.State = runnerwire.RepoStateInsideRepo
		if guard.same(top, abs) || guard.same(resolvedTop, resolved) {
			inspect.State = runnerwire.RepoStateRepo
		}
		repo := runnerwire.Repo{ID: RepoID(top), Name: filepath.Base(top), Path: top}
		if remote, err := adminGit(ctx, gitBin, top, repoGitTimeout, "remote", "get-url", "origin"); err == nil {
			repo.Remote = firstLine(remote)
		}
		for _, existing := range c.cfg().Repos {
			if existing.ID == repo.ID {
				repo, inspect.Already = existing, true
			}
		}
		inspect.Repo = &repo
		return inspect, "", nil
	}
	// Not a work tree, by git's word. A .git above it means git could not
	// read the project it belongs to (a broken or foreign one): making this
	// folder a project of its own inside it is not the answer.
	if parent := gitAbove(abs); parent != "" {
		return inspect, runnerwire.RepoAdminErrGit, fmt.Errorf("git cannot read the project %s belongs to (%s)", abs, parent)
	}
	if parent := gitAbove(resolved); parent != "" {
		return inspect, runnerwire.RepoAdminErrGit, fmt.Errorf("git cannot read the project %s belongs to (%s)", abs, parent)
	}
	plan, class, err := c.initPlan(ctx, gitBin, abs, guard)
	if err != nil {
		return inspect, runnerwire.RepoAdminErrGit, err
	}
	if class != "" {
		// A junction in it reaches a folder agents must never see: git
		// would save what is there as part of the first version.
		return refused(class)
	}
	inspect.State, inspect.Init = runnerwire.RepoStateNotRepo, &plan
	return inspect, "", nil
}

// gitAbove is the first folder from dir up that holds a .git, or "".
func gitAbove(dir string) string {
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// addRepo is add and init_add: look at the folder again, check it again,
// make it a git project first for init_add (only by the plan the owner
// confirmed), then add it.
func (c *Client) addRepo(ctx context.Context, done runnerwire.RepoAdminDone, req runnerwire.RepoAdminRequest) repoAdminAnswer {
	inspect, code, err := c.inspectFolder(ctx, req.Path)
	if code != "" {
		return c.adminFailed(done, code, "%v", err)
	}
	done.Inspect = &inspect
	switch inspect.State {
	case runnerwire.RepoStateRefused:
		return c.adminFailed(done, runnerwire.RepoAdminErrSensitive, "agents must never work in %s (%s)", inspect.Path, inspect.Refused)
	case runnerwire.RepoStateNotRepo:
		if req.Op != runnerwire.RepoAdminInitAdd {
			return c.adminFailed(done, runnerwire.RepoAdminErrNotRepo, "%s is not a git project yet", inspect.Path)
		}
	default:
		if req.Op == runnerwire.RepoAdminInitAdd {
			// It became a project while the owner was deciding.
			return c.adminFailed(done, runnerwire.RepoAdminErrPlanChanged, "%s changed: it is a git project now; check it again", inspect.Path)
		}
		if inspect.Already {
			done.Repo = inspect.Repo
			return c.adminFailed(done, runnerwire.RepoAdminErrAlready, "%s is already added (id %s)", inspect.Repo.Path, inspect.Repo.ID)
		}
		return c.saveNewRepo(done, *inspect.Repo, false)
	}
	plan := inspect.Init
	switch {
	case plan.TooBig:
		return c.adminFailed(done, runnerwire.RepoAdminErrTooBig, "%s holds too many files (%d, %d bytes) for a first version "+
			"here (at most %d files, %d bytes)", inspect.Path, plan.Files, plan.Bytes, initMaxFiles, initMaxBytes)
	case req.InitPlan == "" || req.InitPlan != plan.Hash:
		return c.adminFailed(done, runnerwire.RepoAdminErrPlanChanged, "%s changed since it was checked; check it again", inspect.Path)
	case len(c.cfg().Repos) >= runnerwire.MaxRepos:
		return c.adminFailed(done, runnerwire.RepoAdminErrTooMany, "a runner can hold at most %d repositories", runnerwire.MaxRepos)
	}
	gitBin, err := c.Commands.LookPath("git")
	if err != nil {
		return c.adminFailed(done, runnerwire.RepoAdminErrGitMissing, "git is not installed on this computer")
	}
	initCtx, cancel := context.WithTimeout(ctx, initTotalTimeout)
	undo, err := initRepo(initCtx, gitBin, inspect.Path, plan.WritesGitignore)
	cancel()
	if err != nil {
		return c.adminFailed(done, runnerwire.RepoAdminErrGit, "could not make %s a git project: %v", inspect.Path, err)
	}
	// From here on, a failure takes the init back: the folder is left as it
	// was, not a git project that is in no runner's list.
	failed := func(code, format string, args ...any) repoAdminAnswer {
		undo()
		c.logf("took back the git project made of %s", inspect.Path)
		return c.adminFailed(done, code, format, args...)
	}
	if afterInit != nil {
		afterInit(c)
	}
	// What git says the project is now, checked like any add.
	again, code, err := c.inspectFolder(ctx, inspect.Path)
	switch {
	case code != "":
		return failed(code, "%v", err)
	case again.State == runnerwire.RepoStateRefused:
		done.Inspect = &again
		return failed(runnerwire.RepoAdminErrSensitive, "agents must never work in %s (%s)", again.Path, again.Refused)
	case again.Repo == nil:
		return failed(runnerwire.RepoAdminErrGit, "git does not see %s as a project after git init", inspect.Path)
	}
	done.Inspect = &again
	answer := c.saveNewRepo(done, *again.Repo, true)
	if answer.done.Code != "" {
		undo()
		c.logf("took back the git project made of %s", inspect.Path)
		return answer
	}
	c.logf("made %s a git project: %d files saved as the first version", inspect.Path, plan.Files)
	return answer
}

// saveNewRepo adds repo to the runner's settings and file.
func (c *Client) saveNewRepo(done runnerwire.RepoAdminDone, repo runnerwire.Repo, initialized bool) repoAdminAnswer {
	c.settingsMu.Lock()
	if len(c.cfg().Repos) >= runnerwire.MaxRepos {
		c.settingsMu.Unlock()
		return c.adminFailed(done, runnerwire.RepoAdminErrTooMany, "a runner can hold at most %d repositories", runnerwire.MaxRepos)
	}
	saved, err := c.updateConfig(func(cfg *Config) error {
		if slices.ContainsFunc(cfg.Repos, func(r runnerwire.Repo) bool { return r.ID == repo.ID }) {
			return nil // added on this machine meanwhile
		}
		return cfg.AddRepo(repo)
	})
	c.settingsMu.Unlock()
	how := "added"
	if initialized {
		how = "made a git project and added"
	}
	c.logf("%s %s (id %s) from Nodevas: agent runs may now work there%s", how, repo.Path, repo.ID, savedWhere(saved, err, c.ConfigPath))
	if err != nil && !saved {
		done.Error = truncate(fmt.Sprintf("%v: %v", errNotSaved, err), runnerwire.MaxRepoAdminError)
	}
	done.Repo = &repo
	return repoAdminAnswer{done: done, changed: true}
}

// removeRepo takes a repo off the runner, and off its auto-accept list.
// The folder and its files stay as they are.
func (c *Client) removeRepo(done runnerwire.RepoAdminDone, repoID string) repoAdminAnswer {
	c.settingsMu.Lock()
	var repo runnerwire.Repo
	found := false
	for _, r := range c.cfg().Repos {
		if r.ID == repoID {
			repo, found = r, true
		}
	}
	if !found {
		c.settingsMu.Unlock()
		return c.adminFailed(done, runnerwire.RepoAdminErrNotFound, "no repository %q on this runner", truncate(repoID, 40))
	}
	saved, err := c.updateConfig(func(cfg *Config) error {
		if !slices.ContainsFunc(cfg.Repos, func(r runnerwire.Repo) bool { return r.ID == repoID }) {
			return nil // removed on this machine meanwhile
		}
		_, err := cfg.RemoveRepo(repoID)
		return err
	})
	c.settingsMu.Unlock()
	c.logf("removed %s (id %s) from Nodevas; its files are untouched%s", repo.Path, repo.ID, savedWhere(saved, err, c.ConfigPath))
	if err != nil && !saved {
		done.Error = truncate(fmt.Sprintf("%v: %v", errNotSaved, err), runnerwire.MaxRepoAdminError)
	}
	done.Repo = &repo
	return repoAdminAnswer{done: done, changed: true}
}

// setRepoTest sets or clears a repo's test command, which verify jobs and
// goal judges run on this machine on code agents wrote.
func (c *Client) setRepoTest(done runnerwire.RepoAdminDone, req runnerwire.RepoAdminRequest) repoAdminAnswer {
	if len(req.TestCommand) > runnerwire.MaxTestCommandBytes || req.TestTimeoutMinutes < 0 ||
		req.TestTimeoutMinutes > runnerwire.MaxTestTimeoutMinutes {
		return c.adminFailed(done, runnerwire.RepoAdminErrBadRequest, "the test command must be at most %d bytes and its "+
			"timeout 0 to %d minutes", runnerwire.MaxTestCommandBytes, runnerwire.MaxTestTimeoutMinutes)
	}
	c.settingsMu.Lock()
	if !slices.ContainsFunc(c.cfg().Repos, func(r runnerwire.Repo) bool { return r.ID == req.RepoID }) {
		c.settingsMu.Unlock()
		return c.adminFailed(done, runnerwire.RepoAdminErrNotFound, "no repository %q on this runner", truncate(req.RepoID, 40))
	}
	// The first edit is the clone's: its error is the command's own (a quote
	// left open, a line break), and nothing changed.
	var repo runnerwire.Repo
	var commandErr error
	calls := 0
	saved, err := c.updateConfig(func(cfg *Config) error {
		r, err := cfg.SetRepoTestCommand(req.RepoID, req.TestCommand, req.TestTimeoutMinutes)
		if calls++; calls == 1 {
			repo, commandErr = r, err
		}
		return err
	})
	c.settingsMu.Unlock()
	if commandErr != nil {
		return c.adminFailed(done, runnerwire.RepoAdminErrBadRequest, "%v", commandErr)
	}
	command, _ := c.cfg().RepoTestCommand(repo.ID)
	repo.Verify = len(command) > 0
	what := "test command cleared"
	if repo.Verify {
		what = "test command set"
	}
	c.logf("%s: %s from Nodevas%s", repo.Name, what, savedWhere(saved, err, c.ConfigPath))
	if err != nil && !saved {
		done.Error = truncate(fmt.Sprintf("%v: %v", errNotSaved, err), runnerwire.MaxRepoAdminError)
	}
	done.Repo = &repo
	return repoAdminAnswer{done: done, changed: true}
}

// adminGit runs git at dir, hardened (hardenedArgs: no hooks, no fsmonitor)
// and never prompting, with timeout, in adminGitEnv.
func adminGit(ctx context.Context, gitBin, dir string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, gitBin, hardenedArgs(dir, args)...)
	cmd.Env = adminGitEnv()
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("git %s took too long", gitVerb(args))
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", gitVerb(args), truncate(msg, 300))
	}
	return stdout.String(), nil
}

// adminGitEnv is repoGitEnv without what would point git at another
// repository, work tree or index, or add config of its own (GIT_DIR,
// GIT_CONFIG_PARAMETERS and the like, from the environment serve was
// started in): repo admin's git works on the folder the owner picked, with
// the overrides on its own command line.
func adminGitEnv() []string {
	env := repoGitEnv()
	out := env[:0:0]
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		switch name = strings.ToUpper(name); {
		case name == "GIT_DIR", name == "GIT_WORK_TREE", name == "GIT_INDEX_FILE", name == "GIT_OBJECT_DIRECTORY",
			name == "GIT_ALTERNATE_OBJECT_DIRECTORIES", name == "GIT_COMMON_DIR", name == "GIT_NAMESPACE",
			name == "GIT_CONFIG", name == "GIT_CONFIG_PARAMETERS", name == "GIT_CONFIG_COUNT",
			strings.HasPrefix(name, "GIT_CONFIG_KEY_"), strings.HasPrefix(name, "GIT_CONFIG_VALUE_"):
			continue
		}
		out = append(out, entry)
	}
	return out
}

// noFilterArgs empties every filter driver git's config defines for dir
// (repoFilterDrivers), so a .gitattributes in the folder cannot pick a
// program for `git add` to run on its files.
func noFilterArgs(ctx context.Context, dir string) []string {
	var args []string
	for _, name := range repoFilterDrivers(ctx, dir) {
		args = append(args, "-c", "filter."+name+".clean=", "-c", "filter."+name+".smudge=",
			"-c", "filter."+name+".process=", "-c", "filter."+name+".required=false")
	}
	return args
}

// initPlan counts what a first version of dir would hold: git itself lists
// the files, with dir's own .gitignore files and the owner's global
// excludes, plus defaultGitignore when dir has no .gitignore (an init writes
// it then), in a throwaway repository, so the folder is not touched. Past
// MaxInitFiles, MaxInitBytes or initPlanTimeout the plan is TooBig.
//
// Git for Windows walks into a directory junction as into a folder, so a
// first version would hold what a junction in dir reaches: a folder a file
// sits in that resolves to one guard refuses makes class that refusal
// (runnerwire.RepoRefused*), and the plan is not to be offered.
func (c *Client) initPlan(ctx context.Context, gitBin, dir string, guard *repoGuard) (plan runnerwire.InitPlan, class string, err error) {
	plan = runnerwire.InitPlan{Ignored: []string{}}
	if _, err := os.Lstat(filepath.Join(dir, ".gitignore")); errors.Is(err, fs.ErrNotExist) {
		plan.WritesGitignore = true
	}
	dataDir, err := c.cfg().RunDataDir()
	if err != nil {
		return plan, "", err
	}
	parent := filepath.Join(dataDir, "tmp")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return plan, "", err
	}
	scratch, err := os.MkdirTemp(parent, "init-plan-")
	if err != nil {
		return plan, "", err
	}
	defer os.RemoveAll(scratch)
	if _, err := adminGit(ctx, gitBin, scratch, repoGitTimeout, "init", "-q", "--template="); err != nil {
		return plan, "", err
	}
	gitDir := filepath.Join(scratch, ".git")
	if plan.WritesGitignore {
		if err := os.MkdirAll(filepath.Join(gitDir, "info"), 0o700); err != nil {
			return plan, "", err
		}
		exclude := strings.Join(defaultGitignore, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(gitDir, "info", "exclude"), []byte(exclude), 0o600); err != nil {
			return plan, "", err
		}
	}
	listCtx, cancel := context.WithTimeout(ctx, initPlanTimeout)
	defer cancel()
	// The hash pins what the first version saves: each file with its size
	// and time. What it leaves out only informs, so a listing of that cut
	// short never reads as a change.
	hash := sha256.New()
	fmt.Fprintf(hash, "nodevas init plan 2\n%t\n", plan.WritesGitignore)
	folders := map[string]bool{filepath.Clean(dir): true}
	tooBig, err := listUntracked(listCtx, gitBin, gitDir, dir, false, func(rel string) bool {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if folder := filepath.Dir(path); !folders[folder] {
			folders[folder] = true
			if resolved, err := resolveFolder(folder); err == nil {
				if class = guard.check(resolved); class != "" {
					return false
				}
			}
		}
		info, err := os.Lstat(path)
		size, stamp := int64(0), int64(0)
		if err == nil {
			size, stamp = info.Size(), info.ModTime().UnixNano()
		}
		plan.Files++
		plan.Bytes += size
		fmt.Fprintf(hash, "%s\x00%d\x00%d\n", rel, size, stamp)
		return plan.Files <= initMaxFiles && plan.Bytes <= initMaxBytes
	})
	if class != "" {
		return plan, class, nil
	}
	if tooBig || listCtx.Err() != nil {
		plan.TooBig = true
	} else if err != nil {
		return plan, "", err
	}
	if !plan.TooBig {
		ignoredCtx, cancel := context.WithTimeout(ctx, initIgnoredTimeout)
		defer cancel()
		_, _ = listUntracked(ignoredCtx, gitBin, gitDir, dir, true, func(rel string) bool {
			plan.Ignored = append(plan.Ignored, rel)
			return len(plan.Ignored) < maxInitIgnored
		})
		slices.Sort(plan.Ignored)
	}
	plan.Hash = hex.EncodeToString(hash.Sum(nil))[:32]
	return plan, "", nil
}

// listUntracked hands each file git would add to a new repository at
// workTree (ignored: each file and folder it leaves out, a folder once) to
// each, until each says stop (stopped true).
func listUntracked(ctx context.Context, gitBin, gitDir, workTree string, ignored bool, each func(string) bool) (stopped bool, err error) {
	args := []string{"-c", "core.hooksPath=" + noHooksPath(), "-c", "core.fsmonitor=false", "-c", "core.quotePath=false",
		"--git-dir=" + gitDir, "--work-tree=" + workTree, "ls-files", "-z", "--others", "--exclude-standard"}
	if ignored {
		args = append(args, "--ignored", "--directory", "--no-empty-directory")
	}
	cmd := exec.CommandContext(ctx, gitBin, args...)
	cmd.Dir = workTree
	cmd.Env = adminGitEnv()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return false, err
	}
	if err := cmd.Start(); err != nil {
		return false, err
	}
	reader := bufio.NewReader(out)
	for {
		entry, readErr := reader.ReadString(0)
		if entry = strings.TrimSuffix(entry, "\x00"); entry != "" && !each(entry) {
			stopped = true
			break
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				err = readErr
			}
			break
		}
	}
	if stopped {
		_ = cmd.Process.Kill()
	}
	_, _ = io.Copy(io.Discard, out)
	waitErr := cmd.Wait()
	if stopped || err != nil {
		return stopped, err
	}
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		return false, errors.New("git ls-files: " + truncate(msg, 300))
	}
	return false, nil
}

// initRepo makes dir a git project: git init without sample hooks, a
// default .gitignore when there is none, and every file saved as the first
// commit, hooks, filter drivers and signing off, the owner's git identity
// or, without one, Nodevas's for this commit only. A folder with nothing to
// save (an empty one, or one its .gitignore leaves out whole) gets an empty
// first commit: a worktree needs a HEAD. On a failure what it added goes
// again; once it succeeded, undo takes it back (the .git, and the
// .gitignore if it wrote one), for a failure after it.
func initRepo(ctx context.Context, gitBin, dir string, writeGitignore bool) (undo func(), err error) {
	gitDir := filepath.Join(dir, ".git")
	if _, err := os.Lstat(gitDir); err == nil {
		return nil, errors.New("it already has a .git")
	}
	ignorePath := filepath.Join(dir, ".gitignore")
	wroteIgnore := false
	undo = func() {
		_ = os.RemoveAll(gitDir)
		if wroteIgnore {
			_ = os.Remove(ignorePath)
		}
	}
	defer func() {
		if err != nil {
			undo()
			undo = nil
		}
	}()
	if _, err := adminGit(ctx, gitBin, dir, initGitTimeout, "init", "-q", "--template="); err != nil {
		return nil, err
	}
	if writeGitignore {
		f, err := os.OpenFile(ignorePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return nil, fmt.Errorf("write .gitignore: %w", err)
		}
		wroteIgnore = true
		_, werr := f.WriteString(strings.Join(defaultGitignore, "\n") + "\n")
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return nil, fmt.Errorf("write .gitignore: %w", werr)
		}
	}
	// A .gitattributes in the folder names filter drivers; whatever the
	// owner's git config defines for them, git add runs none.
	noFilters := noFilterArgs(ctx, dir)
	if _, err := adminGit(ctx, gitBin, dir, initGitTimeout, append(slices.Clone(noFilters), "add", "-A")...); err != nil {
		return nil, err
	}
	commit := append(slices.Clone(noFilters), "-c", "commit.gpgsign=false")
	name, nameErr := adminGit(ctx, gitBin, dir, repoGitTimeout, "config", "user.name")
	email, emailErr := adminGit(ctx, gitBin, dir, repoGitTimeout, "config", "user.email")
	if nameErr != nil || emailErr != nil || strings.TrimSpace(name) == "" || strings.TrimSpace(email) == "" {
		commit = append(commit, "-c", "user.name=Nodevas", "-c", "user.email=nodevas@localhost")
	}
	commit = append(commit, "commit", "-q", "--no-verify", "--allow-empty", "-m", initCommitMessage)
	if _, err := adminGit(ctx, gitBin, dir, initGitTimeout, commit...); err != nil {
		return nil, err
	}
	return undo, nil
}
