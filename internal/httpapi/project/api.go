package project

import (
	"context"
	"net/http"

	"nodevas/internal/project"
)

// API serves the workspace and project endpoints. It exists so the handlers
// can be methods without living in the project package: transport stays out
// of the domain, and the domain type stays free of gin.
type API struct {
	pm *project.ProjectManager
	// access filters what a caller may see (internal/members); nil lets
	// everyone see everything, as on a server without membership.
	access Access
	// membership follows a project's members through moves, copies and
	// deletes; nil when membership is off.
	membership Membership
}

// Access answers whether a request's actor may read projects.
type Access interface {
	// Readable: the project in dir.
	Readable(r *http.Request, dir string) bool
	// ReadableTree: the project in dir and every project under it, which is
	// what an archive of dir would contain.
	ReadableTree(r *http.Request, dir string) bool
}

// Membership is told about the project directories an administrator moved,
// copied or deleted, so member lists stay with their projects.
type Membership interface {
	Moved(ctx context.Context, from, to string)
	Copied(ctx context.Context, from, to string)
	Removed(ctx context.Context, dir string)
}

// UseMembers turns on membership filtering and bookkeeping.
func (a *API) UseMembers(access Access, membership Membership) {
	a.access, a.membership = access, membership
}

func (a *API) readable(r *http.Request, dir string) bool {
	return a.access == nil || a.access.Readable(r, dir)
}

// readableProjects drops the projects the caller may not read; folders stay.
func (a *API) readableProjects(r *http.Request, projects []project.ProjectInfo) []project.ProjectInfo {
	if a.access == nil {
		return projects
	}
	out := make([]project.ProjectInfo, 0, len(projects))
	for _, p := range projects {
		if p.IsFolder || a.readable(r, p.Path) {
			out = append(out, p)
		}
	}
	return out
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
