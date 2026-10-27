package agents

import "context"

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

// LogAction records one session action. It does nothing without a database
// and never fails the caller.
func (h *Hub) LogAction(project, sessionID, runID, action, nodeID, detail string) {
	// TODO(green): implement.
}

// Actions lists a session's actions, oldest first, at most limit.
func (h *Hub) Actions(ctx context.Context, project, sessionID string, limit int) ([]Action, error) {
	// TODO(green): implement.
	return nil, nil
}
