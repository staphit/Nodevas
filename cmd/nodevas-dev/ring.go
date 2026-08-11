package main

import (
	"strings"
	"sync"
	"time"
)

// maxLogLines is how much child output the panel keeps. It is bounded because
// a dev server left running overnight writes a request line per request, and
// an unbounded buffer in a process nobody is watching grows until the machine
// notices.
const maxLogLines = 400

// logLine is one line of captured output. The source is kept because the
// server logs ECS JSON to stderr and plain lines to stdout, and telling them
// apart is most of what makes the pane readable.
type logLine struct {
	Seq    int    `json:"seq"`
	At     string `json:"at"`
	Source string `json:"source"`
	Text   string `json:"text"`
}

// logRing is a fixed-capacity buffer of the most recent lines. Older lines are
// dropped, never truncated in place, so a reader always sees whole lines.
type logRing struct {
	mu    sync.Mutex
	lines []logLine
	max   int
	seq   int
}

func newLogRing(max int) *logRing {
	if max < 1 {
		max = 1
	}
	return &logRing{max: max}
}

// Add appends one line, dropping the oldest when the ring is full.
func (r *logRing) Add(source, text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	line := logLine{
		Seq:    r.seq,
		At:     time.Now().Format("15:04:05"),
		Source: source,
		Text:   strings.TrimRight(text, "\r\n"),
	}
	if len(r.lines) < r.max {
		r.lines = append(r.lines, line)
		return
	}
	// Shift by one rather than reallocating: the ring is small and this only
	// runs once the buffer is full.
	copy(r.lines, r.lines[1:])
	r.lines[len(r.lines)-1] = line
}

// Lines returns a copy of the buffer, oldest first.
func (r *logRing) Lines() []logLine {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]logLine, len(r.lines))
	copy(out, r.lines)
	return out
}

// Len is the number of lines currently held, for tests and for the page's
// "showing N lines" note.
func (r *logRing) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.lines)
}
