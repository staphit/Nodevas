package runs

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/locale"
)

// Stub handlers in Wave 0 (F1); filled by C1.

func (a *API) runsBusy(c *gin.Context) {
	httpx.Coded(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", locale.Errorf("not implemented", "尚未實作"), nil)
}

func (a *API) globalBusy(c *gin.Context) {
	httpx.Coded(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", locale.Errorf("not implemented", "尚未實作"), nil)
}
