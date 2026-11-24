package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

func initTouchedRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	runGit := func(args ...string) {
		t.Helper()
		if _, err := git(ctx, dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	runGit("init", "-b", "main")
	runGit("config", "user.name", "Test")
	runGit("config", "user.email", "test@example.com")
	writeTestFile(t, filepath.Join(dir, "base.go"), "package base\n\nfunc Hello() string {\n\treturn \"hi\"\n}\n")
	writeTestFile(t, filepath.Join(dir, "del.go"), "package base\n")
	writeTestFile(t, filepath.Join(dir, "oldname.go"), "package base\n\nvar X = 1\n")
	writeTestFile(t, filepath.Join(dir, "append.txt"), "line1\nline2\n")
	runGit("add", ".")
	runGit("commit", "-m", "init")
	return dir
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type collectedTouched struct {
	mu   sync.Mutex
	msgs []runnerwire.Touched
}

func (c *collectedTouched) send(t runnerwire.Touched) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, t)
}

func (c *collectedTouched) all() []runnerwire.Touched {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.msgs)
}

func (c *collectedTouched) allFiles() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, m := range c.msgs {
		out = append(out, m.Files...)
	}
	return out
}

func TestTouchedFromGitStatus(t *testing.T) {
	dir := initTouchedRepo(t)
	ctx := context.Background()

	// Staged new file, modified file, deleted file, renamed file, untracked file.
	writeTestFile(t, filepath.Join(dir, "staged_new.go"), "package base\n")
	if _, err := git(ctx, dir, "add", "staged_new.go"); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, "base.go"), "package base\n\nfunc Hello() string {\n\treturn \"bye\"\n}\n")
	if err := os.Remove(filepath.Join(dir, "del.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := git(ctx, dir, "mv", "oldname.go", "newname.go"); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, "sub", "untracked.go"), "package sub\n")

	var got collectedTouched
	w := newTouchedWatcher(dir, got.send, touchedOptions{})
	w.poll(ctx)

	files := got.allFiles()
	for _, want := range []string{"staged_new.go", "base.go", "del.go", "oldname.go", "newname.go", "sub/untracked.go"} {
		if !slices.Contains(files, want) {
			t.Errorf("missing %q in touched files: %v", want, files)
		}
	}
}

func TestTouchedFromEditToolsRepoRelative(t *testing.T) {
	dir := initTouchedRepo(t)
	var got collectedTouched
	w := newTouchedWatcher(dir, got.send, touchedOptions{})

	absInRepo := filepath.Join(dir, "pkg", "auth.go")
	w.onEvent(runnerwire.RunEvent{
		Type:  runnerwire.EventToolCall,
		Tool:  "Edit",
		Input: `{"file_path":"` + filepath.ToSlash(absInRepo) + `"}`,
	})
	w.onEvent(runnerwire.RunEvent{
		Type:  runnerwire.EventToolCall,
		Tool:  "write_file",
		Input: `{"path":"src/api.ts"}`,
	})
	w.onEvent(runnerwire.RunEvent{
		Type:  runnerwire.EventToolCall,
		Tool:  "apply_patch",
		Input: "*** Update File: cmd/main.go\n@@\n-a\n+b\n",
	})

	files := got.allFiles()
	for _, want := range []string{"pkg/auth.go", "src/api.ts", "cmd/main.go"} {
		if !slices.Contains(files, want) {
			t.Errorf("missing %q in edit-tool touched files: %v", want, files)
		}
	}
}

func TestTouchedDropsAbsoluteAndOutsidePaths(t *testing.T) {
	dir := initTouchedRepo(t)
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")

	var got collectedTouched
	w := newTouchedWatcher(dir, got.send, touchedOptions{})

	// Absolute outside worktree, relative traversal outside worktree, oversized path, plus one valid path.
	w.onEvent(runnerwire.RunEvent{
		Type:  runnerwire.EventToolCall,
		Tool:  "Edit",
		Input: `{"file_path":"` + filepath.ToSlash(outsideFile) + `"}`,
	})
	w.onEvent(runnerwire.RunEvent{
		Type:  runnerwire.EventToolCall,
		Tool:  "Edit",
		Input: `{"file_path":"../../etc/passwd"}`,
	})
	longName := "a/" + string(make([]byte, runnerwire.MaxTouchedPathBytes+10))
	for i := range longName {
		if longName[i] == 0 {
			longName = longName[:i] + "x" + longName[i+1:]
		}
	}
	w.onEvent(runnerwire.RunEvent{
		Type:  runnerwire.EventToolCall,
		Tool:  "Edit",
		Input: `{"file_path":"` + longName + `"}`,
	})
	w.onEvent(runnerwire.RunEvent{
		Type:  runnerwire.EventToolCall,
		Tool:  "Edit",
		Input: `{"file_path":"inside/good.go"}`,
	})

	files := got.allFiles()
	if !slices.Equal(files, []string{"inside/good.go"}) {
		t.Fatalf("files = %v, want only [inside/good.go]", files)
	}
}

func TestTouchedSkipsSecretPaths(t *testing.T) {
	dir := initTouchedRepo(t)
	ctx := context.Background()

	writeTestFile(t, filepath.Join(dir, ".env"), "SECRET=1\n")
	writeTestFile(t, filepath.Join(dir, "config", ".env.local"), "SECRET=2\n")
	writeTestFile(t, filepath.Join(dir, "certs", "server.pem"), "KEY\n")
	writeTestFile(t, filepath.Join(dir, "src", "ok.go"), "package src\n")

	var got collectedTouched
	w := newTouchedWatcher(dir, got.send, touchedOptions{})
	w.onEvent(runnerwire.RunEvent{
		Type:  runnerwire.EventToolCall,
		Tool:  "Write",
		Input: `{"file_path":".env.production"}`,
	})
	w.poll(ctx)

	files := got.allFiles()
	if !slices.Equal(files, []string{"src/ok.go"}) {
		t.Fatalf("files = %v, want only [src/ok.go]", files)
	}
}

func TestTouchedIgnoresLockfilesAndGenerated(t *testing.T) {
	dir := initTouchedRepo(t)
	ctx := context.Background()

	writeTestFile(t, filepath.Join(dir, ".gitattributes"), "gen/** linguist-generated\n*.pb.go linguist-generated=true\n")
	if _, err := git(ctx, dir, "add", ".gitattributes"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(ctx, dir, "commit", "-m", "add gitattributes"); err != nil {
		t.Fatal(err)
	}

	lockfiles := []string{
		"go.sum", "web/package-lock.json", "yarn.lock", "pnpm-lock.yaml",
		"Cargo.lock", "poetry.lock", "uv.lock", "Pipfile.lock",
		"composer.lock", "Gemfile.lock", "bun.lockb", "npm-shrinkwrap.json",
	}
	for _, lf := range lockfiles {
		writeTestFile(t, filepath.Join(dir, lf), "lock\n")
	}
	writeTestFile(t, filepath.Join(dir, "gen", "schema.go"), "package gen\n")
	writeTestFile(t, filepath.Join(dir, "api", "service.pb.go"), "package api\n")
	writeTestFile(t, filepath.Join(dir, "api", "handler.go"), "package api\n")

	var got collectedTouched
	w := newTouchedWatcher(dir, got.send, touchedOptions{})
	w.poll(ctx)

	files := got.allFiles()
	if !slices.Equal(files, []string{"api/handler.go"}) {
		t.Fatalf("files = %v, want only [api/handler.go]", files)
	}
}

func TestTouchedMarksAddOnlyAndResendsOnChange(t *testing.T) {
	dir := initTouchedRepo(t)
	ctx := context.Background()

	// 1. Append lines to tracked append.txt and create an untracked file -> both are addOnly.
	writeTestFile(t, filepath.Join(dir, "append.txt"), "line1\nline2\nline3\n")
	writeTestFile(t, filepath.Join(dir, "brand_new.go"), "package base\n")

	var got collectedTouched
	w := newTouchedWatcher(dir, got.send, touchedOptions{})
	w.poll(ctx)

	msgs := got.all()
	if len(msgs) != 1 {
		t.Fatalf("first poll msgs = %d, want 1", len(msgs))
	}
	if !slices.Contains(msgs[0].AddOnly, "append.txt") || !slices.Contains(msgs[0].AddOnly, "brand_new.go") {
		t.Fatalf("first poll AddOnly = %v, want both append.txt and brand_new.go", msgs[0].AddOnly)
	}

	// 2. Poll again with no change -> nothing sent.
	w.poll(ctx)
	if len(got.all()) != 1 {
		t.Fatalf("second poll sent duplicate message: %+v", got.all())
	}

	// 3. Now delete a line in append.txt -> stops being addOnly -> re-sent once without AddOnly.
	writeTestFile(t, filepath.Join(dir, "append.txt"), "changed1\nline2\nline3\n")
	w.poll(ctx)

	msgs = got.all()
	if len(msgs) != 2 {
		t.Fatalf("third poll msgs = %d, want 2", len(msgs))
	}
	if !slices.Equal(msgs[1].Files, []string{"append.txt"}) {
		t.Fatalf("third poll Files = %v, want [append.txt]", msgs[1].Files)
	}
	if len(msgs[1].AddOnly) != 0 {
		t.Fatalf("third poll AddOnly = %v, want empty", msgs[1].AddOnly)
	}

	// 4. Subsequent poll sends nothing more.
	w.poll(ctx)
	if len(got.all()) != 2 {
		t.Fatalf("fourth poll should not re-send: %+v", got.all())
	}
}

func TestTouchedSendsEachPathOnce(t *testing.T) {
	dir := initTouchedRepo(t)
	ctx := context.Background()

	writeTestFile(t, filepath.Join(dir, "base.go"), "package base\n// edited\n")

	var got collectedTouched
	w := newTouchedWatcher(dir, got.send, touchedOptions{})
	w.poll(ctx)
	w.poll(ctx)
	w.onEvent(runnerwire.RunEvent{
		Type:  runnerwire.EventToolCall,
		Tool:  "Edit",
		Input: `{"file_path":"base.go"}`,
	})
	w.poll(ctx)

	if len(got.all()) != 1 {
		t.Fatalf("got %d messages, want 1: %+v", len(got.all()), got.all())
	}
	if !slices.Equal(got.allFiles(), []string{"base.go"}) {
		t.Fatalf("files = %v, want [base.go]", got.allFiles())
	}
}

func TestTouchedBacksOffWhenSlow(t *testing.T) {
	dir := initTouchedRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var intervals []time.Duration
	w := newTouchedWatcher(dir, func(runnerwire.Touched) {}, touchedOptions{
		pollInterval:  10 * time.Millisecond,
		slowThreshold: 5 * time.Millisecond,
		maxInterval:   40 * time.Millisecond,
		runStatus: func(context.Context, string) (string, error) {
			time.Sleep(8 * time.Millisecond) // exceeds slowThreshold (5ms)
			return "", nil
		},
		onInterval: func(next time.Duration) {
			mu.Lock()
			intervals = append(intervals, next)
			mu.Unlock()
		},
	})
	w.start(ctx)
	eventually(t, "three backed-off intervals", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(intervals) >= 3
	})
	w.stop()

	mu.Lock()
	got := slices.Clone(intervals[:3])
	mu.Unlock()
	want := []time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 40 * time.Millisecond}
	if !slices.Equal(got, want) {
		t.Fatalf("backoff intervals = %v, want %v", got, want)
	}
}

// An interactive run's touched files come from polling git status, and only
// from it: tool events (a Claude transcript tail can produce some) neither
// report an edit tool's path nor schedule an early poll, as they do for a
// headless run.
func TestTouchedRunsForInteractiveByPollOnly(t *testing.T) {
	dir := initTouchedRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var got collectedTouched
	w := newTouchedWatcher(dir, got.send, touchedOptions{
		pollInterval: 20 * time.Millisecond,
		interactive:  true,
	})

	// Before the watcher starts, so nothing drains the debounce channel and
	// no poll runs: whatever these leave behind came from the event path.
	// tool_event.go is not on disk, so no poll would report it.
	w.onEvent(runnerwire.RunEvent{
		Type:  runnerwire.EventToolCall,
		Tool:  "Edit",
		Input: `{"file_path":"tool_event.go"}`,
	})
	w.onEvent(runnerwire.RunEvent{Type: runnerwire.EventToolResult, Tool: "Edit"})
	if msgs := got.all(); len(msgs) != 0 {
		t.Fatalf("an edit tool's event sent touched files for an interactive run: %+v", msgs)
	}
	if n := len(w.debounceCh); n != 0 {
		t.Fatalf("a tool_result scheduled a debounced poll for an interactive run (%d queued)", n)
	}

	w.start(ctx)
	defer w.stop()

	// Modify a file without any tool_call / tool_result events (interactive runs emit none).
	writeTestFile(t, filepath.Join(dir, "interactive_change.go"), "package base\n")

	eventually(t, "interactive poll picks up file", func() bool {
		return slices.Contains(got.allFiles(), "interactive_change.go")
	})
	if files := got.allFiles(); slices.Contains(files, "tool_event.go") {
		t.Fatalf("touched = %v: tool_event.go came from a tool event, not the poll", files)
	}
}

// The watcher's git status and diff --numstat run with the repository's
// filter drivers emptied, as every other read of a worktree the agent could
// have configured does: a clean filter the agent planted never runs as the
// owner.
func TestTouchedDoesNotRunFilterDrivers(t *testing.T) {
	dir := initTouchedRepo(t)
	marker := filepath.Join(t.TempDir(), "marker")
	writeTestFile(t, filepath.Join(dir, ".gitattributes"), "*.go filter=evil\n")
	gitT(t, dir, "config", "filter.evil.clean", "echo x > '"+filepath.ToSlash(marker)+"'; cat")
	// A modified tracked file, and one only stat-dirty, so git has to read
	// (and would clean) both.
	writeTestFile(t, filepath.Join(dir, "base.go"), "package base\n\nfunc Hello() string { return \"bye\" }\n")
	stamp := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "oldname.go"), stamp, stamp); err != nil {
		t.Fatal(err)
	}

	// Control: plain git status runs the filter, so the test proves something.
	if out, err := exec.Command("git", "-C", dir, "status", "--porcelain").CombinedOutput(); err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Skip("this git did not run the clean filter on status; nothing to prove")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "oldname.go"), stamp.Add(time.Hour), stamp.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	var got collectedTouched
	w := newTouchedWatcher(dir, got.send, touchedOptions{})
	w.poll(context.Background())
	if !slices.Contains(got.allFiles(), "base.go") {
		t.Fatalf("touched = %v, want base.go", got.allFiles())
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the touched-files watcher ran the repository's clean filter")
	}
}

func TestCapabilitiesAdvertiseTouched(t *testing.T) {
	if !slices.Contains(capabilities(nil), runnerwire.CapTouched) {
		t.Fatalf("capabilities(nil) = %v, want %q", capabilities(nil), runnerwire.CapTouched)
	}
	off := false
	cfg := &Config{Interactive: &off, GlobalRuns: &off}
	if !slices.Contains(capabilities(cfg), runnerwire.CapTouched) {
		t.Fatalf("capabilities(off cfg) = %v, want %q", capabilities(cfg), runnerwire.CapTouched)
	}
}
