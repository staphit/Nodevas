package node

import (
	"github.com/gin-gonic/gin"

	"nodevas/internal/httpapi/httpx"
)

// routeNodes serves single nodes and bulk node operations. See node_http.go.
func (a *API) Register(api *gin.RouterGroup) {
	api.POST("/nodes", a.postNode)
	// Static siblings of :id must stay distinct from any node id.
	api.POST("/nodes/delete", a.postNodesDelete)
	api.POST("/nodes/transfer", a.postNodesTransfer)
	api.POST("/nodes/folder", a.postNodesFolder)
	api.GET("/nodes/:id", httpx.ETag(a.getNode))
	api.PUT("/nodes/:id", a.putNode)
	api.DELETE("/nodes/:id", a.deleteNode)
	api.POST("/nodes/:id/duplicate", a.postNodeDuplicate)
	api.POST("/nodes/:id/status", a.postStatus)
	api.POST("/nodes/:id/claim", a.postClaim)
	api.POST("/nodes/:id/release", a.postRelease)
	// Uncached: a claim's whole value is being current, and it also expires on
	// its own, which no revalidation would notice.
	api.GET("/claims", a.getClaims)
	api.POST("/events/move", a.postEventMove)
	a.routeNodeFolders(api)
	a.routePages(api)
}

// routeNodeFolders serves the folders that organise node files in the sidebar.
// They never touch graph.yaml. See node_folders_http.go.
func (a *API) routeNodeFolders(api *gin.RouterGroup) {
	api.GET("/folders", httpx.ETag(a.getNodeFolders))
	api.POST("/folders", a.postNodeFolder)
	api.POST("/folders/rename", a.postNodeFolderRename)
	api.POST("/folders/move", a.postNodeFolderMove)
	api.POST("/folders/delete", a.postNodeFolderDelete)
}

// routePages serves a node's pages and file attachments. See page_http.go and files.go.
func (a *API) routePages(api *gin.RouterGroup) {
	api.GET("/nodes/:id/pages", httpx.ETag(a.getNodePages))
	api.POST("/nodes/:id/pages", a.postNodePage)
	api.POST("/nodes/:id/pages/import", a.postNodePageImport)
	api.GET("/nodes/:id/pages/:page", a.getNodePage)
	api.PUT("/nodes/:id/pages/:page", a.putNodePage)
	api.PATCH("/nodes/:id/pages/:page", a.patchNodePage)
	api.DELETE("/nodes/:id/pages/:page", a.deleteNodePage)
	api.POST("/nodes/:id/files", a.postNodeFile)
	api.GET("/nodes/:id/files/:file", a.getNodeFile)
}
