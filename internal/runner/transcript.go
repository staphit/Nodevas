package runner

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"nodevas/internal/redact"
	"nodevas/internal/runner/adapter"
	"nodevas/internal/runnerwire"
)

const (
	// transcriptPollInterval is how often the Claude interactive transcript
	// tailer checks for the session's .jsonl file and newly appended lines.
	transcriptPollInterval = 250 * time.Millisecond
	// transcriptReadChunk sizes each read from the tailed transcript file.
	transcriptReadChunk = 32 << 10
)

type transcriptOptions struct {
	projectsDir  string
	pollInterval time.Duration
	// expectedUID overrides os.Geteuid in tests so refusal of foreign-owned
	// files can be tested without root privileges.
	expectedUID func() int
}

// transcriptTail tails a Claude interactive session's JSONL transcript
// (~/.claude/projects/*/<sessionID>.jsonl) read-only through Claude's
// stream-json parser into emit, tracking the last non-empty assistant text for
// the end-of-run summary (R3, §14, §16.6).
type transcriptTail struct {
	sessionID    string
	projectsDir  string
	pollInterval time.Duration
	expectedUID  func() int
	emit         func(runnerwire.RunEvent)
	parser       adapter.Parser
	lines        lineWriter

	mu       sync.Mutex
	lastText string
	started  bool

	file     *os.File
	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
}

// startTranscriptTail starts tailing Claude Code's session transcript into
// j.emit for an interactive Claude run with a runner-chosen session ID.
// Returns nil for other agents or invalid session IDs; nil-safe methods keep
// the call site in interactive.go a no-op in those cases.
func (j *jobs) startTranscriptTail(ctx context.Context, r *run, sessionID string) *transcriptTail {
	if r == nil || r.job.Agent != "claude" || !validTranscriptSessionID(sessionID) {
		return nil
	}
	dir, err := claudeProjectsDir()
	if err != nil {
		return nil
	}
	t := newTranscriptTail(sessionID, func(ev runnerwire.RunEvent) {
		j.emit(r, ev)
	}, transcriptOptions{projectsDir: dir})
	t.start(ctx)
	return t
}

func newTranscriptTail(sessionID string, emit func(runnerwire.RunEvent), opts transcriptOptions) *transcriptTail {
	poll := opts.pollInterval
	if poll <= 0 {
		poll = transcriptPollInterval
	}
	expectedUID := opts.expectedUID
	if expectedUID == nil {
		expectedUID = os.Geteuid
	}
	t := &transcriptTail{
		sessionID:    sessionID,
		projectsDir:  opts.projectsDir,
		pollInterval: poll,
		expectedUID:  expectedUID,
		emit:         emit,
		parser:       (adapter.Claude{}).NewParser(),
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
	t.lines = lineWriter{fn: t.handleLine}
	return t
}

func (t *transcriptTail) start(ctx context.Context) {
	if t == nil || !validTranscriptSessionID(t.sessionID) || t.projectsDir == "" {
		return
	}
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return
	}
	t.started = true
	t.mu.Unlock()

	go t.run(ctx)
}

// stop drains any remaining transcript bytes, flushes a final unterminated
// line, closes the file, and waits for the tailer goroutine to exit. It is
// nil-safe and idempotent.
func (t *transcriptTail) stop() {
	if t == nil {
		return
	}
	t.mu.Lock()
	started := t.started
	t.mu.Unlock()
	if !started {
		return
	}
	t.stopOnce.Do(func() { close(t.stopCh) })
	<-t.doneCh
}

// summary returns the last non-empty assistant text seen in the transcript
// (redacted and truncated to MaxSummaryBytes), or fallback if none was seen.
func (t *transcriptTail) summary(fallback string) string {
	if t == nil {
		return fallback
	}
	t.mu.Lock()
	text := t.lastText
	t.mu.Unlock()
	if text == "" {
		return fallback
	}
	text = strings.TrimSpace(redact.Output(text))
	if text == "" {
		return fallback
	}
	return adapter.Truncate(text, runnerwire.MaxSummaryBytes)
}

func (t *transcriptTail) run(ctx context.Context) {
	defer close(t.doneCh)
	defer func() {
		if t.file != nil {
			_ = t.file.Close()
			t.file = nil
		}
	}()

	buf := make([]byte, transcriptReadChunk)
	t.drain(buf)
	ticker := time.NewTicker(t.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-t.stopCh:
			t.drain(buf)
			t.lines.flush()
			return
		case <-ctx.Done():
			t.drain(buf)
			t.lines.flush()
			return
		case <-ticker.C:
			t.drain(buf)
		}
	}
}

func (t *transcriptTail) drain(buf []byte) {
	if t.file == nil {
		f, err := t.openTranscriptFile()
		if err != nil {
			return
		}
		t.file = f
	}
	for {
		n, err := t.file.Read(buf)
		if n > 0 {
			_, _ = t.lines.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func (t *transcriptTail) handleLine(line []byte) {
	for _, ev := range t.parser.Parse(line) {
		if ev.Type == runnerwire.EventText {
			if trimmed := strings.TrimSpace(ev.Text); trimmed != "" {
				t.mu.Lock()
				t.lastText = trimmed
				t.mu.Unlock()
			}
		}
		if t.emit != nil {
			t.emit(ev)
		}
	}
}

// openTranscriptFile looks for <projectsDir>/<project>/<sessionID>.jsonl,
// refusing symlinks at any level under projectsDir, non-regular files, and
// files not owned by the runner user (§14).
func (t *transcriptTail) openTranscriptFile() (*os.File, error) {
	if !validTranscriptSessionID(t.sessionID) || t.projectsDir == "" {
		return nil, fs.ErrInvalid
	}
	pinfo, err := os.Lstat(t.projectsDir)
	if err != nil {
		return nil, err
	}
	if !pinfo.IsDir() || pinfo.Mode()&os.ModeSymlink != 0 || !t.ownedByRunner(pinfo) {
		return nil, fs.ErrPermission
	}
	root, err := os.OpenRoot(t.projectsDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	entries, err := os.ReadDir(t.projectsDir)
	if err != nil {
		return nil, err
	}
	name := t.sessionID + ".jsonl"
	for _, ent := range entries {
		if ent.Type()&os.ModeSymlink != 0 || !ent.IsDir() {
			continue
		}
		dirInfo, err := root.Lstat(ent.Name())
		if err != nil || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 || !t.ownedByRunner(dirInfo) {
			continue
		}
		rel := filepath.Join(ent.Name(), name)
		linfo, err := root.Lstat(rel)
		if err != nil || !linfo.Mode().IsRegular() || !t.ownedByRunner(linfo) {
			continue
		}
		f, err := root.Open(rel)
		if err != nil {
			continue
		}
		finfo, err := f.Stat()
		if err != nil || !finfo.Mode().IsRegular() || !os.SameFile(linfo, finfo) || !t.ownedByRunner(finfo) {
			_ = f.Close()
			continue
		}
		return f, nil
	}
	return nil, fs.ErrNotExist
}

func (t *transcriptTail) ownedByRunner(info os.FileInfo) bool {
	want := t.expectedUID()
	if want < 0 {
		// Windows: os.Geteuid returns -1.
		return true
	}
	uid, ok := fileUID(info)
	return ok && uid == want
}

// fileUID reads the numeric owner UID from info.Sys() without directly naming
// syscall.Stat_t, keeping this file buildable across Unix and Windows.
func fileUID(info os.FileInfo) (int, bool) {
	if info == nil || info.Sys() == nil {
		return 0, false
	}
	v := reflect.ValueOf(info.Sys())
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return 0, false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return 0, false
	}
	f := v.FieldByName("Uid")
	if !f.IsValid() {
		return 0, false
	}
	switch f.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int(f.Uint()), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int(f.Int()), true
	default:
		return 0, false
	}
}

func validTranscriptSessionID(id string) bool {
	if id == "" || !runnerwire.ValidSessionID(id) {
		return false
	}
	if strings.Contains(id, "..") || strings.ContainsAny(id, `/\`) {
		return false
	}
	return filepath.Base(id+".jsonl") == id+".jsonl"
}

// claudeProjectsDir returns Claude Code's projects transcript directory
// (~/.claude/projects or $CLAUDE_CONFIG_DIR/projects).
func claudeProjectsDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return filepath.Join(dir, "projects"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}
