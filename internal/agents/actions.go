package agents

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"nodevas/internal/db"
)

// Session action log (agent analytics A): what an external agent session
// did, for the Explorer's Agent view. Nothing is kept without a database.

// Session actions.
const (
	ActionJoined  = "joined"
	ActionLeft    = "left"
	ActionDoing   = "doing"
	ActionMessage = "message"
	ActionAsk     = "ask"
	ActionChanges = "changes"
	ActionUsage   = "usage"
)

// MaxActionDetail caps an action's detail.
const MaxActionDetail = 300

// Action is one logged session action.
type Action struct {
	At     string `json:"at"`
	Action string `json:"action"`
	NodeID string `json:"nodeId"`
	Detail string `json:"detail"`
	RunID  string `json:"-"`
}

// actionStampLayout is how an action's time is stored (sortable UTC).
const actionStampLayout = "2006-01-02T15:04:05.000000Z"

// LogAction records one session action. It does nothing without a database
// and never fails the caller.
func (h *Hub) LogAction(project, sessionID, runID, action, nodeID, detail string) {
	database := h.actionDB()
	if database == nil || sessionID == "" {
		return
	}
	project = strings.Trim(strings.TrimSpace(project), "/")
	detail = capBytes(detail, MaxActionDetail)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = database.ExecContext(ctx, `
INSERT INTO agent_session_actions (project, session_id, run_id, at, action, node_id, detail)
VALUES (?, ?, ?, ?, ?, ?, ?)`, project, sessionID, runID, h.now().UTC().Format(actionStampLayout), action, nodeID, detail)
}

// Actions lists a session's actions, oldest first, at most limit.
func (h *Hub) Actions(ctx context.Context, project, sessionID string, limit int) ([]Action, error) {
	database := h.actionDB()
	if database == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	project = strings.Trim(strings.TrimSpace(project), "/")
	rows, err := database.QueryContext(ctx, `
SELECT at, action, node_id, detail, run_id FROM (
  SELECT id, at, action, node_id, detail, run_id FROM agent_session_actions
   WHERE project = ? AND session_id = ? ORDER BY at DESC, id DESC LIMIT ?
) newest ORDER BY at, id`, project, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Action
	for rows.Next() {
		var a Action
		if err := rows.Scan(&a.At, &a.Action, &a.NodeID, &a.Detail, &a.RunID); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetActionLog keeps the session action log in database even while the
// hub's messages stay in memory (a server whose agent network has no
// database of its own). SetDatabase covers it too.
func (h *Hub) SetActionLog(database *db.DB) {
	h.mu.Lock()
	h.actionsDB = database
	h.mu.Unlock()
}

// actionDB is where the action log goes: the hub's database, else the one
// SetActionLog gave; nil keeps nothing.
func (h *Hub) actionDB() *db.DB {
	if h == nil {
		return nil
	}
	if h.database != nil {
		return h.database
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.actionsDB
}

// capBytes cuts s to at most n bytes on a rune boundary.
func capBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// sessionRunID is the run a session belongs to, if any.
func (h *Hub) sessionRunID(project, sessionID string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.sessions[strings.Trim(strings.TrimSpace(project), "/")+"\x00"+strings.TrimSpace(sessionID)]; ok {
		return s.RunID
	}
	return ""
}
