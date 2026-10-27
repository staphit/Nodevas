package gitrepo

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Read side for the Explorer's Git tab: the project's own commits and one
// commit's patch.

// MaxPatchBytes caps CommitPatch.
const MaxPatchBytes = 256 << 10

// RecentCommits is the repository's newest commits, at most limit.
func (r *Repo) RecentCommits(ctx context.Context, limit int) ([]Commit, error) {
	if !r.hasHead(ctx) {
		return []Commit{}, nil
	}
	if limit <= 0 {
		limit = 50
	}
	out, err := r.run(ctx, nil, nil, "log", "--no-show-signature", fmt.Sprintf("-n%d", limit),
		"--format=%H"+logSep+"%aI"+logSep+"%an"+logSep+"%s")
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

// CommitPatch is commit sha's patch, cut at max bytes (truncated true).
// External diff drivers and textconv filters never run.
func (r *Repo) CommitPatch(ctx context.Context, sha string, max int) (patch string, truncated bool, err error) {
	if !ValidSHA(sha) {
		return "", false, fmt.Errorf("invalid commit %q", sha)
	}
	if max <= 0 {
		max = MaxPatchBytes
	}
	full := []string{
		"-c", "core.hooksPath=" + noHooksPath(),
		"-c", "core.fsmonitor=false",
		"-c", "core.quotepath=false",
		"-c", "core.pager=cat",
		"-c", "diff.external=",
		"-c", "log.showSignature=false",
		"show", "--no-ext-diff", "--no-textconv", "--no-color", "--format=", sha, "--",
	}
	cmd := exec.CommandContext(ctx, gitPath, full...)
	cmd.Dir = r.workTree
	cmd.Env = r.env(nil)
	out := &capped{limit: max}
	var stderr strings.Builder
	cmd.Stdout, cmd.Stderr = out, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 500 {
			msg = msg[:500]
		}
		return "", false, fmt.Errorf("git show: %w: %s", err, msg)
	}
	return string(out.buf), out.over, nil
}

// capped keeps the first limit bytes written to it and notes any more.
type capped struct {
	limit int
	buf   []byte
	over  bool
}

func (w *capped) Write(p []byte) (int, error) {
	room := w.limit - len(w.buf)
	if len(p) > room {
		w.over = true
		w.buf = append(w.buf, p[:max(room, 0)]...)
	} else {
		w.buf = append(w.buf, p...)
	}
	return len(p), nil
}
