package store

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nodevas/internal/identity"
)

// stallPageFlush makes the first disk flush of a write to a file named like
// page (the new content's) hang until the returned release is called, and
// reports on flushing when it has started.
func stallPageFlush(t *testing.T, page string) (flushing <-chan struct{}, release func()) {
	t.Helper()
	started := make(chan struct{}, 16)
	gate := make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	var stalled atomic.Bool
	previous := syncFile
	syncFile = func(file *os.File) error {
		if strings.Contains(file.Name(), page) && stalled.CompareAndSwap(false, true) {
			started <- struct{}{}
			<-gate
		}
		return previous(file)
	}
	t.Cleanup(func() {
		release()
		syncFile = previous
	})
	return started, release
}

// A board's reads (the graph, a node, its page list) must not queue behind
// the store's writers. A page save holds the store lock shared through its
// write and disk flush (tens of milliseconds on Windows under load); a card
// move that wants the lock exclusively waits for it, and with the lock
// preferring a waiting writer, every reader that arrives after the move used
// to wait too. Under an auto-saving editor and a few people dragging cards,
// that made a mixed load's reads five times slower. These reads take no store
// lock, so they answer while the flush is stalled and the move waits.
func TestReadsDoNotQueueBehindAWriterWaitingOnAPageSave(t *testing.T) {
	st := benchPagesStore(t, 3, 1)
	actor := identity.Actor{ID: "ann", Name: "Ann"}
	_, _, rev, err := st.LoadNodePage("n00000", "page-0001")
	if err != nil {
		t.Fatal(err)
	}
	flushing, release := stallPageFlush(t, "page-0001")

	saved := make(chan error, 1)
	go func() {
		_, err := st.SaveNodePage(actor, "n00000", "page-0001", "# saved while stalled\n", rev)
		saved <- err
	}()
	select {
	case <-flushing:
	case err := <-saved:
		t.Fatalf("the save finished without flushing: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the page save never reached its flush")
	}

	moved := make(chan error, 1)
	go func() {
		x, y := 40.0, 80.0
		_, _, err := st.ApplyGraphOps(actor, []GraphOp{{Kind: "move", NodeID: "n00001", X: &x, Y: &y}})
		moved <- err
	}()
	time.Sleep(50 * time.Millisecond) // the move is waiting for the lock

	reads := map[string]func() error{
		"graph":     func() error { _, _, err := st.LoadGraph(); return err },
		"node":      func() error { _, _, err := st.LoadNodeContent("n00002"); return err },
		"page list": func() error { _, err := st.ListNodePages("n00002"); return err },
	}
	for name, read := range reads {
		done := make(chan error, 1)
		go func() { done <- read() }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s read: %v", name, err)
			}
		case <-time.After(5 * time.Second):
			release()
			t.Fatalf("a %s read waited behind a card move waiting on another page's disk flush", name)
		}
	}

	release()
	if err := <-saved; err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := <-moved; err != nil {
		t.Fatalf("move: %v", err)
	}
	_, content, _, err := st.LoadNodePage("n00000", "page-0001")
	if err != nil {
		t.Fatal(err)
	}
	if content != "# saved while stalled\n" {
		t.Fatalf("page = %q after the save", content)
	}
}
