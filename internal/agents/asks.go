package agents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	AskPending  = "pending"
	AskApproved = "approved"
	AskDenied   = "denied"
	AskExpired  = "expired"

	MaxAskTimeout = 10 * time.Minute

	// AskEvent is emitted with the Ask when one is created, answered or
	// expires.
	AskEvent = "agent-ask"
)

// Ask represents one human-in-the-loop approval/question from an agent session.
type Ask struct {
	ID          string `json:"id"`
	Project     string `json:"project"`
	SessionID   string `json:"sessionId"`
	SessionName string `json:"sessionName"`
	ActorID     string `json:"actorId"`
	Agent       string `json:"agent,omitempty"`
	Question    string `json:"question"`
	AboutMsgID  string `json:"aboutMsgId,omitempty"`
	NodeID      string `json:"nodeId,omitempty"`
	Status      string `json:"status"`
	DecidedBy   string `json:"decidedBy,omitempty"`
	Note        string `json:"note,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
	DecidedAt   int64  `json:"decidedAt,omitempty"`
}

// AskInput is the payload for creating a human-in-the-loop prompt.
type AskInput struct {
	Project     string
	SessionID   string
	SessionName string
	ActorID     string
	Agent       string
	Question    string
	AboutMsgID  string
	NodeID      string
	// FromRun: the asker is a run (the transport recognised it), whose
	// inbox is its own session's alone (recipientAliases).
	FromRun bool
}

// CreateAndWait records a pending ask, broadcasts it to the UI, and blocks
// until a human answers or timeout (or ctx) expires.
func (h *Hub) CreateAndWait(ctx context.Context, in AskInput, timeout time.Duration) (Ask, error) {
	project := strings.Trim(strings.TrimSpace(in.Project), "/")
	sessionID := strings.TrimSpace(in.SessionID)
	sessionName := strings.TrimSpace(in.SessionName)
	question := strings.TrimSpace(in.Question)
	if sessionID == "" {
		sessionID = sessionName
	}
	if sessionName == "" {
		sessionName = sessionID
	}
	if sessionID == "" || question == "" {
		return Ask{}, fmt.Errorf("%w: sessionId and question are required", ErrInvalid)
	}
	if len(question) > maxBodyBytes {
		return Ask{}, fmt.Errorf("%w: question exceeds %d bytes", ErrInvalid, maxBodyBytes)
	}
	if timeout <= 0 || timeout > MaxAskTimeout {
		timeout = MaxAskTimeout
	}

	actorID := strings.TrimSpace(in.ActorID)
	if actorID == "" {
		actorID = "local"
	}
	// The answer goes back to this session and, with aboutMsgId, out as a
	// verified approval from it: asking as a session another account holds
	// would borrow that person's name for both.
	if h.sessionHeldByOther(project, sessionID, sessionName, actorID) {
		return Ask{}, fmt.Errorf("%w: session %q belongs to another actor", ErrRefused, sessionName)
	}
	aboutMsgID := strings.TrimSpace(in.AboutMsgID)
	if aboutMsgID != "" {
		run := in.FromRun || h.runInbox(project, sessionName, actorID)
		if !h.receivedBy(ctx, project, aboutMsgID, sessionName, actorID, run) {
			return Ask{}, fmt.Errorf("%w: aboutMsgId must name a message this session received", ErrInvalid)
		}
	}
	now := h.nowMillis()
	ask := Ask{
		ID:          newID(),
		Project:     project,
		SessionID:   sessionID,
		SessionName: sessionName,
		ActorID:     actorID,
		Agent:       strings.TrimSpace(in.Agent),
		Question:    question,
		AboutMsgID:  aboutMsgID,
		NodeID:      strings.TrimSpace(in.NodeID),
		Status:      AskPending,
		CreatedAt:   now,
	}

	if err := h.insertPendingAsk(ctx, ask); err != nil {
		return Ask{}, err
	}

	waitCh := make(chan Ask, 1)
	h.mu.Lock()
	h.askWaiters[ask.ID] = waitCh
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.askWaiters, ask.ID)
		h.mu.Unlock()
	}()

	h.emit(project, AskEvent, ask)

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case decided := <-waitCh:
		return decided, nil
	case <-timer.C:
		expired, err := h.expireAsk(context.Background(), project, ask.ID)
		if err != nil {
			return ask, err
		}
		h.emit(project, AskEvent, expired)
		return expired, nil
	case <-ctx.Done():
		if expired, err := h.expireAsk(context.Background(), project, ask.ID); err == nil {
			h.emit(project, AskEvent, expired)
		}
		return Ask{}, ctx.Err()
	}
}

// sessionHeldByOther reports whether the session an ask names, by id or by
// name, is registered to an account other than actorID.
func (h *Hub) sessionHeldByOther(project, sessionID, sessionName, actorID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.sessions[project+"\x00"+sessionID]; ok && !strings.EqualFold(s.ActorID, actorID) {
		return true
	}
	return h.nameHeldByOtherLocked(project, sessionName, actorID)
}

// receivedBy reports whether message id was delivered to sessionName (as
// actorID): addressed to one of the inbox keys that session reads, and not
// to a session of another account.
func (h *Hub) receivedBy(ctx context.Context, project, id, sessionName, actorID string, run bool) bool {
	m, err := h.getMessageByID(ctx, project, id)
	if err != nil || m.Status != StatusDelivered {
		return false
	}
	if m.RecipientActor != "" && !strings.EqualFold(m.RecipientActor, actorID) {
		return false
	}
	recipient := strings.ToLower(strings.TrimSpace(m.Recipient))
	for _, alias := range recipientAliases(sessionName, actorID, run) {
		if alias == recipient {
			return true
		}
	}
	return false
}

func (h *Hub) insertPendingAsk(ctx context.Context, ask Ask) error {
	if h.database == nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		for _, existing := range h.memAsks {
			if existing.Project == ask.Project && existing.SessionID == ask.SessionID && existing.Status == AskPending {
				return ErrAskBusy
			}
		}
		copied := ask
		h.memAsks[ask.ID] = &copied
		return nil
	}
	return h.database.Tx(ctx, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM agent_asks
			 WHERE project = ? AND session_id = ? AND status = 'pending'
		`, ask.Project, ask.SessionID).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return ErrAskBusy
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO agent_asks (
				id, project, session_id, session_name, actor_id, agent,
				question, about_msg_id, node_id, status, decided_by, note,
				created_at, decided_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', '', '', ?, 0)
		`,
			ask.ID, ask.Project, ask.SessionID, ask.SessionName, ask.ActorID, ask.Agent,
			ask.Question, ask.AboutMsgID, ask.NodeID, ask.CreatedAt,
		)
		return err
	})
}

func (h *Hub) expireAsk(ctx context.Context, project, askID string) (Ask, error) {
	now := h.nowMillis()
	if h.database == nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		a, ok := h.memAsks[askID]
		if !ok || a.Project != project {
			return Ask{}, ErrNotFound
		}
		if a.Status == AskPending {
			a.Status = AskExpired
			a.DecidedAt = now
		}
		return *a, nil
	}
	_, _ = h.database.ExecContext(ctx, `
		UPDATE agent_asks
		   SET status = 'expired', decided_at = ?
		 WHERE project = ? AND id = ? AND status = 'pending'
	`, now, project, askID)
	return h.getAskByID(ctx, project, askID)
}

// Answer records a human decision ("approved" or "denied") on an ask request.
// If the ask was still waiting, the blocked tool call receives the result
// immediately. If the ask had already expired, the answer is delivered to the
// session's inbox as a message so the agent still receives it on its next turn.
// When `aboutMsgId` was set on the ask, a verified `<nodevas-approval>` notice
// is also sent back to the sender of that message.
//
// Only the person whose session asked, or an administrator, may answer: the
// answer is that person's decision, and an approval forwarded about a message
// speaks for them. To anyone else the ask does not exist.
func (h *Hub) Answer(ctx context.Context, project, askID, decision, note string, viewer Viewer) (Ask, error) {
	project = strings.Trim(strings.TrimSpace(project), "/")
	askID = strings.TrimSpace(askID)
	decision = strings.ToLower(strings.TrimSpace(decision))
	switch decision {
	case "approve", AskApproved:
		decision = AskApproved
	case "deny", AskDenied:
		decision = AskDenied
	default:
		return Ask{}, fmt.Errorf("%w: decision must be approved or denied", ErrInvalid)
	}
	note = strings.TrimSpace(note)
	if viewer.Agent {
		return Ask{}, fmt.Errorf("%w: only a person may answer ask_user requests", ErrRefused)
	}
	decidedBy := strings.TrimSpace(viewer.ID)
	if decidedBy == "" {
		decidedBy = "local"
	}
	mayAnswer := func(a Ask) bool {
		return viewer.Admin || strings.EqualFold(a.ActorID, decidedBy)
	}
	now := h.nowMillis()

	var updated Ask
	var wasExpired bool

	if h.database == nil {
		h.mu.Lock()
		a, ok := h.memAsks[askID]
		if !ok || a.Project != project || !mayAnswer(*a) {
			h.mu.Unlock()
			return Ask{}, ErrNotFound
		}
		if a.Status != AskPending && a.Status != AskExpired {
			existing := *a
			h.mu.Unlock()
			return existing, nil
		}
		wasExpired = a.Status == AskExpired
		a.Status = decision
		a.Note = note
		a.DecidedBy = decidedBy
		a.DecidedAt = now
		updated = *a
		h.mu.Unlock()
	} else {
		existing, err := h.getAskByID(ctx, project, askID)
		if err != nil {
			return Ask{}, err
		}
		if !mayAnswer(existing) {
			return Ask{}, ErrNotFound
		}
		wasExpired = existing.Status == AskExpired
		if existing.Status != AskPending && existing.Status != AskExpired {
			return existing, nil
		}
		if _, err := h.database.ExecContext(ctx, `
			UPDATE agent_asks
			   SET status = ?, note = ?, decided_by = ?, decided_at = ?
			 WHERE project = ? AND id = ?
		`, decision, note, decidedBy, now, project, askID); err != nil {
			return Ask{}, err
		}
		updated, err = h.getAskByID(ctx, project, askID)
		if err != nil {
			return Ask{}, err
		}
	}

	h.mu.Lock()
	waitCh, waiting := h.askWaiters[askID]
	h.mu.Unlock()
	if waiting {
		select {
		case waitCh <- updated:
		default:
		}
	} else if wasExpired && updated.SessionName != "" {
		// Late answer: deliver to the asking session's mailbox.
		body := fmt.Sprintf("[Late answer to ask %s (%q): %s]", updated.ID, updated.Question, updated.Status)
		if updated.Note != "" {
			body += "\nNote: " + updated.Note
		}
		_, _ = h.Send(ctx, SendInput{
			Project:       project,
			SenderActor:   decidedBy,
			SenderSession: "web",
			SenderAgent:   "human",
			IsHuman:       true,
			To:            updated.SessionName,
			NodeID:        updated.NodeID,
			Body:          body,
			system:        true,
		})
	}

	// If this ask referenced a peer message (`about`), forward a verified
	// approval notice to that message's sender.
	if updated.AboutMsgID != "" {
		if orig, err := h.getMessageByID(ctx, project, updated.AboutMsgID); err == nil && orig.SenderSession != "" {
			approvalBody := fmt.Sprintf(
				"<nodevas-approval from=%q about=%q decision=%q verified>\n%s\n</nodevas-approval>",
				shortActor(decidedBy), updated.AboutMsgID, updated.Status, updated.Note,
			)
			_, _ = h.Send(ctx, SendInput{
				Project:       project,
				SenderActor:   decidedBy,
				SenderSession: updated.SessionName,
				SenderAgent:   "human",
				IsHuman:       true,
				To:            orig.SenderSession,
				ReplyTo:       orig.ID,
				NodeID:        updated.NodeID,
				Body:          approvalBody,
				system:        true,
			})
		}
	}

	h.emit(project, AskEvent, updated)
	return updated, nil
}

func (h *Hub) getAskByID(ctx context.Context, project, id string) (Ask, error) {
	var a Ask
	err := h.database.QueryRowContext(ctx, `
		SELECT id, project, session_id, session_name, actor_id, agent,
		       question, about_msg_id, node_id, status, decided_by, note,
		       created_at, decided_at
		  FROM agent_asks
		 WHERE project = ? AND id = ?
	`, project, id).Scan(
		&a.ID, &a.Project, &a.SessionID, &a.SessionName, &a.ActorID, &a.Agent,
		&a.Question, &a.AboutMsgID, &a.NodeID, &a.Status, &a.DecidedBy, &a.Note,
		&a.CreatedAt, &a.DecidedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Ask{}, ErrNotFound
	}
	return a, err
}

// ListAsks returns ask requests for a project (filtered by status when non-empty).
func (h *Hub) ListAsks(ctx context.Context, project, status string) ([]Ask, error) {
	project = strings.Trim(strings.TrimSpace(project), "/")
	status = strings.ToLower(strings.TrimSpace(status))
	if h.database == nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		var out []Ask
		for _, a := range h.memAsks {
			if a.Project != project {
				continue
			}
			if status != "" && a.Status != status {
				continue
			}
			out = append(out, *a)
		}
		return out, nil
	}

	query := `
		SELECT id, project, session_id, session_name, actor_id, agent,
		       question, about_msg_id, node_id, status, decided_by, note,
		       created_at, decided_at
		  FROM agent_asks
		 WHERE project = ?`
	args := []any{project}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC LIMIT 100`

	rows, err := h.database.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ask
	for rows.Next() {
		var a Ask
		if err := rows.Scan(
			&a.ID, &a.Project, &a.SessionID, &a.SessionName, &a.ActorID, &a.Agent,
			&a.Question, &a.AboutMsgID, &a.NodeID, &a.Status, &a.DecidedBy, &a.Note,
			&a.CreatedAt, &a.DecidedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
