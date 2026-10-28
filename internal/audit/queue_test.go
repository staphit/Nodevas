package audit

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func queuedActions(t *testing.T, s *Store) []string {
	t.Helper()
	events, err := s.Query(context.Background(), Filter{Limit: MaxLimit})
	if err != nil {
		t.Fatal(err)
	}
	// Query is newest first; same-second rows by insertion, newest first.
	out := make([]string, len(events))
	for i, event := range events {
		out[len(events)-1-i] = event.Action
	}
	return out
}

// The middleware's writes go through the queue, one writer, in the order
// they were handed over; a read of the trail sees every write made before it.
func TestQueueKeepsTheOrderAndQueryReadsItsWrites(t *testing.T) {
	s := newStore(t)
	q := NewQueue(s, 16)
	t.Cleanup(func() { _ = q.Close(context.Background()) })
	at := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	var want []string
	for i := range 200 {
		action := fmt.Sprintf("node.update.%03d", i)
		want = append(want, action)
		q.RecordOrLog(context.Background(), Event{At: at, ActorID: "u1", Action: action})
	}
	got := queuedActions(t, s)
	if len(got) != len(want) {
		t.Fatalf("trail has %d rows right after 200 writes, want 200", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %s, want %s", i, got[i], want[i])
		}
	}
}

// Close writes everything still queued before it returns: serve closes the
// database right after.
func TestQueueCloseDrainsEveryQueuedWrite(t *testing.T) {
	s := newStore(t)
	q := NewQueue(s, 64)
	release := make(chan struct{})
	q.beforeWrite = func() { <-release }
	for i := range 50 {
		q.RecordOrLog(context.Background(), Event{At: time.Now(), ActorID: "u1", Action: fmt.Sprintf("a.%02d", i)})
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(release)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := q.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if got := queuedActions(t, s); len(got) != 50 {
		t.Fatalf("trail has %d rows after Close, want 50", len(got))
	}
	// After Close a write is made synchronously, never dropped.
	q.RecordOrLog(context.Background(), Event{At: time.Now(), ActorID: "u1", Action: "after.close"})
	if got := queuedActions(t, s); len(got) != 51 || got[50] != "after.close" {
		t.Fatalf("a write after Close: trail = %v", got)
	}
}

// A full queue never drops an event: the caller writes it itself.
func TestQueueOverflowFallsBackToASynchronousWrite(t *testing.T) {
	s := newStore(t)
	q := NewQueue(s, 1)
	release := make(chan struct{})
	writing := make(chan struct{}, 8)
	q.beforeWrite = func() {
		writing <- struct{}{}
		<-release
	}
	q.RecordOrLog(context.Background(), Event{At: time.Now(), ActorID: "u1", Action: "first"})
	select {
	case <-writing: // the writer holds "first"
	case <-time.After(5 * time.Second):
		t.Fatal("no writer took the first event off the queue")
	}
	q.RecordOrLog(context.Background(), Event{At: time.Now(), ActorID: "u1", Action: "second"}) // fills the queue
	q.RecordOrLog(context.Background(), Event{At: time.Now(), ActorID: "u1", Action: "third"})  // overflows

	// "third" is in the table already, written by the caller, while the
	// writer is still stuck on "first".
	rows, err := s.db.QueryContext(context.Background(), `SELECT action FROM audit_events`)
	if err != nil {
		t.Fatal(err)
	}
	var present []string
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatal(err)
		}
		present = append(present, action)
	}
	rows.Close()
	if len(present) != 1 || present[0] != "third" {
		t.Fatalf("rows while the writer is stuck = %v, want only the overflowed third", present)
	}
	if stats := q.Stats(); stats.Overflowed != 1 {
		t.Fatalf("stats = %+v, want one overflow", stats)
	}

	close(release)
	if err := q.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := queuedActions(t, s); len(got) != 3 {
		t.Fatalf("trail after Close = %v, want all three", got)
	}
}
