// Package logtest captures what the server logs, for tests that assert on it.
//
// Capture installs the real logger (logging.Setup), so a test sees the field
// names and the redaction production writes, not a stand-in.
package logtest

import (
	"bytes"
	"encoding/json"
	"log"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/logging"
)

// Recorder collects the lines written while a test runs. It is safe for
// concurrent use: mail delivery and other background work log from their
// own goroutines.
type Recorder struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (r *Recorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Write(p)
}

// Capture makes the default logger, and the standard library's, write JSON to
// a Recorder for the rest of the test.
func Capture(t testing.TB) *Recorder {
	t.Helper()
	previous := slog.Default()
	writer, flags, prefix := log.Writer(), log.Flags(), log.Prefix()
	recorder := &Recorder{}
	if _, err := logging.Setup(recorder, logging.Config{Level: "debug", Service: "nodevas"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		slog.SetDefault(previous)
		log.SetOutput(writer)
		log.SetFlags(flags)
		log.SetPrefix(prefix)
	})
	return recorder
}

// Text is everything written so far.
func (r *Recorder) Text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

// Records decodes every line written so far. A line that is not one JSON
// object fails the test: the log is meant to be machine-read line by line.
func (r *Recorder) Records(t testing.TB) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(r.Text()), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line is not a JSON object: %v (%q)", err, line)
		}
		records = append(records, record)
	}
	return records
}

// Find returns the records in which every field of match has that value.
// Values compare as JSON decodes them, so a number is a float64.
func (r *Recorder) Find(t testing.TB, match map[string]any) []map[string]any {
	t.Helper()
	var found []map[string]any
	for _, record := range r.Records(t) {
		matches := true
		for key, want := range match {
			if record[key] != want {
				matches = false
				break
			}
		}
		if matches {
			found = append(found, record)
		}
	}
	return found
}

// One is Find for exactly one record, which it returns.
func (r *Recorder) One(t testing.TB, match map[string]any) map[string]any {
	t.Helper()
	found := r.Find(t, match)
	if len(found) != 1 {
		t.Fatalf("%d log records match %v, want 1; log:\n%s", len(found), match, r.Text())
	}
	return found[0]
}

// Await is One for a record that work on another goroutine writes, such as a
// mail delivery after the request was answered: it waits up to five seconds
// for the record to appear.
func (r *Recorder) Await(t testing.TB, match map[string]any) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(r.Find(t, match)) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	return r.One(t, match)
}

// Want fails the test unless record has every field of want, with that value.
func Want(t testing.TB, record map[string]any, want map[string]any) {
	t.Helper()
	for key, value := range want {
		if record[key] != value {
			t.Errorf("%s = %#v, want %#v (record %v)", key, record[key], value, record)
		}
	}
}

// Absent fails the test if record has any of keys.
func Absent(t testing.TB, record map[string]any, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if value, ok := record[key]; ok {
			t.Errorf("%s = %#v, want it absent (record %v)", key, value, record)
		}
	}
}

// Never fails the test if any of secrets appears anywhere in the log.
func (r *Recorder) Never(t testing.TB, secrets ...string) {
	t.Helper()
	text := r.Text()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(text, secret) {
			t.Errorf("a credential reached the log: %q in\n%s", secret, text)
		}
	}
}
