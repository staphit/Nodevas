package runs

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/engine"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/locale"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// maxRoutingBody fits MaxRouteRules rules with full fallback chains.
const maxRoutingBody = 512 << 10

// routing returns the caller's routing rules, in order.
func (a *API) routing(c *gin.Context) {
	actor := auth.ActorFrom(c.Request)
	rules, err := a.d.Store().Routing(c.Request.Context(), actor.ID)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, runnerwire.RoutingConfig{Rules: rules})
}

// setRouting replaces the caller's routing rules. Rules choose what runs on
// the caller's machines, so only a person may change them.
func (a *API) setRouting(c *gin.Context) {
	actor := auth.ActorFrom(c.Request)
	if actor.IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", locale.Errorf("agents cannot change routing", "agent 不能更改路由"), nil)
		return
	}
	var body runnerwire.RoutingConfig
	if !httpx.DecodeJSONLimit(c, &body, maxRoutingBody) {
		return
	}
	rules, err := a.d.Store().SetRouting(c.Request.Context(), actor.ID, body.Rules)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, runnerwire.RoutingConfig{Rules: rules})
}

// route says what the caller's rules pick for a node in the requested
// project, for the dispatch dialog to pre-fill.
func (a *API) route(c *gin.Context) {
	st, projectName, ok := a.requestProject(c)
	if !ok {
		return
	}
	nodeID := strings.TrimSpace(c.Query("nodeId"))
	if !engine.ValidNodeID(nodeID) {
		invalid(c, "nodeId is not a valid node id", "nodeId 不是有效的節點代號")
		return
	}
	graph, _, err := st.LoadGraph()
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	node := graph.NodeByID(nodeID)
	if node == nil {
		notFound(c, "node")
		return
	}
	role := strings.TrimSpace(c.Query("role"))
	if role == "" {
		role = engine.RoleExecutor
	}
	if !engine.ValidRoleID(role) {
		invalid(c, "role is not a valid role id", "role 不是有效的角色代號")
		return
	}
	actor := auth.ActorFrom(c.Request)
	rules, err := a.d.Store().Routing(c.Request.Context(), actor.ID)
	if err != nil {
		fail(c, err)
		return
	}
	suggestion := runs.Suggest(rules, runs.RouteNode{Project: projectName, Kind: node.Kind, Tags: node.Tags})
	if a.autoRoute != nil && !actor.IsAgent() {
		suggestion = a.autoRoute(c.Request, actor.ID, projectName, nodeID, role, suggestion)
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, suggestion)
}

// pipeline returns one pipeline, to whoever may see its runs, and to whoever
// may review it (humanAnyEditor: any editor of its project).
func (a *API) pipeline(c *gin.Context) {
	_, projectName, ok := a.requestProject(c)
	if !ok {
		return
	}
	p, err := a.d.Store().Pipeline(c.Request.Context(), c.Param("id"))
	actor := auth.ActorFrom(c.Request)
	if errors.Is(err, runs.ErrNotFound) || (err == nil && (p.Project != projectName ||
		!(actor.IsAdmin() || p.CreatedBy == actor.ID || p.OwnerID == actor.ID ||
			(p.Pipeline.Human && p.Pipeline.HumanAnyEditor && !actor.IsAgent())))) {
		notFound(c, "pipeline")
		return
	}
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, p.PipelineState)
}
