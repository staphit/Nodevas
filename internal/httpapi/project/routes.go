package project

import (
	"github.com/gin-gonic/gin"

	"nodevas/internal/httpapi/httpx"
)

// Register wires the workspace, project and transfer endpoints.
func (a *API) Register(api *gin.RouterGroup) {
	api.GET("/projects", httpx.ETag(a.GetProjects))
	// /api/search and /api/projects/export are left alone: search answers a
	// query rather than serving a resource, and the export streams an archive
	// that must not be buffered to be hashed.
	api.GET("/search", a.GetSearch)
	api.GET("/projects/export", a.GetProjectExport)
	api.GET("/workspaces/statuses", httpx.ETag(a.GetWorkspaceStatuses))
	api.GET("/workspaces/order", httpx.ETag(a.GetProjectOrder))
	api.GET("/links", httpx.ETag(a.GetNodeLinks))
	api.GET("/links/targets", httpx.ETag(a.GetNodeIndex))

	admin := api.Group("")
	admin.Use(httpx.RequireAdmin)
	admin.POST("/projects/open", a.PostProjectOpen)
	admin.POST("/projects/remove", a.PostProjectRemove)
	admin.POST("/projects/move", a.PostProjectMove)
	admin.POST("/projects/copy", a.PostProjectCopy)
	admin.POST("/projects/folder", a.PostProjectFolder)
	admin.POST("/projects/import-path", a.PostProjectImportPath)
	admin.POST("/projects/import", a.PostProjectImport)
	admin.POST("/workspaces/add", a.PostWorkspaceAdd)
	admin.POST("/workspaces/open", a.PostWorkspaceOpen)
	admin.POST("/workspaces/remove", a.PostWorkspaceRemove)
	admin.PUT("/workspaces/statuses", a.PutWorkspaceStatuses)
	admin.PUT("/workspaces/order", a.PutProjectOrder)
}
