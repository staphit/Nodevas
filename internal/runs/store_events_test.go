package runs

// P11: a batch of run events is written as one INSERT, not one round trip per
// event on the single writer, with the same resend, ordering and cap rules.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
	"nodevas/internal/testpg"
)

// countInsertStatements installs a statement-level trigger on run_events and
// returns a func reading how many INSERT statements ran since.
func countInsertStatements(t *testing.T, s *Store) func() int {
	t.Helper()
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `
CREATE TABLE test_event_inserts (n INTEGER NOT NULL);
INSERT INTO test_event_inserts VALUES (0);
CREATE FUNCTION test_count_event_inserts() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE test_event_inserts SET n = n + 1;
    RETURN NULL;
END $$;
CREATE TRIGGER test_count_event_inserts AFTER INSERT ON run_events
    FOR EACH STATEMENT EXECUTE FUNCTION test_count_event_inserts();`); err != nil {
		t.Fatal(err)
	}
	return func() int {
		t.Helper()
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT n FROM test_event_inserts`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
}

func textEvents(seqs ...int64) []runnerwire.RunEvent {
	out := make([]runnerwire.RunEvent, 0, len(seqs))
	for _, seq := range seqs {
		out = append(out, runnerwire.RunEvent{Seq: seq, Type: runnerwire.EventText, Text: fmt.Sprintf("e%d", seq)})
	}
	return out
}

func TestAppendEventsWritesABatchInOneStatement(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := createRun(t, s, "n1")
	inserts := countInsertStatements(t, s)

	batch := make([]int64, runnerwire.MaxEventsPerMessage)
	for i := range batch {
		batch[i] = int64(i + 1)
	}
	if n, err := s.AppendEvents(ctx, r.ID, textEvents(batch...)); err != nil || n != len(batch) {
		t.Fatalf("append = %d, %v", n, err)
	}
	if got := inserts(); got != 1 {
		t.Fatalf("%d INSERT statements for one batch of %d events, want 1", got, len(batch))
	}
	// A pure resend inserts nothing and runs no INSERT at all.
	if n, err := s.AppendEvents(ctx, r.ID, textEvents(batch...)); err != nil || n != 0 {
		t.Fatalf("resend = %d, %v", n, err)
	}
	if got := inserts(); got != 1 {
		t.Fatalf("%d INSERT statements after a pure resend, want still 1", got)
	}
}

// Within one batch, a seq at or below the last kept one is a resend, so an
// out-of-order or repeated event is dropped exactly as it was one row at a
// time; what is stored reads back in seq order with its own payload.
func TestAppendEventsKeepsSeqSemanticsWithinABatch(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := createRun(t, s, "n1")
	if n, err := s.AppendEvents(ctx, r.ID, textEvents(1, 2)); err != nil || n != 2 {
		t.Fatalf("first append = %d, %v", n, err)
	}
	n, err := s.AppendEvents(ctx, r.ID, append(textEvents(3, 1, 4, 4, 6, 5, 8), runnerwire.RunEvent{Seq: 0, Type: runnerwire.EventText}))
	if err != nil || n != 4 {
		t.Fatalf("second append = %d, %v, want 4 (3, 4, 6, 8)", n, err)
	}
	events, more, err := s.Events(ctx, r.ID, 0, 0)
	if err != nil || more {
		t.Fatalf("events: more %v, err %v", more, err)
	}
	var got []string
	for _, event := range events {
		if event.Text != fmt.Sprintf("e%d", event.Seq) {
			t.Errorf("seq %d carries %q", event.Seq, event.Text)
		}
		got = append(got, fmt.Sprint(event.Seq))
	}
	if strings.Join(got, ",") != "1,2,3,4,6,8" {
		t.Fatalf("stored seqs %v, want 1,2,3,4,6,8", got)
	}
}

// A batch that crosses the cap keeps the events up to it and drops the rest.
func TestAppendEventsCapCutsABatch(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := createRun(t, s, "n1")
	var seq int64
	for seq < runnerwire.MaxRunEvents-3 {
		size := min(runnerwire.MaxEventsPerMessage, runnerwire.MaxRunEvents-3-int(seq))
		batch := make([]int64, size)
		for i := range batch {
			seq++
			batch[i] = seq
		}
		if _, err := s.AppendEvents(ctx, r.ID, textEvents(batch...)); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.AppendEvents(ctx, r.ID, textEvents(seq+1, seq+2, seq+3, seq+4, seq+5))
	if err != nil || n != 3 {
		t.Fatalf("append across the cap = %d, %v, want 3", n, err)
	}
	var count int
	var last int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), MAX(seq) FROM run_events WHERE run_id = ?`, r.ID).Scan(&count, &last); err != nil {
		t.Fatal(err)
	}
	if count != runnerwire.MaxRunEvents || last != seq+3 {
		t.Fatalf("stored %d events up to seq %d, want %d up to %d", count, last, runnerwire.MaxRunEvents, seq+3)
	}
}

// BenchmarkAppendEvents writes full batches (MaxEventsPerMessage events of
// about 200 bytes each), the shape a busy runner sends.
func BenchmarkAppendEvents(b *testing.B) {
	ctx := context.Background()
	s := NewStore(testpg.New(b))
	text := strings.Repeat("x", 200)
	var r Record
	var seq int64
	newRun := func() {
		var err error
		r, err = s.Create(ctx, New{
			Project: ".", NodeID: "n1", NodeTitle: "Title", RunnerID: "r1", RunnerName: "desk",
			OwnerID: "u-ann", Agent: "claude", Model: "default", RepoID: "repo", RepoName: "repo",
			Prompt: "extra", JobPrompt: "# Title", CreatedBy: "u-ann", CreatedByName: "ann",
		})
		if err != nil {
			b.Fatal(err)
		}
		seq = 0
	}
	newRun()
	batch := make([]runnerwire.RunEvent, runnerwire.MaxEventsPerMessage)
	events := 0
	for b.Loop() {
		if seq+int64(len(batch)) > runnerwire.MaxRunEvents {
			b.StopTimer()
			newRun()
			b.StartTimer()
		}
		for i := range batch {
			seq++
			batch[i] = runnerwire.RunEvent{Seq: seq, Type: runnerwire.EventText, Text: text}
		}
		n, err := s.AppendEvents(ctx, r.ID, batch)
		if err != nil || n != len(batch) {
			b.Fatalf("append = %d, %v", n, err)
		}
		events += n
	}
	b.ReportMetric(float64(events)/b.Elapsed().Seconds(), "events/s")
}
