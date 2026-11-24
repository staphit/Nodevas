package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

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
	// transcriptScanMax caps the backoff between looks through every project
	// folder, for a transcript that is not where the worktree's own folder
	// name says (see claudeProjectDirName).
	transcriptScanMax = 15 * time.Second
	// claudeProjectNameMax is the longest folder name Claude Code uses as is;
	// a longer one it shortens and suffixes with a hash this runner does not
	// reproduce, so such a transcript is found by the scan.
	claudeProjectNameMax = 200
)

type transcriptOptions struct {
	projectsDir string
	// workdir is the folder the CLI runs in: its transcript is first looked
	// for in the project folder Claude Code names after it.
	workdir      string
	pollInterval time.Duration
	// expectedUID overrides os.Geteuid in tests so refusal of foreign-owned
	// files can be tested without root privileges.
	expectedUID func() int
	// resume: the CLI resumes an earlier session, so the transcript may
	// already hold earlier turns (see newTranscriptTail).
	resume bool
	// logf writes to the runner's log; onSession is told the id of a new
	// session the resumed CLI wrote instead of the old one. Either may be nil.
	logf      func(format string, args ...any)
	onSession func(id string)
}

// transcriptTail tails a Claude interactive session's JSONL transcript
// (~/.claude/projects/*/<sessionID>.jsonl) read-only through Claude's
// stream-json parser into emit, tracking the last non-empty assistant text for
// the end-of-run summary (R3, §14, §16.6).
//
// Until the file exists it is looked for each poll only in the project
// folders named after the worktree (projectDirs); a look through every
// project folder, which can be thousands on a busy machine, backs off from
// the poll interval to transcriptScanMax.
//
// A resumed session (transcriptOptions.resume) goes on writing the earlier
// turns' file, so the tail starts where that file ended before the CLI
// started: earlier turns are neither sent again nor taken as this turn's
// summary. A CLI that writes a new session instead leaves that file as it
// was; the tail then follows the newest transcript that appeared in the
// same folders since the start, skipping lines stamped before it (the
// history such a session starts with), and reports the new id.
type transcriptTail struct {
	sessionID    string
	projectsDir  string
	projectDirs  []string
	pollInterval time.Duration
	expectedUID  func() int
	emit         func(runnerwire.RunEvent)
	parser       adapter.Parser
	lines        lineWriter

	mu       sync.Mutex
	lastText string
	started  bool
	// idle: stopped before it started, so no goroutine runs.
	idle bool
	// handled counts the lines handed to the parser this turn.
	handled int

	file *os.File

	// A resumed session (see the type's comment); the tail goroutine's own
	// once it starts.
	resume    bool
	logf      func(format string, args ...any)
	onSession func(id string)
	startedAt time.Time
	// watchDirs are the folders a new session's transcript is looked for
	// in; known is the transcripts already there at the start (dir/name).
	watchDirs []string
	known     map[string]bool
	// grew: bytes came from the followed file after the start. switched:
	// following a new session's file, whose lines stamped before
	// skipBefore are history. skipPartial: the resumed file ended without
	// a newline, so the first line read is the rest of an old one.
	grew        bool
	switched    bool
	skipBefore  time.Time
	skipPartial bool
	// nextScan and scanGap pace the full scans; scans counts them (tests).
	nextScan time.Time
	scanGap  time.Duration
	scans    int

	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
}

// prepareTranscriptTail makes the tail of Claude Code's session transcript
// into j.emit for an interactive Claude run, before the CLI starts (resume:
// it resumes sessionID, whose transcript is followed from where it ends
// now); start it once the CLI runs. A new session the resumed CLI writes
// instead becomes the run's session. Returns nil for other agents or
// invalid session IDs; nil-safe methods keep the call site in
// interactive.go a no-op in those cases.
func (j *jobs) prepareTranscriptTail(r *run, sessionID, workdir string, resume bool) *transcriptTail {
	if r == nil || r.job.Agent != "claude" || !validTranscriptSessionID(sessionID) {
		return nil
	}
	dir, err := claudeProjectsDir()
	if err != nil {
		return nil
	}
	runID := r.job.RunID
	t := newTranscriptTail(sessionID, func(ev runnerwire.RunEvent) {
		j.emit(r, ev)
	}, transcriptOptions{
		projectsDir: dir,
		workdir:     workdir,
		resume:      resume,
		logf: func(format string, args ...any) {
			j.c.logf("run %s: "+format, append([]any{runID}, args...)...)
		},
		onSession: func(id string) {
			if !runnerwire.ValidSessionID(id) {
				return
			}
			j.mu.Lock()
			r.session = id
			j.mu.Unlock()
		},
	})
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
		projectDirs:  claudeProjectDirs(opts.workdir),
		pollInterval: poll,
		scanGap:      poll,
		expectedUID:  expectedUID,
		emit:         emit,
		parser:       (adapter.Claude{}).NewParser(),
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
		logf:         opts.logf,
		onSession:    opts.onSession,
	}
	t.lines = lineWriter{fn: t.handleLine}
	if opts.resume {
		t.markResume()
	}
	return t
}

// markResume records, before a resumed CLI starts, where its transcript
// ends and which transcripts its folders already hold.
func (t *transcriptTail) markResume() {
	t.resume = true
	t.startedAt = time.Now()
	t.watchDirs = slices.Clone(t.projectDirs)
	if !validTranscriptSessionID(t.sessionID) || t.projectsDir == "" {
		return
	}
	if f, dir, err := t.openTranscriptFile(); err == nil {
		if info, err := f.Stat(); err != nil {
			_ = f.Close()
		} else if _, err := f.Seek(info.Size(), io.SeekStart); err != nil {
			_ = f.Close()
		} else {
			t.file = f
			if size := info.Size(); size > 0 {
				var last [1]byte
				if _, err := f.ReadAt(last[:], size-1); err == nil && last[0] != '\n' {
					t.skipPartial = true
				}
			}
			if !slices.Contains(t.watchDirs, dir) {
				t.watchDirs = append(t.watchDirs, dir)
			}
		}
	}
	t.known = map[string]bool{}
	for _, dir := range t.watchDirs {
		for _, name := range t.sessionFiles(dir) {
			t.known[dir+"/"+name] = true
		}
	}
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
	if !t.started {
		// Never started (the CLI did not): it never will now; close the
		// file a resume opened.
		t.started, t.idle = true, true
		f := t.file
		t.file = nil
		t.mu.Unlock()
		if f != nil {
			_ = f.Close()
		}
		return
	}
	idle := t.idle
	t.mu.Unlock()
	if idle {
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

// sawLines reports whether this turn's transcript had any lines.
func (t *transcriptTail) sawLines() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.handled > 0
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
			t.end(buf)
			return
		case <-ctx.Done():
			t.end(buf)
			return
		case <-ticker.C:
			t.drain(buf)
		}
	}
}

// end reads the last of the transcript and, for a resumed session that
// wrote nothing the tail could find, says so rather than going quiet.
func (t *transcriptTail) end(buf []byte) {
	t.drain(buf)
	t.lines.flush()
	if t.resume && !t.grew {
		t.note(fmt.Sprintf("no transcript lines from resumed Claude session %s this turn; its work shows only in the terminal", t.sessionID))
	}
}

func (t *transcriptTail) drain(buf []byte) {
	if t.file == nil {
		if f, _, err := t.openTranscriptFile(); err == nil {
			t.file = f
		}
	}
	t.read(buf)
	// A resumed CLI that has written nothing to the old session may have
	// started a new one.
	if t.resume && !t.grew && !t.switched {
		t.followNewSession()
		t.read(buf)
	}
}

func (t *transcriptTail) read(buf []byte) {
	if t.file == nil {
		return
	}
	for {
		n, err := t.file.Read(buf)
		if n > 0 {
			t.grew = true
			_, _ = t.lines.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// followNewSession switches to the newest session transcript that appeared
// in the watched folders since a resumed CLI started.
func (t *transcriptTail) followNewSession() {
	f, id := t.newSessionFile()
	if f == nil {
		return
	}
	if t.file != nil {
		_ = t.file.Close()
	}
	t.file = f
	t.switched = true
	t.skipPartial = false
	t.skipBefore = t.startedAt.Truncate(time.Second)
	t.lines = lineWriter{fn: t.handleLine}
	t.note(fmt.Sprintf("resumed Claude session %s continues as session %s; following its transcript", t.sessionID, id))
	if t.onSession != nil {
		t.onSession(id)
	}
}

// note tells the runner's log and the run's events.
func (t *transcriptTail) note(text string) {
	if t.logf != nil {
		t.logf("%s", text)
	}
	if t.emit != nil {
		t.emit(runnerwire.RunEvent{Type: runnerwire.EventLog, Text: text})
	}
}

// historyLine reports whether a line of a new session's transcript is
// stamped before the resumed CLI started: history it was started with.
func (t *transcriptTail) historyLine(line []byte) bool {
	if t.skipBefore.IsZero() {
		return false
	}
	var rec struct {
		Timestamp string `json:"timestamp"`
	}
	if json.Unmarshal(line, &rec) != nil || rec.Timestamp == "" {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, rec.Timestamp)
	return err == nil && at.Before(t.skipBefore)
}

func (t *transcriptTail) handleLine(line []byte) {
	if t.skipPartial {
		t.skipPartial = false
		return
	}
	if t.historyLine(line) {
		return
	}
	t.mu.Lock()
	t.handled++
	t.mu.Unlock()
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
// It returns the project folder it was found in, too.
func (t *transcriptTail) openTranscriptFile() (*os.File, string, error) {
	if !validTranscriptSessionID(t.sessionID) {
		return nil, "", fs.ErrInvalid
	}
	root, err := t.openProjectsRoot()
	if err != nil {
		return nil, "", err
	}
	defer root.Close()

	for _, dir := range t.projectDirs {
		if f := t.openIn(root, dir, t.sessionID); f != nil {
			return f, dir, nil
		}
	}
	now := time.Now()
	if now.Before(t.nextScan) {
		return nil, "", fs.ErrNotExist
	}
	t.nextScan = now.Add(t.scanGap)
	t.scanGap = min(2*t.scanGap, transcriptScanMax)
	t.mu.Lock()
	t.scans++
	t.mu.Unlock()
	entries, err := os.ReadDir(t.projectsDir)
	if err != nil {
		return nil, "", err
	}
	for _, ent := range entries {
		if ent.Type()&os.ModeSymlink != 0 || !ent.IsDir() {
			continue
		}
		if f := t.openIn(root, ent.Name(), t.sessionID); f != nil {
			return f, ent.Name(), nil
		}
	}
	return nil, "", fs.ErrNotExist
}

// openProjectsRoot opens projectsDir when it is a real directory of the
// runner user's.
func (t *transcriptTail) openProjectsRoot() (*os.Root, error) {
	if t.projectsDir == "" {
		return nil, fs.ErrInvalid
	}
	pinfo, err := os.Lstat(t.projectsDir)
	if err != nil {
		return nil, err
	}
	if !pinfo.IsDir() || pinfo.Mode()&os.ModeSymlink != 0 || !t.ownedByRunner(pinfo) {
		return nil, fs.ErrPermission
	}
	return os.OpenRoot(t.projectsDir)
}

// sessionFiles lists the session transcripts (<id>.jsonl) in project
// folder dir.
func (t *transcriptTail) sessionFiles(dir string) []string {
	root, err := t.openProjectsRoot()
	if err != nil {
		return nil
	}
	defer root.Close()
	if info, err := root.Lstat(dir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(t.projectsDir, dir))
	if err != nil {
		return nil
	}
	var names []string
	for _, ent := range entries {
		id, ok := strings.CutSuffix(ent.Name(), ".jsonl")
		if ok && ent.Type().IsRegular() && validTranscriptSessionID(id) {
			names = append(names, ent.Name())
		}
	}
	return names
}

// newSessionFile opens the newest session transcript in the watched folders
// that was not there when the resumed CLI started and has been written
// since, and returns its session id; nil when there is none.
func (t *transcriptTail) newSessionFile() (*os.File, string) {
	var bestDir, bestID string
	var bestAt time.Time
	for _, dir := range t.watchDirs {
		for _, name := range t.sessionFiles(dir) {
			id := strings.TrimSuffix(name, ".jsonl")
			if id == t.sessionID || t.known[dir+"/"+name] {
				continue
			}
			info, err := os.Lstat(filepath.Join(t.projectsDir, dir, name))
			if err != nil || info.ModTime().Before(t.startedAt.Truncate(time.Second)) {
				continue
			}
			if bestID == "" || info.ModTime().After(bestAt) {
				bestDir, bestID, bestAt = dir, id, info.ModTime()
			}
		}
	}
	if bestID == "" {
		return nil, ""
	}
	root, err := t.openProjectsRoot()
	if err != nil {
		return nil, ""
	}
	defer root.Close()
	f := t.openIn(root, bestDir, bestID)
	if f == nil {
		return nil, ""
	}
	return f, bestID
}

// openIn opens <dir>/<id>.jsonl under root when dir is a real directory and
// the file a regular one, both the runner user's; nil otherwise.
func (t *transcriptTail) openIn(root *os.Root, dir, id string) *os.File {
	dirInfo, err := root.Lstat(dir)
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 || !t.ownedByRunner(dirInfo) {
		return nil
	}
	rel := filepath.Join(dir, id+".jsonl")
	linfo, err := root.Lstat(rel)
	if err != nil || !linfo.Mode().IsRegular() || !t.ownedByRunner(linfo) {
		return nil
	}
	f, err := root.Open(rel)
	if err != nil {
		return nil
	}
	finfo, err := f.Stat()
	if err != nil || !finfo.Mode().IsRegular() || !os.SameFile(linfo, finfo) || !t.ownedByRunner(finfo) {
		_ = f.Close()
		return nil
	}
	return f
}

// claudeProjectDirs is the project folder names Claude Code may give a
// session started in workdir: its name for the path as given, and for the
// path with symlinks resolved (the CLI's working directory is the resolved
// one, /private/var for /var on macOS).
func claudeProjectDirs(workdir string) []string {
	if workdir == "" {
		return nil
	}
	var dirs []string
	add := func(path string) {
		if name := claudeProjectDirName(path); name != "" && !slices.Contains(dirs, name) {
			dirs = append(dirs, name)
		}
	}
	add(workdir)
	if resolved, err := filepath.EvalSymlinks(workdir); err == nil {
		add(resolved)
	}
	return dirs
}

// claudeProjectDirName is the folder Claude Code keeps a project's
// transcripts in, under ~/.claude/projects: the absolute path with every
// character other than an ASCII letter or digit turned into "-" (its
// path.replace(/[^a-zA-Z0-9]/g, "-"), which sees UTF-16 code units, so a
// character beyond the BMP becomes two). "" for a name it would shorten and
// hash instead.
func claudeProjectDirName(path string) string {
	if path == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range path {
		switch {
		case r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
		case r > 0xFFFF:
			b.WriteString("--")
		default:
			b.WriteByte('-')
		}
	}
	if b.Len() > claudeProjectNameMax {
		return ""
	}
	return b.String()
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
