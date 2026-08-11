package remoteapi

import (
	"nodevas/internal/auth"
	"nodevas/internal/project"
	"nodevas/internal/remote"
)

// API serves this package's endpoints. It holds only what these
// handlers need, so the router wires each group explicitly.
type API struct {
	pm      *project.ProjectManager
	remotes *remote.RemoteManager
	auth    auth.Authenticator
}

// New builds the handler set.
func New(pm *project.ProjectManager, remotes *remote.RemoteManager, authenticator auth.Authenticator) *API {
	return &API{pm: pm, remotes: remotes, auth: authenticator}
}
