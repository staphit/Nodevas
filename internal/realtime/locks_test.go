package realtime

import (
	"fmt"
	"testing"

	"nodevas/internal/identity"
)

// A browser window holds one soft lock, for the editor it has open. One
// account taking locks on made-up node ids would grow a table that goes whole
// to everybody in the room on every change.
func TestOneAccountHoldsABoundedNumberOfLocks(t *testing.T) {
	hub := NewHub()
	account := identity.Actor{ID: "aaa", Name: "aaa", Role: identity.RoleMember}
	windows := []*wsClient{docClient(hub, "aaa-1", identity.RoleMember), docClient(hub, "aaa-2", identity.RoleMember)}
	for _, window := range windows {
		window.actor = account
	}
	other := docClient(hub, "bbb", identity.RoleMember)

	for index := range 100 {
		hub.acquireLock(windows[index%2], fmt.Sprintf("node-%03d", index), false)
	}
	hub.mu.Lock()
	held := len(hub.locks["room"])
	hub.mu.Unlock()
	if held > maxLocksPerActor {
		t.Fatalf("one account holds %d locks, past %d", held, maxLocksPerActor)
	}
	docEvents(t, windows[0])
	hub.acquireLock(windows[0], "one-more", false)
	if denied := firstEvent(t, windows[0], "lock-denied"); denied.ID != "one-more" || denied.Actor != nil {
		t.Fatalf("a lock past the account's share = %+v", denied)
	}

	// Moving a lock between the account's own windows adds none, and the
	// share is the account's alone: somebody else is not refused for it.
	hub.acquireLock(windows[1], "node-000", true)
	hub.acquireLock(other, "theirs", false)
	hub.mu.Lock()
	moved := hub.locks["room"]["node-000"]
	theirs := hub.locks["room"]["theirs"]
	hub.mu.Unlock()
	if moved == nil || moved.client != windows[1] {
		t.Fatalf("a lock could not move between one account's windows: %+v", moved)
	}
	if theirs == nil || theirs.client != other {
		t.Fatalf("another account was refused a lock: %+v", theirs)
	}
}

// A lock change that changes nothing tells the room nothing. The table goes
// whole to every reader in the room, so an unlock of a lock this connection
// does not hold, a lock it already holds, or somebody joining the room must
// not send it to everybody again.
func TestALockChangeThatChangesNothingIsNotPublished(t *testing.T) {
	hub := NewHub()
	hub.SetProjectResolver(func(name string) (string, error) { return name, nil })
	holder := docClient(hub, "aaa", identity.RoleMember)
	reader := docClient(hub, "bbb", identity.RoleMember)
	hub.acquireLock(holder, "alpha", false)
	docEvents(t, holder)
	docEvents(t, reader)

	hub.releaseLock(holder, "never-held")
	hub.releaseLock(reader, "alpha")
	hub.acquireLock(holder, "alpha", false)
	joiner := docClientIn(hub, "ccc", "elsewhere")
	hub.subscribe(joiner, "room")
	if locks := firstEvent(t, joiner, "locks"); len(locks.Locks) != 1 || locks.Locks[0].NodeID != "alpha" {
		t.Fatalf("the joiner was not told who holds what: %+v", locks)
	}
	for _, event := range docEvents(t, reader) {
		if event.Type == "locks" {
			t.Fatalf("a lock change that changed nothing was published: %+v", event)
		}
	}

	// A real change still is.
	hub.releaseLock(holder, "alpha")
	if locks := firstEvent(t, reader, "locks"); len(locks.Locks) != 0 {
		t.Fatalf("a released lock = %+v", locks)
	}
}
