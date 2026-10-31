package gitrepo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newRepo(t *testing.T) (*Repo, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	r, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return r, dir
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCommitLogShow(t *testing.T) {
	r, dir := newRepo(t)
	ctx := context.Background()
	ann := Person{Name: "ann", Email: "ann@example.test"}
	at := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)

	write(t, dir, "nodes/n1.md", "one")
	write(t, dir, ".vised/drafts/n1.md", "draft")
	write(t, dir, "run/journal.jsonl", "{}")
	first, err := r.Commit(ctx, nil, "n1: create", ann, at)
	if err != nil || !ValidSHA(first) {
		t.Fatalf("first commit %q, %v", first, err)
	}
	// Nothing changed: no commit.
	if again, err := r.Commit(ctx, nil, "nothing", ann, at); err != nil || again != "" {
		t.Fatalf("empty commit %q, %v", again, err)
	}
	write(t, dir, "nodes/n1.md", "two")
	second, err := r.Commit(ctx, []string{"nodes/n1.md"}, "n1: edit", Person{Name: "run:claude"}, at.Add(time.Minute))
	if err != nil || second == "" {
		t.Fatal(err)
	}

	log, err := r.Log(ctx, "nodes/n1.md", 0)
	if err != nil || len(log) != 2 {
		t.Fatalf("log %+v, %v", log, err)
	}
	if log[0].SHA != second || log[0].Author != "run:claude" || log[0].Subject != "n1: edit" ||
		!log[0].At.Equal(at.Add(time.Minute)) || log[1].Author != "ann" {
		t.Fatalf("log = %+v", log)
	}
	old, err := r.Show(ctx, first, "nodes/n1.md")
	if err != nil || string(old) != "one" {
		t.Fatalf("show %q, %v", old, err)
	}

	// Nodevas's own state and the journal are never tracked.
	tracked, err := r.Run(ctx, nil, "ls-files")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(tracked)); got != "nodes/n1.md" {
		t.Fatalf("tracked = %q", got)
	}
}

func TestEmptyRepositoryHasNoLog(t *testing.T) {
	r, _ := newRepo(t)
	log, err := r.Log(context.Background(), "graph.yaml", 10)
	if err != nil || len(log) != 0 {
		t.Fatalf("log %+v, %v", log, err)
	}
}

// The machine's git config does not reach the repository: a global hook or
// signing setting would otherwise run, or fail, on every save.
func TestIgnoresTheMachinesGitConfig(t *testing.T) {
	home := t.TempDir()
	hooks := filepath.Join(home, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "hook-ran")
	hook := "#!/bin/sh\ntouch " + marker + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	global := "[core]\n\thooksPath = " + hooks + "\n[commit]\n\tgpgsign = true\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(global), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)

	r, dir := newRepo(t)
	write(t, dir, "graph.yaml", "nodes: []\n")
	if sha, err := r.Commit(context.Background(), nil, "graph", Person{Name: "ann"}, time.Now()); err != nil || sha == "" {
		t.Fatalf("commit %q, %v", sha, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a global hook ran")
	}
}

func TestShowRefusesAnythingButACommitID(t *testing.T) {
	r, _ := newRepo(t)
	for _, bad := range []string{"HEAD", "main", "--output=/tmp/x", "abc", strings.Repeat("g", 40)} {
		if _, err := r.Show(context.Background(), bad, "graph.yaml"); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

// Nothing git reads from the environment survives from the server's own:
// a stray GIT_INDEX_FILE would commit the wrong index, and GIT_CURL_VERBOSE
// would print a push's Authorization header.
func TestDropsInheritedGitVariables(t *testing.T) {
	t.Setenv("GIT_CURL_VERBOSE", "0")
	t.Setenv("GIT_TRACE", "1")
	t.Setenv("GIT_INDEX_FILE", filepath.Join(t.TempDir(), "other-index"))
	r, dir := newRepo(t)
	env := r.env([]string{"GIT_CONFIG_COUNT=0"})
	for _, kv := range env {
		for _, bad := range []string{"GIT_CURL_VERBOSE=", "GIT_TRACE=", "GIT_INDEX_FILE="} {
			if strings.HasPrefix(kv, bad) {
				t.Errorf("inherited %s", kv)
			}
		}
	}
	if env[len(env)-1] != "GIT_CONFIG_COUNT=0" {
		t.Error("the caller's own variables come last")
	}
	write(t, dir, "graph.yaml", "nodes: []\n")
	if sha, err := r.Commit(context.Background(), nil, "graph", Person{Name: "ann"}, time.Now()); err != nil || sha == "" {
		t.Fatalf("commit %q, %v", sha, err)
	}
}

// A project inside the user's own repository adds nothing to it: the
// object store is ignored there, not listed as untracked.
func TestEnclosingRepositoryIgnoresTheProjectsState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	outer := t.TempDir()
	if out, err := exec.Command("git", "-C", outer, "init", "--quiet").CombinedOutput(); err != nil {
		t.Fatalf("init outer: %v %s", err, out)
	}
	project := filepath.Join(outer, "plans")
	r, err := Open(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	write(t, project, "graph.yaml", "nodes: []\n")
	if _, err := r.Commit(context.Background(), nil, "graph", Person{Name: "ann"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", outer, "status", "--porcelain", "--untracked-files=all").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.Contains(line, ".vised") {
			t.Fatalf("the enclosing repository sees %q", line)
		}
	}
	if !strings.Contains(string(out), "plans/graph.yaml") {
		t.Fatalf("the project's own files should still be the enclosing repository's business: %q", out)
	}
}

func TestVersionFloor(t *testing.T) {
	for out, ok := range map[string]bool{
		"git version 2.49.0\n":               true,
		"git version 2.32.0":                 true,
		"git version 2.39.3 (Apple Git-146)": true,
		"git version 2.31.8":                 false,
		"git version 2.30.2":                 false,
		"git version 3.0.0":                  true,
		"git version 2.45.1.windows.1":       true,
		"something else":                     false,
	} {
		if got := versionAtLeast(out, MinVersion); got != ok {
			t.Errorf("%q: %v, want %v", out, got, ok)
		}
	}
}

// Hooks are switched off without a folder of our own: every hub process used
// to leave an empty nodevas-gitrepo-nohooks-* folder in the temp directory.
// The hooks path is one nothing can ever be made at, so no hook can be
// planted there either.
func TestNoHooksPathLeavesNothingBehind(t *testing.T) {
	if !Available() {
		t.Skip("git is not installed")
	}
	path := filepath.FromSlash(noHooksPath())
	if !filepath.IsAbs(path) {
		t.Fatalf("hooks path %q is relative: git would read it inside the work tree", path)
	}
	if info, err := os.Stat(path); err == nil {
		t.Fatalf("hooks path %q exists (dir=%v): a folder left behind, or one a hook could be put in", path, info.IsDir())
	}
	if err := os.MkdirAll(path, 0o700); err == nil {
		_ = os.Remove(path)
		t.Fatalf("hooks path %q could be created: a hook could be planted there", path)
	}
}

// The repository's own hooks folder never runs either.
func TestRepositoryHookDoesNotRun(t *testing.T) {
	r, dir := newRepo(t)
	marker := filepath.Join(t.TempDir(), "hook-ran")
	hook := "#!/bin/sh\ntouch '" + filepath.ToSlash(marker) + "'\nexit 1\n"
	write(t, r.GitDir(), "hooks/pre-commit", hook)
	if err := os.Chmod(filepath.Join(r.GitDir(), "hooks", "pre-commit"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "graph.yaml", "nodes: []\n")
	if sha, err := r.Commit(context.Background(), nil, "graph", Person{Name: "ann"}, time.Now()); err != nil || sha == "" {
		t.Fatalf("commit %q, %v", sha, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the repository's pre-commit hook ran")
	}
}
