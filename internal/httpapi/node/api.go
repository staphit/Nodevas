package node

import (
	"net/http"

	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/project"
	"nodevas/internal/realtime"
)

// API serves this package's endpoints. It holds only what these
// handlers need, so the router wires each group explicitly.
type API struct {
	pm  *project.ProjectManager
	hub *realtime.Hub
	// access checks the projects a request names in its body (a
	// transfer's source and target), which the per-request project gate
	// never sees; nil lets everyone reach every project, as on a server
	// without membership.
	access Access
}

// Access answers whether a request's actor may read or change the project
// in dir (internal/members).
type Access interface {
	Readable(r *http.Request, dir string) bool
	Writable(r *http.Request, dir string) bool
}

// New builds the handler set.
func New(pm *project.ProjectManager, hub *realtime.Hub) *API {
	return &API{pm: pm, hub: hub}
}

// UseAccess turns on membership checks for the projects a body names.
func (a *API) UseAccess(access Access) {
	a.access = access
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
