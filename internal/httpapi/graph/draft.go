// HTTP handlers for editor drafts: content saved before the user commits it.
// Routes: routes.go (routeDrafts).

package graph

import (
	"errors"
	"nodevas/internal/httpapi/httpx"
	"os"

	"github.com/gin-gonic/gin"
)

func (a *API) getDraft(c *gin.Context) {
	content, err := httpx.StoreFor(c.Request, a.pm).LoadDraft(c.Param("id"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.JSON(200, map[string]any{"exists": false})
			return
		}
		httpx.Err(c, 400, err)
		return
	}
	c.JSON(200, map[string]any{"exists": true, "content": content})
}

func (a *API) putDraft(c *gin.Context) {
	var body struct {
		Content string `json:"content"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	if err := httpx.StoreFor(c.Request, a.pm).SaveDraft(c.Param("id"), body.Content); err != nil {
		httpx.Err(c, 400, err)
		return
	}
	c.JSON(200, map[string]bool{"ok": true})
}

func (a *API) deleteDraft(c *gin.Context) {
	if err := httpx.StoreFor(c.Request, a.pm).DeleteDraft(c.Param("id")); err != nil {
		httpx.Err(c, 400, err)
		return
	}
	c.JSON(200, map[string]bool{"ok": true})
}
