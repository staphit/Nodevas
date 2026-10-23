package agents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"nodevas/internal/runnerwire"
)

// External sessions (W6): an agent its owner runs in their own terminal
// reports, through `nodevas hook`, which files its edit tools touched while it
// held a node, and at Stop a diff snapshot of its working tree. The hub keeps
// one record per project, node and session: files accumulate (deduplicated,
// at most runnerwire.MaxSessionFiles), the snapshot is replaced each Stop.

// SessionChangesEvent is the realtime event a recorded change sends to the
// project's room, with the node's ID as the event ID.
const SessionChangesEvent = "session-changes"

// changeStampLayout is fixed width, so stored stamps sort as text.
const changeStampLayout = "2006-01-02T15:04:05.000000Z07:00"

// MaxSessionChangesListed caps one GET of a node's session changes.
const MaxSessionChangesListed = 50

// ClaimedNode is the node session holds a live claim on, or "" for none.
//
// The session's own report comes first: an agent that claims through
// `nodevas mcp` heartbeats the node it took, and that node counts while a
// live claim on it exists. Failing that, a live claim owned by the session's
// name counts, or one owned by its actor when no other online session in the
// project shares that actor — with two, the claim could be either's, and
// recording changes against the wrong node is worse than recording none.
func (h *Hub) ClaimedNode(project, sessionID string, claims []ClaimInfo) (Session, string, bool) {
	project = strings.Trim(strings.TrimSpace(project), "/")
	sessionID = strings.TrimSpace(sessionID)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pruneExpiredLocked(h.nowMillis())
	s, ok := h.sessions[project+"\x00"+sessionID]
	if !ok {
		return Session{}, "", false
	}
	session := *s
	live := make(map[string]bool, len(claims))
	for _, c := range claims {
		live[c.NodeID] = true
	}
	if session.NodeID != "" && live[session.NodeID] {
		return session, session.NodeID, true
	}
	actorUnique := true
	for _, other := range h.sessions {
		if other.Project == project && other.ID != session.ID && other.Online &&
			strings.EqualFold(other.ActorID, session.ActorID) {
			actorUnique = false
			break
		}
	}
	for _, c := range claims {
		owner := strings.ToLower(strings.TrimSpace(c.Owner))
		if owner == "" {
			continue
		}
		if owner == strings.ToLower(session.Name) {
			return session, c.NodeID, true
		}
		if actorUnique && (owner == strings.ToLower(session.ActorID) ||
			strings.TrimPrefix(owner, "mcp:") == strings.ToLower(shortActor(session.ActorID))) {
			return session, c.NodeID, true
		}
	}
	return session, "", true
}

// SessionChangeInput is one report from an external session's hook.
type SessionChangeInput struct {
	// Project is the project's name as ?project= takes it (stored); Room is
	// the key its browsers watch (realtime).
	Project string
	Room    string
	NodeID  string
	Session Session
	// Files are appended (deduplicated); Snapshot, when set, replaces the
	// stored one.
	Files    []string
	Snapshot *runnerwire.RunChanges
}

// memSessionChange is a stored record when the hub has no database.
type memSessionChange struct {
	runnerwire.SessionChange
}

// RecordSessionChange stores a hook's report against the node the session
// holds and announces it.
func (h *Hub) RecordSessionChange(ctx context.Context, in SessionChangeInput) (runnerwire.SessionChange, error) {
	if in.Project == "" || in.NodeID == "" || in.Session.ID == "" {
		return runnerwire.SessionChange{}, fmt.Errorf("%w: project, node and session are required", ErrInvalid)
	}
	now := h.now().UTC().Format(changeStampLayout)
	var out runnerwire.SessionChange
	var err error
	if h.database == nil {
		out = h.recordSessionChangeMem(in, now)
	} else {
		out, err = h.recordSessionChangeDB(ctx, in, now)
	}
	if err != nil {
		return runnerwire.SessionChange{}, err
	}
	room := in.Room
	if room == "" {
		room = in.Project
	}
	h.emit(room, SessionChangesEvent, in.NodeID)
	return out, nil
}

// mergeFiles appends what is new in add to files, keeping the first
// runnerwire.MaxSessionFiles.
func mergeFiles(files, add []string) []string {
	seen := make(map[string]bool, len(files)+len(add))
	out := make([]string, 0, len(files)+len(add))
	for _, list := range [][]string{files, add} {
		for _, f := range list {
			if f == "" || seen[f] || len(out) >= runnerwire.MaxSessionFiles {
				continue
			}
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

func (h *Hub) recordSessionChangeMem(in SessionChangeInput, now string) runnerwire.SessionChange {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.memSessionChanges == nil {
		h.memSessionChanges = map[string]*memSessionChange{}
	}
	key := in.Project + "\x00" + in.NodeID + "\x00" + in.Session.ID
	rec, ok := h.memSessionChanges[key]
	if !ok {
		rec = &memSessionChange{runnerwire.SessionChange{
			ID: newID(), Project: in.Project, NodeID: in.NodeID, SessionID: in.Session.ID,
			Files: []string{}, CreatedAt: now,
		}}
		h.memSessionChanges[key] = rec
	}
	applySession(&rec.SessionChange, in.Session)
	rec.Files = mergeFiles(rec.Files, in.Files)
	if in.Snapshot != nil {
		snap := *in.Snapshot
		rec.Snapshot = &snap
	}
	rec.UpdatedAt = now
	return copyChange(rec.SessionChange)
}

func applySession(rec *runnerwire.SessionChange, s Session) {
	rec.Session, rec.Agent, rec.ActorID = s.Name, s.Agent, s.ActorID
	if s.Repo != "" {
		rec.Repo = s.Repo
	}
	if s.Branch != "" {
		rec.Branch = s.Branch
	}
}

func copyChange(c runnerwire.SessionChange) runnerwire.SessionChange {
	c.Files = append([]string{}, c.Files...)
	if c.Snapshot != nil {
		snap := *c.Snapshot
		c.Snapshot = &snap
	}
	return c
}

const sessionChangeColumns = `id, project, node_id, session_id, session_name, agent, actor_id,
       repo, branch, files, snapshot, created_at, updated_at`

func scanSessionChange(row interface{ Scan(...any) error }) (runnerwire.SessionChange, error) {
	var c runnerwire.SessionChange
	var files, snapshot string
	if err := row.Scan(&c.ID, &c.Project, &c.NodeID, &c.SessionID, &c.Session, &c.Agent, &c.ActorID,
		&c.Repo, &c.Branch, &files, &snapshot, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return runnerwire.SessionChange{}, err
	}
	if err := json.Unmarshal([]byte(files), &c.Files); err != nil || c.Files == nil {
		c.Files = []string{}
	}
	if snapshot != "" {
		var snap runnerwire.RunChanges
		if json.Unmarshal([]byte(snapshot), &snap) == nil {
			c.Snapshot = &snap
		}
	}
	return c, nil
}

func (h *Hub) recordSessionChangeDB(ctx context.Context, in SessionChangeInput, now string) (runnerwire.SessionChange, error) {
	var out runnerwire.SessionChange
	err := h.database.Tx(ctx, func(tx *sql.Tx) error {
		rec, err := scanSessionChange(tx.QueryRowContext(ctx,
			`SELECT `+sessionChangeColumns+` FROM session_changes
			  WHERE project = ? AND node_id = ? AND session_id = ?`,
			in.Project, in.NodeID, in.Session.ID))
		fresh := errors.Is(err, sql.ErrNoRows)
		if err != nil && !fresh {
			return err
		}
		if fresh {
			rec = runnerwire.SessionChange{
				ID: newID(), Project: in.Project, NodeID: in.NodeID, SessionID: in.Session.ID,
				Files: []string{}, CreatedAt: now,
			}
		}
		applySession(&rec, in.Session)
		rec.Files = mergeFiles(rec.Files, in.Files)
		if in.Snapshot != nil {
			snap := *in.Snapshot
			rec.Snapshot = &snap
		}
		rec.UpdatedAt = now
		files, err := json.Marshal(rec.Files)
		if err != nil {
			return err
		}
		snapshot := ""
		if rec.Snapshot != nil {
			raw, err := json.Marshal(rec.Snapshot)
			if err != nil {
				return err
			}
			snapshot = string(raw)
		}
		if fresh {
			_, err = tx.ExecContext(ctx, `
INSERT INTO session_changes (id, project, node_id, session_id, session_name, agent, actor_id,
                             repo, branch, files, snapshot, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				rec.ID, rec.Project, rec.NodeID, rec.SessionID, rec.Session, rec.Agent, rec.ActorID,
				rec.Repo, rec.Branch, string(files), snapshot, rec.CreatedAt, rec.UpdatedAt)
		} else {
			_, err = tx.ExecContext(ctx, `
UPDATE session_changes
   SET session_name = ?, agent = ?, actor_id = ?, repo = ?, branch = ?, files = ?, snapshot = ?, updated_at = ?
 WHERE id = ?`,
				rec.Session, rec.Agent, rec.ActorID, rec.Repo, rec.Branch, string(files), snapshot, rec.UpdatedAt,
				rec.ID)
		}
		if err != nil {
			return err
		}
		out = rec
		return nil
	})
	return out, err
}

// NodeSessionChanges lists a node's session changes, most recently updated
// first.
func (h *Hub) NodeSessionChanges(ctx context.Context, project, nodeID string) ([]runnerwire.SessionChange, error) {
	out := []runnerwire.SessionChange{}
	if h.database == nil {
		h.mu.Lock()
		for _, rec := range h.memSessionChanges {
			if rec.Project == project && rec.NodeID == nodeID {
				out = append(out, copyChange(rec.SessionChange))
			}
		}
		h.mu.Unlock()
		sort.Slice(out, func(i, j int) bool {
			if out[i].UpdatedAt != out[j].UpdatedAt {
				return out[i].UpdatedAt > out[j].UpdatedAt
			}
			return out[i].ID > out[j].ID
		})
		if len(out) > MaxSessionChangesListed {
			out = out[:MaxSessionChangesListed]
		}
		return out, nil
	}
	rows, err := h.database.QueryContext(ctx,
		`SELECT `+sessionChangeColumns+` FROM session_changes
		  WHERE project = ? AND node_id = ?
		  ORDER BY updated_at DESC, id DESC LIMIT ?`,
		project, nodeID, MaxSessionChangesListed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanSessionChange(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
