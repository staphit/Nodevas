package remoteapi

import (
	"nodevas/internal/project"
	"nodevas/internal/remote"
)

// API serves this package's endpoints. It holds only what these
// handlers need, so the router wires each group explicitly.
type API struct {
	pm      *project.ProjectManager
	remotes *remote.RemoteManager
}

// New builds the handler set.
func New(pm *project.ProjectManager, remotes *remote.RemoteManager) *API {
	return &API{pm: pm, remotes: remotes}
}
