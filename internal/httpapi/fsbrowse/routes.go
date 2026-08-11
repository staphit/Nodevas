package fsbrowse

import "github.com/gin-gonic/gin"

// Register wires the host directory picker. It is refused on a networked server.
// See fsbrowse.go.
func (a *API) Register(api *gin.RouterGroup) {
	api.GET("/fs/dirs", a.getFSDirs)
	api.POST("/fs/mkdir", a.postFSMkdir)
	api.POST("/fs/open", a.postFSOpen)
}
