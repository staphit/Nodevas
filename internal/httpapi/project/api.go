package project

import (
	"nodevas/internal/project"
)

// API serves the workspace and project endpoints. It exists so the handlers
// can be methods without living in the project package: transport stays out
// of the domain, and the domain type stays free of gin.
type API struct {
	pm *project.ProjectManager
}

// New builds the handler set.
func New(pm *project.ProjectManager) *API {
	return &API{pm: pm}
}

// visibleHostPath keeps filesystem paths inside the desktop trust boundary.
// Remote clients address projects by catalog name, never by server path.
func (a *API) visibleHostPath(path string) string {
	if a.pm != nil && a.pm.IsRemote() {
		return ""
	}
	return path
}
