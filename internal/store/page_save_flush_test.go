package store

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/identity"
)

// stallPageFlush makes the disk flush of every write to a file named like
// page hang until the returned release is called, and reports on flushing
// when one has started.
func stallPageFlush(t *testing.T, page string) (flushing <-chan struct{}, release func()) {
	t.Helper()
	started := make(chan struct{}, 16)
	gate := make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	previous := syncFile
	syncFile = func(file *os.File) error {
		if strings.Contains(file.Name(), page) {
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

// A page save spends most of its time waiting for the disk to flush the new
// content (tens of milliseconds on Windows under load). If it holds the store
// lock meanwhile, even shared, a card move that wants the lock exclusively
// waits for the flush, and every reader that arrives behind the move waits
// too: under an auto-saving editor and a few people dragging cards, the board's
// reads queue behind flushes they have nothing to do with. So the flush holds
// no store lock: the move and the reads go on while it is stalled.
func TestAPageSavesDiskFlushHoldsNoStoreLock(t *testing.T) {
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
	time.Sleep(50 * time.Millisecond) // the move is waiting for the lock, or done

	read := make(chan error, 1)
	go func() {
		_, _, err := st.LoadGraph()
		read <- err
	}()
	select {
	case err := <-read:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		release()
		t.Fatal("a graph read waited for another page's disk flush (behind a waiting card move)")
	}
	select {
	case err := <-moved:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		release()
		t.Fatal("a card move waited for another page's disk flush")
	}

	release()
	if err := <-saved; err != nil {
		t.Fatalf("save: %v", err)
	}
	_, content, _, err := st.LoadNodePage("n00000", "page-0001")
	if err != nil {
		t.Fatal(err)
	}
	if content != "# saved while stalled\n" {
		t.Fatalf("page = %q after the save", content)
	}
}
