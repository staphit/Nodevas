package project

import (
	"context"
	"errors"
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
	// current is the per-account current project; nil on loopback.
	current Current
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

// Current is each account's own current project on a server with accounts
// (internal/server/current_project.go). Without it the server's one active
// project is everyone's, as on loopback.
type Current interface {
	// Current is the request's actor's current project, false when they may
	// open none.
	Current(r *http.Request) (string, bool)
	// Open makes name the actor's current project and moves their other
	// connections along (ErrNotReadable, ErrAgentSwitch, or a bad name).
	Open(r *http.Request, name string) error
	// Follow records name as the actor's current project after a request
	// that opened it for them, such as an import.
	Follow(r *http.Request, name string)
	// Moved keeps accounts on a project renamed or moved from one name to
	// another within the workspace.
	Moved(ctx context.Context, from, to string)
}

var (
	// ErrNotReadable refuses opening a project the caller may not read.
	ErrNotReadable = errors.New("no such project")
	// ErrAgentSwitch refuses an agent moving its person's current project.
	ErrAgentSwitch = errors.New("an agent works in the project its requests name; it cannot change its person's current project")
)

// UseCurrent turns on per-account current projects.
func (a *API) UseCurrent(current Current) {
	a.current = current
	a.pm.SetPerAccount(current != nil)
}

// currentName is the project a request that names none works in: the actor's
// current project with per-account projects, "" (the active one) otherwise.
func (a *API) currentName(r *http.Request) string {
	if a.current == nil {
		return ""
	}
	name, _ := a.current.Current(r)
	return name
}

// follow makes name the caller's current project after this request opened
// it; the active project already moved on loopback.
func (a *API) follow(r *http.Request, name string) {
	if a.current != nil && name != "" {
		a.current.Follow(r, name)
	}
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
