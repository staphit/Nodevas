package agenthook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"nodevas/internal/redact"
	"nodevas/internal/runnerwire"
)

// External session changes (W6): while a session holds a node, every edit
// tool call reports the file it touched, and every Stop a diff snapshot of
// the session's working tree against HEAD. The hub files both under the node.
// Like the rest of the hook, nothing here may block or fail the agent: every
// git call is bounded and every failure means "report nothing".

// gitTimeout bounds one git call from the hook.
var gitTimeout = 4 * time.Second // a var so tests on a loaded machine can widen it

// editTools are the tools whose input names a file they write: Claude Code's
// (Edit, Write, MultiEdit, NotebookEdit), Gemini CLI's (write_file, replace,
// edit), OpenCode's (edit, write) and the Antigravity CLI's (write_to_file,
// replace_file_content, multi_replace_file_content; its stream calls a write
// write_file). Codex's apply_patch, which OpenCode also uses for some models,
// names its files inside the patch.
var editTools = map[string]bool{
	"edit": true, "write": true, "multiedit": true, "notebookedit": true,
	"write_file": true, "replace": true,
	"write_to_file": true, "replace_file_content": true, "multi_replace_file_content": true,
}

// pathKeys are where those tools put the file they write: snake_case for
// Claude Code and Gemini CLI, filePath for OpenCode, and the Antigravity
// CLI's capitalised argument names.
var pathKeys = []string{
	"file_path", "notebook_path", "absolute_path", "path",
	"filePath",
	"TargetFile", "Path", "AbsolutePath", "FilePath",
}

// EditedFiles is the files a tool call wrote, as its input names them
// (absolute, or relative to the session's directory). Anything that is not a
// known edit tool names none.
func EditedFiles(toolName string, toolInput map[string]any) []string {
	name := strings.ToLower(strings.TrimSpace(toolName))
	var files []string
	switch {
	case editTools[name]:
		for _, key := range pathKeys {
			if s, _ := toolInput[key].(string); strings.TrimSpace(s) != "" {
				files = append(files, strings.TrimSpace(s))
				break
			}
		}
	case name == "apply_patch" || name == "applypatch":
		var walk func(v any)
		walk = func(v any) {
			switch t := v.(type) {
			case string:
				files = append(files, patchFiles(t)...)
			case []any:
				for _, item := range t {
					walk(item)
				}
			case map[string]any:
				for _, item := range t {
					walk(item)
				}
			}
		}
		walk(toolInput)
	}
	return dedupe(files)
}

// patchFiles reads the file headers of a Codex apply_patch envelope.
func patchFiles(patch string) []string {
	var files []string
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: "} {
			if rest, ok := strings.CutPrefix(line, prefix); ok && strings.TrimSpace(rest) != "" {
				files = append(files, strings.TrimSpace(rest))
			}
		}
	}
	return files
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// git runs git in dir with extra environment and returns its stdout.
func git(ctx context.Context, dir string, env []string, stdin io.Reader, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_OPTIONAL_LOCKS=0"), env...)
	cmd.Stdin = stdin
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}

// repoRoot is the top of the git working tree holding dir, or "".
func repoRoot(ctx context.Context, dir string) string {
	if dir == "" {
		return ""
	}
	out, err := git(ctx, dir, nil, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return filepath.Clean(strings.TrimSpace(out))
}

// RepoRelative turns the files a tool named into paths relative to the
// session's repository, slash-separated, when they are inside it; a file
// outside it (or with no repository) is reported as named, cleaned.
func RepoRelative(ctx context.Context, cwd string, files []string) []string {
	root := repoRoot(ctx, cwd)
	out := make([]string, 0, len(files))
	for _, f := range files {
		path := f
		if !filepath.IsAbs(path) && cwd != "" {
			path = filepath.Join(cwd, path)
		}
		path = filepath.Clean(path)
		if rel, ok := inside(root, path); ok {
			out = append(out, rel)
			continue
		}
		out = append(out, filepath.ToSlash(path))
	}
	return dedupe(out)
}

// inside reports path relative to root when it is within it, trying the
// paths as given and then with symlinks resolved (git prints the resolved
// top level; a temp directory on macOS is a symlink).
func inside(root, path string) (string, bool) {
	if root == "" {
		return "", false
	}
	try := func(root, path string) (string, bool) {
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return "", false
		}
		return filepath.ToSlash(rel), true
	}
	if rel, ok := try(root, path); ok {
		return rel, true
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	dir, base := filepath.Split(path)
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", false
	}
	return try(realRoot, filepath.Join(realDir, base))
}

// Snapshot is the session's working tree against HEAD, in the shape a run's
// changes take: file stats and the patch (dropped past MaxPatchBytes). Files
// git does not track yet count as added, through a scratch copy of the index
// so the session's own index is never touched. ok is false when there is no
// repository or no HEAD, or git failed.
func Snapshot(ctx context.Context, cwd string) (runnerwire.RunChanges, bool) {
	root := repoRoot(ctx, cwd)
	if root == "" {
		return runnerwire.RunChanges{}, false
	}
	headOut, err := git(ctx, root, nil, nil, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return runnerwire.RunChanges{}, false
	}
	head := strings.TrimSpace(headOut)
	env, cleanup := scratchIndex(ctx, root)
	defer cleanup()

	diff := func(args ...string) []string {
		return append(append([]string{"diff", "--no-color", "--no-ext-diff", "-M"}, args...), "HEAD")
	}
	statuses, err := git(ctx, root, env, nil, diff("--name-status", "-z")...)
	if err != nil {
		return runnerwire.RunChanges{}, false
	}
	numstat, err := git(ctx, root, env, nil, diff("--numstat", "-z")...)
	if err != nil {
		return runnerwire.RunChanges{}, false
	}
	changes := runnerwire.RunChanges{Base: head, Files: []runnerwire.FileChange{}}
	files := parseNameStatus(statuses)
	stats := parseNumstat(numstat)
	for i := range files {
		if s, ok := stats[files[i].Path]; ok {
			files[i].Additions, files[i].Deletions, files[i].Binary = s.additions, s.deletions, s.binary
		}
		changes.Additions += files[i].Additions
		changes.Deletions += files[i].Deletions
	}
	if len(files) > runnerwire.MaxChangedFiles {
		files = files[:runnerwire.MaxChangedFiles]
	}
	changes.Files = files
	if len(files) == 0 {
		return changes, true
	}
	patch, over, err := gitCapped(ctx, root, env, runnerwire.MaxPatchBytes, diff()...)
	switch {
	case err != nil:
		changes.PatchTruncated = true
	case over:
		changes.PatchTruncated = true
	default:
		changes.Patch = redact.String(patch)
	}
	return changes, true
}

// withoutSecretFiles drops, from `git ls-files -z` output, the untracked
// files that look like they hold secrets: the snapshot goes to the hub, and
// a new .env or key file is the commonest way one would get there.
func withoutSecretFiles(list string) string {
	var kept []string
	for _, path := range strings.Split(list, "\x00") {
		if path != "" && !secretFile(path) {
			kept = append(kept, path)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, "\x00") + "\x00"
}

// secretFile reports whether path's name is one secrets are kept under.
func secretFile(path string) bool {
	name := strings.ToLower(filepath.Base(filepath.FromSlash(path)))
	switch name {
	case ".npmrc", ".pypirc", ".netrc", "_netrc", ".git-credentials":
		return true
	}
	for _, prefix := range []string{".env", "id_rsa", "id_dsa", "id_ecdsa", "id_ed25519", "credentials"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	for _, ext := range []string{".pem", ".key", ".p12", ".pfx"} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return strings.Contains(name, "kubeconfig")
}

// scratchIndex copies the repository's index to a temporary file and marks
// the untracked, unignored files in it as intended to add, so a diff against
// HEAD shows them as new files without staging anything in the real index.
// On any failure it returns no environment, and the diff covers tracked files
// only.
func scratchIndex(ctx context.Context, root string) ([]string, func()) {
	nothing := func() {}
	indexOut, err := git(ctx, root, nil, nil, "rev-parse", "--git-path", "index")
	if err != nil {
		return nil, nothing
	}
	index := strings.TrimSpace(indexOut)
	if !filepath.IsAbs(index) {
		index = filepath.Join(root, index)
	}
	data, err := os.ReadFile(index)
	if err != nil {
		return nil, nothing
	}
	info, err := os.Stat(index)
	if err != nil {
		return nil, nothing
	}
	tmp, err := os.CreateTemp("", "nodevas-hook-index-*")
	if err != nil {
		return nil, nothing
	}
	cleanup := func() { _ = os.Remove(tmp.Name()) }
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		cleanup()
		return nil, nothing
	}
	// git trusts a cached stat only for files older than the index file, so
	// the copy keeps the index's time: a fresh one would hide a same-size edit
	// made in the second the index was last written.
	if err := os.Chtimes(tmp.Name(), info.ModTime(), info.ModTime()); err != nil {
		cleanup()
		return nil, nothing
	}
	env := []string{"GIT_INDEX_FILE=" + tmp.Name()}
	untracked, err := git(ctx, root, env, nil, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		cleanup()
		return nil, nothing
	}
	untracked = withoutSecretFiles(untracked)
	if untracked != "" && strings.Count(untracked, "\x00") <= runnerwire.MaxChangedFiles {
		if _, err := git(ctx, root, env, strings.NewReader(untracked),
			"add", "--intent-to-add", "--pathspec-from-file=-", "--pathspec-file-nul"); err != nil {
			cleanup()
			return nil, nothing
		}
	}
	return env, cleanup
}

// gitCapped runs git keeping at most limit bytes of its output; over says
// there was more.
func gitCapped(ctx context.Context, dir string, env []string, limit int, args ...string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_OPTIONAL_LOCKS=0"), env...)
	out := &capWriter{limit: limit}
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		return "", false, fmt.Errorf("git diff: %w", err)
	}
	return string(out.buf), out.over, nil
}

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

// parseNameStatus reads `git diff --name-status -z -M`: a status, then one
// path, or two for a rename or copy. (The runner has its own copy; the hook
// does not import the runner.)
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
			next()
			files = append(files, runnerwire.FileChange{Path: next(), Status: runnerwire.FileAdded})
		case 'A':
			files = append(files, runnerwire.FileChange{Path: next(), Status: runnerwire.FileAdded})
		case 'D':
			files = append(files, runnerwire.FileChange{Path: next(), Status: runnerwire.FileDeleted})
		default:
			files = append(files, runnerwire.FileChange{Path: next(), Status: runnerwire.FileModified})
		}
	}
	return files
}

type numstat struct {
	additions, deletions int
	binary               bool
}

// parseNumstat reads `git diff --numstat -z -M`, keyed by new path.
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

// toolInputMap decodes a hook payload's tool_input, which agents send as an
// object (Claude Code, Gemini CLI) or occasionally as a JSON string.
func toolInputMap(v any) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		return t
	case string:
		var m map[string]any
		if json.Unmarshal([]byte(t), &m) == nil {
			return m
		}
		// A bare patch string, as Codex's apply_patch may send.
		return map[string]any{"input": t}
	}
	return nil
}
