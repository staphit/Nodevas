package agents

import "context"

// ReleaseRunClaim releases a node's live board claim only when its owner is
// the MCP session belonging to runID. Stub in Wave 0 (F1); filled by C1.
func (h *Hub) ReleaseRunClaim(ctx context.Context, project, nodeID, runID string) {}
