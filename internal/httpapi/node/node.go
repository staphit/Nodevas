// HTTP handlers for nodes: create, read, update, delete, duplicate, status and
// bulk delete. Routes: routes.go (routeNodes).

package node

import (
	"errors"
	"log"
	"net/http"
	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/store"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"nodevas/internal/engine"
	"nodevas/internal/locale"
)

func (a *API) getNode(c *gin.Context) {
	id := c.Param("id")
	if !engine.ValidNodeID(id) {
		httpx.Err(c, 400, locale.Errorf("invalid node id", "節點代號無效"))
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
		httpx.Err(c, 400, locale.Errorf("invalid node id", "節點代號無效"))
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
	id, err := httpx.StoreFor(c.Request, a.pm).CreateNode(auth.ActorFrom(c.Request), &body.Node, body.Body)
	if err != nil {
		if httpx.WriteDenied(c, err) {
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
	c.JSON(201, map[string]any{"ok": true, "id": id})
}

func (a *API) postNodeDuplicate(c *gin.Context) {
	id, err := httpx.StoreFor(c.Request, a.pm).DuplicateNode(
		auth.ActorFrom(c.Request), c.Param("id"), httpx.Language(c.Request))
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
		httpx.Err(c, http.StatusBadRequest, locale.Errorf("ids is required", "需要提供 ids"))
		return
	}
	if len(body.IDs) > store.MaxGraphNodes {
		httpx.Err(c, http.StatusBadRequest, locale.Errorf("too many nodes in one request", "單次請求的節點太多"))
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
		httpx.Err(c, 400, locale.Errorf("by or note is too long", "by 或 note 太長"))
		return
	}
	if len(body.Owner) > 128 || len(body.RequestID) > maxRequestID {
		httpx.Err(c, 400, locale.Errorf("owner or requestId is too long", "owner 或 requestId 太長"))
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
		httpx.Err(c, 400, locale.Errorf("body must be {id, t, by?, note?}", "內容須為 {id, t, by?, note?}"))
		return
	}
	if len(body.ID) > 128 || len(body.By) > 128 || len(body.Note) > 4096 {
		httpx.Err(c, 400, locale.Errorf("event fields are too long", "事件欄位太長"))
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
		httpx.Err(c, http.StatusBadRequest, locale.Errorf("unknown transfer mode %q", "不明的轉移方式「%s」", mode))
		return
	}
	if strings.TrimSpace(body.Target) == "" {
		httpx.Err(c, http.StatusBadRequest, locale.Errorf("target is required", "需要提供目標"))
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
			locale.Errorf("the source and target are the same project; use Duplicate instead", "來源與目標是同一個專案；請用「建立副本」"))
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
	} else if sourceName, ok := a.pm.NameFor(source.Root()); ok {
		// The request's own project: the one ?project= named, the caller's
		// current one, or the active one.
		payload.SetSourceProject(sourceName)
	}
	result, err := target.ImportNodes(auth.ActorFrom(c.Request), payload)
	language := httpx.Language(c.Request)
	if err != nil {
		var conflict *store.ErrConflict
		if errors.As(err, &conflict) {
			httpx.Conflict(c, conflict)
			return
		}
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	// Only the two projects changed; windows on any other keep their graph.
	a.hub.BroadcastRoom(target.Root(), "graph-changed", "")

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
				"error": locale.Say(
					"The nodes were copied to the target project, but removing them from the source failed: %v. Delete the source nodes by hand.",
					"節點已複製到目標專案，但來源專案的刪除失敗：%v。請手動刪除來源節點。", err).In(language),
				"ids":      result.IDs,
				"order":    result.Order,
				"warnings": locale.RenderAll(result.Warnings, language),
				"copied":   true,
			})
			return
		}
		trashFiles = deleteOutcome.TrashFiles
		cleanupPending = deleteOutcome.CleanupPending
		a.hub.BroadcastRoom(source.Root(), "graph-changed", "")
	}
	c.JSON(http.StatusOK, map[string]any{
		"ok":             true,
		"mode":           mode,
		"ids":            result.IDs,
		"order":          result.Order,
		"warnings":       locale.RenderAll(result.Warnings, language),
		"trashFiles":     trashFiles,
		"cleanupPending": cleanupPending,
	})
}
