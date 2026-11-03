package agents

import (
	"net/http"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"nodevas/internal/agents"
	"nodevas/internal/engine"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/locale"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// External session changes (W6):
//
//	POST /api/agents/sessions/:id/changes {files?, snapshot?, actor?}
//	     → {recorded, nodeId}; recorded false when the session holds no node
//	GET  /api/nodes/:id/session-changes → runnerwire.SessionChangesResponse
//
// The node a report counts against is never the client's to name: it is the
// node the session holds a live claim on when the report arrives.

// projectName is the request's project as ?project= takes it: its path below
// the workspace, slash-separated, or "." for the workspace root (as runs
// record it).
func (a *API) projectName(r *http.Request) string {
	if a.pm == nil {
		// No workspace behind this handler (tests): the name asked for is
		// the only name there is.
		if name := httpx.RequestProjectName(r); name != "" {
			return name
		}
		return "."
	}
	st := httpx.StoreFor(r, a.pm)
	if st == nil {
		return ""
	}
	rel, err := filepath.Rel(a.pm.Workspace(), st.Root())
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

func validChangedPath(p string) bool {
	return p != "" && len(p) <= runs.MaxPathBytes && utf8.ValidString(p) && !strings.ContainsRune(p, 0)
}

func (a *API) postSessionChanges(c *gin.Context) {
	sessionID := strings.TrimSpace(c.Param("id"))
	var body struct {
		Actor    string                 `json:"actor"`
		Files    []string               `json:"files"`
		Snapshot *runnerwire.RunChanges `json:"snapshot"`
	}
	if !httpx.DecodeJSONLimit(c, &body, runnerwire.MaxChangesMessageBytes) {
		return
	}
	invalid := func(english, chinese string) {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", locale.Errorf(english, chinese), nil)
	}
	if sessionID == "" || len(sessionID) > 256 {
		invalid("session id is missing or too long", "工作階段代號缺少或太長")
		return
	}
	if len(body.Files) == 0 && body.Snapshot == nil {
		invalid("body must carry files or a snapshot", "內容須包含檔案或快照")
		return
	}
	if len(body.Files) > runnerwire.MaxSessionFiles {
		invalid("too many files", "檔案太多")
		return
	}
	for _, f := range body.Files {
		if !validChangedPath(f) {
			invalid("a file path is empty, too long or not text", "檔案路徑是空的、太長或不是文字")
			return
		}
	}
	if body.Snapshot != nil {
		if body.Snapshot.Files == nil {
			body.Snapshot.Files = []runnerwire.FileChange{}
		}
		if err := runs.ValidateChanges(*body.Snapshot); err != nil {
			httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", err, nil)
			return
		}
	}
	name := a.projectName(c.Request)
	if name == "" {
		invalid("no project selected", "沒有選擇專案")
		return
	}
	projectKey := a.projectKey(c.Request)
	actorID, _ := resolveActor(c.Request, body.Actor)
	claims, _ := a.liveClaimsAndUsers(c.Request)
	session, nodeID, found := a.hub.ClaimedNode(projectKey, sessionID, claims)
	if !found {
		httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", locale.Errorf("no such session; heartbeat first", "沒有這個工作階段；請先送出心跳"), nil)
		return
	}
	// A session ID is bound to the actor that registered it, as in Touch.
	if !strings.EqualFold(session.ActorID, actorID) {
		fail(c, agents.ErrRefused)
		return
	}
	if nodeID == "" {
		c.JSON(http.StatusOK, map[string]any{"recorded": false})
		return
	}
	if _, err := a.hub.RecordSessionChange(c.Request.Context(), agents.SessionChangeInput{
		Project: name, Room: projectKey, NodeID: nodeID, Session: session,
		Files: body.Files, Snapshot: body.Snapshot,
	}); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"recorded": true, "nodeId": nodeID})
}

// getNodeSessionChanges lists a node's external session changes, for anyone
// who can see the node.
func (a *API) getNodeSessionChanges(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	id := c.Param("id")
	if !engine.ValidNodeID(id) {
		httpx.Err(c, http.StatusBadRequest, locale.Errorf("invalid node id", "節點代號無效"))
		return
	}
	name := a.projectName(c.Request)
	if name == "" {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", locale.Errorf("no project selected", "沒有選擇專案"), nil)
		return
	}
	changes, err := a.hub.NodeSessionChanges(c.Request.Context(), name, id)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, runnerwire.SessionChangesResponse{Changes: changes})
}
