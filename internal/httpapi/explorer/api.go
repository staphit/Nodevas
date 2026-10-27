// Package explorer serves the Explorer sidebar: the workspace's own files
// and history, read-only browsing of the viewer's registered repos through
// their own runners, and the agents working the project.
//
// The hub runs no process for any of it: workspace history is the store's
// own git backend, and a repo is only ever read by its runner.
package explorer

import (
	"errors"
	"net/http"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"nodevas/internal/agents"
	"nodevas/internal/auth"
	"nodevas/internal/db"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/identity"
	"nodevas/internal/project"
	"nodevas/internal/runners"
	"nodevas/internal/runs"
	"nodevas/internal/store"
)

// Options are what the Explorer reads.
type Options struct {
	PM      *project.ProjectManager
	Runners *runners.Registry
	Repo    *runners.RepoRelay
	Runs    *runs.Store
	Agents  *agents.Hub
	DB      *db.DB
}

// API serves /api/explorer/*.
type API struct{ o Options }

// New builds the handlers.
func New(o Options) *API { return &API{o: o} }

// person is the request's actor, or a 403 for an agent: the Explorer is a
// person's view.
func person(c *gin.Context) (identity.Actor, bool) {
	actor := auth.ActorFrom(c.Request)
	if actor.IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", errors.New("the Explorer is for people, not agents"), nil)
		return actor, false
	}
	return actor, true
}

// project is the request's store, its name as ?project= takes it, and its
// realtime key (the root).
func (a *API) project(c *gin.Context) (*store.Store, string, bool) {
	st := httpx.StoreFor(c.Request, a.o.PM)
	if a.o.PM == nil || st == nil {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", errors.New("no project selected"), nil)
		return nil, "", false
	}
	rel, err := filepath.Rel(a.o.PM.Workspace(), st.Root())
	if err != nil {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", errors.New("no project selected"), nil)
		return nil, "", false
	}
	return st, filepath.ToSlash(rel), true
}
