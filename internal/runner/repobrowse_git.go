package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"nodevas/internal/runnerwire"
)

// repoGitTimeout bounds each git command repo browsing runs.
const repoGitTimeout = 10 * time.Second

// repoGitArgs puts browsing's read-only overrides in front of a git command
// (on top of hardenedArgs): no pager, no external diff, no signature checks,
// and every filter driver the repository configures emptied, so nothing the
// repository (or an agent run in it) configured runs.
func repoGitArgs(ctx context.Context, root string, args []string) []string {
	front := []string{"-c", "core.pager=cat", "-c", "diff.external=", "-c", "log.showSignature=false",
		"-c", "color.ui=false", "-c", "core.quotePath=false"}
	for _, name := range repoFilterDrivers(ctx, root) {
		front = append(front, "-c", "filter."+name+".clean=", "-c", "filter."+name+".smudge=",
			"-c", "filter."+name+".process=", "-c", "filter."+name+".required=false")
	}
	return append(front, args...)
}

var filterKey = regexp.MustCompile(`^filter\.(.+)\.(clean|smudge|process|required)$`)

// repoFilterDrivers names the filter drivers git's config defines for root.
// Reading config runs nothing.
func repoFilterDrivers(ctx context.Context, root string) []string {
	ctx, cancel := context.WithTimeout(ctx, repoGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", hardenedArgs(root, []string{"config", "--null", "--name-only", "--get-regexp", `^filter\.`})...)
	cmd.Env = repoGitEnv()
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var names []string
	for _, key := range strings.Split(string(out), "\x00") {
		key = strings.TrimSpace(key)
		m := filterKey.FindStringSubmatch(strings.ToLower(key))
		if m == nil || seen[m[1]] || strings.ContainsAny(m[1], "=\x00\n") {
			continue
		}
		seen[m[1]] = true
		names = append(names, m[1])
	}
	return names
}

func repoGitEnv() []string {
	return append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_PAGER=cat", "GIT_EXTERNAL_DIFF=")
}

// repoGit runs a read-only git command at root, its output capped.
func repoGit(ctx context.Context, root string, limit int, args ...string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, repoGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", hardenedArgs(root, repoGitArgs(ctx, root, args))...)
	cmd.Env = repoGitEnv()
	out := &capWriter{limit: limit}
	var stderr strings.Builder
	cmd.Stdout, cmd.Stderr = out, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", false, refuseRepo(runnerwire.RepoErrGit, "git took too long")
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", false, refuseRepo(runnerwire.RepoErrGit, "git: "+truncate(msg, 300))
	}
	return string(out.buf), out.over, nil
}

// repoStatus is the worktree's branch, head and changed paths.
func repoStatus(ctx context.Context, root string) (runnerwire.RepoGitStatus, error) {
	raw, over, err := repoGit(ctx, root, 4<<20, "status", "--porcelain=v2", "-z", "--branch", "--no-renames",
		"--ignore-submodules=all", "--untracked-files=normal")
	if err != nil {
		return runnerwire.RepoGitStatus{}, err
	}
	out := runnerwire.RepoGitStatus{Entries: []runnerwire.RepoStatusEntry{}, Truncated: over}
	for _, rec := range strings.Split(raw, "\x00") {
		switch {
		case strings.HasPrefix(rec, "# branch.head "):
			out.Branch = strings.TrimPrefix(rec, "# branch.head ")
			if out.Branch == "(detached)" {
				out.Branch = ""
			}
		case strings.HasPrefix(rec, "# branch.oid "):
			if oid := strings.TrimPrefix(rec, "# branch.oid "); oid != "(initial)" {
				out.Head = oid
			}
		case strings.HasPrefix(rec, "1 "):
			// 1 XY sub mH mI mW hH hI path
			fields := strings.SplitN(rec, " ", 9)
			if len(fields) == 9 {
				addStatus(&out, fields[8], statusMark(fields[1]))
			}
		case strings.HasPrefix(rec, "u "):
			// u XY sub m1 m2 m3 mW h1 h2 h3 path
			fields := strings.SplitN(rec, " ", 11)
			if len(fields) == 11 {
				addStatus(&out, fields[10], "M")
			}
		case strings.HasPrefix(rec, "? "):
			addStatus(&out, strings.TrimPrefix(rec, "? "), "?")
		}
	}
	if len(out.Entries) > runnerwire.MaxRepoStatusEntries {
		out.Entries, out.Truncated = out.Entries[:runnerwire.MaxRepoStatusEntries], true
	}
	return out, nil
}

// statusMark is one letter for an XY pair: the index's change, else the
// worktree's.
func statusMark(xy string) string {
	for _, c := range xy {
		switch c {
		case 'M', 'T':
			return "M"
		case 'A':
			return "A"
		case 'D':
			return "D"
		case 'R', 'C':
			return "R"
		}
	}
	return "M"
}

// repoLog is the newest commits, at most limit.
func repoLog(ctx context.Context, root string, limit int) ([]runnerwire.RepoCommit, error) {
	if limit <= 0 {
		limit = runnerwire.DefaultRepoLogEntries
	}
	limit = min(limit, runnerwire.MaxRepoLogEntries)
	raw, _, err := repoGit(ctx, root, 1<<20, "log", "--no-show-signature", "-n"+strconv.Itoa(limit),
		"--format=%H%x1f%aI%x1f%an%x1f%s%x1e")
	if err != nil {
		// A repository with no commits yet has no log.
		if strings.Contains(err.Error(), "does not have any commits") {
			return []runnerwire.RepoCommit{}, nil
		}
		return nil, err
	}
	out := []runnerwire.RepoCommit{}
	for _, rec := range strings.Split(raw, "\x1e") {
		rec = strings.TrimLeft(rec, "\r\n")
		if rec == "" {
			continue
		}
		f := strings.SplitN(rec, "\x1f", 4)
		if len(f) != 4 {
			continue
		}
		out = append(out, runnerwire.RepoCommit{SHA: f[0], At: f[1], Author: f[2], Subject: strings.TrimRight(f[3], "\r\n")})
	}
	return out, nil
}

// repoDiff is the worktree against HEAD (untracked files as new files) or
// one commit's patch, capped, with hidden files dropped.
func repoDiff(ctx context.Context, root, ref string) (runnerwire.RepoDiff, error) {
	out := runnerwire.RepoDiff{Ref: ref}
	var raw string
	var over bool
	var err error
	if ref == "" {
		raw, over, err = repoGit(ctx, root, runnerwire.MaxRepoPatchBytes, "diff", "--no-ext-diff", "--no-textconv", "--no-color", "HEAD")
	} else {
		raw, over, err = repoGit(ctx, root, runnerwire.MaxRepoPatchBytes, "show", "--no-ext-diff", "--no-textconv", "--no-color", "--format=", ref)
	}
	if err != nil {
		return out, err
	}
	patch, hidden := dropHiddenFiles(raw)
	out.Hidden, out.Truncated = hidden, over
	if ref == "" && !over {
		untracked, more, hiddenNew, err := untrackedPatch(ctx, root, runnerwire.MaxRepoPatchBytes-len(patch))
		if err != nil {
			return out, err
		}
		patch += untracked
		out.Hidden += hiddenNew
		out.Truncated = more
	}
	out.Patch = patch
	return out, nil
}

// dropHiddenFiles removes the sections of a patch that touch hidden files
// and counts them.
func dropHiddenFiles(patch string) (string, int) {
	if patch == "" {
		return "", 0
	}
	sections := splitPatch(patch)
	var b strings.Builder
	hidden := 0
	for _, sec := range sections {
		if a, z, ok := patchPaths(sec); ok && (secretPath(a) || secretPath(z)) {
			hidden++
			continue
		}
		b.WriteString(sec)
	}
	return b.String(), hidden
}

// splitPatch cuts a patch at each "diff --git " line.
func splitPatch(patch string) []string {
	var out []string
	start := 0
	for i := 0; i < len(patch); {
		next := strings.Index(patch[i:], "diff --git ")
		if next < 0 {
			break
		}
		pos := i + next
		if pos == 0 || patch[pos-1] == '\n' {
			if pos > start {
				out = append(out, patch[start:pos])
			}
			start = pos
		}
		i = pos + len("diff --git ")
	}
	return append(out, patch[start:])
}

// patchPaths are a section's a/ and b/ paths.
func patchPaths(section string) (string, string, bool) {
	line, _, _ := strings.Cut(section, "\n")
	if !strings.HasPrefix(line, "diff --git ") {
		return "", "", false
	}
	rest := strings.TrimPrefix(line, "diff --git ")
	if strings.HasPrefix(rest, `"`) {
		a, after, ok := cutQuoted(rest)
		if !ok {
			return "", "", false
		}
		b := strings.TrimSpace(after)
		if strings.HasPrefix(b, `"`) {
			b, _, _ = cutQuoted(b)
		}
		return strings.TrimPrefix(a, "a/"), strings.TrimPrefix(b, "b/"), true
	}
	// Unquoted: "a/<p> b/<p>"; the two halves are the same path for a
	// modification, so split at " b/".
	if i := strings.Index(rest, " b/"); i >= 0 {
		return strings.TrimPrefix(rest[:i], "a/"), rest[i+3:], true
	}
	return "", "", false
}

func cutQuoted(s string) (string, string, bool) {
	for i := 1; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '"' {
			unq, err := strconv.Unquote(s[:i+1])
			if err != nil {
				return "", "", false
			}
			return unq, s[i+1:], true
		}
	}
	return "", "", false
}

// untrackedPatch shows untracked files as new files, read straight from the
// worktree (no git diff runs on them), within room bytes.
func untrackedPatch(ctx context.Context, root string, room int) (string, bool, int, error) {
	raw, _, err := repoGit(ctx, root, 4<<20, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", false, 0, err
	}
	var b strings.Builder
	hidden := 0
	for _, rel := range strings.Split(raw, "\x00") {
		if rel == "" {
			continue
		}
		if secretPath(rel) {
			hidden++
			continue
		}
		section, err := newFilePatch(root, rel)
		if err != nil {
			continue
		}
		if b.Len()+len(section) > room {
			b.WriteString(section[:max(room-b.Len(), 0)])
			return b.String(), true, hidden, nil
		}
		b.WriteString(section)
	}
	return b.String(), false, hidden, nil
}

// newFilePatch is a new file's patch section; nothing outside root is read.
func newFilePatch(root, rel string) (string, error) {
	real, err := resolveRepoPath(root, rel)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a file")
	}
	data, err := os.ReadFile(real)
	if err != nil {
		return "", err
	}
	header := fmt.Sprintf("diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n", rel, rel, rel)
	if bytes.IndexByte(data[:min(len(data), binarySniffBytes)], 0) >= 0 || !utf8.Valid(data) {
		return fmt.Sprintf("diff --git a/%s b/%s\nnew file mode 100644\nBinary files /dev/null and b/%s differ\n", rel, rel, rel), nil
	}
	if len(data) == 0 {
		return fmt.Sprintf("diff --git a/%s b/%s\nnew file mode 100644\n", rel, rel), nil
	}
	text := strings.TrimSuffix(string(data), "\n")
	lines := strings.Split(text, "\n")
	var b strings.Builder
	b.WriteString(header)
	fmt.Fprintf(&b, "@@ -0,0 +1,%d @@\n", len(lines))
	for _, line := range lines {
		b.WriteString("+")
		b.WriteString(line)
		b.WriteString("\n")
	}
	if !strings.HasSuffix(string(data), "\n") {
		b.WriteString("\\ No newline at end of file\n")
	}
	return b.String(), nil
}

// addStatus keeps one status entry, or counts it hidden.
func addStatus(s *runnerwire.RepoGitStatus, path, mark string) {
	if secretPath(path) {
		s.Hidden++
		return
	}
	s.Entries = append(s.Entries, runnerwire.RepoStatusEntry{Path: path, Mark: mark})
}

// readCapped reads at most limit bytes of path.
func readCapped(path string, limit int) ([]byte, error) {
	return os.ReadFile(path)
}
