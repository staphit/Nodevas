package remoteapi

import (
	"github.com/gin-gonic/gin"

	"nodevas/internal/httpapi/httpx"
)

// routeRemote serves cloud backup and the Drive OAuth flow. See remote_*.go.
func (a *API) Register(api *gin.RouterGroup) {
	// The callback is authenticated by its one-time OAuth state and must stay
	// public because SameSite=Strict omits the session cookie on Google's
	// cross-site redirect.
	api.GET("/remote/drive/callback", a.getDriveCallback)

	admin := api.Group("")
	admin.Use(httpx.RequireAdmin)
	admin.GET("/remote/config", a.getRemoteConfig)
	admin.GET("/remote/sync/status", a.getRemoteSyncStatus)
	admin.POST("/remote/sync/flush", a.postRemoteSyncFlush)
	admin.PUT("/remote/config", a.putRemoteConfig)
	admin.GET("/remote/list", a.requireConfiguredRemoteOwner, a.getRemoteList)
	admin.POST("/remote/push", a.requireConfiguredRemoteOwner, a.postRemotePush)
	admin.POST("/remote/import", a.requireConfiguredRemoteOwner, a.postRemoteImport)
	admin.GET("/remote/drive/folders", a.requireDriveOwner, a.getDriveFolders)
	admin.GET("/remote/drive/bundles", a.requireDriveOwner, a.getDriveBundles)
	admin.GET("/remote/drive/credentials", a.getDriveCredentials)
	admin.PUT("/remote/drive/credentials", a.putDriveCredentials)
	admin.DELETE("/remote/drive/credentials", a.deleteDriveCredentials)
	admin.POST("/remote/drive/connect", a.requireDriveOwner, a.postDriveConnect)
	// Re-auth and disconnect remain available for an unbound token so an
	// administrator can recover it. They still reject a different owner.
	//
	// /remote/drive/auth changes state (it mints a one-time OAuth state entry)
	// while staying a GET, because the browser has to follow its 302 to Google
	// as a real navigation: a fetch cannot read a cross-origin redirect target,
	// and a form POST cannot carry the CSRF header a networked deployment
	// demands. The cross-site protection a POST would have got for free is
	// applied to it by name in server.secureHTTP (stateChangingGETPaths).
	admin.GET("/remote/drive/auth", a.getDriveAuth)
	admin.POST("/remote/drive/disconnect", a.postDriveDisconnect)
}
