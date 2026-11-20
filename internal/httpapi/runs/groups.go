package runs

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/locale"
)

// Stub handlers in Wave 0 (F1); filled by G2 (and J1 for followUpGlobalGroup).

func (a *API) createGlobalGroup(c *gin.Context) {
	httpx.Coded(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", locale.Errorf("not implemented", "尚未實作"), nil)
}

func (a *API) getGlobalGroup(c *gin.Context) {
	httpx.Coded(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", locale.Errorf("not implemented", "尚未實作"), nil)
}

func (a *API) cancelGlobalGroup(c *gin.Context) {
	httpx.Coded(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", locale.Errorf("not implemented", "尚未實作"), nil)
}

func (a *API) resumeGlobalGroup(c *gin.Context) {
	httpx.Coded(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", locale.Errorf("not implemented", "尚未實作"), nil)
}

func (a *API) followUpGlobalGroup(c *gin.Context) {
	httpx.Coded(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", locale.Errorf("not implemented", "尚未實作"), nil)
}
