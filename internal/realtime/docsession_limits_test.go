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
	hub.mu.Lock()
	share := hub.actorDocSessionsLocked("aaa")
	hub.mu.Unlock()
	if share > maxDocSessionsPerActor {
		t.Fatalf("one account holds %d live documents, past its share of %d", share, maxDocSessionsPerActor)
	}
	assertOneAccountLeftRoom(t, hub)

	// Past its share, the account still joins a document somebody else
	// opened: that takes no slot.
	joiner := docClientIn(hub, "aaa-other-window", "roomB")
	joiner.actor = identity.Actor{ID: "aaa", Name: "aaa", Role: identity.RoleMember}
	hub.openDoc(joiner, "real")
	hub.mu.Lock()
	joined := memberLocked(hub.docs["roomB"]["real"], joiner)
	hub.mu.Unlock()
	if !joined {
		t.Fatal("an account past its share could not join somebody else's document")
	}
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

// Somebody clicking through documents faster than the reload grace leaves a
// trail of empty sessions behind. Those are the account's own to give up: the
// oldest make way for the next document, so an ordinary person is never told
// to edit alone beside a colleague, and the one just left is kept for a
// reload.
func TestClickingThroughDocumentsNeverLeavesYouEditingAlone(t *testing.T) {
	hub := NewHub()
	hub.SetWriteAccess(func(identity.Actor, string) bool { return true })
	person := docClient(hub, "aaa", identity.RoleMember)
	colleague := docClient(hub, "bbb", identity.RoleMember)
	last := ""
	for index := range 2 * maxDocSessionsPerActor {
		last = fmt.Sprintf("doc-%03d", index)
		hub.openDoc(person, last)
		hub.closeDoc(person, last)
	}
	docEvents(t, person)

	hub.openDoc(person, "next")
	hub.openDoc(colleague, "next")
	hub.mu.Lock()
	members := 0
	if session := hub.docs["room"]["next"]; session != nil {
		members = len(session.members)
	}
	_, kept := hub.docs["room"][last]
	share := hub.actorDocSessionsLocked("aaa")
	hub.mu.Unlock()
	if members != 2 {
		t.Fatalf("after clicking through documents, the next one is shared by %d members, want 2", members)
	}
	leaders := 0
	for _, client := range []*wsClient{person, colleague} {
		if firstEvent(t, client, "doc-state").Leader {
			leaders++
		}
	}
	if leaders != 1 {
		t.Fatalf("%d leaders for one document, want one", leaders)
	}
	if !kept {
		t.Fatal("the document just left lost its reload grace")
	}
	if share > maxDocSessionsPerActor {
		t.Fatalf("the account holds %d live documents, past its share of %d", share, maxDocSessionsPerActor)
	}
}
