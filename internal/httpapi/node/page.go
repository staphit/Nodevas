// HTTP handlers for the pages inside a node. Routes: routes.go (routePages).

package node

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/store"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

func (a *API) getNodePages(c *gin.Context) {
	pages, err := httpx.StoreFor(c.Request, a.pm).ListNodePages(c.Param("id"))
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"pages": pages})
}

func (a *API) postNodePage(c *gin.Context) {
	var body struct {
		Title  string `json:"title"`
		Format string `json:"format"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	nodeID := c.Param("id")
	page, content, rev, err := httpx.StoreFor(c.Request, a.pm).CreateNodePage(nodeID, body.Title, body.Format)
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	a.notifyRoom(c.Request, "node-changed", nodeID)
	c.JSON(http.StatusCreated, map[string]any{
		"page":    page,
		"content": content,
		"rev":     rev,
	})
}

// postNodePageImport turns an uploaded document into a subpage.
func (a *API) postNodePageImport(c *gin.Context) {
	nodeID := c.Param("id")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, store.MaxAttachmentBytes)
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("read upload: %w", err))
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	upload, header, err := c.Request.FormFile("file")
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, errors.New("file is required"))
		return
	}
	defer upload.Close()
	data, err := io.ReadAll(io.LimitReader(upload, store.MaxAttachmentBytes))
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	page, content, rev, err := httpx.StoreFor(c.Request, a.pm).ImportNodePage(
		nodeID,
		c.Request.FormValue("title"),
		header.Filename,
		data,
	)
	if err != nil {
		if page.ID == "" {
			httpx.Err(c, http.StatusBadRequest, err)
			return
		}
		// The file is stored; only the conversion for the editor failed.
		log.Printf("import page %s/%s: %v", nodeID, page.ID, err)
	}
	a.notifyRoom(c.Request, "node-changed", nodeID)
	c.JSON(http.StatusCreated, map[string]any{
		"page":    page,
		"content": content,
		"rev":     rev,
	})
}

func (a *API) getNodePage(c *gin.Context) {
	nodeID := c.Param("id")
	pageID := c.Param("page")
	page, content, rev, err := httpx.StoreFor(c.Request, a.pm).LoadNodePage(nodeID, pageID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			httpx.Err(c, http.StatusNotFound, err)
			return
		}
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, map[string]string{
		"nodeId":  nodeID,
		"pageId":  pageID,
		"format":  page.Format,
		"content": content,
		"rev":     rev,
	})
}

func (a *API) putNodePage(c *gin.Context) {
	var body struct {
		Content string `json:"content"`
		BaseRev string `json:"baseRev"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	nodeID := c.Param("id")
	rev, err := httpx.StoreFor(c.Request, a.pm).SaveNodePage(
		nodeID,
		c.Param("page"),
		body.Content,
		body.BaseRev,
	)
	if err != nil {
		var conflict *store.ErrConflict
		if errors.As(err, &conflict) {
			httpx.Conflict(c, conflict)
			return
		}
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	a.notifyRoom(c.Request, "node-changed", nodeID)
	c.JSON(http.StatusOK, map[string]any{"ok": true, "rev": rev})
}

func (a *API) patchNodePage(c *gin.Context) {
	var body struct {
		Title  string `json:"title"`
		Index  *int   `json:"index"`
		Format string `json:"format"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	nodeID := c.Param("id")
	pageID := c.Param("page")
	response := map[string]any{"ok": true}
	// A format change rewrites the file; the title and order only touch the
	// manifest, so both can arrive in one request.
	if strings.TrimSpace(body.Format) != "" {
		pages, content, rev, err := httpx.StoreFor(c.Request, a.pm).ConvertNodePage(nodeID, pageID, body.Format)
		if err != nil {
			httpx.Err(c, http.StatusBadRequest, err)
			return
		}
		response["pages"] = pages
		response["content"] = content
		response["rev"] = rev
	}
	if strings.TrimSpace(body.Title) != "" || body.Index != nil {
		pages, err := httpx.StoreFor(c.Request, a.pm).UpdateNodePage(nodeID, pageID, body.Title, body.Index)
		if err != nil {
			httpx.Err(c, http.StatusBadRequest, err)
			return
		}
		response["pages"] = pages
	}
	if _, ok := response["pages"]; !ok {
		pages, err := httpx.StoreFor(c.Request, a.pm).ListNodePages(nodeID)
		if err != nil {
			httpx.Err(c, http.StatusBadRequest, err)
			return
		}
		response["pages"] = pages
	}
	a.notifyRoom(c.Request, "node-changed", nodeID)
	c.JSON(http.StatusOK, response)
}

func (a *API) deleteNodePage(c *gin.Context) {
	nodeID := c.Param("id")
	outcome, err := httpx.StoreFor(c.Request, a.pm).DeleteNodePage(nodeID, c.Param("page"))
	if err != nil {
		var cleanupUnavailable *store.DeleteCleanupUnavailableError
		if errors.As(err, &cleanupUnavailable) {
			httpx.Err(c, http.StatusServiceUnavailable, cleanupUnavailable)
			return
		}
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	a.notifyRoom(c.Request, "node-changed", nodeID)
	c.JSON(http.StatusOK, map[string]any{
		"ok":             true,
		"trashFile":      outcome.TrashFile,
		"cleanupPending": outcome.CleanupPending,
	})
}
