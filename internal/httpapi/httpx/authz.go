package httpx

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
)

// RequireAdmin protects server-wide settings while leaving project editing
// available to ordinary collaborators. Local desktop mode remains allowed via
// identity.Local's administrator role.
func RequireAdmin(c *gin.Context) {
	if err := auth.RequireAdmin(c.Request); err != nil {
		Err(c, http.StatusForbidden, errors.New("administrator access required"))
		return
	}
	c.Next()
}
