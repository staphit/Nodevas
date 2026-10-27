// Package members serves a project's member list (internal/members):
//
//	GET    /api/projects/access?project=        the caller's own access
//	GET    /api/projects/members?project=       the list (anyone who may read the project)
//	PUT    /api/projects/members                {project, user, role}: add or change (owners, admins)
//	DELETE /api/projects/members?project=&userId=   remove (owners, admins)
//	POST   /api/projects/members/open           {project}: drop every member, opening the project
//
// The project is named as ?project= names it everywhere else; empty is the
// active project. These routes sit under /api/projects, which the per-request
// project middleware and the central access gate leave alone, so each handler
// resolves the project and checks access itself.
package members

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/members"
	"nodevas/internal/project"
)

// FindAccount resolves an account by exact name or sign-in address.
type FindAccount func(ctx context.Context, nameOrEmail string) (id, name string, ok bool)

// API serves the member routes.
type API struct {
	pm    *project.ProjectManager
	store *members.Store
	find  FindAccount
	// current names the caller's own current project when the request names
	// none, on a server where each account has one; nil means the active one.
	current func(*http.Request) string
}

// New builds the handler set. find may be nil on a server without accounts,
// where membership has nobody to name.
func New(pm *project.ProjectManager, store *members.Store, find FindAccount) *API {
	return &API{pm: pm, store: store, find: find}
}

// UseCurrent resolves an unnamed project to the caller's current one.
func (a *API) UseCurrent(current func(*http.Request) string) *API {
	a.current = current
	return a
}

// Register wires the routes.
func (a *API) Register(api *gin.RouterGroup) {
	api.GET("/projects/access", a.getAccess)
	api.GET("/projects/members", a.list)
	api.PUT("/projects/members", a.put)
	api.DELETE("/projects/members", a.remove)
	api.POST("/projects/members/open", a.open)
}

// target resolves the named project to its name and key, refusing (as a 404,
// so probing names teaches nothing) a project the caller may not read.
func (a *API) target(c *gin.Context, name string) (string, string, members.Access, bool) {
	name = strings.Trim(strings.TrimSpace(name), "/")
	if name == "" && a.current != nil {
		name = a.current(c.Request)
		if name == "" {
			httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", errors.New("no project selected"), nil)
			return "", "", members.Access{}, false
		}
	}
	var dir string
	if name == "" {
		active, err := a.pm.ActiveProject()
		if err != nil {
			httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", errors.New("no project selected"), nil)
			return "", "", members.Access{}, false
		}
		name, dir = active.Name, active.Path
	} else {
		st, err := a.pm.StoreFor(name)
		if err != nil {
			httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", errors.New("no such project"), nil)
			return "", "", members.Access{}, false
		}
		dir = st.Root()
	}
	key := project.Key(dir)
	access := a.store.Access(auth.ActorFrom(c.Request), key)
	if !access.CanRead() {
		httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", errors.New("no such project"), nil)
		return "", "", members.Access{}, false
	}
	// These routes name their project themselves, so the audit row of a
	// change would file it under the active project; this names the project
	// it was made to.
	audit.Annotate(c.Request.Context(), audit.Annotation{Project: dir})
	return name, key, access, true
}

func (a *API) getAccess(c *gin.Context) {
	name, key, access, ok := a.target(c, c.Query("project"))
	if !ok {
		return
	}
	actor := auth.ActorFrom(c.Request)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"project":    name,
		"restricted": access.Restricted,
		"role":       access.EffectiveRole(),
		"member":     access.Role,
		"admin":      access.Admin,
		"canWrite":   access.CanWrite(),
		"canManage":  a.find != nil && a.store.CanManage(actor, key),
	})
}

func (a *API) list(c *gin.Context) {
	name, key, _, ok := a.target(c, c.Query("project"))
	if !ok {
		return
	}
	a.respond(c, name, key)
}

// respond answers with the project's member list as it stands now.
func (a *API) respond(c *gin.Context, name, key string) {
	access := a.store.Access(auth.ActorFrom(c.Request), key)
	list, err := a.store.List(c.Request.Context(), key)
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"project":    name,
		"restricted": access.Restricted,
		"members":    list,
	})
}

func (a *API) put(c *gin.Context) {
	var body struct {
		Project string `json:"project"`
		// User is an account's exact name or sign-in address, or its id.
		User   string `json:"user"`
		UserID string `json:"userId"`
		Role   string `json:"role"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	name, key, _, ok := a.target(c, body.Project)
	if !ok {
		return
	}
	if a.find == nil {
		httpx.Coded(c, http.StatusConflict, "FAILED_PRECONDITION",
			errors.New("project members need accounts; this server has none"), nil)
		return
	}
	role, valid := members.ParseRole(body.Role)
	if !valid {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", errors.New("role must be owner, editor or viewer"), nil)
		return
	}
	actor := auth.ActorFrom(c.Request)
	if !a.store.CanManage(actor, key) {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", members.ErrForbidden, nil)
		return
	}
	userID := strings.TrimSpace(body.UserID)
	if userID == "" {
		id, _, found := a.find(c.Request.Context(), body.User)
		if !found {
			httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", errors.New("no account has that name or sign-in address"), nil)
			return
		}
		userID = id
	} else if !a.isMember(c.Request.Context(), key, userID) {
		// An id is only accepted for someone already on the list (changing
		// their role); new members are named, so ids cannot be guessed at.
		httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", errors.New("no such member"), nil)
		return
	}
	if err := a.store.Set(c.Request.Context(), actor, key, userID, role); err != nil {
		a.fail(c, err)
		return
	}
	// A membership change changes what an account may reach, so its audit
	// row names the account, and the role it was given.
	audit.Annotate(c.Request.Context(), audit.Annotation{Target: userID, Detail: map[string]any{"role": string(role)}})
	a.respond(c, name, key)
}

func (a *API) remove(c *gin.Context) {
	name, key, _, ok := a.target(c, c.Query("project"))
	if !ok {
		return
	}
	userID := strings.TrimSpace(c.Query("userId"))
	if err := a.store.Remove(c.Request.Context(), auth.ActorFrom(c.Request), key, userID); err != nil {
		a.fail(c, err)
		return
	}
	audit.Annotate(c.Request.Context(), audit.Annotation{Target: userID})
	a.respond(c, name, key)
}

func (a *API) open(c *gin.Context) {
	var body struct {
		Project string `json:"project"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	name, key, _, ok := a.target(c, body.Project)
	if !ok {
		return
	}
	if err := a.store.Open(c.Request.Context(), auth.ActorFrom(c.Request), key); err != nil {
		a.fail(c, err)
		return
	}
	a.respond(c, name, key)
}

func (a *API) isMember(ctx context.Context, key, userID string) bool {
	list, err := a.store.List(ctx, key)
	if err != nil {
		return false
	}
	for _, m := range list {
		if m.UserID == userID {
			return true
		}
	}
	return false
}

func (a *API) fail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, members.ErrForbidden):
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", err, nil)
	case errors.Is(err, members.ErrLastOwner):
		httpx.Coded(c, http.StatusConflict, "FAILED_PRECONDITION", err, nil)
	case errors.Is(err, members.ErrInvalid):
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", err, nil)
	default:
		httpx.Err(c, http.StatusInternalServerError, err)
	}
}
