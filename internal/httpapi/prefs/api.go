// Package prefs serves a person's app preferences (internal/prefs): what
// they chose in the app, kept on the hub so it follows them to another
// browser, address or the desktop app.
//
// Preferences are a person's. Each request reads and writes its own
// actor's: the local user's ("local") on a hub without accounts, the
// signed-in account's otherwise. An agent has none and is refused, whoever
// it acts for. The routes belong to no project, write no audit row (a theme
// click is not an administrative act, and the values are nobody else's
// business) and are rate-limited as reads (internal/server).
package prefs

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/locale"
	"nodevas/internal/prefs"
)

// Path is where a person's preferences are served. The middleware names it
// too: a change is charged as a read, and leaves no audit row.
const Path = "/api/me/preferences"

// maxChangeBody is room for a change at its largest: MaxKeysPerChange
// values of MaxValueBytes, their keys and the JSON around them.
const maxChangeBody = prefs.MaxKeysPerChange*(prefs.MaxValueBytes+prefs.MaxKeyBytes+16) + 4<<10

// API serves Path.
type API struct {
	store *prefs.Store
}

// New serves preferences from store.
func New(store *prefs.Store) *API {
	return &API{store: store}
}

var (
	errPrefsAgent  = locale.Errorf("agents have no preferences of their own", "agent 沒有自己的偏好設定")
	errPrefsNobody = locale.Errorf("sign in to keep preferences", "請先登入才能保存偏好設定")
)

// owner is the account whose preferences the request reads or writes, or
// false once it has been refused.
func owner(c *gin.Context) (string, bool) {
	actor, attached := auth.AttachedActor(c.Request)
	if actor.IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", errPrefsAgent, nil)
		return "", false
	}
	if !attached || strings.TrimSpace(actor.ID) == "" {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", errPrefsNobody, nil)
		return "", false
	}
	return actor.ID, true
}

// answer writes snapshot. A person's preferences are theirs alone, and the
// next read must see the last change: no cache keeps either.
func answer(c *gin.Context, snapshot prefs.Snapshot) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, snapshot)
}

// get is GET /api/me/preferences: {values: {key: value}, updatedAt: {key:
// time}}.
func (a *API) get(c *gin.Context) {
	account, ok := owner(c)
	if !ok {
		return
	}
	snapshot, err := a.store.Get(c.Request.Context(), account)
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	answer(c, snapshot)
}

// patch is PATCH /api/me/preferences: {set: {key: value}, remove: [key]},
// a null value removing its key too. It answers as get does, after the
// change.
func (a *API) patch(c *gin.Context) {
	account, ok := owner(c)
	if !ok {
		return
	}
	var change prefs.Change
	if !httpx.DecodeJSONLimit(c, &change, maxChangeBody) {
		return
	}
	snapshot, err := a.store.Apply(c.Request.Context(), account, change)
	var refusal *prefs.RefusedError
	switch {
	case errors.As(err, &refusal) && refusal.Full:
		httpx.Coded(c, http.StatusRequestEntityTooLarge, "RESOURCE_EXHAUSTED", refusal, map[string]any{
			"maxKeys": prefs.MaxKeysPerAccount, "maxBytes": prefs.MaxBytesPerAccount,
		})
	case errors.As(err, &refusal):
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", refusal, nil)
	case err != nil:
		httpx.Err(c, http.StatusInternalServerError, err)
	default:
		answer(c, snapshot)
	}
}
