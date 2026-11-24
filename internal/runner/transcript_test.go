package runner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

// claudeTranscriptFixture returns hand-written JSONL lines in the shape of a
// Claude Code interactive session transcript (record types, field names and
// content blocks as the transcript uses them). It is not a recording of a
// real session.
func claudeTranscriptFixture(sessionID string) []string {
	return []string{
		`{"type":"last-prompt","lastPrompt":"Check status and update greeting","sessionId":"` + sessionID + `"}`,
		`{"type":"mode","mode":"default","sessionId":"` + sessionID + `"}`,
		`{"type":"permission-mode","permissionMode":"acceptEdits","sessionId":"` + sessionID + `"}`,
		`{"type":"session-owner","owner":"local","sessionId":"` + sessionID + `"}`,
		`{"type":"file-history-snapshot","messageId":"msg-snap-1","snapshot":{}}`,
		`{"type":"user","isSidechain":false,"sessionId":"` + sessionID + `","uuid":"u-1","parentUuid":null,"message":{"role":"user","content":"Check status and update greeting"}}`,
		`{"type":"assistant","isSidechain":false,"sessionId":"` + sessionID + `","session_id":"` + sessionID + `","uuid":"a-1","parentUuid":"u-1","message":{"role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"thinking","thinking":"Inspecting the repository before making changes."}]}}`,
		`{"type":"assistant","isSidechain":false,"sessionId":"` + sessionID + `","session_id":"` + sessionID + `","uuid":"a-2","parentUuid":"a-1","message":{"role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"I will inspect the repository status first."}]}}`,
		`{"type":"assistant","isSidechain":false,"sessionId":"` + sessionID + `","session_id":"` + sessionID + `","uuid":"a-3","parentUuid":"a-2","message":{"role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"tool_use","id":"toolu_01BashCheck","name":"Bash","input":{"command":"git status -s","description":"Check working tree status"},"caller":{"type":"direct"}}]}}`,
		`{"type":"attachment","isSidechain":false,"sessionId":"` + sessionID + `","session_id":"` + sessionID + `","uuid":"att-1","parentUuid":"a-3"}`,
		`{"type":"user","isSidechain":false,"sessionId":"` + sessionID + `","session_id":"` + sessionID + `","uuid":"u-2","parentUuid":"a-3","toolUseResult":{"stdout":"M hello.txt"},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01BashCheck","is_error":false,"content":"M hello.txt"}]}}`,
		`{"type":"assistant","isSidechain":false,"sessionId":"` + sessionID + `","session_id":"` + sessionID + `","uuid":"a-4","parentUuid":"u-2","message":{"role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"tool_use","id":"toolu_02ReadFail","name":"Read","input":{"file_path":"missing.txt"}}]}}`,
		`{"type":"user","isSidechain":false,"sessionId":"` + sessionID + `","session_id":"` + sessionID + `","uuid":"u-3","parentUuid":"a-4","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_02ReadFail","is_error":true,"content":[{"type":"text","text":"File does not exist."}]}]}}`,
		`{"type":"assistant","isSidechain":false,"sessionId":"` + sessionID + `","session_id":"` + sessionID + `","uuid":"a-5","parentUuid":"u-3","message":{"role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"Done: checked status and confirmed hello.txt is updated."}]}}`,
		`{"type":"system","subtype":"turn_duration","durationMs":1420,"sessionId":"` + sessionID + `"}`,
		`{"type":"cost-state","sessionId":"` + sessionID + `"}`,
	}
}

func appendFile(t *testing.T, path, data string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(data); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTailEmitsTextAndToolLines(t *testing.T) {
	projectsDir := filepath.Join(t.TempDir(), "projects")
	projDir := filepath.Join(projectsDir, "-private-tmp-nodevas-smoke-wt2")
	if err := os.MkdirAll(projDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sessionID := newUUIDv4()
	lines := claudeTranscriptFixture(sessionID)

	var mu sync.Mutex
	var events []runnerwire.RunEvent
	emit := func(ev runnerwire.RunEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}
	snapshot := func() []runnerwire.RunEvent {
		mu.Lock()
		defer mu.Unlock()
		out := make([]runnerwire.RunEvent, len(events))
		copy(out, events)
		return out
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start tailing before the transcript file exists (Claude interactive creates
	// the file on the first turn, not at startup).
	tail := newTranscriptTail(sessionID, emit, transcriptOptions{
		projectsDir:  projectsDir,
		pollInterval: 10 * time.Millisecond,
	})
	tail.start(ctx)
	defer tail.stop()

	transcriptPath := filepath.Join(projDir, sessionID+".jsonl")
	// Write the first part of the transcript plus a partial JSON line split across writes.
	part1 := strings.Join(lines[:9], "\n") + "\n"
	splitLine := lines[10]
	half := len(splitLine) / 2
	appendFile(t, transcriptPath, part1+lines[9]+"\n"+splitLine[:half])

	// Wait for the first 3 structured events (thinking, text, tool_call).
	eventually(t, "initial transcript events", func() bool {
		return len(snapshot()) == 3
	})

	// Complete the split line and append the remaining lines.
	appendFile(t, transcriptPath, splitLine[half:]+"\n"+strings.Join(lines[11:], "\n")+"\n")

	eventually(t, "all transcript events", func() bool {
		return len(snapshot()) == 7
	})
	tail.stop()

	got := snapshot()
	if len(got) != 7 {
		t.Fatalf("got %d events, want 7: %+v", len(got), got)
	}
	if got[0].Type != runnerwire.EventThinking || !strings.Contains(got[0].Text, "Inspecting the repository") {
		t.Errorf("event[0] = %+v, want thinking", got[0])
	}
	if got[1].Type != runnerwire.EventText || got[1].Text != "I will inspect the repository status first." {
		t.Errorf("event[1] = %+v, want first text", got[1])
	}
	if got[2].Type != runnerwire.EventToolCall || got[2].Tool != "Bash" || got[2].ToolID != "toolu_01BashCheck" || !strings.Contains(got[2].Input, "git status -s") {
		t.Errorf("event[2] = %+v, want Bash tool_call", got[2])
	}
	if got[3].Type != runnerwire.EventToolResult || got[3].ToolID != "toolu_01BashCheck" || got[3].IsError || got[3].Text != "M hello.txt" {
		t.Errorf("event[3] = %+v, want Bash tool_result", got[3])
	}
	if got[4].Type != runnerwire.EventToolCall || got[4].Tool != "Read" || got[4].ToolID != "toolu_02ReadFail" {
		t.Errorf("event[4] = %+v, want Read tool_call", got[4])
	}
	if got[5].Type != runnerwire.EventToolResult || got[5].ToolID != "toolu_02ReadFail" || !got[5].IsError || got[5].Text != "File does not exist." {
		t.Errorf("event[5] = %+v, want error Read tool_result", got[5])
	}
	if got[6].Type != runnerwire.EventText || got[6].Text != "Done: checked status and confirmed hello.txt is updated." {
		t.Errorf("event[6] = %+v, want final text", got[6])
	}
}

func TestTailRefusesSymlinkOrForeignFile(t *testing.T) {
	sessionID := newUUIDv4()
	payload := `{"type":"assistant","sessionId":"` + sessionID + `","message":{"role":"assistant","content":[{"type":"text","text":"should not be read"}]}}` + "\n"

	runCase := func(t *testing.T, projectsDir string, opts transcriptOptions) {
		t.Helper()
		var mu sync.Mutex
		var events []runnerwire.RunEvent
		opts.projectsDir = projectsDir
		opts.pollInterval = 10 * time.Millisecond
		tail := newTranscriptTail(sessionID, func(ev runnerwire.RunEvent) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		}, opts)
		tail.start(context.Background())
		time.Sleep(45 * time.Millisecond)
		tail.stop()

		mu.Lock()
		defer mu.Unlock()
		if len(events) != 0 {
			t.Fatalf("expected 0 events, got %d: %+v", len(events), events)
		}
	}

	t.Run("symlink transcript file", func(t *testing.T) {
		base := t.TempDir()
		projectsDir := filepath.Join(base, "projects")
		projDir := filepath.Join(projectsDir, "-proj")
		if err := os.MkdirAll(projDir, 0o700); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(base, "secret.jsonl")
		if err := os.WriteFile(outside, []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(projDir, sessionID+".jsonl")); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		runCase(t, projectsDir, transcriptOptions{})
	})

	t.Run("symlink project directory", func(t *testing.T) {
		base := t.TempDir()
		projectsDir := filepath.Join(base, "projects")
		if err := os.MkdirAll(projectsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		outsideDir := filepath.Join(base, "outside-proj")
		if err := os.MkdirAll(outsideDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(outsideDir, sessionID+".jsonl"), []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outsideDir, filepath.Join(projectsDir, "-linked")); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		runCase(t, projectsDir, transcriptOptions{})
	})

	t.Run("symlink projects directory", func(t *testing.T) {
		base := t.TempDir()
		realProjects := filepath.Join(base, "real-projects")
		projDir := filepath.Join(realProjects, "-proj")
		if err := os.MkdirAll(projDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(projDir, sessionID+".jsonl"), []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
		linkProjects := filepath.Join(base, "projects")
		if err := os.Symlink(realProjects, linkProjects); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		runCase(t, linkProjects, transcriptOptions{})
	})

	t.Run("foreign owner uid", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("unix uid ownership check")
		}
		projectsDir := filepath.Join(t.TempDir(), "projects")
		projDir := filepath.Join(projectsDir, "-proj")
		if err := os.MkdirAll(projDir, 0o700); err != nil {
			t.Fatal(err)
		}
		filePath := filepath.Join(projDir, sessionID+".jsonl")
		if err := os.WriteFile(filePath, []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(filePath)
		if err != nil {
			t.Fatal(err)
		}
		uid, ok := fileUID(info)
		if !ok || uid != os.Geteuid() {
			t.Fatalf("fileUID = (%d, %v), want (%d, true)", uid, ok, os.Geteuid())
		}
		runCase(t, projectsDir, transcriptOptions{
			expectedUID: func() int { return os.Geteuid() + 1 },
		})
	})

	t.Run("foreign session id and wrong depth", func(t *testing.T) {
		projectsDir := filepath.Join(t.TempDir(), "projects")
		projDir := filepath.Join(projectsDir, "-proj")
		nestedDir := filepath.Join(projDir, "sub")
		if err := os.MkdirAll(nestedDir, 0o700); err != nil {
			t.Fatal(err)
		}
		// Another session's file, a file directly in projectsDir, and a nested file.
		otherID := newUUIDv4()
		for _, p := range []string{
			filepath.Join(projDir, otherID+".jsonl"),
			filepath.Join(projectsDir, sessionID+".jsonl"),
			filepath.Join(nestedDir, sessionID+".jsonl"),
		} {
			if err := os.WriteFile(p, []byte(payload), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		runCase(t, projectsDir, transcriptOptions{})
	})

	t.Run("non-regular directory at transcript path", func(t *testing.T) {
		projectsDir := filepath.Join(t.TempDir(), "projects")
		dirAsFile := filepath.Join(projectsDir, "-proj", sessionID+".jsonl")
		if err := os.MkdirAll(dirAsFile, 0o700); err != nil {
			t.Fatal(err)
		}
		runCase(t, projectsDir, transcriptOptions{})
	})
}

func TestClaudeProjectDirName(t *testing.T) {
	for path, want := range map[string]string{
		"/private/tmp/nodevas-smoke-wt2":     "-private-tmp-nodevas-smoke-wt2",
		"/Users/a.b/work_tree/run-1":         "-Users-a-b-work-tree-run-1",
		`C:\Users\me\worktrees\run-1`:        "C--Users-me-worktrees-run-1",
		"/home/me/文件/x":                      "-home-me----x",
		"/" + strings.Repeat("a", 250):       "",
		"":                                   "",
		"/tmp/emoji-\U0001F600/run":          "-tmp-emoji----run",
		"/var/folders/xy/T/nodevas/wt/run-9": "-var-folders-xy-T-nodevas-wt-run-9",
	} {
		if got := claudeProjectDirName(path); got != want {
			t.Errorf("claudeProjectDirName(%q) = %q, want %q", path, got, want)
		}
	}
}

// The tail looks for the transcript in the project folder named after the
// worktree each poll, and through every project folder only with a backoff:
// a busy ~/.claude/projects is not read four times a second for 20 minutes.
func TestTailLooksInTheWorktreesProjectFolder(t *testing.T) {
	base := t.TempDir()
	projectsDir := filepath.Join(base, "projects")
	for i := range 50 {
		if err := os.MkdirAll(filepath.Join(projectsDir, "-other-"+strings.Repeat("x", i)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	workdir := filepath.Join(base, "worktrees", "run-tail")
	if err := os.MkdirAll(workdir, 0o700); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(workdir)
	if err != nil {
		t.Fatal(err)
	}
	projDir := filepath.Join(projectsDir, claudeProjectDirName(resolved))
	if err := os.MkdirAll(projDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sessionID := newUUIDv4()

	var mu sync.Mutex
	var texts []string
	tail := newTranscriptTail(sessionID, func(ev runnerwire.RunEvent) {
		mu.Lock()
		texts = append(texts, ev.Text)
		mu.Unlock()
	}, transcriptOptions{projectsDir: projectsDir, workdir: workdir, pollInterval: 5 * time.Millisecond})
	tail.start(context.Background())
	defer tail.stop()

	// No transcript yet: 300ms of 5ms polls is 60 looks in the worktree's
	// folder, but only a handful through all of them.
	time.Sleep(300 * time.Millisecond)
	tail.mu.Lock()
	scans := tail.scans
	tail.mu.Unlock()
	if scans > 8 {
		t.Fatalf("scanned every project folder %d times in 300ms of 5ms polls; want a backoff", scans)
	}

	// Once it is there, the next poll finds it, without waiting for a scan.
	appendFile(t, filepath.Join(projDir, sessionID+".jsonl"),
		`{"type":"assistant","sessionId":"`+sessionID+`","message":{"role":"assistant","content":[{"type":"text","text":"found it"}]}}`+"\n")
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		found := slices.Contains(texts, "found it")
		mu.Unlock()
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the transcript in the worktree's project folder was not found within a second")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTailStopsAtEnd(t *testing.T) {
	projectsDir := filepath.Join(t.TempDir(), "projects")
	projDir := filepath.Join(projectsDir, "-proj")
	if err := os.MkdirAll(projDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sessionID := newUUIDv4()
	filePath := filepath.Join(projDir, sessionID+".jsonl")

	var mu sync.Mutex
	var events []runnerwire.RunEvent
	emit := func(ev runnerwire.RunEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(events)
	}

	tail := newTranscriptTail(sessionID, emit, transcriptOptions{
		projectsDir:  projectsDir,
		pollInterval: 10 * time.Millisecond,
	})
	tail.start(context.Background())

	appendFile(t, filePath, `{"type":"assistant","sessionId":"`+sessionID+`","message":{"role":"assistant","content":[{"type":"text","text":"before end"}]}}`+"\n")
	eventually(t, "first event emitted", func() bool { return count() == 1 })

	// Write a final line without a trailing newline right before stop; stop must
	// flush it and then exit its goroutine.
	appendFile(t, filePath, `{"type":"assistant","sessionId":"`+sessionID+`","message":{"role":"assistant","content":[{"type":"text","text":"at end"}]}}`)
	tail.stop()

	select {
	case <-tail.doneCh:
	default:
		t.Fatal("tail goroutine still running after stop returned")
	}
	if n := count(); n != 2 {
		t.Fatalf("events after stop = %d, want 2", n)
	}

	// Appending after stop must produce no further events.
	appendFile(t, filePath, "\n"+`{"type":"assistant","sessionId":"`+sessionID+`","message":{"role":"assistant","content":[{"type":"text","text":"after end"}]}}`+"\n")
	time.Sleep(50 * time.Millisecond)
	if n := count(); n != 2 {
		t.Fatalf("events after post-stop append = %d, want 2", n)
	}
	// Stop must be idempotent.
	tail.stop()
}

func TestSummaryUsesLastText(t *testing.T) {
	claudeDir := t.TempDir()
	projDir := filepath.Join(claudeDir, "projects", "-worktree")
	if err := os.MkdirAll(projDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)

	env, started := startInteractiveRunner(t, nil)
	env.hub.hello()
	env.hub.offer(interactiveJob("run-claude-summary"))
	st := waitStarted(t, started)
	env.hub.wait("run-claude-summary", runnerwire.TypeAccept)

	env.client.jobs.mu.Lock()
	sessionID := env.client.jobs.runs["run-claude-summary"].session
	env.client.jobs.mu.Unlock()
	if sessionID == "" {
		t.Fatal("interactive claude run has empty session id")
	}

	// Write a transcript with multiple assistant messages, a tool call, and a
	// secret token to verify both last-text summary selection and redaction.
	secret := "ghp_0123456789abcdef0123456789abcdef0123"
	transcriptPath := filepath.Join(projDir, sessionID+".jsonl")
	appendFile(t, transcriptPath, strings.Join([]string{
		`{"type":"assistant","sessionId":"` + sessionID + `","message":{"role":"assistant","content":[{"type":"text","text":"First step: reading files."}]}}`,
		`{"type":"assistant","sessionId":"` + sessionID + `","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_sum_1","name":"Read","input":{"file_path":"README.md"}}]}}`,
		`{"type":"user","sessionId":"` + sessionID + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_sum_1","is_error":false,"content":"hello"}]}}`,
		`{"type":"assistant","sessionId":"` + sessionID + `","message":{"role":"assistant","content":[{"type":"text","text":"Updated README.md and verified token ` + secret + ` is masked."}]}}`,
	}, "\n")+"\n")

	eventually(t, "transcript events relayed to hub", func() bool {
		return hubHasEvent(env, "run-claude-summary", "Updated README.md")
	})

	st.term.emit("screen noise that should not replace the assistant summary\r\n")
	st.term.exit <- 0
	finish := env.hub.wait("run-claude-summary", runnerwire.TypeFinish)
	if finish.Result.Status != runnerwire.RunSucceeded {
		t.Fatalf("finish = %+v", finish.Result)
	}
	if !strings.Contains(finish.Result.Summary, "Updated README.md") {
		t.Fatalf("summary = %q, want last assistant text", finish.Result.Summary)
	}
	if strings.Contains(finish.Result.Summary, "First step") || strings.Contains(finish.Result.Summary, "Interactive session ended") {
		t.Fatalf("summary = %q, should use only last assistant text", finish.Result.Summary)
	}
	if strings.Contains(finish.Result.Summary, secret) {
		t.Fatalf("summary leaked secret: %q", finish.Result.Summary)
	}
	if hubHasEvent(env, "run-claude-summary", secret) {
		t.Fatal("hub events leaked unredacted secret")
	}

	// Fallback: when an interactive Claude session exits without any transcript
	// assistant text, the summary still falls back to the terminal screen tail.
	env.hub.offer(interactiveJob("run-claude-no-transcript"))
	st2 := waitStarted(t, started)
	env.hub.wait("run-claude-no-transcript", runnerwire.TypeAccept)
	st2.term.emit("Last terminal line\r\n")
	eventually(t, "term_out received", func() bool {
		out, _ := termOutput(env, "run-claude-no-transcript")
		return strings.Contains(out, "Last terminal line")
	})
	st2.term.exit <- 0
	finish2 := env.hub.wait("run-claude-no-transcript", runnerwire.TypeFinish)
	if !strings.Contains(finish2.Result.Summary, "Interactive session ended") || !strings.Contains(finish2.Result.Summary, "Last terminal line") {
		t.Fatalf("fallback summary = %q, want terminal tail summary", finish2.Result.Summary)
	}
}
