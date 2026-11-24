package agents

import (
	"context"
	"strings"
)

// ClaimStore is the narrow board-claim surface ReleaseRunClaim needs to check
// and release a node's live claim without coupling internal/agents to
// internal/store.
type ClaimStore interface {
	LiveClaimOwner(nodeID string) string
	ReleaseClaim(nodeID, owner string) error
}

// ClaimStoreFunc resolves a project key to its ClaimStore, or nil when none is
// available.
type ClaimStoreFunc func(project string) ClaimStore

// SetClaimStore registers how h resolves a project's board claim store for
// ReleaseRunClaim (§C.2.5). Call it before serving.
func (h *Hub) SetClaimStore(fn ClaimStoreFunc) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.claimStores = fn
	h.mu.Unlock()
}

func (h *Hub) claimStoreFor(project string) ClaimStore {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	fn := h.claimStores
	h.mu.Unlock()
	if fn == nil {
		return nil
	}
	return fn(project)
}

// ReleaseRunClaim releases a node's live board claim only when its owner is
// the MCP session belonging to runID (§C.2.5, §C.10). It never releases a
// claim held by any other session or actor.
func (h *Hub) ReleaseRunClaim(_ context.Context, project, nodeID, runID string) {
	if h == nil {
		return
	}
	project = strings.TrimSpace(project)
	nodeID = strings.TrimSpace(nodeID)
	runID = strings.TrimSpace(runID)
	if nodeID == "" || runID == "" {
		return
	}
	cs := h.claimStoreFor(project)
	if cs == nil {
		return
	}
	liveOwner := strings.TrimSpace(cs.LiveClaimOwner(nodeID))
	if liveOwner == "" {
		return
	}

	h.mu.Lock()
	var matched *Session
	var allInProject []*Session
	for _, s := range h.sessions {
		if project != "" && s.Project != project {
			continue
		}
		copySess := *s
		allInProject = append(allInProject, &copySess)
		if s.RunID == runID && matched == nil {
			matched = &copySess
		}
	}
	h.mu.Unlock()

	if matched == nil {
		return
	}
	if !claimOwnedByRunSession(liveOwner, nodeID, matched, allInProject) {
		return
	}
	_ = cs.ReleaseClaim(nodeID, liveOwner)
}

// claimOwnedByRunSession reports whether liveOwner belongs unambiguously to
// matched (the MCP session with RunID == runID).
func claimOwnedByRunSession(liveOwner, nodeID string, matched *Session, peers []*Session) bool {
	if matched == nil || liveOwner == "" {
		return false
	}
	// Exact session identity: session ID, unique session name, or address.
	if liveOwner == matched.ID ||
		(matched.Name != "" && strings.EqualFold(liveOwner, matched.Name)) ||
		(matched.Name != "" && strings.EqualFold(liveOwner, matched.Address())) {
		return true
	}
	// ActorID match is allowed only when this run session explicitly reported
	// working on nodeID and no other session in the project shares that actor
	// on the same node (or without a reported node).
	if matched.NodeID == nodeID && matched.ActorID != "" &&
		(strings.EqualFold(liveOwner, matched.ActorID) ||
			strings.EqualFold(strings.TrimPrefix(liveOwner, "mcp:"), shortActor(matched.ActorID))) {
		for _, other := range peers {
			if other.ID == matched.ID || other.RunID == matched.RunID {
				continue
			}
			if sameActor(other.ActorID, matched.ActorID) && (other.NodeID == "" || other.NodeID == nodeID) {
				return false
			}
		}
		return true
	}
	return false
}
