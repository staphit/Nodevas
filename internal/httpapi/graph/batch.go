// POST /api/graph/batches/:id/undo and /redo: take a recorded subgraph batch
// (a Copilot draft's Create) back as one, and put it back again.

package graph

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/store"
)

// postBatchUndo trashes the batch's nodes that nobody has changed since, and
// lists the ones it kept. Only the batch's author or an administrator may; to
// anyone else, as for an unknown id, it is 404 BATCH_NOT_FOUND. Undoing twice
// answers an empty result.
func (a *API) postBatchUndo(c *gin.Context) {
	st := httpx.StoreFor(c.Request, a.pm)
	var options store.BatchUndoOptions
	if a.activeRun != nil {
		// A node a run is working on is kept, like a claimed one (a run
		// that has not claimed yet holds no claim).
		ctx := c.Request.Context()
		options.Busy = func(nodeID string) (bool, string) {
			if a.activeRun(ctx, st, nodeID) {
				return true, "a run is working on it"
			}
			return false, ""
		}
	}
	result, err := st.UndoBatch(auth.ActorFrom(c.Request), c.Param("id"), options)
	if err != nil {
		writeBatchError(c, err)
		return
	}
	if len(result.Trashed) > 0 {
		a.notifyRoom(c.Request, "graph-changed", "")
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true, "trashed": result.Trashed, "refused": result.Refused})
}

// postBatchRedo restores what the undo trashed and the dependencies the batch
// had put into existing nodes. Redoing a batch that is not undone answers an
// empty result.
func (a *API) postBatchRedo(c *gin.Context) {
	result, err := httpx.StoreFor(c.Request, a.pm).RedoBatch(auth.ActorFrom(c.Request), c.Param("id"))
	if err != nil {
		writeBatchError(c, err)
		return
	}
	if len(result.Restored) > 0 {
		a.notifyRoom(c.Request, "graph-changed", "")
	}
	c.JSON(http.StatusOK, map[string]any{
		"ok": true, "restored": result.Restored, "skipped": result.Skipped, "links": result.Links,
	})
}

func writeBatchError(c *gin.Context, err error) {
	var notFound *store.ErrBatchNotFound
	if errors.As(err, &notFound) {
		httpx.Coded(c, http.StatusNotFound, "BATCH_NOT_FOUND", err, nil)
		return
	}
	if httpx.WriteDenied(c, err) {
		return
	}
	// A redo the board can no longer take (it would close a cycle).
	var invalid *store.ErrInvalidSubgraph
	if errors.As(err, &invalid) {
		httpx.Coded(c, http.StatusConflict, "BATCH_REDO_REFUSED", err, nil)
		return
	}
	var conflict *store.ErrConflict
	if errors.As(err, &conflict) {
		httpx.Conflict(c, conflict)
		return
	}
	var cleanupUnavailable *store.DeleteCleanupUnavailableError
	if errors.As(err, &cleanupUnavailable) {
		httpx.Err(c, http.StatusServiceUnavailable, cleanupUnavailable)
		return
	}
	httpx.Err(c, http.StatusBadRequest, err)
}
