package node

import (
	"errors"
	"net/http"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/store"

	"github.com/gin-gonic/gin"
)

// HTTP surface for node folders. Every handler here touches files only — none
// of them reads or writes graph.yaml, which is what keeps organising the
// sidebar from ever changing the dependency graph.

func (a *API) getNodeFolders(c *gin.Context) {
	folders, assignments := httpx.StoreFor(c.Request, a.pm).NodeFolders()
	c.JSON(200, map[string]any{"folders": folders, "nodes": assignments})
}

// folderStatus maps the folder errors onto HTTP codes; anything else is a 500.
func folderStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrFolderName), errors.Is(err, store.ErrFolderIntoSelf):
		return http.StatusBadRequest
	case errors.Is(err, store.ErrFolderMissing):
		return http.StatusNotFound
	case errors.Is(err, store.ErrFolderExists):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func (a *API) postNodeFolder(c *gin.Context) {
	var body struct {
		Path string `json:"path"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	created, err := httpx.StoreFor(c.Request, a.pm).CreateNodeFolder(body.Path)
	if err != nil {
		httpx.Err(c, folderStatus(err), err)
		return
	}
	a.notifyRoom(c.Request, "folders-changed", "")
	c.JSON(200, map[string]any{"ok": true, "path": created})
}

func (a *API) postNodeFolderRename(c *gin.Context) {
	var body struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	renamed, err := httpx.StoreFor(c.Request, a.pm).RenameNodeFolder(body.Path, body.Name)
	if err != nil {
		httpx.Err(c, folderStatus(err), err)
		return
	}
	a.notifyRoom(c.Request, "folders-changed", "")
	c.JSON(200, map[string]any{"ok": true, "path": renamed})
}

func (a *API) postNodeFolderMove(c *gin.Context) {
	var body struct {
		Path   string `json:"path"`
		Parent string `json:"parent"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	moved, err := httpx.StoreFor(c.Request, a.pm).MoveNodeFolder(body.Path, body.Parent)
	if err != nil {
		httpx.Err(c, folderStatus(err), err)
		return
	}
	a.notifyRoom(c.Request, "folders-changed", "")
	c.JSON(200, map[string]any{"ok": true, "path": moved})
}

func (a *API) postNodeFolderDelete(c *gin.Context) {
	var body struct {
		Path string `json:"path"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	if err := httpx.StoreFor(c.Request, a.pm).DeleteNodeFolder(body.Path); err != nil {
		httpx.Err(c, folderStatus(err), err)
		return
	}
	a.notifyRoom(c.Request, "folders-changed", "")
	c.JSON(200, map[string]any{"ok": true})
}

func (a *API) postNodesFolder(c *gin.Context) {
	var body struct {
		IDs    []string `json:"ids"`
		Folder string   `json:"folder"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	if len(body.IDs) == 0 {
		httpx.Err(c, http.StatusBadRequest, errors.New("no nodes given"))
		return
	}
	if err := httpx.StoreFor(c.Request, a.pm).MoveNodesToFolder(body.IDs, body.Folder); err != nil {
		httpx.Err(c, folderStatus(err), err)
		return
	}
	a.notifyRoom(c.Request, "folders-changed", "")
	c.JSON(200, map[string]any{"ok": true})
}
