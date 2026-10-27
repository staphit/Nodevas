package node

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/engine"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/store"
)

// postAcceptance replaces a node's acceptance criteria (the set_acceptance
// tool). Who may write them, on which node types, and whether they are
// locked is the store's decision (store.SetAcceptance).
func (a *API) postAcceptance(c *gin.Context) {
	id := c.Param("id")
	var body struct {
		Items   []engine.AcceptanceItem `json:"items"`
		BaseRev string                  `json:"baseRev"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	if len(body.Items) > engine.MaxAcceptanceItems {
		httpx.Err(c, http.StatusBadRequest, errors.New("too many acceptance criteria"))
		return
	}
	st := httpx.StoreFor(c.Request, a.pm)
	rev, err := st.SetAcceptance(auth.ActorFrom(c.Request), id, body.Items, body.BaseRev)
	if err != nil {
		if httpx.WriteDenied(c, err) {
			return
		}
		var conflict *store.ErrConflict
		if errors.As(err, &conflict) {
			httpx.Conflict(c, conflict)
			return
		}
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	g, _, err := st.LoadGraph()
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	a.notifyRoom(c.Request, "graph-changed", "")
	var acceptance *engine.Acceptance
	if n := g.NodeByID(id); n != nil {
		acceptance = n.Acceptance
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true, "rev": rev, "acceptance": acceptance})
}
