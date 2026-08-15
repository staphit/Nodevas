// HTTP handlers for nodes: create, read, update, delete, duplicate, status and
// bulk delete. Routes: routes.go (routeNodes).

package node

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/store"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"nodevas/internal/engine"
)

func (a *API) getNode(c *gin.Context) {
	id := c.Param("id")
	if !engine.ValidNodeID(id) {
		httpx.Err(c, 400, errors.New("invalid node id"))
		return
	}
	content, rev, err := httpx.StoreFor(c.Request, a.pm).LoadNodeContent(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.JSON(200, map[string]string{"id": id, "content": "", "rev": ""})
			return
		}
		httpx.Err(c, 500, err)
		return
	}
	c.JSON(200, map[string]string{"id": id, "content": content, "rev": rev})
}

func (a *API) putNode(c *gin.Context) {
	id := c.Param("id")
	if !engine.ValidNodeID(id) {
		httpx.Err(c, 400, errors.New("invalid node id"))
		return
	}
	var body struct {
		Content string `json:"content"`
		BaseRev string `json:"baseRev"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	if _, err := engine.ParseNodeFile([]byte(body.Content)); err != nil {
		httpx.Err(c, http.StatusUnprocessableEntity, err)
		return
	}
	saved, rev, err := httpx.StoreFor(c.Request, a.pm).SaveNodeContent(
		auth.ActorFrom(c.Request), id, body.Content, body.BaseRev)
	if err != nil {
		if httpx.WriteDenied(c, err) {
			return
		}
		var conflict *store.ErrConflict
		if errors.As(err, &conflict) {
			httpx.Conflict(c, conflict)
			return
		}
		httpx.Err(c, 500, err)
		return
	}
	a.notifyRoom(c.Request, "node-changed", id)
	// The composed file rides back with the revision so an auto-saving client
	// does not have to re-read what it just wrote.
	c.JSON(200, map[string]any{"ok": true, "rev": rev, "content": saved})
}

func (a *API) postNode(c *gin.Context) {
	var body struct {
		engine.Node
		Body string `json:"body"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	id, err := httpx.StoreFor(c.Request, a.pm).CreateNode(&body.Node, body.Body)
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
	c.JSON(201, map[string]any{"ok": true, "id": id})
}

func (a *API) postNodeDuplicate(c *gin.Context) {
	id, err := httpx.StoreFor(c.Request, a.pm).DuplicateNode(c.Param("id"))
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	a.notifyRoom(c.Request, "graph-changed", "")
	a.notifyRoom(c.Request, "node-changed", id)
	c.JSON(http.StatusCreated, map[string]any{"ok": true, "id": id})
}

func (a *API) deleteNode(c *gin.Context) {
	outcome, err := httpx.StoreFor(c.Request, a.pm).DeleteNode(auth.ActorFrom(c.Request), c.Param("id"))
	if err != nil {
		if httpx.WriteDenied(c, err) {
			return
		}
		var cleanupUnavailable *store.DeleteCleanupUnavailableError
		if errors.As(err, &cleanupUnavailable) {
			httpx.Err(c, http.StatusServiceUnavailable, cleanupUnavailable)
			return
		}
		var conflict *store.ErrConflict
		if errors.As(err, &conflict) {
			httpx.Conflict(c, conflict)
			return
		}
		httpx.Err(c, 400, err)
		return
	}
	a.notifyRoom(c.Request, "graph-changed", "")
	c.JSON(200, map[string]any{
		"ok":             true,
		"trashFile":      outcome.TrashFile,
		"cleanupPending": outcome.CleanupPending,
	})
}

// postNodesDelete removes a whole selection in one graph write, so a batch
// delete cannot leave the board half cleared when one node turns out to be
// referenced by something that stays.
func (a *API) postNodesDelete(c *gin.Context) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	if len(body.IDs) == 0 {
		httpx.Err(c, http.StatusBadRequest, errors.New("ids is required"))
		return
	}
	if len(body.IDs) > store.MaxGraphNodes {
		httpx.Err(c, http.StatusBadRequest, errors.New("too many nodes in one request"))
		return
	}
	outcome, err := httpx.StoreFor(c.Request, a.pm).DeleteNodes(auth.ActorFrom(c.Request), body.IDs)
	if err != nil {
		if httpx.WriteDenied(c, err) {
			return
		}
		var cleanupUnavailable *store.DeleteCleanupUnavailableError
		if errors.As(err, &cleanupUnavailable) {
			httpx.Err(c, http.StatusServiceUnavailable, cleanupUnavailable)
			return
		}
		var conflict *store.ErrConflict
		if errors.As(err, &conflict) {
			httpx.Conflict(c, conflict)
			return
		}
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	a.notifyRoom(c.Request, "graph-changed", "")
	c.JSON(http.StatusOK, map[string]any{
		"ok":             true,
		"trashFiles":     outcome.TrashFiles,
		"cleanupPending": outcome.CleanupPending,
	})
}

// postStatus applies a status change; each transition is journaled and
// feeds the timeline view.
func (a *API) postStatus(c *gin.Context) {
	id := c.Param("id")
	var body struct {
		Status string `json:"status"`
		By     string `json:"by"`
		Note   string `json:"note"`
		// Owner names the claim the caller is acting under. An agent sends it
		// and is held to it; the editor sends nothing, and a person is not
		// locked out of their own board by an agent's claim — the override is
		// recorded instead of refused. See store.ReportStatus.
		Owner string `json:"owner"`
		// RequestID lets a retry after a lost answer be recognised, so the work
		// is written into the timeline once rather than twice.
		RequestID string `json:"requestId"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	if len(body.By) > 128 || len(body.Note) > 4096 {
		httpx.Err(c, 400, errors.New("by or note is too long"))
		return
	}
	if len(body.Owner) > 128 || len(body.RequestID) > maxRequestID {
		httpx.Err(c, 400, errors.New("owner or requestId is too long"))
		return
	}
	st := httpx.StoreFor(c.Request, a.pm)
	rs, err := st.ReportStatus(auth.ActorFrom(c.Request), id, engine.Status(body.Status),
		body.By, body.Note, body.Owner, body.RequestID)
	if err != nil {
		if httpx.WriteDenied(c, err) {
			return
		}
		respondClaimError(c, err)
		return
	}
	g, _, err := st.LoadGraph()
	if err != nil {
		httpx.Err(c, 500, err)
		return
	}
	a.notifyRoom(c.Request, "state-changed", "")
	c.JSON(200, map[string]any{
		"ok":       true,
		"state":    rs,
		"statuses": engine.ComputeStatuses(g, rs),
	})
}

// postEventMove drags a lifecycle stamp to another date (audited).
func (a *API) postEventMove(c *gin.Context) {
	var body struct {
		ID   string `json:"id"`
		T    string `json:"t"`
		By   string `json:"by"`
		Note string `json:"note"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	if body.ID == "" || body.T == "" {
		httpx.Err(c, 400, errors.New("body must be {id, t, by?, note?}"))
		return
	}
	if len(body.ID) > 128 || len(body.By) > 128 || len(body.Note) > 4096 {
		httpx.Err(c, 400, errors.New("event fields are too long"))
		return
	}
	st := httpx.StoreFor(c.Request, a.pm)
	rs, err := st.MoveEvent(auth.ActorFrom(c.Request), body.ID, body.T, body.By, body.Note)
	if err != nil {
		if httpx.WriteDenied(c, err) {
			return
		}
		httpx.Err(c, 400, err)
		return
	}
	g, _, err := st.LoadGraph()
	if err != nil {
		httpx.Err(c, 500, err)
		return
	}
	a.notifyRoom(c.Request, "state-changed", "")
	c.JSON(200, map[string]any{
		"ok":       true,
		"state":    rs,
		"statuses": engine.ComputeStatuses(g, rs),
	})
}

// postNodesTransfer copies or moves a node selection into another project.
//
// The two projects are named, not implied: source defaults to the request's
// project (?project= / X-Nodevas-Project, else the active one) so a client can
// paste into the project it currently shows while naming where the selection
// was cut from.
func (a *API) postNodesTransfer(c *gin.Context) {
	var body struct {
		Source string   `json:"source"`
		Target string   `json:"target"`
		IDs    []string `json:"ids"`
		Mode   string   `json:"mode"` // copy (default) | cut
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	mode := strings.TrimSpace(body.Mode)
	if mode == "" {
		mode = "copy"
	}
	if mode != "copy" && mode != "cut" {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("unknown transfer mode %q", mode))
		return
	}
	if strings.TrimSpace(body.Target) == "" {
		httpx.Err(c, http.StatusBadRequest, errors.New("target is required"))
		return
	}
	source := httpx.StoreFor(c.Request, a.pm)
	if name := strings.TrimSpace(body.Source); name != "" {
		resolved, err := a.pm.StoreFor(name)
		if err != nil {
			httpx.Err(c, http.StatusBadRequest, err)
			return
		}
		source = resolved
	}
	target, err := a.pm.StoreFor(strings.TrimSpace(body.Target))
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	if source == target {
		httpx.Err(c, http.StatusBadRequest,
			errors.New("來源與目標是同一個專案；請用「建立副本」"))
		return
	}

	payload, err := source.ExportNodes(body.IDs, mode == "cut")
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	// Node links in the moved documents are rewritten against this name, so
	// the API hands over the one the workspace uses instead of leaving the
	// store to infer it from directory paths.
	if name := strings.TrimSpace(body.Source); name != "" {
		payload.SetSourceProject(name)
	} else if active, activeErr := a.pm.ActiveProject(); activeErr == nil {
		payload.SetSourceProject(active.Name)
	}
	result, err := target.ImportNodes(payload)
	if err != nil {
		var conflict *store.ErrConflict
		if errors.As(err, &conflict) {
			httpx.Conflict(c, conflict)
			return
		}
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	a.hub.Broadcast("graph-changed", "")

	var trashFiles []string
	cleanupPending := false
	if mode == "cut" {
		deleteOutcome, deleteErr := source.DeleteNodes(auth.ActorFrom(c.Request), body.IDs)
		err = deleteErr
		if err != nil {
			// The copy is already committed. Saying "move failed" would be a
			// lie that leads the user to retry and copy everything twice.
			log.Printf("node transfer: copied but source cleanup failed: %v", err)
			c.JSON(http.StatusConflict, map[string]any{
				"error": fmt.Sprintf(
					"節點已複製到目標專案，但來源專案的刪除失敗：%v。請手動刪除來源節點。", err),
				"ids":      result.IDs,
				"order":    result.Order,
				"warnings": result.Warnings,
				"copied":   true,
			})
			return
		}
		trashFiles = deleteOutcome.TrashFiles
		cleanupPending = deleteOutcome.CleanupPending
		a.hub.Broadcast("graph-changed", "")
	}
	c.JSON(http.StatusOK, map[string]any{
		"ok":             true,
		"mode":           mode,
		"ids":            result.IDs,
		"order":          result.Order,
		"warnings":       result.Warnings,
		"trashFiles":     trashFiles,
		"cleanupPending": cleanupPending,
	})
}
