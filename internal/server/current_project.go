package server

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync"

	"nodevas/internal/auth"
	"nodevas/internal/db"
	projectapi "nodevas/internal/httpapi/project"
	"nodevas/internal/store"
)

// Each account's current project, on a server with accounts.
//
// On loopback there is one person, and the project manager's active project
// is theirs, as it always was. With accounts that one value would be shared by
// everybody: one person switching would move everyone else, and a person who
// may not open it would land on a "not a member" banner. So each account keeps
// its own current project, and a request that names no project (?project= or
// X-Nodevas-Project) is served from it. The account's browser tabs, its MCP
// sessions and its runners' proxied calls all act as the account, so they all
// resolve the same one; a request that names a project is never redirected.
//
// The choice is a settings row per account, so it survives a restart without
// a schema change. An account that never chose, or whose choice it can no
// longer open (deleted, detached, moved away, membership revoked), gets a
// default: the server's active project if it may read it, else the first
// project in the catalog it may read. The default is not written back, so a
// project opening up later is picked up without anyone choosing again.

// currentProjectSetting is the settings key holding an account's choice.
func currentProjectSetting(userID string) string {
	return "account." + userID + ".current_project"
}

const (
	currentProjectSettingPrefix = "account."
	currentProjectSettingSuffix = ".current_project"
)

// currentProjects is every account's chosen project, by account id, as the
// name ?project= takes. The table is small (one row per person), so it is
// held in memory and read on every request without a query.
type currentProjects struct {
	// database persists the choices; nil keeps them in memory only.
	database *db.DB

	mu    sync.RWMutex
	names map[string]string
}

func newCurrentProjects(database *db.DB) *currentProjects {
	return &currentProjects{database: database, names: map[string]string{}}
}

// load reads every saved choice.
func (c *currentProjects) load(ctx context.Context) error {
	if c.database == nil {
		return nil
	}
	rows, err := c.database.QueryContext(ctx,
		`SELECT key, value FROM settings WHERE key LIKE 'account.%.current_project'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	names := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return err
		}
		// LIKE's _ matches any character; the suffix check is exact.
		if !strings.HasPrefix(key, currentProjectSettingPrefix) ||
			!strings.HasSuffix(key, currentProjectSettingSuffix) {
			continue
		}
		user := strings.TrimSuffix(strings.TrimPrefix(key, currentProjectSettingPrefix), currentProjectSettingSuffix)
		if user != "" && value != "" {
			names[user] = value
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.names = names
	c.mu.Unlock()
	return nil
}

func (c *currentProjects) get(userID string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.names[userID]
}

// set records an account's choice. The in-memory copy is updated even when
// the write fails: the person did switch, and only the restart would forget.
func (c *currentProjects) set(ctx context.Context, userID, name string) error {
	c.mu.Lock()
	c.names[userID] = name
	c.mu.Unlock()
	if c.database == nil {
		return nil
	}
	return c.database.SetSetting(ctx, currentProjectSetting(userID), name)
}

// moved follows a project (and everything under it) to its new name, so the
// people working in it stay there when an administrator renames or moves it.
func (c *currentProjects) moved(ctx context.Context, from, to string) {
	c.mu.Lock()
	changed := map[string]string{}
	for user, name := range c.names {
		switch {
		case name == from:
			changed[user] = to
		case strings.HasPrefix(name, from+"/"):
			changed[user] = to + strings.TrimPrefix(name, from)
		}
	}
	for user, name := range changed {
		c.names[user] = name
	}
	c.mu.Unlock()
	if c.database == nil {
		return
	}
	for user, name := range changed {
		if err := c.database.SetSetting(ctx, currentProjectSetting(user), name); err != nil {
			log.Printf("current project: follow move for %s: %v", user, err)
		}
	}
}

// perAccountProjects reports whether each account has its own current
// project: a server with accounts. Loopback keeps the one active project.
func (s *Server) perAccountProjects() bool {
	return s.users != nil && s.pm != nil && s.current != nil
}

// currentProject is the project a request that names none is served from on a
// server with accounts: the actor's choice, or the default above. It reports
// false when the actor may open no project at all, and on loopback.
func (s *Server) currentProject(r *http.Request) (string, *store.Store, bool) {
	if !s.perAccountProjects() {
		return "", nil, false
	}
	actor := auth.ActorFrom(r)
	if actor.ID == "" {
		return "", nil, false
	}
	if name := s.current.get(actor.ID); name != "" {
		if st, err := s.pm.ListedStore(name); err == nil && s.projectReadable(r, st.Root()) {
			s.pm.Watch(st)
			return name, st, true
		}
	}
	name, st, ok := s.defaultProject(r)
	if ok {
		s.pm.Watch(st)
	}
	return name, st, ok
}

// defaultProject is the project an account starts on: the server's active
// project when it may read it, else the first one in the catalog it may.
func (s *Server) defaultProject(r *http.Request) (string, *store.Store, bool) {
	if active := s.pm.Store(); active != nil && s.projectReadable(r, active.Root()) {
		if name, ok := s.pm.NameFor(active.Root()); ok {
			return name, active, true
		}
	}
	projects, err := s.pm.List()
	if err != nil {
		log.Printf("current project: list projects: %v", err)
		return "", nil, false
	}
	for _, info := range projects {
		if info.IsFolder || !s.projectReadable(r, info.Path) {
			continue
		}
		if st, err := s.pm.StoreFor(info.Name); err == nil {
			return info.Name, st, true
		}
	}
	return "", nil, false
}

// currentProjectHooks gives the project API the per-account current project.
type currentProjectHooks struct{ s *Server }

func (h currentProjectHooks) Current(r *http.Request) (string, bool) {
	name, _, ok := h.s.currentProject(r)
	return name, ok
}

// Open makes name the actor's current project and tells the actor's other
// connections, which follow it; nobody else's view moves.
func (h currentProjectHooks) Open(r *http.Request, name string) error {
	actor := auth.ActorFrom(r)
	if actor.IsAgent() {
		return projectapi.ErrAgentSwitch
	}
	st, err := h.s.pm.ListedStore(name)
	if err != nil {
		// A missing project answers like an unreadable one, so a member
		// cannot learn which restricted project names exist.
		return projectapi.ErrNotReadable
	}
	if !h.s.projectReadable(r, st.Root()) {
		return projectapi.ErrNotReadable
	}
	h.s.pm.Watch(st)
	if err := h.s.current.set(r.Context(), actor.ID, name); err != nil {
		log.Printf("current project: save for %s: %v", actor.ID, err)
	}
	if h.s.hub != nil {
		h.s.hub.BroadcastActor(actor.ID, "project-changed", name)
	}
	return nil
}

// Follow records name as the actor's current project after a request that
// opened it for them (an import, a copy opened on arrival), without telling
// anyone: the request's own response says where it landed.
func (h currentProjectHooks) Follow(r *http.Request, name string) {
	actor := auth.ActorFrom(r)
	if actor.ID == "" || actor.IsAgent() || name == "" {
		return
	}
	if err := h.s.current.set(r.Context(), actor.ID, name); err != nil {
		log.Printf("current project: save for %s: %v", actor.ID, err)
	}
}

// Moved keeps accounts on a project an administrator renamed or moved.
func (h currentProjectHooks) Moved(ctx context.Context, from, to string) {
	h.s.current.moved(ctx, from, to)
}
