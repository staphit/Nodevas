package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"nodevas/internal/agenthook"
	"nodevas/internal/runnerwire"
)

const (
	touchedPollInterval  = 30 * time.Second
	touchedDebounceDelay = 3 * time.Second
	touchedGitTimeout    = 4 * time.Second
	touchedSlowThreshold = 2 * time.Second
	touchedMaxInterval   = 5 * time.Minute
)

// ignoredLockfiles are dropped on the runner before sending touched files
// (§C.3.1), in any directory.
var ignoredLockfiles = map[string]bool{
	"go.sum":              true,
	"package-lock.json":   true,
	"npm-shrinkwrap.json": true,
	"yarn.lock":           true,
	"pnpm-lock.yaml":      true,
	"bun.lockb":           true,
	"Cargo.lock":          true,
	"poetry.lock":         true,
	"uv.lock":             true,
	"Pipfile.lock":        true,
	"composer.lock":       true,
	"Gemfile.lock":        true,
}

type touchedOptions struct {
	pollInterval  time.Duration
	debounceDelay time.Duration
	gitTimeout    time.Duration
	slowThreshold time.Duration
	maxInterval   time.Duration
	interactive   bool
	// Test seams (nil in production).
	runStatus  func(ctx context.Context, dir string) (string, error)
	onInterval func(next time.Duration)
}

// touchedWatcher tracks repo-relative files modified inside a run's worktree
// and emits incremental runnerwire.Touched messages (§C.3.1).
type touchedWatcher struct {
	dir           string
	send          func(runnerwire.Touched)
	pollInterval  time.Duration
	debounceDelay time.Duration
	gitTimeout    time.Duration
	slowThreshold time.Duration
	maxInterval   time.Duration
	interactive   bool
	runStatus     func(ctx context.Context, dir string) (string, error)
	onInterval    func(next time.Duration)

	mu        sync.Mutex
	sent      map[string]bool // path -> last sent addOnly (true = add-only, false = strong)
	moreSent  bool
	attrCache map[string]bool // path -> linguist-generated

	debounceCh chan struct{}
	stopCh     chan struct{}
	doneCh     chan struct{}
	stopOnce   sync.Once
	started    bool
}

func newTouchedWatcher(dir string, send func(runnerwire.Touched), opts touchedOptions) *touchedWatcher {
	w := &touchedWatcher{
		dir:           dir,
		send:          send,
		pollInterval:  opts.pollInterval,
		debounceDelay: opts.debounceDelay,
		gitTimeout:    opts.gitTimeout,
		slowThreshold: opts.slowThreshold,
		maxInterval:   opts.maxInterval,
		interactive:   opts.interactive,
		runStatus:     opts.runStatus,
		onInterval:    opts.onInterval,
		sent:          make(map[string]bool),
		attrCache:     make(map[string]bool),
		debounceCh:    make(chan struct{}, 1),
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	if w.pollInterval <= 0 {
		w.pollInterval = touchedPollInterval
	}
	if w.debounceDelay <= 0 {
		w.debounceDelay = touchedDebounceDelay
	}
	if w.gitTimeout <= 0 {
		w.gitTimeout = touchedGitTimeout
	}
	if w.slowThreshold <= 0 {
		w.slowThreshold = touchedSlowThreshold
	}
	if w.maxInterval <= 0 {
		w.maxInterval = touchedMaxInterval
	}
	if w.runStatus == nil {
		w.runStatus = w.gitStatus
	}
	return w
}

func (w *touchedWatcher) start(ctx context.Context) {
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return
	}
	w.started = true
	w.mu.Unlock()

	go func() {
		defer close(w.doneCh)
		interval := w.pollInterval
		pollTimer := time.NewTimer(interval)
		defer pollTimer.Stop()

		var debounceTimer *time.Timer
		var debounceC <-chan time.Time
		stopDebounce := func() {
			if debounceTimer != nil {
				debounceTimer.Stop()
				debounceTimer = nil
				debounceC = nil
			}
		}
		defer stopDebounce()

		runPoll := func() {
			start := time.Now()
			w.poll(ctx)
			if time.Since(start) > w.slowThreshold {
				interval = min(interval*2, w.maxInterval)
				if w.onInterval != nil {
					w.onInterval(interval)
				}
			}
			pollTimer.Reset(interval)
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-w.stopCh:
				return
			case <-pollTimer.C:
				stopDebounce()
				runPoll()
			case <-w.debounceCh:
				if debounceTimer == nil {
					debounceTimer = time.NewTimer(w.debounceDelay)
					debounceC = debounceTimer.C
				} else {
					if !debounceTimer.Stop() {
						select {
						case <-debounceTimer.C:
						default:
						}
					}
					debounceTimer.Reset(w.debounceDelay)
				}
			case <-debounceC:
				debounceTimer = nil
				debounceC = nil
				if !pollTimer.Stop() {
					select {
					case <-pollTimer.C:
					default:
					}
				}
				runPoll()
			}
		}
	}()
}

// close halts background polling and waits for the watcher goroutine to exit
// without running a final poll. Callers that commit the worktree next close
// it when the agent exits and call pollFinal before committing: once the
// work is committed, git status shows nothing left to report.
func (w *touchedWatcher) close() {
	if w == nil {
		return
	}
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
	w.mu.Lock()
	started := w.started
	w.mu.Unlock()
	if started {
		<-w.doneCh
	}
}

// pollFinal runs one final bounded git status poll after the agent has exited.
func (w *touchedWatcher) pollFinal() {
	if w == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), w.gitTimeout)
	defer cancel()
	w.poll(ctx)
}

// stop halts background polling and runs one final git status poll (§C.3.1).
func (w *touchedWatcher) stop() {
	w.close()
	w.pollFinal()
}

// onEvent handles headless agent events: edit-tool paths are reported
// immediately, and tool_result schedules a debounced git status poll (§C.3.1).
func (w *touchedWatcher) onEvent(ev runnerwire.RunEvent) {
	if w == nil || w.interactive {
		return
	}
	switch ev.Type {
	case runnerwire.EventToolCall:
		inputMap := decodeToolInput(ev.Input)
		raw := agenthook.EditedFiles(ev.Tool, inputMap)
		if len(raw) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), w.gitTimeout)
		defer cancel()
		rel := agenthook.RepoRelative(ctx, w.dir, raw)
		candidates := make([]string, 0, len(rel))
		for _, p := range rel {
			if validTouchedPath(p) && !agenthook.SecretPath(p) && !isIgnoredLockfile(p) {
				candidates = append(candidates, p)
			}
		}
		if len(candidates) == 0 {
			return
		}
		kept := w.filterGenerated(ctx, candidates)
		if len(kept) == 0 {
			return
		}
		w.recordEditPaths(kept)
	case runnerwire.EventToolResult:
		select {
		case w.debounceCh <- struct{}{}:
		default:
		}
	}
}

func decodeToolInput(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) == nil && m != nil {
		return m
	}
	return map[string]any{"input": raw}
}

func (w *touchedWatcher) recordEditPaths(paths []string) {
	w.mu.Lock()
	var toSend []string
	hitCap := false
	for _, p := range paths {
		if _, exists := w.sent[p]; exists {
			continue
		}
		if len(w.sent) >= runnerwire.MaxTouchedFiles {
			if !w.moreSent {
				w.moreSent = true
				hitCap = true
			}
			continue
		}
		w.sent[p] = false
		toSend = append(toSend, p)
	}
	w.mu.Unlock()
	if len(toSend) > 0 || hitCap {
		w.emitBatches(toSend, nil, hitCap)
	}
}

func (w *touchedWatcher) poll(ctx context.Context) {
	out, err := w.runStatus(ctx, w.dir)
	if err != nil || out == "" {
		return
	}
	entries := parsePorcelainStatus(out)
	if len(entries) == 0 {
		return
	}

	// Filter invalid, secret and lockfile paths before invoking check-attr or diff.
	filtered := make([]statusEntry, 0, len(entries))
	candPaths := make([]string, 0, len(entries))
	for _, e := range entries {
		if !validTouchedPath(e.path) || agenthook.SecretPath(e.path) || isIgnoredLockfile(e.path) {
			continue
		}
		filtered = append(filtered, e)
		candPaths = append(candPaths, e.path)
	}
	if len(filtered) == 0 {
		return
	}

	keptSlice := w.filterGenerated(ctx, candPaths)
	keptSet := make(map[string]bool, len(keptSlice))
	for _, p := range keptSlice {
		keptSet[p] = true
	}

	// Determine addOnly status for each kept path.
	var diffPaths []string
	addOnly := make(map[string]bool, len(keptSet))
	for _, e := range filtered {
		if !keptSet[e.path] {
			continue
		}
		switch e.kind {
		case statusUntracked:
			if _, seen := addOnly[e.path]; !seen {
				addOnly[e.path] = true
			}
		case statusDeleted:
			addOnly[e.path] = false
		default:
			if _, seen := addOnly[e.path]; !seen {
				addOnly[e.path] = false
			}
			diffPaths = append(diffPaths, e.path)
		}
	}

	if len(diffPaths) > 0 {
		slices.Sort(diffPaths)
		diffPaths = slices.Compact(diffPaths)
		numstats, err := w.gitNumstat(ctx, w.dir, diffPaths)
		if err == nil {
			for _, p := range diffPaths {
				if st, ok := numstats[p]; ok {
					addOnly[p] = !st.binary && st.deletions == 0
				}
			}
		} else {
			// Unborn HEAD (no commits yet): staged additions have no deletions.
			for _, e := range filtered {
				if keptSet[e.path] && e.kind == statusAdded {
					addOnly[e.path] = true
				}
			}
		}
	}

	// Compute incremental delta against w.sent.
	allPaths := make([]string, 0, len(addOnly))
	for p := range addOnly {
		allPaths = append(allPaths, p)
	}
	slices.Sort(allPaths)

	w.mu.Lock()
	var sendFiles []string
	sendAddOnly := make(map[string]bool)
	hitCap := false
	for _, p := range allPaths {
		ao := addOnly[p]
		prevAO, seen := w.sent[p]
		if !seen {
			if len(w.sent) >= runnerwire.MaxTouchedFiles {
				if !w.moreSent {
					w.moreSent = true
					hitCap = true
				}
				continue
			}
			w.sent[p] = ao
			sendFiles = append(sendFiles, p)
			if ao {
				sendAddOnly[p] = true
			}
		} else if prevAO && !ao {
			// Path stopped being add-only: re-send once without AddOnly.
			w.sent[p] = false
			sendFiles = append(sendFiles, p)
		}
	}
	w.mu.Unlock()

	if len(sendFiles) > 0 || hitCap {
		w.emitBatches(sendFiles, sendAddOnly, hitCap)
	}
}

func (w *touchedWatcher) emitBatches(files []string, addOnly map[string]bool, more bool) {
	if w.send == nil {
		return
	}
	slices.Sort(files)
	if len(files) == 0 && more {
		w.send(runnerwire.Touched{Files: []string{}, More: true})
		return
	}
	for start := 0; start < len(files); start += runnerwire.MaxTouchedPerMessage {
		end := min(start+runnerwire.MaxTouchedPerMessage, len(files))
		chunk := slices.Clone(files[start:end])
		var ao []string
		for _, p := range chunk {
			if addOnly[p] {
				ao = append(ao, p)
			}
		}
		msg := runnerwire.Touched{
			Files:   chunk,
			AddOnly: ao,
			More:    more && end == len(files),
		}
		w.send(msg)
	}
}

type statusKind int

const (
	statusModified statusKind = iota
	statusAdded
	statusUntracked
	statusDeleted
)

type statusEntry struct {
	path string
	kind statusKind
}

// parsePorcelainStatus parses `git status --porcelain=v1 -z --untracked-files=all`.
func parsePorcelainStatus(out string) []statusEntry {
	fields := strings.Split(out, "\x00")
	var entries []statusEntry
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		x, y := f[0], f[1]
		p := f[3:]
		if x == '!' && y == '!' {
			continue
		}
		if x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			// Porcelain v1 -z emits: XY new_path\0old_path\0.
			entries = append(entries, statusEntry{path: p, kind: statusModified})
			if i+1 < len(fields) {
				i++
				if old := fields[i]; old != "" {
					entries = append(entries, statusEntry{path: old, kind: statusDeleted})
				}
			}
			continue
		}
		switch {
		case x == '?' && y == '?':
			entries = append(entries, statusEntry{path: p, kind: statusUntracked})
		case x == 'D' || y == 'D':
			entries = append(entries, statusEntry{path: p, kind: statusDeleted})
		case x == 'A' && y == ' ':
			entries = append(entries, statusEntry{path: p, kind: statusAdded})
		default:
			entries = append(entries, statusEntry{path: p, kind: statusModified})
		}
	}
	return entries
}

type numstatInfo struct {
	additions int
	deletions int
	binary    bool
}

func (w *touchedWatcher) gitTouched(ctx context.Context, dir string, stdin []byte, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, w.gitTimeout)
	defer cancel()
	// The agent can write the worktree's git config while it runs: its filter
	// drivers are emptied (repoGitArgs), as for every other read of a run's
	// worktree, so status and diff never run a command it planted.
	cmd := exec.CommandContext(ctx, "git", hardenedArgs(dir, repoGitArgs(ctx, dir, args))...)
	cmd.Env = append(repoGitEnv(), "GIT_OPTIONAL_LOCKS=0")
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func (w *touchedWatcher) gitStatus(ctx context.Context, dir string) (string, error) {
	return w.gitTouched(ctx, dir, nil, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=dirty")
}

func (w *touchedWatcher) gitNumstat(ctx context.Context, dir string, paths []string) (map[string]numstatInfo, error) {
	stats := make(map[string]numstatInfo, len(paths))
	const chunkSize = 200
	for start := 0; start < len(paths); start += chunkSize {
		end := min(start+chunkSize, len(paths))
		args := append([]string{"diff", "--numstat", "-z", "--no-ext-diff", "--no-textconv", "--ignore-submodules=dirty", "HEAD", "--"}, paths[start:end]...)
		out, err := w.gitTouched(ctx, dir, nil, args...)
		if err != nil {
			return nil, err
		}
		parseNumstatZ(out, stats)
	}
	return stats, nil
}

func parseNumstatZ(out string, stats map[string]numstatInfo) {
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		parts := strings.SplitN(fields[i], "\t", 3)
		if len(parts) != 3 {
			continue
		}
		p := parts[2]
		if p == "" && i+2 < len(fields) {
			p = fields[i+2]
			i += 2
		}
		var info numstatInfo
		if parts[0] == "-" && parts[1] == "-" {
			info.binary = true
		} else {
			info.additions = parseNonNegInt(parts[0])
			info.deletions = parseNonNegInt(parts[1])
		}
		stats[p] = info
	}
}

func parseNonNegInt(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0
		}
		n = n*10 + int(s[i]-'0')
	}
	return n
}

// filterGenerated drops paths that `git check-attr -z --stdin linguist-generated`
// marks set or true (§C.3.1), caching results per path.
func (w *touchedWatcher) filterGenerated(ctx context.Context, paths []string) []string {
	var unchecked []string
	w.mu.Lock()
	for _, p := range paths {
		if _, cached := w.attrCache[p]; !cached {
			unchecked = append(unchecked, p)
		}
	}
	w.mu.Unlock()

	if len(unchecked) > 0 {
		slices.Sort(unchecked)
		unchecked = slices.Compact(unchecked)
		var buf bytes.Buffer
		for _, p := range unchecked {
			buf.WriteString(p)
			buf.WriteByte(0)
		}
		out, err := w.gitTouched(ctx, w.dir, buf.Bytes(), "check-attr", "-z", "--stdin", "linguist-generated")
		checked := make(map[string]bool, len(unchecked))
		for _, p := range unchecked {
			checked[p] = false
		}
		if err == nil && out != "" {
			fields := strings.Split(out, "\x00")
			for i := 0; i+2 < len(fields); i += 3 {
				p, val := fields[i], fields[i+2]
				if val == "set" || val == "true" {
					checked[p] = true
				}
			}
		}
		w.mu.Lock()
		for p, gen := range checked {
			w.attrCache[p] = gen
		}
		w.mu.Unlock()
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	kept := make([]string, 0, len(paths))
	for _, p := range paths {
		if !w.attrCache[p] {
			kept = append(kept, p)
		}
	}
	return kept
}

func isIgnoredLockfile(p string) bool {
	return ignoredLockfiles[path.Base(p)]
}

// validTouchedPath checks that p is a clean repo-relative slash path within
// MaxTouchedPathBytes, with no NUL/control bytes, no absolute prefix and no "..".
func validTouchedPath(p string) bool {
	if p == "" || len(p) > runnerwire.MaxTouchedPathBytes || !utf8.ValidString(p) {
		return false
	}
	if strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || strings.ContainsRune(p, 0) {
		return false
	}
	// Reject Windows drive-letter paths (e.g. "C:/foo").
	if len(p) >= 2 && p[1] == ':' && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z')) {
		return false
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return false
		}
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}
