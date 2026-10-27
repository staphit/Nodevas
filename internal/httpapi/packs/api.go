// Package packs serves the workspace's view packs (docs/view-packs.md):
//
//	GET    /api/packs                   → {packs: [packs.Pack]}
//	GET    /api/packs/:id/files/*path   → one sprite image
//	POST   /api/packs {zip: base64}     → 201 {pack: packs.Pack} (administrators)
//	DELETE /api/packs/:id               → 204 (administrators)
//
// Packs belong to the workspace, not to a project, so these routes are left
// out of the per-project access gate and the request audit trail; install and
// remove write their own server-wide rows, pack.install and pack.remove.
package packs

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/identity"
	"nodevas/internal/logging"
	"nodevas/internal/packs"
)

// MaxInstallBody fits a zip of packs.MaxZip bytes once base64 has grown it by
// a third, plus the few bytes of JSON around it.
const MaxInstallBody = (packs.MaxZip/3+1)*4 + 1<<10

// Audit actions for the trail.
const (
	ActionInstall = "pack.install"
	ActionRemove  = "pack.remove"
)

// ChangedEvent is the websocket event every client gets after an install or a
// removal, so open view switches refetch the list.
const ChangedEvent = "packs-changed"

// fileCSP is stricter than the page's: a pack file is only ever an image, so
// even an SVG opened on its own in a tab runs nothing and loads nothing. The
// inline styles an SVG carries are allowed so it still looks right.
const fileCSP = "sandbox; default-src 'none'; style-src 'unsafe-inline'"

var (
	errAdminOnly = errors.New("only a server administrator can install or remove view packs")
	errNoZip     = errors.New(`the body must be {"zip": "<base64 of the .zip>"}`)
	errNoFile    = errors.New("no such view pack file")
)

// API serves the pack routes.
type API struct {
	store   *packs.Store
	audit   *audit.Store
	changed func(id string)
}

// New builds the handlers over a pack store.
func New(store *packs.Store) *API {
	return &API{store: store}
}

// UseAudit records installs and removals in the workspace trail.
func (a *API) UseAudit(store *audit.Store) *API {
	a.audit = store
	return a
}

// UseChanged is called after every install and removal.
func (a *API) UseChanged(changed func(id string)) *API {
	a.changed = changed
	return a
}

// Register wires /api/packs onto the router group.
func (a *API) Register(api *gin.RouterGroup) {
	api.GET("/packs", httpx.ETag(a.getPacks))
	api.GET("/packs/:id/files/*path", a.getFile)
	api.POST("/packs", a.postPack)
	api.DELETE("/packs/:id", a.deletePack)
}

func (a *API) getPacks(c *gin.Context) {
	list, err := a.store.List()
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"packs": list})
}

func (a *API) getFile(c *gin.Context) {
	path := strings.TrimPrefix(c.Param("path"), "/")
	content, kind, err := a.store.Open(c.Param("id"), path)
	if err != nil {
		httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", errNoFile, nil)
		return
	}
	c.Header("X-Content-Type-Options", "nosniff")
	// Replaces the page policy secureHTTP set on every response.
	c.Header("Content-Security-Policy", fileCSP)
	// The URL carries ?v=<installedAt>, which a reinstall changes.
	c.Header("Cache-Control", "private, max-age=86400")
	c.Data(http.StatusOK, kind.ContentType(), content)
}

type installBody struct {
	// Zip is base64 in the JSON; encoding/json decodes it.
	Zip []byte `json:"zip"`
}

func (a *API) postPack(c *gin.Context) {
	actor, ok := admin(c)
	if !ok {
		return
	}
	var body installBody
	if !httpx.DecodeJSONLimit(c, &body, MaxInstallBody) {
		return
	}
	if len(body.Zip) == 0 {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_PACK", errNoZip, nil)
		return
	}
	pack, err := a.store.Install(body.Zip, actor.Name)
	var refused *packs.InvalidError
	if errors.As(err, &refused) {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_PACK", err, nil)
		return
	}
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	a.record(c, actor, ActionInstall, pack.ID, map[string]any{
		"name": pack.Name["en"], "layout": pack.Layout, "sprites": len(pack.Sprites),
	})
	a.notify(pack.ID)
	c.JSON(http.StatusCreated, gin.H{"pack": pack})
}

func (a *API) deletePack(c *gin.Context) {
	actor, ok := admin(c)
	if !ok {
		return
	}
	id := c.Param("id")
	if err := a.store.Remove(id); err != nil {
		if errors.Is(err, packs.ErrNotFound) {
			httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", err, nil)
			return
		}
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	a.record(c, actor, ActionRemove, id, nil)
	a.notify(id)
	c.Status(http.StatusNoContent)
}

// admin admits a server administrator: on a loopback server with no
// accounts, the local user. An agent is refused even for its administrator:
// a pack changes what every person on the server sees, which is a person's
// decision to make.
func admin(c *gin.Context) (identity.Actor, bool) {
	actor := auth.ActorFrom(c.Request)
	if auth.RequireAdmin(c.Request) != nil || actor.IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", errAdminOnly, nil)
		return identity.Actor{}, false
	}
	return actor, true
}

func (a *API) record(c *gin.Context, actor identity.Actor, action, id string, detail map[string]any) {
	if a.audit == nil {
		return
	}
	r := c.Request
	a.audit.RecordOrLog(r.Context(), audit.Event{
		At:         time.Now(),
		ActorID:    actor.ID,
		ActorName:  actor.Name,
		Action:     action,
		Target:     id,
		ClientIP:   auth.ClientIP(r),
		Detail:     detail,
		ActorEmail: logging.UserEmailFrom(r.Context()),
	})
}

func (a *API) notify(id string) {
	if a.changed != nil {
		a.changed(id)
	}
}
