package logging

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock is a clock a test moves by hand.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func floodMiddleware(t *testing.T, buf *bytes.Buffer, clock *fakeClock, status *int) http.Handler {
	t.Helper()
	logger, err := Setup(buf, Config{Level: "debug"})
	if err != nil {
		t.Fatal(err)
	}
	return MiddlewareWithOptions(Options{
		Logger:   logger,
		ClientIP: func(*http.Request) string { return "198.51.100.4" },
		clock:    clock.Now,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(*status)
	}))
}

func logLines(buf *bytes.Buffer) []map[string]any {
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) == nil {
			out = append(out, record)
		}
	}
	return out
}

// A flood of refusals the client caused (429 above all, which a limiter
// hands out by the tens of thousands a second) is a handful of lines and a
// count, not a line each: one line per 429 wrote 12.9 GB in 17 minutes.
func TestAFloodOfClientErrorsIsSampledWithACount(t *testing.T) {
	var buf bytes.Buffer
	clock := &fakeClock{now: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	status := http.StatusTooManyRequests
	handler := floodMiddleware(t, &buf, clock, &status)

	for range 10_000 {
		serve(handler, httptest.NewRequest(http.MethodGet, "/api/graph", nil))
	}
	if lines := logLines(&buf); len(lines) > 10 {
		t.Fatalf("10k 429s wrote %d lines, want at most a handful", len(lines))
	}

	// The next second's first request says how many were left out.
	clock.Advance(1100 * time.Millisecond)
	serve(handler, httptest.NewRequest(http.MethodGet, "/api/graph", nil))
	lines := logLines(&buf)
	if len(lines) > 12 {
		t.Fatalf("10k 429s and one more wrote %d lines, want at most a handful", len(lines))
	}
	var suppressed float64
	for _, line := range lines {
		if line["event.action"] == "http.request.suppressed" {
			n, _ := line["nodevas.suppressed_count"].(float64)
			suppressed += n
		}
	}
	logged := 0
	for _, line := range lines {
		if line["event.action"] == "http.request" {
			logged++
		}
	}
	if int(suppressed)+logged != 10_001 {
		t.Fatalf("logged %d lines and counted %v left out, want them to add up to 10001; lines: %v",
			logged, suppressed, lines)
	}
}

// Server failures and refused credentials are what an operator looks for
// after the fact; they are never sampled.
func TestServerErrorsAndAuthFailuresAreNeverSampled(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError, http.StatusBadGateway} {
		var buf bytes.Buffer
		clock := &fakeClock{now: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
		status := code
		handler := floodMiddleware(t, &buf, clock, &status)
		for range 200 {
			serve(handler, httptest.NewRequest(http.MethodGet, "/api/graph", nil))
		}
		if lines := logLines(&buf); len(lines) != 200 {
			t.Fatalf("200 responses with status %d wrote %d lines, want 200", code, len(lines))
		}
	}
}

// Sampling client errors leaves everything else alone: successes are still
// one line each, before, during and after a flood.
func TestSamplingClientErrorsKeepsEverySuccess(t *testing.T) {
	var buf bytes.Buffer
	clock := &fakeClock{now: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	status := http.StatusNotFound
	handler := floodMiddleware(t, &buf, clock, &status)
	for range 1000 {
		serve(handler, httptest.NewRequest(http.MethodGet, "/api/missing", nil))
	}
	buf.Reset()
	status = http.StatusOK
	for range 50 {
		serve(handler, httptest.NewRequest(http.MethodGet, "/api/graph", nil))
	}
	ok := 0
	for _, line := range logLines(&buf) {
		if line["http.response.status_code"] == float64(http.StatusOK) {
			ok++
		}
	}
	if ok != 50 {
		t.Fatalf("50 successes during a 404 flood wrote %d lines, want 50", ok)
	}
}
