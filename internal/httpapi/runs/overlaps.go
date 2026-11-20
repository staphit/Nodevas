package runs

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/locale"
)

// Stub handler in Wave 0 (F1); filled by C2.

func (a *API) runOverlaps(c *gin.Context) {
	httpx.Coded(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", locale.Errorf("not implemented", "尚未實作"), nil)
}
