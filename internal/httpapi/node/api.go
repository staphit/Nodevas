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
