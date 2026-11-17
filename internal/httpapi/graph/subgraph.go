// POST /api/graph/subgraph: many nodes and their wiring in one write.
// POST /api/graph/subgraph/check: the same request, checked and not written.

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

// postSubgraph commits a subgraph. With "batch": {"via": ...} the call is also
// recorded for POST /api/graph/batches/:id/undo, and the answer carries
// batchId and undoable (false when the record could not be written; the nodes
// were made all the same). A via starting "copilot-draft:" is held to
// store.MaxCopilotDraftNodes. A draft plan run's call (P6) is recorded
// through planDraft instead, and answered 200 with draft true.
func (a *API) postSubgraph(c *gin.Context) {
	var spec store.SubgraphSpec
	if !httpx.DecodeJSONLimit(c, &spec, maxSubgraphRequestBytes) {
		return
	}
	st := httpx.StoreFor(c.Request, a.pm)
	if a.planDraft != nil {
		if held, handled, err := a.planDraft(c.Request, st, spec); handled {
			if err != nil {
				writeSubgraphError(c, err)
				return
			}
			c.JSON(http.StatusOK, map[string]any{"ok": true, "draft": true, "ids": held.IDs,
				"created": []store.SubgraphCreated{}, "issues": held.Issues, "note": PlanDraftNote,
				"draftNodes": held.Nodes})
			return // no broadcast: nothing changed
		}
	}
	result, err := st.CreateSubgraph(auth.ActorFrom(c.Request), spec)
	if err != nil {
		writeSubgraphError(c, err)
		return
	}
	// One write, one broadcast, however many nodes it made.
	a.notifyRoom(c.Request, "graph-changed", "")
	body := subgraphBody(result)
	if spec.Batch != nil {
		body["batchId"] = result.BatchID
		body["undoable"] = result.Undoable
	}
	c.JSON(http.StatusCreated, body)
}

// postSubgraphCheck is the dry run: the answer a commit of the same request
// would give now (ids are provisional, rev is the board's current one), or
// the same refusal, and nothing written or broadcast. It changes nothing, so a
// read-only member may call it (internal/server readOnlyPosts).
func (a *API) postSubgraphCheck(c *gin.Context) {
	var spec store.SubgraphSpec
	if !httpx.DecodeJSONLimit(c, &spec, maxSubgraphRequestBytes) {
		return
	}
	result, err := httpx.StoreFor(c.Request, a.pm).CheckSubgraph(auth.ActorFrom(c.Request), spec)
	if err != nil {
		writeSubgraphError(c, err)
		return
	}
	c.JSON(http.StatusOK, subgraphBody(result))
}

func subgraphBody(result store.SubgraphResult) map[string]any {
	return map[string]any{
		"ok": true, "ids": result.IDs, "created": result.Created, "rev": result.Rev, "issues": result.Issues,
		"dependencies": result.Dependencies, "references": result.References,
	}
}

// writeSubgraphError answers a refused subgraph, the same for the commit and
// its dry run.
func writeSubgraphError(c *gin.Context, err error) {
	var refusal *PlanDraftRefusal
	if errors.As(err, &refusal) {
		httpx.Coded(c, refusal.Status, refusal.Code, refusal.Err, fieldExtras(refusal.Field))
		return
	}
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
}
