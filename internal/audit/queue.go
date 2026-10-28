package audit

import "context"

// QueueStats reports a Queue.
type QueueStats struct {
	Capacity   int    `json:"capacity"`
	Depth      int    `json:"depth"`
	Written    uint64 `json:"written"`
	Overflowed uint64 `json:"overflowed"`
	Closed     bool   `json:"closed"`
}

// Queue writes audit rows.
type Queue struct {
	store       *Store
	beforeWrite func()
}

// NewQueue makes a queue for store.
func NewQueue(store *Store, size int) *Queue { return &Queue{store: store} }

// RecordOrLog is Store.RecordOrLog.
func (q *Queue) RecordOrLog(ctx context.Context, event Event) { q.store.RecordOrLog(ctx, event) }

// Close stops the queue.
func (q *Queue) Close(ctx context.Context) error { return nil }

// Stats reports the queue.
func (q *Queue) Stats() QueueStats { return QueueStats{} }
