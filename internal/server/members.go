package server

import (
	"context"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/db"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/identity"
	"nodevas/internal/members"
	"nodevas/internal/project"
)

// Project membership (internal/members). One gate, here, decides for every
// project-scoped request whether its actor may read or write the project the
// request resolved to; the realtime hub gets the same check for its rooms; the
// catalog, search and export filter by it; the member routes manage it.

// UseMembersDB turns project membership on against the workspace database.
// Call it before Handler. A project nobody has added members to stays open
// to every signed-in account, so turning this on changes nothing until
// someone restricts a project.
func (s *Server) UseMembersDB(database *db.DB) {
	s.useDatabase(database)
	store := members.NewStore(database)
	if err := store.Load(context.Background()); err != nil {
		// Failing open here would expose every restricted project, so the
		// store refuses everyone who is not an administrator until it loads.
		log.Printf("project members: %v; restricted projects are closed to everyone but administrators until a restart loads them", err)
		store = members.Unavailable()
	}
	s.members = store
	// Each account's current project lives in the same database.
	s.current = newCurrentProjects(database)
	if err := s.current.load(context.Background()); err != nil {
		// Only the remembered choices are lost: everyone starts on a
		// default they may open, and choosing again saves again.
		log.Printf("current projects: %v; accounts start on their default project", err)
	}
	if s.hub != nil {
		s.hub.SetAccess(func(actor identity.Actor, room string) bool {
			return s.members.Access(actor, project.Key(room)).CanRead()
		})
		s.hub.SetWriteAccess(func(actor identity.Actor, room string) bool {
			return s.members.Access(actor, project.Key(room)).CanWrite()
		})
		store.OnChange(func(string) { s.hub.RecheckAccess() })
	}
}

// accountLevelPaths are /api routes that belong to an account or the server,
// not to the project a request happens to resolve to: a person's run budget
// or copilot conversations do not depend on which project is open. The gate
// leaves them to their own handlers. Everything else under /api that
// ProjectScopedPath calls project-scoped is gated.
var accountLevelPaths = []string{
	"/api/auth/",
	"/api/copilot/",
	"/api/runs/budget",
	"/api/runs/routing",
	// Prices are the server's; /api/usage itself reports one project and is gated.
	"/api/usage/prices",
	"/api/notify/",
	"/api/audit",
	// Agent analytics across projects: the actor's own runs.
	"/api/analytics/scorecards",
	"/api/analytics/tiers",
	"/api/analytics/overview",
	"/api/analytics/retention",
}

// readOnlyPosts are POST routes that change nothing: a viewer may use them.
var readOnlyPosts = map[string]bool{
	"/api/export":      true,
	"/api/export/next": true,
	"/api/dsl/check":   true,
}

// projectlessPaths are the account-level routes that never read the
// request's project, so it is not resolved for them (withRequestProject):
// whatever a request names there, the answer is the same. Some serve
// clients that are not signed in, and a browser still showing a project it
// lost sends that name on every request, which must not keep it from its
// own settings or from signing out.
var projectlessPaths = []string{
	"/api/auth/",
	"/api/runs/budget",
	"/api/runs/routing",
	"/api/usage/prices",
	"/api/notify/",
}

func accountLevelPath(path string) bool {
	return underAny(path, accountLevelPaths)
}

// projectlessPath reports a route that never reads the request's project:
// projectlessPaths, and the health probes.
func projectlessPath(r *http.Request) bool {
	return underAny(r.URL.Path, projectlessPaths) || publicHealthPath(r)
}

func underAny(path string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if path == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// withProjectAccess refuses a project-scoped request whose actor may not read
// the project it resolved to (403 PROJECT_ACCESS_DENIED), or may only read it
// and asked to change something (403 PROJECT_READ_ONLY). It runs after
// withRequestProject, so the project is the one ?project= named or, without
// one, the active project: either way, the one the handler will touch.
func (s *Server) withProjectAccess(c *gin.Context) {
	r := c.Request
	if s.members == nil || !httpx.ProjectScopedPath(r.URL.Path) || accountLevelPath(r.URL.Path) {
		c.Next()
		return
	}
	st := httpx.StoreFor(r, s.pm)
	if st == nil {
		c.Next()
		return
	}
	access := s.members.Access(auth.ActorFrom(r), project.Key(st.Root()))
	if !access.CanRead() {
		httpx.DenyProjectAccess(c)
		return
	}
	writes := (isUnsafeMethod(r.Method) && !readOnlyPosts[r.URL.Path]) || stateChangingGET(r)
	if writes && !access.CanWrite() {
		httpx.DenyProjectWrite(c)
		return
	}
	c.Next()
}

// projectReadable is the catalog's filter: whether the request's actor may
// see the project in dir.
func (s *Server) projectReadable(r *http.Request, dir string) bool {
	if s.members == nil {
		return true
	}
	return s.members.Access(auth.ActorFrom(r), project.Key(dir)).CanRead()
}

// projectWritable is whether the request's actor may change the project in
// dir, for the routes that name a project past the gate (a transfer's
// source and target).
func (s *Server) projectWritable(r *http.Request, dir string) bool {
	if s.members == nil {
		return true
	}
	return s.members.Access(auth.ActorFrom(r), project.Key(dir)).CanWrite()
}

// AccountMayWrite reports whether the account accountID may still change
// the project named name. Work it handed off (an autopilot, a copilot
// follow-up) runs past any request of its own and stops once it may not.
// Without membership everyone may; an account that is gone, or a project
// that no longer resolves, may not.
func (s *Server) AccountMayWrite(ctx context.Context, name, accountID string) bool {
	if s.members == nil {
		return true
	}
	st, err := s.pm.StoreFor(name)
	if err != nil {
		return false
	}
	actor := identity.Actor{ID: accountID}
	if accounts, ok := s.auth.(*auth.SessionAuth); ok {
		current, _, found := accounts.CurrentActor(ctx, accountID)
		if !found {
			return false
		}
		actor = current
	}
	return s.members.Access(actor, project.Key(st.Root())).CanWrite()
}

// projectAccess is the catalog's view of membership.
type projectAccess struct{ s *Server }

func (p projectAccess) Readable(r *http.Request, dir string) bool { return p.s.projectReadable(r, dir) }

func (p projectAccess) Writable(r *http.Request, dir string) bool { return p.s.projectWritable(r, dir) }

// ReadableTree also requires every restricted project under dir to be
// readable: an archive of a folder carries all of them.
func (p projectAccess) ReadableTree(r *http.Request, dir string) bool {
	if !p.s.projectReadable(r, dir) {
		return false
	}
	actor := auth.ActorFrom(r)
	for _, key := range p.s.members.RestrictedUnder(project.Key(dir)) {
		if !p.s.members.Access(actor, key).CanRead() {
			return false
		}
	}
	return true
}

// membershipHooks keeps member lists on their projects when an administrator
// moves, copies or deletes one.
type membershipHooks struct{ store *members.Store }

func (m membershipHooks) Moved(ctx context.Context, from, to string) {
	if err := m.store.Moved(ctx, project.Key(from), project.Key(to)); err != nil {
		log.Printf("project members: follow move %s -> %s: %v", from, to, err)
	}
}

func (m membershipHooks) Copied(ctx context.Context, from, to string) {
	if err := m.store.Copied(ctx, project.Key(from), project.Key(to)); err != nil {
		log.Printf("project members: copy %s -> %s: %v", from, to, err)
	}
}

func (m membershipHooks) Removed(ctx context.Context, dir string) {
	if err := m.store.Removed(ctx, project.Key(dir)); err != nil {
		log.Printf("project members: forget %s: %v", dir, err)
	}
}
