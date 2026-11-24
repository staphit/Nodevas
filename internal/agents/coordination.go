package agents

import (
	"context"
	"errors"
	"strings"
	"sync"
)

const (
	// MaxCoordinationNotices is the maximum number of overlap coordination
	// notices delivered to any single run (§C.3.3).
	MaxCoordinationNotices = 5

	coordinationActor = "nodevas"
	coordinationAgent = "nodevas"
)

var (
	// ErrNoSession is returned by CoordinationNotice when the target run has
	// no registered MCP session yet. Callers keep mailed_x = false and retry on
	// the run's next touched report (§C.3.3).
	ErrNoSession = errors.New("no session for run")

	// ErrCoordinationCap is returned when a run has already received
	// MaxCoordinationNotices overlap notices.
	ErrCoordinationCap = errors.New("coordination notice cap reached")
)

// hubCoordCounts stores per-Hub delivered coordination notice counts keyed by
// project + "\x00" + runID. Guarded by h.mu.
var hubCoordCounts sync.Map // *Hub -> map[string]int

func (h *Hub) coordCountMapLocked() map[string]int {
	if raw, ok := hubCoordCounts.Load(h); ok {
		return raw.(map[string]int)
	}
	m := make(map[string]int)
	hubCoordCounts.Store(h, m)
	return m
}

func coordinationSenderSession(runID string) string {
	id := strings.TrimSpace(runID)
	if len(id) > 8 {
		id = id[:8]
	}
	return SessionName("coordination-" + id)
}

// SessionForRun returns the session belonging to runID in project, preferring
// an online session and then the most recently seen one (§C.3.3). It never
// falls back to another session or node claim owner.
func (h *Hub) SessionForRun(project, runID string) (Session, bool) {
	if h == nil {
		return Session{}, false
	}
	project = strings.Trim(strings.TrimSpace(project), "/")
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return Session{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var best *Session
	for _, s := range h.sessions {
		if s.RunID != runID {
			continue
		}
		if project != "" && s.Project != project {
			continue
		}
		if best == nil || (s.Online && !best.Online) ||
			(s.Online == best.Online && s.LastSeen > best.LastSeen) {
			best = s
		}
	}
	if best == nil {
		return Session{}, false
	}
	return *best, true
}

// CoordinationNotice delivers an overlap coordination notice to runID's own
// MCP session in project (§C.3.3). It bypasses inbound holds (system: true)
// without carrying the HubNotice stamp (sender actor/agent "nodevas"), uses a
// dedicated sender session per recipient run so one busy repo cannot starve
// others, neutralises trust markers in body, and caps notices at
// MaxCoordinationNotices per run.
func (h *Hub) CoordinationNotice(ctx context.Context, project, runID, body string) error {
	if h == nil {
		return ErrNoSession
	}
	project = strings.Trim(strings.TrimSpace(project), "/")
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return ErrNoSession
	}
	sess, ok := h.SessionForRun(project, runID)
	if !ok {
		return ErrNoSession
	}

	// Reserve one slot under the per-run cap before sending so concurrent
	// notices cannot exceed MaxCoordinationNotices; release it if Send fails.
	capKey := project + "\x00" + runID
	h.mu.Lock()
	counts := h.coordCountMapLocked()
	if counts[capKey] >= MaxCoordinationNotices {
		h.mu.Unlock()
		return ErrCoordinationCap
	}
	counts[capKey]++
	h.mu.Unlock()

	_, err := h.Send(ctx, SendInput{
		Project:        project,
		SenderActor:    coordinationActor,
		SenderSession:  coordinationSenderSession(runID),
		SenderAgent:    coordinationAgent,
		To:             sess.Address(),
		NodeID:         sess.NodeID,
		Body:           EscapeTrustMarkers(body),
		system:         true,
		recipientActor: sess.ActorID,
	})
	if err != nil {
		h.mu.Lock()
		if counts := h.coordCountMapLocked(); counts[capKey] > 0 {
			counts[capKey]--
		}
		h.mu.Unlock()
		return err
	}
	return nil
}
