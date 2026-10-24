package realtime

import "nodevas/internal/identity"

// Project access (internal/members). A room is a project directory; whether
// an actor may watch it is the server's call, through the function given to
// SetAccess. Without one, every actor may watch every room, as before.
//
// Three places honour it:
//   - subscribe: a client asking for a room it may not watch is parked in a
//     room of its own, which no broadcast names and nobody else shares, so it
//     neither receives the project's events nor learns who is in it;
//   - the catch-all room: a client that never subscribed receives a room's
//     events only if it may watch that room;
//   - RecheckAccess: after a membership change, clients that lost access are
//     moved out of the room they were in.
//
// Writing is a second check, given to SetWriteAccess: a client that may watch
// a room but not change it (a viewer) never joins a live document there —
// doc-open answers it with a read-only seed, so none of its CRDT updates is
// stored, relayed or flushed by an editor's leader — and it cannot take a
// node's soft lock or drag cards in front of the room. Without the function,
// every watcher may write, as before; with it, the catch-all room is
// read-only, since no project's membership covers it.

// AccessFunc reports whether actor may watch room (or, for SetWriteAccess,
// change it).
type AccessFunc func(actor identity.Actor, room string) bool

// deniedRoomPrefix starts the private room of a client refused its project.
// It is not a directory, so nothing persists under it, and each client gets
// its own, so refused clients do not see each other either.
const deniedRoomPrefix = "\x00denied:"

// accessHolder keeps the function out of h.mu: broadcast filters run with the
// hub's lock held and must not take it again.
type accessHolder struct {
	fn AccessFunc
}

// SetAccess installs the access check. Call it before serving.
func (h *Hub) SetAccess(fn AccessFunc) {
	h.access.Store(&accessHolder{fn: fn})
}

func (h *Hub) accessFunc() AccessFunc {
	holder, _ := h.access.Load().(*accessHolder)
	if holder == nil {
		return nil
	}
	return holder.fn
}

// SetWriteAccess installs the write check. Call it before serving.
func (h *Hub) SetWriteAccess(fn AccessFunc) {
	h.writeAccess.Store(&accessHolder{fn: fn})
}

// mayWrite reports whether actor may change room: join its live documents,
// lock its nodes, drag its cards.
func (h *Hub) mayWrite(actor identity.Actor, room string) bool {
	holder, _ := h.writeAccess.Load().(*accessHolder)
	if holder == nil || holder.fn == nil {
		return true
	}
	if room == "" || isDeniedRoom(room) {
		return false
	}
	return holder.fn(actor, room)
}

// mayWatch reports whether actor may watch room. Rooms that are not projects
// (the catch-all, a private denied room) are not checked here.
func (h *Hub) mayWatch(actor identity.Actor, room string) bool {
	if room == "" || isDeniedRoom(room) {
		return true
	}
	fn := h.accessFunc()
	return fn == nil || fn(actor, room)
}

func isDeniedRoom(room string) bool {
	return len(room) >= len(deniedRoomPrefix) && room[:len(deniedRoomPrefix)] == deniedRoomPrefix
}

// RecheckAccess moves every client that may no longer watch its room into a
// private one, releasing its locks and documents there and telling it why.
// A client that may still watch but no longer write is disconnected instead:
// its documents and locks are released, and when it reconnects it reopens
// them read-only. Call it after a project's members change.
func (h *Hub) RecheckAccess() {
	h.mu.Lock()
	clients := make([]*wsClient, 0, len(h.conns))
	for client := range h.conns {
		clients = append(clients, client)
	}
	h.mu.Unlock()
	for _, client := range clients {
		client.mu.Lock()
		room, actor, project := client.room, client.actor, client.project
		client.mu.Unlock()
		if isDeniedRoom(room) {
			continue
		}
		if room != "" && !h.mayWatch(actor, room) {
			h.deny(client, room, project)
			continue
		}
		if !h.mayWrite(actor, room) && h.writesIn(client, room) {
			h.remove(client)
		}
	}
}

// deny parks client in its private room, leaving previous.
func (h *Hub) deny(client *wsClient, previous, project string) {
	client.mu.Lock()
	client.room = deniedRoomPrefix + client.id
	client.nodeID = ""
	client.editing = false
	client.mu.Unlock()
	if previous != "" && !isDeniedRoom(previous) {
		h.releaseAll(client, previous)
		_ = h.leaveAllDocs(client)
		h.publishPresence(previous)
	}
	h.sendTo(client, Event{Type: "access-denied", Project: project})
}

// writesIn reports whether client holds a lock or a live document in room:
// what a client that lost write access must give up.
func (h *Hub) writesIn(client *wsClient, room string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, held := range h.locks[room] {
		if held.client == client {
			return true
		}
	}
	for _, session := range h.docs[room] {
		if memberLocked(session, client) {
			return true
		}
	}
	return false
}
