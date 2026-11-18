package graph

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/project"
	"nodevas/internal/realtime"
	"nodevas/internal/store"
)

// API serves this package's endpoints. It holds only what these
// handlers need, so the router wires each group explicitly.
type API struct {
	pm  *project.ProjectManager
	hub *realtime.Hub
	// activeRun, when set, reports whether a run that has not ended works
	// on the node; batch undo keeps such a node. Wired by the server, which
	// holds the run records.
	activeRun func(ctx context.Context, st *store.Store, nodeID string) bool
	// planDraft, when set, records a draft plan run's create_subgraph
	// instead of writing it (P6, SetPlanDraft).
	planDraft PlanDraftCapture
}

// SetActiveRun gives batch undo a way to see runs (internal/runs), which this
// handler set does not otherwise reach.
func (a *API) SetActiveRun(check func(ctx context.Context, st *store.Store, nodeID string) bool) *API {
	a.activeRun = check
	return a
}

// New builds the handler set.
func New(pm *project.ProjectManager, hub *realtime.Hub) *API {
	return &API{pm: pm, hub: hub}
}

// notifyRoom tells the clients watching this request's project that it
// changed. Broadcasting to everyone would reload windows showing a different
// project for an edit they cannot see.
func (a *API) notifyRoom(r *http.Request, evType, id string) {
	st := httpx.StoreFor(r, a.pm)
	if st == nil {
		a.hub.Broadcast(evType, id)
		return
	}
	a.hub.BroadcastRoom(st.Root(), evType, id)
}

// notifyNodeFiles announces the node documents a graph write rewrote (their
// frontmatter mirrors the graph). The store's own writes are hidden from the
// file watcher, so without this an editor holding one of them keeps a revision
// that no longer exists, and its next save is refused as a conflict.
func (a *API) notifyNodeFiles(r *http.Request, ids []string) {
	for _, id := range ids {
		a.notifyRoom(r, "node-changed", id)
	}
}

// PeerHeader names the connection a request was made from, so the change it
// causes can be sent to the room without bouncing back to its author.
const PeerHeader = "X-Nodevas-Peer"

// notifyOps tells the room exactly what changed. A client that understands the
// ops moves the one card they touched; one that does not falls back to
// reloading, which is what it did for every edit before this.
func (a *API) notifyOps(r *http.Request, ops json.RawMessage) {
	room := ""
	if st := httpx.StoreFor(r, a.pm); st != nil {
		room = st.Root()
	}
	peer := r.Header.Get(PeerHeader)
	// A peer id is echoed back to every client in the room, so it is bounded
	// and kept to the alphabet the hub mints them from.
	if len(peer) > 32 || strings.ContainsFunc(peer, func(r rune) bool {
		return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F')
	}) {
		peer = ""
	}
	a.hub.BroadcastOps(room, peer, ops)
}
