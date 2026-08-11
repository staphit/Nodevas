package graph

import (
	"github.com/gin-gonic/gin"

	"nodevas/internal/httpapi/httpx"
)

// routeGraph serves the graph document and whole-graph checks. See graph_http.go.
func (a *API) Register(api *gin.RouterGroup) {
	// The client refetches the graph and the run state after every mutation and
	// on every websocket event, so both revalidate rather than re-download.
	api.GET("/graph", httpx.ETag(a.getGraph))
	api.PUT("/graph", a.putGraph)
	api.POST("/graph/ops", a.postGraphOps)
	api.GET("/state", httpx.ETag(a.getState))
	api.GET("/validate", a.getValidate)
	// The ready queue changes whenever anyone finishes anything, which is a
	// status write, not a graph write — so it revalidates like the two above.
	api.GET("/ready", httpx.ETag(a.getReady))
	api.POST("/dsl/check", a.postDSLCheck)
	a.routeHistory(api)
	a.routeDrafts(api)
}

// routeHistory serves version history and the trash. See history_http.go.
func (a *API) routeHistory(api *gin.RouterGroup) {
	// /api/history is a listing: a new snapshot appears in it whenever anyone
	// saves, so it stays uncached. Only /api/history/version, which returns one
	// finished snapshot, gets a lifetime — see getHistoryVersion.
	api.GET("/history", a.getHistory)
	api.GET("/history/version", a.getHistoryVersion)
	api.POST("/history/restore", a.postHistoryRestore)
	api.GET("/trash", httpx.ETag(a.getTrash))
	api.POST("/trash/restore", a.postTrashRestore)
}

// routeDrafts serves unsaved editor content. See draft_http.go.
func (a *API) routeDrafts(api *gin.RouterGroup) {
	api.GET("/drafts/:id", a.getDraft)
	api.PUT("/drafts/:id", a.putDraft)
	api.DELETE("/drafts/:id", a.deleteDraft)
}
