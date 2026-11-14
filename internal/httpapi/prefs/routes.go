package prefs

import "github.com/gin-gonic/gin"

// Register wires /api/me/preferences.
func (a *API) Register(api *gin.RouterGroup) {
	api.GET("/me/preferences", a.get)
	api.PATCH("/me/preferences", a.patch)
}
