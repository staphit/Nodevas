package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"nodevas/internal/redact"
	"nodevas/internal/runnerwire"
)

// After the agent exits, a run's worktree is committed and described: what the
// run changed, as the hub stores it and the diff viewer shows it.

const (
	// commitTimeout covers the repository's own commit hooks.
	commitTimeout = 2 * time.Minute
	// diffTimeout covers a big diff.
	diffTimeout = 2 * time.Minute
	// maxSubject is a commit subject's length, in runes.
	maxSubject = 72
	// maxCommitBody caps the commit body taken from the agent's summary.
	maxCommitBody = 2 << 10
	// Fallback identity for a machine whose git has none configured.
	fallbackName  = "Nodevas runner"
	fallbackEmail = "runner@nodevas.invalid"
)

// commitMessage is a run's commit message: the node's title as the subject,
// the agent's summary as the body, and trailers naming the node and the run.
// The title, not the summary's first line: agents open with "Done." as often
// as with anything that says what changed, and the node is what the work was.
func commitMessage(summary string, job runnerwire.Job) string {
	summary = strings.TrimSpace(strings.ReplaceAll(redact.String(summary), "\r\n", "\n"))
	subject := subjectLine(oneLine(job.NodeTitle))
	if subject == "" {
		for _, line := range strings.Split(summary, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				subject = subjectLine(line)
				break
			}
		}
	}
	if subject == "" {
		runID := job.RunID
		if len(runID) > 8 {
			runID = runID[:8]
		}
		subject = "Nodevas run " + runID
	}
	var b strings.Builder
	b.WriteString(subject)
	b.WriteString("\n\n")
	if body := truncateUTF8(summary, maxCommitBody); body != "" {
		b.WriteString(body)
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "Nodevas-Node: %s\nNodevas-Run: %s\n", oneLine(job.NodeID), oneLine(job.RunID))
	return b.String()
}

// subjectLine cuts a line to maxSubject runes, at a word when one ends late
// enough, and drops the markdown a summary heading tends to start with.
func subjectLine(line string) string {
	line = stripMarkup(line)
	runes := []rune(line)
	if len(runes) <= maxSubject {
		return line
	}
	cut := string(runes[:maxSubject])
	if i := strings.LastIndexByte(cut, ' '); i >= 40 {
		cut = cut[:i]
	}
	return strings.TrimSpace(cut)
}

// stripMarkup drops a heading's or list item's leading markdown and bold stars.
func stripMarkup(line string) string {
	line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#*>- "))
	return strings.TrimSpace(strings.TrimRight(line, "*"))
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// truncateUTF8 cuts s to at most n bytes on a rune boundary.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// commitResult is what the commit step left behind.
type commitResult struct {
	// Base is the commit the run's changes are measured from.
	Base string
	// Commit is the run's head commit, "" when there is none (no changes, or
	// the commit failed).
	Commit string
	// Changed: the worktree differs from Base, committed or not.
	Changed bool
	// Err is why a commit with changes failed (git refused it, or the
	// worktree's git directory was changed).
	Err error
}

// commitRun stages everything in the worktree and commits it. The agent may
// have committed on its own; its commits count as the run's too.
func commitRun(ctx context.Context, wt Worktree, message string) commitResult {
	var res commitResult
	if err := checkGitDir(ctx, wt); err != nil {
		res.Err = err
		res.Changed = true
		return res
	}
	head, err := revParse(ctx, wt.Path, "HEAD")
	if err != nil {
		res.Err = err
		return res
	}
	res.Base = head
	if out, err := git(ctx, wt.Path, "merge-base", "HEAD", wt.BaseRef); err == nil && strings.TrimSpace(out) != "" {
		res.Base = strings.TrimSpace(out)
	}
	if _, err := git(ctx, wt.Path, "add", "-A"); err != nil {
		res.Err = err
		res.Changed = true
		return res
	}
	staged, err := git(ctx, wt.Path, "diff", "--cached", "--name-only", "-z")
	if err != nil {
		res.Err = err
		res.Changed = true
		return res
	}
	if staged != "" {
		args := identityArgs(ctx, wt.Path)
		args = append(args, "commit", "--quiet", "--no-verify", "-m", message)
		commitCtx, cancel := context.WithTimeout(ctx, commitTimeout)
		_, err := git(commitCtx, wt.Path, args...)
		cancel()
		if err != nil {
			res.Err = err
			res.Changed = true
			return res
		}
		if head, err = revParse(ctx, wt.Path, "HEAD"); err != nil {
			res.Err = err
			res.Changed = true
			return res
		}
	}
	if head != res.Base {
		res.Commit = head
		res.Changed = true
	}
	return res
}

// identityArgs names the fallback author when git has no identity of its own;
// a configured one (repository, global or system) is always used as it is.
func identityArgs(ctx context.Context, dir string) []string {
	var args []string
	if out, err := git(ctx, dir, "config", "--get", "user.name"); err != nil || strings.TrimSpace(out) == "" {
		args = append(args, "-c", "user.name="+fallbackName)
	}
	if out, err := git(ctx, dir, "config", "--get", "user.email"); err != nil || strings.TrimSpace(out) == "" {
		args = append(args, "-c", "user.email="+fallbackEmail)
	}
	return args
}

func revParse(ctx context.Context, dir, ref string) (string, error) {
	out, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// describeChanges is RunChanges for the run: the commit against its base, or,
// when there is no commit (it failed), the staged worktree against the base.
func describeChanges(ctx context.Context, dir string, res commitResult, base string) (runnerwire.RunChanges, error) {
	changes := runnerwire.RunChanges{Commit: res.Commit, Base: base, Files: []runnerwire.FileChange{}}
	if !res.Changed || res.Base == "" {
		return changes, nil
	}
	ctx, cancel := context.WithTimeout(ctx, diffTimeout)
	defer cancel()
	span := []string{res.Base, res.Commit}
	if res.Commit == "" {
		span = []string{"--cached", res.Base}
	}
	diff := func(args ...string) []string {
		return append(append([]string{"diff", "--no-color", "--no-ext-diff", "-M"}, args...), span...)
	}
	statuses, err := git(ctx, dir, diff("--name-status", "-z")...)
	if err != nil {
		return changes, err
	}
	numstat, err := git(ctx, dir, diff("--numstat", "-z")...)
	if err != nil {
		return changes, err
	}
	files := parseNameStatus(statuses)
	applyNumstat(files, parseNumstat(numstat))
	for _, f := range files {
		changes.Additions += f.Additions
		changes.Deletions += f.Deletions
	}
	if len(files) > runnerwire.MaxChangedFiles {
		files = files[:runnerwire.MaxChangedFiles]
	}
	changes.Files = files
	patch, over, err := gitCapped(ctx, dir, runnerwire.MaxPatchBytes, diff()...)
	switch {
	case err != nil:
		return changes, err
	case over:
		changes.PatchTruncated = true
	default:
		changes.Patch = patch
	}
	return changes, nil
}

// parseNameStatus reads `git diff --name-status -z -M`: a status, then one
// path, or two for a rename or copy.
func parseNameStatus(out string) []runnerwire.FileChange {
	fields := strings.Split(out, "\x00")
	var files []runnerwire.FileChange
	for i := 0; i < len(fields); i++ {
		status := fields[i]
		if status == "" {
			continue
		}
		next := func() string {
			if i+1 < len(fields) {
				i++
				return fields[i]
			}
			return ""
		}
		switch status[0] {
		case 'R':
			old := next()
			files = append(files, runnerwire.FileChange{Path: next(), OldPath: old, Status: runnerwire.FileRenamed})
		case 'C':
			next() // the source of a copy stays where it was
			files = append(files, runnerwire.FileChange{Path: next(), Status: runnerwire.FileAdded})
		case 'A':
			files = append(files, runnerwire.FileChange{Path: next(), Status: runnerwire.FileAdded})
		case 'D':
			files = append(files, runnerwire.FileChange{Path: next(), Status: runnerwire.FileDeleted})
		default: // M, T (type change), U, X
			files = append(files, runnerwire.FileChange{Path: next(), Status: runnerwire.FileModified})
		}
	}
	return files
}

// numstat is one file's line counts; binary files have none.
type numstat struct {
	additions, deletions int
	binary               bool
}

// parseNumstat reads `git diff --numstat -z -M`: "<added>\t<deleted>\t<path>"
// or, for a rename, "<added>\t<deleted>\t" then the old and new paths as
// fields of their own. A binary file counts "-" lines. Keyed by new path.
func parseNumstat(out string) map[string]numstat {
	stats := map[string]numstat{}
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		parts := strings.SplitN(fields[i], "\t", 3)
		if len(parts) != 3 {
			continue
		}
		path := parts[2]
		if path == "" && i+2 < len(fields) {
			path = fields[i+2]
			i += 2
		}
		var s numstat
		if parts[0] == "-" && parts[1] == "-" {
			s.binary = true
		} else {
			s.additions, _ = strconv.Atoi(parts[0])
			s.deletions, _ = strconv.Atoi(parts[1])
		}
		stats[path] = s
	}
	return stats
}

func applyNumstat(files []runnerwire.FileChange, stats map[string]numstat) {
	for i := range files {
		if s, ok := stats[files[i].Path]; ok {
			files[i].Additions, files[i].Deletions, files[i].Binary = s.additions, s.deletions, s.binary
		}
	}
}

// gitCapped runs git and keeps at most limit bytes of its output; over says
// there was more.
func gitCapped(ctx context.Context, dir string, limit int, args ...string) (string, bool, error) {
	cmd := exec.CommandContext(ctx, "git", hardenedArgs(dir, args)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
	out := &capWriter{limit: limit}
	var stderr strings.Builder
	cmd.Stdout, cmd.Stderr = out, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", false, fmt.Errorf("git diff: %s", truncate(msg, 500))
	}
	return string(out.buf), out.over, nil
}

// capWriter keeps the first limit bytes written to it and notes any more.
type capWriter struct {
	limit int
	buf   []byte
	over  bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	room := w.limit - len(w.buf)
	if len(p) > room {
		w.over = true
		w.buf = append(w.buf, p[:max(room, 0)]...)
	} else {
		w.buf = append(w.buf, p...)
	}
	return len(p), nil
}

// removeWorktree deletes a run's worktree. With dropBranch it deletes the
// run's local branch too (it holds nothing: the run changed nothing); a
// branch with a pull request is left alone, since GitHub owns it.
func removeWorktree(ctx context.Context, repoPath string, wt Worktree, dropBranch bool) error {
	var errs []error
	if _, err := git(ctx, repoPath, "worktree", "remove", "--force", wt.Path); err != nil {
		// A worktree git no longer knows (its repository moved) is just a
		// directory; one already gone is fine.
		if _, statErr := os.Stat(wt.Path); statErr == nil {
			if rmErr := os.RemoveAll(wt.Path); rmErr != nil {
				errs = append(errs, err, rmErr)
			}
		}
		_, _ = git(ctx, repoPath, "worktree", "prune")
	}
	if dropBranch && wt.Branch != "" {
		if _, err := git(ctx, repoPath, "branch", "-D", wt.Branch); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
