package server

import (
	"context"

	runsapi "nodevas/internal/httpapi/runs"
	"nodevas/internal/runs"
	"nodevas/internal/store"
)

// activeRunOn reports whether a run that has not ended (queued, offered or
// running) works on the node, for batch undo: a Copilot draft's node an
// agent was just dispatched on is kept, not trashed under it. A lookup that
// fails counts as busy, so an undo never removes what it could not check.
func (s *Server) activeRunOn(ctx context.Context, st *store.Store, nodeID string) bool {
	if s.runs == nil {
		return false
	}
	project := runsapi.ProjectName(s.pm, st)
	if project == "" {
		return false
	}
	active, err := s.runs.Store().List(ctx, runs.Filter{Project: project, NodeID: nodeID, Active: true})
	return err != nil || len(active) > 0
}
