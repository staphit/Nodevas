package realtime

import (
	"fmt"
	"testing"

	"nodevas/internal/identity"
)

// The hub keeps a bounded number of live documents for everybody. One account
// opening documents under made-up names must not take them all: every other
// project would be left editing alone, or not at all.
func TestOneAccountCannotTakeEveryLiveDocument(t *testing.T) {
	hub := NewHub()
	hub.SetWriteAccess(func(identity.Actor, string) bool { return true })
	attacker := docClientIn(hub, "aaa", "roomA")
	for index := range maxDocSessions {
		hub.openDoc(attacker, fmt.Sprintf("bogus-%03d", index))
	}
	assertOneAccountLeftRoom(t, hub)
}

// A closed document keeps its session for a moment, in case the closer was
// only reloading. Those moments count against whoever left them, or one
// account could fill the table one open-and-close at a time.
func TestOneAccountCannotTakeEveryLiveDocumentOneCloseAtATime(t *testing.T) {
	hub := NewHub()
	hub.SetWriteAccess(func(identity.Actor, string) bool { return true })
	attacker := docClientIn(hub, "aaa", "roomA")
	for index := range maxDocSessions {
		key := fmt.Sprintf("bogus-%03d", index)
		hub.openDoc(attacker, key)
		hub.closeDoc(attacker, key)
	}
	assertOneAccountLeftRoom(t, hub)
}

// assertOneAccountLeftRoom checks that the table is not full and that two
// editors of another project still meet in one session with one leader.
func assertOneAccountLeftRoom(t *testing.T, hub *Hub) {
	t.Helper()
	hub.mu.Lock()
	held := hub.docSessionCountLocked()
	hub.mu.Unlock()
	if held >= maxDocSessions {
		t.Fatalf("one account holds %d of the hub's %d live documents", held, maxDocSessions)
	}

	first := docClientIn(hub, "bbb", "roomB")
	second := docClientIn(hub, "ccc", "roomB")
	hub.openDoc(first, "real")
	hub.openDoc(second, "real")
	hub.mu.Lock()
	members := 0
	if session := hub.docs["roomB"]["real"]; session != nil {
		members = len(session.members)
	}
	hub.mu.Unlock()
	if members != 2 {
		t.Fatalf("two editors of another project share no session: %d members", members)
	}
	leaders := 0
	for _, client := range []*wsClient{first, second} {
		if firstEvent(t, client, "doc-state").Leader {
			leaders++
		}
	}
	if leaders != 1 {
		t.Fatalf("%d leaders for one document, want one", leaders)
	}
}
