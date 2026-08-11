// HTTP handlers for version history and the trash. Routes: routes.go (routeHistory).

package graph

import (
	"errors"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/store"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

// getHistory lists snapshots for ?path=graph.yaml or ?path=nodes/<id>.md.
func (a *API) getHistory(c *gin.Context) {
	rel := c.Request.URL.Query().Get("path")
	if len(rel) > 256 {
		httpx.Err(c, 400, errors.New("history path is too long"))
		return
	}
	versions, err := httpx.StoreFor(c.Request, a.pm).ListHistory(rel)
	if err != nil {
		httpx.Err(c, 400, err)
		return
	}
	c.JSON(200, map[string]any{"path": rel, "versions": versions})
}

func (a *API) getHistoryVersion(c *gin.Context) {
	rel := c.Request.URL.Query().Get("path")
	version := c.Request.URL.Query().Get("version")
	if len(rel) > 256 || len(version) > 256 {
		httpx.Err(c, 400, errors.New("history path is too long"))
		return
	}
	data, err := httpx.StoreFor(c.Request, a.pm).ReadHistory(rel, version)
	if err != nil {
		httpx.Err(c, 400, err)
		return
	}
	// A .docx snapshot is a zip: there is nothing readable to send, and the
	// bytes would not survive JSON encoding intact.
	if !utf8.Valid(data) {
		httpx.Err(c, 400, errors.New("此格式的版本無法預覽，請直接還原"))
		return
	}
	// A snapshot is finished the moment it is written and is never rewritten:
	// snapshotHistory names each one after the nanosecond it was taken and only
	// ever creates new files, RestoreHistory copies a snapshot forward into the
	// live file rather than editing the snapshot, and pruneHistory only deletes
	// the oldest names. Because the names are monotonic timestamps, a pruned
	// name is never handed out again, so this URL cannot come to mean different
	// bytes later — it can only stop existing. Nothing else here changes: path
	// and version are echoed straight back from the query.
	httpx.Immutable(c)
	c.JSON(200, map[string]any{
		"path":    rel,
		"version": version,
		"content": string(data),
	})
}

func (a *API) postHistoryRestore(c *gin.Context) {
	var body struct {
		Path    string `json:"path"`
		Version string `json:"version"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	st := httpx.StoreFor(c.Request, a.pm)
	if err := st.RestoreHistory(body.Path, body.Version); err != nil {
		httpx.Err(c, 400, err)
		return
	}
	if id, ok := restoredNodeID(body.Path); ok {
		a.notifyRoom(c.Request, "node-changed", id)
	} else {
		a.notifyRoom(c.Request, "graph-changed", "")
	}
	c.JSON(200, map[string]bool{"ok": true})
}

// restoredNodeID names the node a restored file belongs to, so a subpage
// restore refreshes the same node its main document would.
func restoredNodeID(rel string) (string, bool) {
	const nodePrefix = "nodes/"
	if !strings.HasPrefix(rel, nodePrefix) {
		return "", false
	}
	tail := strings.TrimPrefix(rel, nodePrefix)
	if directory, _, isPage := strings.Cut(tail, "/"); isPage {
		id, ok := strings.CutSuffix(directory, ".pages")
		return id, ok
	}
	if !strings.HasSuffix(tail, ".md") {
		return "", false
	}
	return strings.TrimSuffix(tail, ".md"), true
}

func (a *API) getTrash(c *gin.Context) {
	items, err := httpx.StoreFor(c.Request, a.pm).ListTrash()
	if err != nil {
		httpx.Err(c, 500, err)
		return
	}
	c.JSON(200, map[string]any{"items": items})
}

func (a *API) postTrashRestore(c *gin.Context) {
	var body struct {
		File string `json:"file"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	if len(body.File) > 255 {
		httpx.Err(c, 400, errors.New("trash filename is too long"))
		return
	}
	id, err := httpx.StoreFor(c.Request, a.pm).RestoreTrash(body.File)
	if err != nil {
		var conflict *store.ErrConflict
		if errors.As(err, &conflict) {
			httpx.Conflict(c, conflict)
			return
		}
		httpx.Err(c, 400, err)
		return
	}
	a.notifyRoom(c.Request, "graph-changed", "")
	c.JSON(200, map[string]any{"ok": true, "id": id})
}
