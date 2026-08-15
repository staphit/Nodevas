package httpx

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/store"
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

// WriteDenied answers a per-node write refusal with 403 and reports whether it
// did. Handlers call it first in their error branch, so a permission denial is
// never mistaken for a bad request or a server fault.
func WriteDenied(c *gin.Context, err error) bool {
	var denied *store.ErrNodeWriteDenied
	if !errors.As(err, &denied) {
		return false
	}
	Err(c, http.StatusForbidden, err)
	return true
}
