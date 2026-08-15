// HTTP handlers for the graph document itself: read, replace, validate and
// check DSL. Routes: routes.go (routeGraph).

package graph

import (
	"encoding/json"
	"errors"
	"net/http"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/store"

	"github.com/gin-gonic/gin"

	"nodevas/internal/engine"
	"nodevas/internal/engine/dsl"
)

// getGraph returns the graph, its rev, computed statuses, and issues.
func (a *API) getGraph(c *gin.Context) {
	st := httpx.StoreFor(c.Request, a.pm)
	g, rev, err := st.LoadGraph()
	if err != nil {
		httpx.Err(c, 500, err)
		return
	}
	rs, err := st.LoadState()
	if err != nil {
		httpx.Err(c, 500, err)
		return
	}
	c.JSON(200, map[string]any{
		"graph":    g,
		"rev":      rev,
		"statuses": engine.ComputeStatuses(g, rs),
		"issues":   engine.Validate(g),
	})
}

func (a *API) putGraph(c *gin.Context) {
	var body struct {
		Graph   *engine.Graph `json:"graph"`
		BaseRev string        `json:"baseRev"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	if body.Graph == nil {
		httpx.Err(c, 400, errors.New("body must be {graph, baseRev?}"))
		return
	}
	if err := store.ValidateGraphForStorage(body.Graph); err != nil {
		c.JSON(http.StatusUnprocessableEntity, map[string]any{
			"error": err.Error(),
		})
		return
	}
	issues := engine.Validate(body.Graph)
	rev, err := httpx.StoreFor(c.Request, a.pm).SaveGraph(body.Graph, body.BaseRev)
	if err != nil {
		var conflict *store.ErrConflict
		if errors.As(err, &conflict) {
			httpx.Conflict(c, conflict)
			return
		}
		httpx.Err(c, 500, err)
		return
	}
	a.notifyRoom(c.Request, "graph-changed", "")
	c.JSON(200, map[string]any{"ok": true, "rev": rev, "issues": issues})
}

func (a *API) getState(c *gin.Context) {
	st := httpx.StoreFor(c.Request, a.pm)
	g, _, err := st.LoadGraph()
	if err != nil {
		httpx.Err(c, 500, err)
		return
	}
	rs, err := st.LoadState()
	if err != nil {
		httpx.Err(c, 500, err)
		return
	}
	c.JSON(200, map[string]any{
		"state":    rs,
		"statuses": engine.ComputeStatuses(g, rs),
	})
}

func (a *API) getValidate(c *gin.Context) {
	g, _, err := httpx.StoreFor(c.Request, a.pm).LoadGraph()
	if err != nil {
		httpx.Err(c, 500, err)
		return
	}
	c.JSON(200, map[string]any{"issues": engine.Validate(g)})
}

// postDSLCheck is the live syntax checker behind the requires input field.
func (a *API) postDSLCheck(c *gin.Context) {
	var body struct {
		Expr string `json:"expr"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	if len(body.Expr) > 64<<10 {
		httpx.Err(c, 400, errors.New("expression is too long"))
		return
	}
	expr, perr := dsl.Parse(body.Expr)
	if perr != nil {
		c.JSON(200, map[string]any{"ok": false, "error": perr})
		return
	}
	resp := map[string]any{"ok": true}
	if expr != nil {
		resp["nodeRefs"] = dsl.NodeRefs(expr)
		resp["flagRefs"] = dsl.FlagRefs(expr)
		resp["canonical"] = expr.String()
	}
	c.JSON(200, resp)
}

// postGraphOps applies server-side graph commands.
//
// Unlike PUT /api/graph it carries no base revision: each command names the
// smallest thing it changes, so two people working on different parts of one
// board no longer collide over the whole file.
func (a *API) postGraphOps(c *gin.Context) {
	var body struct {
		Ops []store.GraphOp `json:"ops"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	st := httpx.StoreFor(c.Request, a.pm)
	graph, rev, err := st.ApplyGraphOps(body.Ops)
	if err != nil {
		var conflict *store.ErrConflict
		if errors.As(err, &conflict) {
			httpx.Conflict(c, conflict)
			return
		}
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	runState, err := st.LoadState()
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	// Dependency ops materialize both an edge and node.requires. Older clients
	// only know how to replay the edge half, so every peer reloads the canonical
	// graph. Layout/metadata ops remain cheap, targeted graph-ops events.
	if dependencyOpsRequireReload(body.Ops) {
		a.notifyRoom(c.Request, "graph-changed", "")
	} else if applied, err := json.Marshal(body.Ops); err == nil {
		a.notifyOps(c.Request, applied)
	} else {
		a.notifyRoom(c.Request, "graph-changed", "")
	}
	c.JSON(http.StatusOK, map[string]any{
		"ok":       true,
		"graph":    graph,
		"rev":      rev,
		"statuses": engine.ComputeStatuses(graph, runState),
		"issues":   engine.Validate(graph),
	})
}

func dependencyOpsRequireReload(ops []store.GraphOp) bool {
	for _, op := range ops {
		switch op.Kind {
		case "add-edge", "remove-edge", "set-edge-style":
			return true
		}
	}
	return false
}
