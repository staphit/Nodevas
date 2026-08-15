package engine

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

// moveHeavyJournal builds n status events followed by n move events, each
// retargeting the newest stamp. That is the shape the old replay handled
// quadratically: every move rescanned the whole history to find its ref.
func moveHeavyJournal(tb testing.TB, n int) []byte {
	tb.Helper()
	at := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	var journal bytes.Buffer
	statuses := []Status{StatusStarted, StatusInProgress, StatusDone, StatusReady}
	write := func(ev HistoryEvent) {
		line, err := AppendJournalLine(ev)
		if err != nil {
			tb.Fatal(err)
		}
		journal.Write(line)
	}
	for i := 0; i < n; i++ {
		write(HistoryEvent{
			ID:    fmt.Sprintf("ev-%d", i),
			T:     at.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
			Event: "status",
			Node:  fmt.Sprintf("node-%d", i%8),
			To:    statuses[i%len(statuses)],
			By:    "tester",
		})
	}
	for i := 0; i < n; i++ {
		write(HistoryEvent{
			ID:    fmt.Sprintf("mv-%d", i),
			T:     at.Add(time.Duration(i) * time.Hour).Format(time.RFC3339),
			Event: "move",
			Ref:   fmt.Sprintf("ev-%d", n-1),
			Node:  fmt.Sprintf("node-%d", (n-1)%8),
			By:    "tester",
		})
	}
	return journal.Bytes()
}

// Replay must be linear in the number of events. Eight times the input may
// cost roughly eight times the work; a quadratic replay would cost sixty-four.
func TestJournalReplayScalesLinearly(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive")
	}
	measure := func(n int) time.Duration {
		journal := moveHeavyJournal(t, n)
		best := time.Duration(1<<62 - 1)
		for attempt := 0; attempt < 3; attempt++ {
			start := time.Now()
			if _, err := StateFromJournalChecked(journal); err != nil {
				t.Fatal(err)
			}
			if elapsed := time.Since(start); elapsed < best {
				best = elapsed
			}
		}
		return best
	}
	small := measure(5_000)
	large := measure(40_000)
	if small <= 0 {
		small = time.Microsecond
	}
	if ratio := float64(large) / float64(small); ratio > 24 {
		t.Fatalf("replay of 8x the events took %.1fx the time (%v vs %v) — replay looks quadratic",
			ratio, large, small)
	}
}

func TestJournalReplayHandlesLargeInput(t *testing.T) {
	journal := moveHeavyJournal(t, 20_000)
	rs, err := StateFromJournalChecked(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.History) != 40_000 {
		t.Fatalf("history = %d events, want 40000", len(rs.History))
	}
	// Every move retargeted the same stamp, so it carries the last move's T.
	last := rs.History[len(rs.History)-1]
	if rs.History[19_999].T != last.T {
		t.Fatalf("retargeted stamp T = %q, want %q", rs.History[19_999].T, last.T)
	}
}

// ResumeJournal is the checkpoint shortcut: reducing a prefix and then
// replaying the rest must equal one straight replay of the whole journal.
func TestResumeJournalMatchesFullReplayAtEverySplit(t *testing.T) {
	journal := moveHeavyJournal(t, 40)
	want, err := StateFromJournalChecked(journal)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := MarshalRunState(want)
	if err != nil {
		t.Fatal(err)
	}

	lines := bytes.SplitAfter(journal, []byte{'\n'})
	for split := 0; split < len(lines); split++ {
		prefix := bytes.Join(lines[:split], nil)
		suffix := bytes.Join(lines[split:], nil)
		head, err := StateFromJournalChecked(prefix)
		if err != nil {
			t.Fatalf("split %d prefix: %v", split, err)
		}
		// Round-trip the prefix state through JSON exactly as a checkpoint
		// file would, so the test also covers what serialisation does to it.
		encoded, err := MarshalRunState(head)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := ParseRunState(encoded)
		if err != nil {
			t.Fatal(err)
		}
		resumed, err := ResumeJournal(decoded, suffix)
		if err != nil {
			t.Fatalf("split %d resume: %v", split, err)
		}
		gotJSON, err := MarshalRunState(resumed)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(gotJSON, wantJSON) {
			t.Fatalf("split %d: resumed replay differs from full replay", split)
		}
	}
}

func BenchmarkStateFromJournal(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 50_000} {
		journal := moveHeavyJournal(b, n)
		b.Run(fmt.Sprintf("events=%d", 2*n), func(b *testing.B) {
			b.SetBytes(int64(len(journal)))
			for i := 0; i < b.N; i++ {
				if _, err := StateFromJournalChecked(journal); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
