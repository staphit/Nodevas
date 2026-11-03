// POST /api/graph/subgraph: many nodes and their wiring in one write.

package graph

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/store"
)

// maxSubgraphRequestBytes bounds the request: 1 MiB of bodies plus the rest.
const maxSubgraphRequestBytes = 2 << 20

func (a *API) postSubgraph(c *gin.Context) {
	var spec store.SubgraphSpec
	if !httpx.DecodeJSONLimit(c, &spec, maxSubgraphRequestBytes) {
		return
	}
	result, err := httpx.StoreFor(c.Request, a.pm).CreateSubgraph(auth.ActorFrom(c.Request), spec)
	if err != nil {
		if httpx.WriteDenied(c, err) {
			return
		}
		var invalid *store.ErrInvalidSubgraph
		if errors.As(err, &invalid) {
			httpx.Coded(c, http.StatusBadRequest, "INVALID_SUBGRAPH", err, map[string]any{"field": invalid.Field})
			return
		}
		var tooLarge *store.ErrSubgraphTooLarge
		if errors.As(err, &tooLarge) {
			httpx.Coded(c, http.StatusRequestEntityTooLarge, "SUBGRAPH_TOO_LARGE", err, nil)
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
	// One write, one broadcast, however many nodes it made.
	a.notifyRoom(c.Request, "graph-changed", "")
	c.JSON(http.StatusCreated, map[string]any{
		"ok": true, "ids": result.IDs, "created": result.Created, "rev": result.Rev, "issues": result.Issues,
		"dependencies": result.Dependencies, "references": result.References,
	})
}
