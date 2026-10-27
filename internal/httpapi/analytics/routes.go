package analytics

import "github.com/gin-gonic/gin"

// Register wires /api/analytics/*.
func (a *API) Register(api *gin.RouterGroup) {
	g := api.Group("/analytics")
	g.GET("/insights", a.insights)
	g.GET("/overview", a.overview)
	g.GET("/scorecards", a.scorecards)
	g.GET("/tiers", a.tiers)
	g.GET("/settings", a.settings)
	g.PUT("/settings", a.putSettings)
	g.GET("/route-picks", a.routePicks)
	g.POST("/suggestions/apply", a.apply)
	g.POST("/suggestions/dismiss", a.dismiss)
	g.GET("/retention", a.retention)
	g.PUT("/retention", a.putRetention)
}
