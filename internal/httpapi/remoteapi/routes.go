package remoteapi

import (
	"github.com/gin-gonic/gin"

	"nodevas/internal/httpapi/httpx"
)

// Register serves cloud backup. See remote.go, sync.go and git.go.
func (a *API) Register(api *gin.RouterGroup) {
	admin := api.Group("")
	admin.Use(httpx.RequireAdmin)
	admin.GET("/remote/config", a.getRemoteConfig)
	admin.GET("/remote/sync/status", a.getRemoteSyncStatus)
	admin.POST("/remote/sync/flush", a.postRemoteSyncFlush)
	admin.PUT("/remote/config", a.putRemoteConfig)
	admin.GET("/remote/list", a.getRemoteList)
	admin.POST("/remote/push", a.postRemotePush)
	admin.POST("/remote/import", a.postRemoteImport)
	admin.POST("/remote/git/test", a.postGitTest)
	admin.POST("/remote/git/keep-local", a.postGitKeepLocal)
}
