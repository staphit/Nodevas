package runs

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/locale"
)

// Stub handlers in Wave 0 (F1); filled by C3h.

func (a *API) createContest(c *gin.Context) {
	httpx.Coded(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", locale.Errorf("not implemented", "尚未實作"), nil)
}

func (a *API) createGlobalContest(c *gin.Context) {
	httpx.Coded(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", locale.Errorf("not implemented", "尚未實作"), nil)
}

func (a *API) getContest(c *gin.Context) {
	httpx.Coded(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", locale.Errorf("not implemented", "尚未實作"), nil)
}

func (a *API) judgeContest(c *gin.Context) {
	httpx.Coded(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", locale.Errorf("not implemented", "尚未實作"), nil)
}

func (a *API) pickContest(c *gin.Context) {
	httpx.Coded(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", locale.Errorf("not implemented", "尚未實作"), nil)
}

func (a *API) cancelContest(c *gin.Context) {
	httpx.Coded(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", locale.Errorf("not implemented", "尚未實作"), nil)
}
