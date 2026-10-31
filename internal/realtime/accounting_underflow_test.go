package realtime

import (
	"testing"
	"time"
)

// An accounting bug must not take the hub down with it. Releasing more
// snapshot bytes than were charged used to panic while holding h.mu; the
// websocket writer's recover then called remove, which locks h.mu again, and
// every Broadcast after that waited forever.

func assertBroadcastCompletes(t *testing.T, hub *Hub) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		hub.Broadcast("graph-changed", "")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Broadcast deadlocked after a snapshot accounting underflow")
	}
}

func TestSnapshotJobUnderflowDoesNotDeadlockBroadcast(t *testing.T) {
	hub := NewHub()
	func() {
		// The writer goroutine recovers panics; do the same here so a
		// panicking release is judged by what it leaves behind.
		defer func() { _ = recover() }()
		hub.releaseSnapshotJob(&docOutboundSnapshot{tailBytes: 4096})
	}()
	assertBroadcastCompletes(t, hub)
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.snapshotBytes != 0 {
		t.Fatalf("snapshotBytes = %d after underflow, want clamped to 0", hub.snapshotBytes)
	}
}

func TestSnapshotBlobUnderflowClampsInsteadOfPanicking(t *testing.T) {
	hub := NewHub()
	hub.mu.Lock()
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Errorf("releasing an over-released blob panicked: %v", recovered)
			}
		}()
		hub.releaseSnapshotBlobLocked(&docSnapshotBlob{payload: "abcdef", refs: 1})
	}()
	got := hub.snapshotBytes
	hub.mu.Unlock()
	if got != 0 {
		t.Fatalf("snapshotBytes = %d, want clamped to 0", got)
	}
	assertBroadcastCompletes(t, hub)
}

func TestCompactionUnderflowClampsInsteadOfPanicking(t *testing.T) {
	hub := NewHub()
	session := &docSession{compaction: &docCompaction{assembly: &docSnapshotAssembly{size: 1024}}}
	hub.mu.Lock()
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Errorf("clearing a compaction with uncharged bytes panicked: %v", recovered)
			}
		}()
		hub.clearCompactionLocked(session)
	}()
	got := hub.snapshotBytes
	hub.mu.Unlock()
	if got != 0 {
		t.Fatalf("snapshotBytes = %d, want clamped to 0", got)
	}
	assertBroadcastCompletes(t, hub)
}
