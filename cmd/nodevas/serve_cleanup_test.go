package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/testpg"
)

// slowGitDir builds testdata/slowgit as "git" in a directory of its own, for
// the front of a child's PATH, and returns that directory and the real git.
// The "git" holds its first command for seconds; see testdata/slowgit.
func slowGitDir(t *testing.T) (dir, realGit string) {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	dir = t.TempDir()
	name := "git"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	out, err := exec.Command("go", "build", "-o", filepath.Join(dir, name), "./testdata/slowgit").CombinedOutput()
	if err != nil {
		t.Fatalf("go build slowgit: %v\n%s", err, out)
	}
	return dir, realGit
}

// A serve a test started is stopped by the test's cleanup so that nothing it
// started still runs in the project once the cleanup returns. A new
// workspace's project opens its history repository with git at once; killed,
// serve left that git running, writing .vised/git while t.TempDir removed the
// folder, and on Windows the removal failed ("The directory is not empty",
// TestSkipImportEnv under load). A held git command keeps one in flight when
// the test ends, every time.
func TestServeCleanupLeavesNothingRunningInTheProject(t *testing.T) {
	gitDir, realGit := slowGitDir(t)
	url := testpg.UseEnv(t)
	root := t.TempDir()
	hold := filepath.Join(t.TempDir(), "git-held")
	env := isolatedEnv(t, db.URLEnv+"="+url, db.WaitEnv+"=0s",
		"PATH="+gitDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"NODEVAS_TEST_REAL_GIT="+realGit, "NODEVAS_TEST_GIT_HOLD="+hold)
	t.Run("serve", func(t *testing.T) {
		server := startServe(t, root, env)
		server.ready(t, 90*time.Second)
		// The project's history has started its first git command, which
		// is held for seconds: the test ends with it in flight.
		deadline := time.Now().Add(90 * time.Second)
		for {
			if _, err := os.Stat(hold); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("serve ran no git for the project's history:\n%s", server.output.String())
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("the project could not be removed once serve's cleanup returned: %v", err)
	}
}
