// Package gitrepo is a project's own git repository: the history Nodevas
// keeps of a project's files (docs/history-and-backup.md).
//
// The repository lives in <project>/.vised/git, with the project folder as
// its work tree. It is never the user's own repository: a project folder
// that sits inside a code checkout gets no commits in that checkout's
// history, and `git status` there sees nothing new (.vised/ is Nodevas's).
//
// Every command runs isolated from the machine's git setup. No system or
// global config is read (so no signing, no hooks, no credential helpers
// from there), hooks are off, nothing prompts, and pathspecs are literal.
// The repository's own config is Nodevas's alone.
package gitrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"nodevas/internal/locale"
)

// Dir is the git directory inside a project folder.
const Dir = ".vised/git"

// exclude is what the repository never tracks: Nodevas's own state, the run
// journal, and lock files.
const exclude = `# Written by Nodevas: the project's own state is not its history.
/.vised/
/run/
*.lock
`

// Committer is who commits on everyone's behalf; the author is the person
// (or agent) whose save it was.
var Committer = Person{Name: "Nodevas", Email: "nodevas@localhost"}

// MinVersion is the oldest git the repository may be run with: 2.32 is the
// first that honours GIT_CONFIG_GLOBAL and GIT_CONFIG_SYSTEM, without which
// the machine's own git config (signing, hooks, helpers) would apply.
const MinVersion = "2.32"

// ErrUnavailable is returned when no git of MinVersion or newer is
// installed.
var ErrUnavailable = locale.Errorf("git %s or newer is not installed on this server", "這個伺服器沒有安裝 git %s 以上版本", MinVersion)

// Person is a commit's author or committer.
type Person struct {
	Name  string
	Email string
}

// Commit is one entry of a file's log.
type Commit struct {
	SHA     string
	At      time.Time
	Author  string
	Subject string
}

// Repo is one project's repository.
type Repo struct {
	gitDir   string
	workTree string
}

var (
	lookPath = exec.LookPath
	// gitVersion runs `git --version`; a seam for tests.
	gitVersion = func(path string) (string, error) {
		out, err := exec.Command(path, "--version").Output()
		return string(out), err
	}

	availableOnce sync.Once
	gitPath       string
	unavailable   string
)

// Available reports whether a git of MinVersion or newer can be run here.
func Available() bool {
	availableOnce.Do(func() {
		path, err := lookPath("git")
		if err != nil {
			unavailable = "git is not installed"
			return
		}
		out, err := gitVersion(path)
		if err != nil {
			unavailable = fmt.Sprintf("git --version: %v", err)
			return
		}
		if !versionAtLeast(out, MinVersion) {
			unavailable = fmt.Sprintf("%s is older than %s", strings.TrimSpace(out), MinVersion)
			return
		}
		gitPath = path
	})
	return gitPath != ""
}

// UnavailableReason says why Available is false, for a log line.
func UnavailableReason() string {
	Available()
	return unavailable
}

var versionPattern = regexp.MustCompile(`(\d+)\.(\d+)`)

// versionAtLeast reads "git version 2.49.0" (or "2.39.3 (Apple Git-146)")
// and compares major.minor with min.
func versionAtLeast(out, min string) bool {
	have := versionPattern.FindStringSubmatch(out)
	want := versionPattern.FindStringSubmatch(min)
	if have == nil || want == nil {
		return false
	}
	atoi := func(s string) int {
		n := 0
		for _, r := range s {
			n = n*10 + int(r-'0')
		}
		return n
	}
	if atoi(have[1]) != atoi(want[1]) {
		return atoi(have[1]) > atoi(want[1])
	}
	return atoi(have[2]) >= atoi(want[2])
}

// noHooksPath is the core.hooksPath that runs no hook: a path inside the
// git executable, which is a file, so no folder can ever exist there, nothing
// is left behind, and no hook can be planted there by anyone who could not
// already replace git itself. Git finds no hook and runs none.
func noHooksPath() string {
	base := gitPath
	if base == "" {
		base = os.DevNull
	}
	return filepath.ToSlash(base) + "/no-hooks"
}

// Open opens the repository of the project folder workTree, creating it on
// first use.
func Open(ctx context.Context, workTree string) (*Repo, error) {
	if !Available() {
		return nil, ErrUnavailable
	}
	abs, err := filepath.Abs(workTree)
	if err != nil {
		return nil, err
	}
	r := &Repo{gitDir: filepath.Join(abs, filepath.FromSlash(Dir)), workTree: abs}
	if _, err := os.Stat(filepath.Join(r.gitDir, "HEAD")); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err := os.MkdirAll(r.gitDir, 0o755); err != nil {
			return nil, err
		}
		if _, err := r.run(ctx, nil, nil, "init", "--quiet", "--initial-branch=main"); err != nil {
			return nil, err
		}
	}
	// A project folder can sit inside the user's own repository, where the
	// object store would show up as thousands of untracked files and go in
	// with the next `git add -A`. A .gitignore of "*" in .vised/ makes any
	// enclosing repository ignore the whole folder; this repository
	// excludes .vised/ on its own.
	selfIgnore := filepath.Join(abs, ".vised", ".gitignore")
	if _, err := os.Stat(selfIgnore); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(selfIgnore, []byte("# Written by Nodevas: .vised/ is its own state, not your repository's.\n*\n"), 0o644); err != nil {
			return nil, err
		}
	}
	excludeFile := filepath.Join(r.gitDir, "info", "exclude")
	if current, err := os.ReadFile(excludeFile); err != nil || string(current) != exclude {
		if err := os.MkdirAll(filepath.Dir(excludeFile), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(excludeFile, []byte(exclude), 0o644); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// GitDir is the repository's git directory.
func (r *Repo) GitDir() string { return r.gitDir }

// WorkTree is the project folder.
func (r *Repo) WorkTree() string { return r.workTree }

// env is the environment every command runs with: this repository, and
// none of the machine's git config. Every inherited GIT_* variable is
// dropped first: one left in the server's environment could point git at
// another repository or index (GIT_DIR, GIT_INDEX_FILE), or turn on tracing
// that prints a backup push's credentials (GIT_TRACE*, GIT_CURL_VERBOSE,
// which counts as on whatever its value). Callers add their own in extra.
func (r *Repo) env(extra []string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "GIT_") {
			env = append(env, kv)
		}
	}
	env = append(env,
		"GIT_DIR="+r.gitDir,
		"GIT_WORK_TREE="+r.workTree,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=never",
		"GIT_LITERAL_PATHSPECS=1",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		"LC_ALL=C",
	)
	return append(env, extra...)
}

// run is git with the isolation overrides; command-line -c wins over the
// repository's config.
func (r *Repo) run(ctx context.Context, stdin []byte, env []string, args ...string) ([]byte, error) {
	full := append([]string{
		"-c", "core.hooksPath=" + noHooksPath(),
		"-c", "core.fsmonitor=false",
		"-c", "core.autocrlf=false",
		"-c", "core.quotepath=false",
		"-c", "commit.gpgsign=false",
		"-c", "tag.gpgsign=false",
		"-c", "gc.autoDetach=false",
	}, args...)
	cmd := exec.CommandContext(ctx, gitPath, full...)
	cmd.Dir = r.workTree
	cmd.Env = r.env(env)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 500 {
			msg = msg[:500]
		}
		return stdout.Bytes(), fmt.Errorf("git %s: %w: %s", args[0], err, msg)
	}
	return stdout.Bytes(), nil
}

// Run runs any git command in this repository, isolated like every other.
// For callers that need more than the helpers here (the backup push).
func (r *Repo) Run(ctx context.Context, env []string, args ...string) ([]byte, error) {
	return r.run(ctx, nil, env, args...)
}

// Commit stages paths (every change when paths is empty) and commits them
// as author at the given time. It returns "" with no error when nothing
// changed.
func (r *Repo) Commit(ctx context.Context, paths []string, message string, author Person, at time.Time) (string, error) {
	add := []string{"add", "--all", "--"}
	if len(paths) == 0 {
		add = append(add, ".")
	} else {
		add = append(add, paths...)
	}
	if _, err := r.run(ctx, nil, nil, add...); err != nil {
		return "", err
	}
	// diff --cached --quiet exits 1 when something is staged.
	if _, err := r.run(ctx, nil, nil, "diff", "--cached", "--quiet"); err == nil {
		return "", nil
	}
	if author.Name == "" {
		author = Committer
	}
	if author.Email == "" {
		author.Email = Committer.Email
	}
	date := at.UTC().Format(time.RFC3339)
	env := []string{
		"GIT_AUTHOR_NAME=" + author.Name,
		"GIT_AUTHOR_EMAIL=" + author.Email,
		"GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_NAME=" + Committer.Name,
		"GIT_COMMITTER_EMAIL=" + Committer.Email,
		"GIT_COMMITTER_DATE=" + date,
	}
	if _, err := r.run(ctx, []byte(message), env, "commit", "--quiet", "--no-verify", "-F", "-"); err != nil {
		return "", err
	}
	out, err := r.run(ctx, nil, nil, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

const logSep = "\x1f"

// Log is path's history, newest first, at most limit entries (all when
// limit <= 0). A repository with no commit yet has none.
func (r *Repo) Log(ctx context.Context, path string, limit int) ([]Commit, error) {
	if !r.hasHead(ctx) {
		return []Commit{}, nil
	}
	args := []string{"log", "--format=%H" + logSep + "%aI" + logSep + "%an" + logSep + "%s"}
	if limit > 0 {
		args = append(args, fmt.Sprintf("-n%d", limit))
	}
	args = append(args, "--", filepath.ToSlash(path))
	out, err := r.run(ctx, nil, nil, args...)
	if err != nil {
		return nil, err
	}
	commits := []Commit{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, logSep, 4)
		if len(parts) != 4 {
			continue
		}
		at, _ := time.Parse(time.RFC3339, parts[1])
		commits = append(commits, Commit{SHA: parts[0], At: at, Author: parts[2], Subject: parts[3]})
	}
	return commits, nil
}

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// ValidSHA reports whether s is a full commit ID.
func ValidSHA(s string) bool { return shaPattern.MatchString(s) }

// Show is path's content at commit sha.
func (r *Repo) Show(ctx context.Context, sha, path string) ([]byte, error) {
	if !ValidSHA(sha) {
		return nil, locale.Errorf("invalid commit %q", "提交「%s」無效", sha)
	}
	return r.run(ctx, nil, nil, "show", sha+":"+filepath.ToSlash(path))
}

func (r *Repo) hasHead(ctx context.Context) bool {
	_, err := r.run(ctx, nil, nil, "rev-parse", "--verify", "--quiet", "HEAD")
	return err == nil
}
