package agenthook

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// The hook's git runs as the person, at the end of an agent's turn, in a
// repository that agent can write. Like the runner's git (internal/runner
// gitguard.go, repobrowse_git.go), it runs nothing the repository's config
// names: no hooks, no fsmonitor, no external diff, no filter driver; the diff
// also passes --no-textconv.

var (
	noHooksOnce sync.Once
	noHooksDir  string
)

// noHooksPath is a core.hooksPath that holds no hook and never can: a path
// inside the git executable, which is a file (as internal/gitrepo's).
func noHooksPath() string {
	noHooksOnce.Do(func() {
		base, err := exec.LookPath("git")
		if err != nil {
			base = os.DevNull
		}
		noHooksDir = filepath.ToSlash(base) + "/no-hooks"
	})
	return noHooksDir
}

// gitArgs is a git command line at dir with the overrides in front:
// command-line -c wins over any repository config.
func gitArgs(ctx context.Context, dir string, args []string) []string {
	front := []string{"-C", dir, "-c", "core.hooksPath=" + noHooksPath(), "-c", "core.fsmonitor=false",
		"-c", "diff.external="}
	for _, name := range filterDrivers(ctx, front) {
		front = append(front, "-c", "filter."+name+".clean=", "-c", "filter."+name+".smudge=",
			"-c", "filter."+name+".process=", "-c", "filter."+name+".required=false")
	}
	return append(front, args...)
}

// filterKey matches a filter driver's config key; the driver's name keeps
// its case, as "-c" overrides match it exactly.
var filterKey = regexp.MustCompile(`^(?i:filter)\.(.+)\.(?i:clean|smudge|process|required)$`)

// filterDrivers names the filter drivers git's config defines there. Reading
// config runs nothing.
func filterDrivers(ctx context.Context, front []string) []string {
	cmd := exec.CommandContext(ctx, "git", append(front[:len(front):len(front)],
		"config", "--null", "--name-only", "--get-regexp", `^filter\.`)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var names []string
	for _, key := range strings.Split(string(out), "\x00") {
		m := filterKey.FindStringSubmatch(strings.TrimSpace(key))
		if m == nil || seen[m[1]] || strings.ContainsAny(m[1], "=\x00\n") {
			continue
		}
		seen[m[1]] = true
		names = append(names, m[1])
	}
	return names
}
