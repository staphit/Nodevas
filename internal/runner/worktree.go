package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Worktree is where one run works: its own checkout and branch, so the run
// never touches the owner's main checkout and runs never touch each other.
type Worktree struct {
	Path   string
	Branch string
	// Base is the branch the run started from, as reported to the hub.
	Base string
	// BaseRef is what the branch was cut from: origin/<base> when the fetch
	// worked, the local branch when offline.
	BaseRef string
}

const (
	fetchTimeout = 60 * time.Second
	gitTimeout   = 30 * time.Second
	maxBranchLen = 60
)

// validRunID keeps a hub-chosen ID safe to use as a directory name.
var validRunID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

// validBase is a conservative branch name: no leading dash (it would be read
// as a git option), no colon (a refspec that writes elsewhere), no "..".
var validBase = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._/-]*$`)

func checkBase(base string) error {
	if len(base) > 200 || !validBase.MatchString(base) || strings.Contains(base, "..") ||
		strings.HasSuffix(base, "/") || strings.HasSuffix(base, ".lock") || strings.Contains(base, "//") {
		return fmt.Errorf("base branch %q is not a plain branch name", base)
	}
	return nil
}

// BranchName is the run's branch: agent/<node>-<first 8 of the run ID>,
// lowercase letters, digits and dashes only, at most 60 characters.
func BranchName(nodeID, runID string) string {
	run := slug(runID)
	if len(run) > 8 {
		run = run[:8]
	}
	node := slug(nodeID)
	if node == "" {
		node = "node"
	}
	if run == "" {
		run = "run"
	}
	const prefix = "agent/"
	if room := maxBranchLen - len(prefix) - 1 - len(run); len(node) > room {
		node = strings.Trim(node[:room], "-")
	}
	return prefix + node + "-" + run
}

// slug lowercases s and turns every run of other characters into one dash.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// CreateWorktree makes <dataDir>/worktrees/<runID> on a new branch from the
// job's base, or from the repository's default branch when it names none. It
// fetches the base first but carries on from the local branch when the fetch
// fails: a laptop on a train can still run.
func CreateWorktree(ctx context.Context, repoPath, dataDir, runID, nodeID, base string) (Worktree, error) {
	if !validRunID.MatchString(runID) {
		return Worktree{}, fmt.Errorf("run id %q is not usable as a directory name", runID)
	}
	if base != "" {
		if err := checkBase(base); err != nil {
			return Worktree{}, err
		}
	} else {
		var err error
		if base, err = defaultBranch(ctx, repoPath); err != nil {
			return Worktree{}, err
		}
	}

	baseRef := base
	fetchCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	_, fetchErr := git(fetchCtx, repoPath, "fetch", "--quiet", "origin", base)
	cancel()
	remoteRef := "origin/" + base
	switch {
	case fetchErr == nil && refExists(ctx, repoPath, remoteRef):
		baseRef = remoteRef
	case refExists(ctx, repoPath, base):
		baseRef = base
	case refExists(ctx, repoPath, remoteRef):
		baseRef = remoteRef
	default:
		return Worktree{}, fmt.Errorf("base branch %q exists neither locally nor on origin", base)
	}

	wt := Worktree{
		Path:    filepath.Join(dataDir, "worktrees", runID),
		Branch:  BranchName(nodeID, runID),
		Base:    base,
		BaseRef: baseRef,
	}
	if err := os.MkdirAll(filepath.Dir(wt.Path), 0o700); err != nil {
		return Worktree{}, err
	}
	if _, err := git(ctx, repoPath, "worktree", "add", "--quiet", "-b", wt.Branch, "--", wt.Path, baseRef); err != nil {
		return Worktree{}, err
	}
	return wt, nil
}

// defaultBranch is what origin calls HEAD, or failing that the branch the main
// checkout is on.
func defaultBranch(ctx context.Context, repoPath string) (string, error) {
	if out, err := git(ctx, repoPath, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if branch, ok := strings.CutPrefix(strings.TrimSpace(out), "origin/"); ok && branch != "" {
			return branch, nil
		}
	}
	out, err := git(ctx, repoPath, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || strings.TrimSpace(out) == "" {
		return "", errors.New("cannot tell the repository's default branch (no origin/HEAD and a detached HEAD); " +
			"name a base branch")
	}
	return strings.TrimSpace(out), nil
}

func refExists(ctx context.Context, repoPath, ref string) bool {
	_, err := git(ctx, repoPath, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

// git runs git in repoPath. Its error carries git's own words, trimmed: that is
// what ends up on the run in the UI.
func git(ctx context.Context, repoPath string, args ...string) (string, error) {
	// A fetch brings its own, longer deadline; everything else gets this one.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, gitTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repoPath}, args...)...)
	// Never wait for a credential prompt nobody will see.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 500 {
			msg = msg[len(msg)-500:]
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return stdout.String(), nil
}
