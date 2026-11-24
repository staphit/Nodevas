package runs

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/runnerwire"
)

// runOverlaps serves GET /api/runs/:id/overlaps and GET /api/global-runs/:id/overlaps
// (§C.6). Both routes sit behind personOnly, and a.visible enforces canSee plus
// project vs global route scoping.
func (a *API) runOverlaps(c *gin.Context) {
	if !a.takesRuns(c) {
		return
	}
	r, ok := a.visible(c)
	if !ok {
		return
	}
	overlaps, err := a.d.Overlaps(c.Request.Context(), auth.ActorFrom(c.Request), r)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, runnerwire.OverlapsResponse{Overlaps: overlaps})
}
