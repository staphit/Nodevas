package audit

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Queue takes audit rows off the request path. Every write the request
// middleware sees used to wait for its row on the database's single writer
// connection; through a Queue the event's log line is written at once, as
// RecordOrLog writes it, and its row by one background writer, in the order
// the events were handed over.
//
// Nothing is dropped silently. A full queue makes the caller write its row
// itself (synchronously, as before); a closed queue does the same; and a row
// the database refuses gets RecordOrLog's fallback line and degraded health,
// whichever goroutine wrote it. The store reads its own writes: Query and
// Health wait for the rows queued before them.
type Queue struct {
	store *Store
	ch    chan queuedEvent

	// mu orders enqueueing against Close, so nothing is sent on a closed
	// channel and seq follows channel order.
	mu      sync.Mutex
	closed  bool
	lastSeq uint64

	doneMu    sync.Mutex
	done      *sync.Cond
	written   uint64 // seq of the last row the writer finished
	overflows uint64
	finished  chan struct{}

	// beforeWrite runs before each queued row is written; tests hold the
	// writer with it.
	beforeWrite func()
	// overflowWait is how long a caller whose row did not fit waits for the
	// rows ahead of it before writing its own anyway.
	overflowWait time.Duration
}

type queuedEvent struct {
	ctx   context.Context
	event Event
	seq   uint64
}

// DefaultQueueSize is how many rows a Queue holds before callers write
// their own.
const DefaultQueueSize = 1024

// queuedWriteTimeout bounds one queued row's write; the request it came
// from is gone, so its context no longer ends it.
const queuedWriteTimeout = 30 * time.Second

// defaultOverflowWait bounds how long an overflowing caller waits for the
// rows queued ahead of its own (Queue.overflowWait).
const defaultOverflowWait = 5 * time.Second

// queuedReadWait bounds how long a read of the trail waits for queued rows.
const queuedReadWait = 5 * time.Second

// QueueStats reports a Queue.
type QueueStats struct {
	Capacity   int    `json:"capacity"`
	Depth      int    `json:"depth"`
	Written    uint64 `json:"written"`
	Overflowed uint64 `json:"overflowed"`
	Closed     bool   `json:"closed"`
}

// NewQueue starts a queue of size rows (DefaultQueueSize when size <= 0)
// writing to store, and makes store's reads wait for it. Close it before
// closing the database.
func NewQueue(store *Store, size int) *Queue {
	if size <= 0 {
		size = DefaultQueueSize
	}
	q := &Queue{store: store, ch: make(chan queuedEvent, size), finished: make(chan struct{}),
		overflowWait: defaultOverflowWait}
	q.done = sync.NewCond(&q.doneMu)
	if store != nil {
		store.queueMu.Lock()
		store.queue = q
		store.queueMu.Unlock()
	}
	go q.run()
	return q
}

func (q *Queue) run() {
	defer close(q.finished)
	for item := range q.ch {
		q.write(item)
		q.doneMu.Lock()
		q.written = item.seq
		q.doneMu.Unlock()
		q.done.Broadcast()
	}
}

func (q *Queue) write(item queuedEvent) {
	// A panic in one row (a hook, a driver) must not stop the writer, or
	// every later row would wait forever.
	defer func() {
		if recovered := recover(); recovered != nil {
			q.store.recordFailed(item.ctx, item.event, fmt.Errorf("audit writer panicked: %v", recovered))
		}
	}()
	if q.beforeWrite != nil {
		q.beforeWrite()
	}
	ctx, cancel := context.WithTimeout(item.ctx, queuedWriteTimeout)
	defer cancel()
	q.store.recordOrFallBack(ctx, item.event)
}

// RecordOrLog is Store.RecordOrLog with the row written by the queue: the
// line now, the row in order behind the rows queued before it, or by the
// caller when the queue is full or closed.
func (q *Queue) RecordOrLog(ctx context.Context, event Event) {
	logEvent(ctx, event)
	// The request's values (its id for the fallback line) ride along; its
	// cancellation does not, since the request ends before the row is written.
	detached := context.WithoutCancel(ctx)
	q.mu.Lock()
	if !q.closed {
		seq := q.lastSeq + 1
		select {
		case q.ch <- queuedEvent{ctx: detached, event: event, seq: seq}:
			q.lastSeq = seq
			q.mu.Unlock()
			return
		default:
		}
	}
	overflow := !q.closed
	ahead := q.lastSeq
	q.mu.Unlock()
	if overflow {
		q.doneMu.Lock()
		q.overflows++
		q.doneMu.Unlock()
		// The rows ahead of this one go first, so the trail keeps the order;
		// a writer stuck past overflowWait does not hold this one back.
		wait, cancel := context.WithTimeout(detached, q.overflowWait)
		_ = q.waitFor(wait, ahead)
		cancel()
	}
	q.store.recordOrFallBack(ctx, event)
}

// Flush waits until every row queued before it has been written, or ctx
// ends.
func (q *Queue) Flush(ctx context.Context) error {
	q.mu.Lock()
	target := q.lastSeq
	q.mu.Unlock()
	return q.waitFor(ctx, target)
}

func (q *Queue) waitFor(ctx context.Context, target uint64) error {
	q.doneMu.Lock()
	reached := q.written >= target
	q.doneMu.Unlock()
	if reached {
		return nil
	}
	// sync.Cond cannot select on ctx; a helper wakes the waiter when ctx
	// ends.
	stop := context.AfterFunc(ctx, func() {
		q.doneMu.Lock()
		q.done.Broadcast()
		q.doneMu.Unlock()
	})
	defer stop()
	q.doneMu.Lock()
	defer q.doneMu.Unlock()
	for q.written < target {
		if err := ctx.Err(); err != nil {
			return err
		}
		q.done.Wait()
	}
	return nil
}

// Close stops taking rows and waits until every queued row is written, or
// ctx ends (the writer then carries on with what is left). A row handed
// over after Close is written by its caller.
func (q *Queue) Close(ctx context.Context) error {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.ch)
	}
	q.mu.Unlock()
	select {
	case <-q.finished:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("audit queue: %d rows not yet written: %w", len(q.ch), ctx.Err())
	}
}

// Stats reports the queue.
func (q *Queue) Stats() QueueStats {
	q.mu.Lock()
	closed := q.closed
	q.mu.Unlock()
	q.doneMu.Lock()
	defer q.doneMu.Unlock()
	return QueueStats{Capacity: cap(q.ch), Depth: len(q.ch), Written: q.written, Overflowed: q.overflows, Closed: closed}
}

// flushQueue waits, up to limit, for the rows queued before it, so a read
// of the trail sees every write made before the read.
func (s *Store) flushQueue(ctx context.Context, limit time.Duration) {
	if s == nil {
		return
	}
	s.queueMu.Lock()
	q := s.queue
	s.queueMu.Unlock()
	if q == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	_ = q.Flush(ctx)
}
