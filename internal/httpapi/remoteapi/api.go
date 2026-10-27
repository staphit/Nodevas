package remoteapi

import (
	"net/http"

	"nodevas/internal/project"
	"nodevas/internal/remote"
)

// API serves this package's endpoints. It holds only what these
// handlers need, so the router wires each group explicitly.
type API struct {
	pm      *project.ProjectManager
	remotes *remote.RemoteManager
	// current names the caller's own current project when a push names
	// none, on a server where each account has one; nil means the active one.
	current func(*http.Request) string
}

// New builds the handler set.
func New(pm *project.ProjectManager, remotes *remote.RemoteManager) *API {
	return &API{pm: pm, remotes: remotes}
}

// UseCurrent resolves an unnamed project to the caller's current one.
func (a *API) UseCurrent(current func(*http.Request) string) *API {
	a.current = current
	return a
}
