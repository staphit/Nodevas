package realtime

import (
	"nodevas/internal/identity"
	"sync"
	"testing"
)

// writers is a write check naming who may change "room".
type writers struct {
	mu  sync.Mutex
	ids map[string]bool
}

func (w *writers) allow(id string, may bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ids[id] = may
}

func (w *writers) check(actor identity.Actor, room string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return room == "room" && w.ids[actor.ID]
}

func connected(hub *Hub, client *wsClient) bool {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	_, live := hub.conns[client]
	return live
}

// A viewer must not be able to type into a document an editor has open: its
// updates would be stored, relayed to the editor and flushed to the file by
// the editor's leader, so read-only membership would mean nothing.
func TestAViewerCannotInjectIntoALiveDocument(t *testing.T) {
	hub := NewHub()
	check := &writers{ids: map[string]bool{"bbb": true}}
	hub.SetWriteAccess(check.check)
	editor := docClient(hub, "bbb", identity.RoleMember)
	viewer := docClient(hub, "aaa", identity.RoleMember) // the lower id would lead

	hub.openDoc(editor, "alpha")
	if state := firstEvent(t, editor, "doc-state"); !state.Leader || !state.Seed {
		t.Fatalf("the editor did not seed and lead: %+v", state)
	}
	hub.acquireLock(editor, "alpha", false)
	docEvents(t, editor)

	hub.openDoc(viewer, "alpha")
	state := firstEvent(t, viewer, "doc-state")
	if state.Leader || !state.Seed {
		t.Fatalf("the viewer was not given a read-only seed: %+v", state)
	}
	hub.mu.Lock()
	session := hub.docs["room"]["alpha"]
	member := memberLocked(session, viewer)
	leader := session.leader
	hub.mu.Unlock()
	if member || leader != editor {
		t.Fatalf("the viewer joined the session (member=%v, leader=%s)", member, leader.id)
	}

	hub.docUpdate(viewer, "alpha", "INJECTED")
	hub.docSnapshot(viewer, "alpha", "INJECTED")
	hub.docCompactRequest(viewer, "alpha")
	hub.docFlushed(viewer, "alpha", "rev")
	hub.acquireLock(viewer, "alpha", true)
	hub.handleMessage(viewer, clientMessage{Type: "graph-drag", IDs: []string{"alpha"}, Active: true})

	for _, event := range docEvents(t, editor) {
		switch event.Type {
		case "doc-update", "doc-flushed", "graph-drag", "doc-leader":
			t.Fatalf("the viewer reached the editor: %+v", event)
		}
	}
	hub.mu.Lock()
	logged := len(session.log)
	lock := hub.locks["room"]["alpha"]
	hub.mu.Unlock()
	if logged != 0 {
		t.Fatalf("the viewer's update was stored: %d entries", logged)
	}
	if lock == nil || lock.client != editor {
		t.Fatalf("the viewer took the editor's lock: %+v", lock)
	}
	if denied := firstEvent(t, viewer, "lock-denied"); denied.ID != "alpha" {
		t.Fatalf("the viewer's lock was not refused: %+v", denied)
	}
}

// With a write check installed, the catch-all room is read-only: no
// project's membership covers it.
func TestTheCatchAllRoomIsReadOnlyUnderAWriteCheck(t *testing.T) {
	hub := NewHub()
	hub.SetWriteAccess(func(identity.Actor, string) bool { return true })
	client := docClientIn(hub, "aaa", "")
	hub.openDoc(client, "alpha")
	if state := firstEvent(t, client, "doc-state"); state.Leader || !state.Seed {
		t.Fatalf("the catch-all room handed out a writable document: %+v", state)
	}
	hub.mu.Lock()
	sessions := len(hub.docs[""])
	hub.mu.Unlock()
	if sessions != 0 {
		t.Fatal("a session was opened in the catch-all room")
	}
}

// An editor demoted to viewer gives up its documents and locks at once; one
// that held neither keeps its connection.
func TestRecheckAccessDisconnectsADemotedWriter(t *testing.T) {
	hub := NewHub()
	check := &writers{ids: map[string]bool{"aaa": true, "bbb": true, "ccc": true}}
	hub.SetWriteAccess(check.check)
	demoted := docClient(hub, "aaa", identity.RoleMember)
	peer := docClient(hub, "bbb", identity.RoleMember)
	idle := docClient(hub, "ccc", identity.RoleMember)
	hub.openDoc(demoted, "alpha")
	hub.openDoc(peer, "alpha")
	hub.acquireLock(demoted, "beta", false)

	check.allow("aaa", false)
	check.allow("ccc", false)
	hub.RecheckAccess()

	if connected(hub, demoted) {
		t.Fatal("the demoted writer kept its connection")
	}
	if !connected(hub, idle) || !connected(hub, peer) {
		t.Fatal("a client with nothing to give up was disconnected")
	}
	hub.mu.Lock()
	session := hub.docs["room"]["alpha"]
	member := memberLocked(session, demoted)
	leader := session.leader
	lock := hub.locks["room"]["beta"]
	hub.mu.Unlock()
	if member || leader != peer || lock != nil {
		t.Fatalf("the demoted writer kept its place: member=%v leader=%s lock=%+v", member, leader.id, lock)
	}
}
