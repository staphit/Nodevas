package agents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxBodyBytes            = 32 << 10
	maxMessagesPerMinute    = 20
	maxConsecutiveAgentTurn = 12
	MaxInboxWait            = 10 * time.Minute

	// A message is delivered (in its recipient's inbox), held (waiting for a
	// person to approve it, because the recipient session holds its inbound
	// mail), or rejected (a person said no; it never reaches the inbox).
	StatusDelivered = "delivered"
	StatusHeld      = "held"
	StatusRejected  = "rejected"
)

// Message is one envelope in the agent network.
type Message struct {
	ID            string     `json:"id"`
	Project       string     `json:"project"`
	From          string     `json:"from"`
	SenderActor   string     `json:"senderActor"`
	SenderSession string     `json:"senderSession,omitempty"`
	SenderAgent   string     `json:"senderAgent,omitempty"`
	Recipient     string     `json:"to"`
	ReplyTo       string     `json:"replyTo,omitempty"`
	ThreadID      string     `json:"threadId,omitempty"`
	NodeID        string     `json:"nodeId,omitempty"`
	Body          string     `json:"body"`
	CreatedAt     int64      `json:"createdAt"`
	ReadAt        int64      `json:"readAt,omitempty"`
	Status        string     `json:"status"`
	Notify        bool       `json:"notify"`
	Files         []FileInfo `json:"files,omitempty"`
	DecidedBy     string     `json:"decidedBy,omitempty"`
	DecidedAt     int64      `json:"decidedAt,omitempty"`
	// RecipientActor is the account behind the recipient session when the
	// message was sent, empty when the address named no known session. It
	// decides who may approve the message or read its files, so it is not
	// something to hand to every browser in the project.
	RecipientActor string `json:"-"`
}

// SendInput describes an outbound message from an agent session or human.
type SendInput struct {
	Project       string
	SenderActor   string
	SenderSession string
	SenderAgent   string
	IsHuman       bool
	To            string
	ReplyTo       string
	NodeID        string
	Body          string
	Notify        bool
	Files         []FileInput
	Claims        []ClaimInfo
	// FromRun: the sender is a run (Session.Run), set by the transport. Its
	// mail to anything but another run is held for the recipient's person.
	FromRun bool

	// system marks the hub's own notices (a late ask_user answer, a verified
	// approval): they carry a decision a person already made, so a hold that
	// asks a person again would only ask the same question twice.
	system bool
}

// DeliveryStatus reports the outcome for one resolved recipient.
type DeliveryStatus struct {
	Recipient string `json:"recipient"`
	Status    string `json:"status"` // "delivered" | "queued" | "held"
	MessageID string `json:"messageId"`
}

// SendResult is returned by Send.
type SendResult struct {
	ThreadID   string           `json:"threadId"`
	Deliveries []DeliveryStatus `json:"deliveries"`
}

// Send validates, resolves recipients, enforces loop guards, persists the
// message(s), and wakes any waiting inbox pollers or WebSocket listeners.
func (h *Hub) Send(ctx context.Context, in SendInput) (SendResult, error) {
	project := strings.Trim(strings.TrimSpace(in.Project), "/")
	to := strings.TrimSpace(in.To)
	body := strings.TrimSpace(in.Body)
	if to == "" {
		return SendResult{}, fmt.Errorf("%w: 'to' address is required", ErrInvalid)
	}
	files, err := prepareFiles(in.Files)
	if err != nil {
		return SendResult{}, err
	}
	// Files can be the whole message; text alone must say something.
	if body == "" && len(files) == 0 {
		return SendResult{}, fmt.Errorf("%w: 'message' body is required", ErrInvalid)
	}
	if len(body) > maxBodyBytes {
		return SendResult{}, fmt.Errorf("%w: message exceeds %d bytes", ErrInvalid, maxBodyBytes)
	}

	// Group broadcast `*` is reserved for humans.
	if (to == "*" || strings.HasSuffix(to, "/*") || strings.HasPrefix(to, "repo:")) && !in.IsHuman {
		return SendResult{}, fmt.Errorf("%w: broadcast address %q is only allowed from a human session", ErrRefused, to)
	}

	senderActor := strings.TrimSpace(in.SenderActor)
	if senderActor == "" {
		senderActor = "local"
	}
	senderSession := strings.TrimSpace(in.SenderSession)
	if senderSession == "" {
		if in.IsHuman {
			senderSession = "web"
		} else {
			senderSession = shortActor(senderActor)
		}
	}
	senderAgent := strings.TrimSpace(in.SenderAgent)
	if senderAgent == "" {
		if in.IsHuman {
			senderAgent = "human"
		} else {
			senderAgent = "agent"
		}
	}

	replyTo := strings.TrimSpace(in.ReplyTo)
	threadID := ""
	nodeID := strings.TrimSpace(in.NodeID)
	if replyTo != "" {
		parent, err := h.getMessageByID(ctx, project, replyTo)
		if err == nil {
			threadID = parent.ThreadID
			if threadID == "" {
				threadID = parent.ID
			}
			if nodeID == "" {
				nodeID = parent.NodeID
			}
		}
	}

	now := h.nowMillis()
	targets, onlineMap, err := h.resolveRecipients(project, to, in.Claims)
	if err != nil {
		return SendResult{}, err
	}

	h.mu.Lock()
	if err := h.checkAndRecordLoopGuardLocked(project, senderActor+"/"+senderSession, threadID, in.IsHuman, now); err != nil {
		h.mu.Unlock()
		return SendResult{}, err
	}
	// The hold is decided per recipient, now: a broadcast reaches the open
	// sessions at once and waits at the holding ones.
	recipientActor := make(map[string]string, len(targets))
	held := make(map[string]bool, len(targets))
	for _, recipient := range targets {
		s := h.sessionByNameLocked(project, recipient)
		if s != nil {
			recipientActor[recipient] = s.ActorID
			held[recipient] = s.Inbound == InboundHold && !in.system
		}
		// A run (which reads attacker-controllable content) does not reach a
		// person's session, or an address no session holds, unasked.
		if in.FromRun && !in.system && (s == nil || !s.Run) {
			held[recipient] = true
		}
	}
	h.mu.Unlock()

	from := shortActor(senderActor) + "/" + senderSession
	deliveries := make([]DeliveryStatus, 0, len(targets))
	var created []Message
	delivered := false

	for _, recipient := range targets {
		msgID := newID()
		tID := threadID
		if tID == "" {
			tID = msgID
		}
		status := StatusDelivered
		if held[recipient] {
			status = StatusHeld
		}
		msg := Message{
			ID:             msgID,
			Project:        project,
			From:           from,
			SenderActor:    senderActor,
			SenderSession:  senderSession,
			SenderAgent:    senderAgent,
			Recipient:      recipient,
			ReplyTo:        replyTo,
			ThreadID:       tID,
			NodeID:         nodeID,
			Body:           body,
			CreatedAt:      now,
			Status:         status,
			Notify:         in.Notify,
			Files:          fileInfos(files),
			RecipientActor: recipientActor[recipient],
		}
		if err := h.persistMessage(ctx, msg, files); err != nil {
			return SendResult{}, err
		}
		state := "queued"
		switch {
		case status == StatusHeld:
			state = StatusHeld
		case onlineMap[recipient]:
			state = StatusDelivered
		}
		if status == StatusDelivered {
			delivered = true
		}
		deliveries = append(deliveries, DeliveryStatus{
			Recipient: recipient,
			Status:    state,
			MessageID: msgID,
		})
		created = append(created, msg)
		if threadID == "" {
			threadID = tID
		}
	}

	if delivered {
		h.wakeInboxWaiters(project)
	}
	for _, msg := range created {
		h.emit(project, "agent-message", msg)
		if msg.Status == StatusDelivered && msg.Notify {
			h.emit(project, "agent-notify", msg.ID)
		}
	}
	if in.SenderSession != "" && !in.IsHuman {
		h.LogAction(project, in.SenderSession, h.sessionRunID(project, in.SenderSession), ActionMessage,
			strings.TrimSpace(in.NodeID), to)
	}
	return SendResult{
		ThreadID:   threadID,
		Deliveries: deliveries,
	}, nil
}

func (h *Hub) checkAndRecordLoopGuardLocked(project, senderKey, threadID string, isHuman bool, now int64) error {
	// 1. Per-sender rate limit (rolling 60s window).
	rateKey := project + "\x00" + senderKey
	cutoff := now - 60_000
	history := h.senderHistory[rateKey]
	kept := history[:0]
	for _, ts := range history {
		if ts >= cutoff {
			kept = append(kept, ts)
		}
	}
	if !isHuman && len(kept) >= maxMessagesPerMinute {
		h.senderHistory[rateKey] = kept
		return ErrRateLimited
	}
	h.senderHistory[rateKey] = append(kept, now)

	// 2. Thread consecutive agent-to-agent turn cap.
	if threadID != "" {
		tKey := project + "\x00" + threadID
		if isHuman {
			delete(h.threadTurns, tKey)
		} else {
			turns := h.threadTurns[tKey] + 1
			if turns > maxConsecutiveAgentTurn {
				return ErrLoopDetected
			}
			h.threadTurns[tKey] = turns
		}
	}
	return nil
}

// resolveRecipients maps an address (`alice/nodevas-claude`, `nodevas-claude`,
// `alice/*`, `repo:foo`, `node:task-1`, `*`) onto canonical recipient keys and
// reports whether each target currently has an online session.
func (h *Hub) resolveRecipients(project, to string, claims []ClaimInfo) ([]string, map[string]bool, error) {
	sessions := h.ListSessions(project, "", true, claims)
	online := make(map[string]bool)
	var matched []string
	seen := make(map[string]bool)
	add := func(rec string, isOnline bool) {
		rec = strings.ToLower(strings.TrimSpace(rec))
		if rec == "" || seen[rec] {
			return
		}
		seen[rec] = true
		matched = append(matched, rec)
		if isOnline {
			online[rec] = true
		}
	}

	lowerTo := strings.ToLower(strings.TrimSpace(to))
	switch {
	case lowerTo == "*":
		for _, s := range sessions {
			if s.Online {
				add(s.Name, true)
			}
		}
		if len(matched) == 0 {
			add("*", false)
		}
	case strings.HasSuffix(lowerTo, "/*"):
		actorPrefix := strings.TrimSuffix(lowerTo, "/*")
		for _, s := range sessions {
			if strings.EqualFold(shortActor(s.ActorID), actorPrefix) || strings.EqualFold(s.ActorID, actorPrefix) {
				add(s.Name, s.Online)
			}
		}
		if len(matched) == 0 {
			return nil, nil, fmt.Errorf("%w: no sessions found for %q", ErrNotFound, to)
		}
	case strings.HasPrefix(lowerTo, "repo:"):
		wantRepo := strings.TrimPrefix(lowerTo, "repo:")
		for _, s := range sessions {
			base := strings.ToLower(filepath.Base(s.Repo))
			if strings.EqualFold(s.Repo, wantRepo) || base == wantRepo {
				add(s.Name, s.Online)
			}
		}
		if len(matched) == 0 {
			return nil, nil, fmt.Errorf("%w: no sessions found for %q", ErrNotFound, to)
		}
	case strings.HasPrefix(lowerTo, "node:"):
		nodeID := strings.TrimSpace(to[len("node:"):])
		for _, s := range sessions {
			if strings.EqualFold(s.NodeID, nodeID) {
				add(s.Name, s.Online)
			}
		}
		if len(matched) == 0 {
			for _, c := range claims {
				if strings.EqualFold(c.NodeID, nodeID) && c.Owner != "" {
					add(strings.TrimPrefix(c.Owner, "mcp:"), false)
				}
			}
		}
		if len(matched) == 0 {
			return nil, nil, fmt.Errorf("%w: no agent holds node %q", ErrNotFound, nodeID)
		}
	default:
		// Direct session name or `<actor>/<session>`.
		targetName := lowerTo
		targetActor := ""
		if parts := strings.SplitN(lowerTo, "/", 2); len(parts) == 2 {
			targetActor = parts[0]
			targetName = parts[1]
		}
		for _, s := range sessions {
			nameMatch := strings.EqualFold(s.Name, targetName) || strings.EqualFold(s.ID, lowerTo)
			actorMatch := targetActor == "" ||
				strings.EqualFold(shortActor(s.ActorID), targetActor) ||
				strings.EqualFold(s.ActorID, targetActor)
			if nameMatch && actorMatch {
				add(s.Name, s.Online)
			}
		}
		// Offline-session mail waits under the session name even if the session
		// has not connected yet.
		if len(matched) == 0 {
			add(targetName, false)
		}
	}
	return matched, online, nil
}

// messageColumns is every agent_messages column a Message is read from, in
// scanMessage's order.
const messageColumns = `id, project, sender_actor, sender_session, sender_agent,
	       recipient, reply_to, thread_id, node_id, body, created_at, read_at,
	       status, notify, recipient_actor, decided_by, decided_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanMessage(row rowScanner) (Message, error) {
	var m Message
	// notify is an INTEGER on both engines: a Go bool does not bind to one on
	// PostgreSQL.
	var notify int64
	if err := row.Scan(
		&m.ID, &m.Project, &m.SenderActor, &m.SenderSession, &m.SenderAgent,
		&m.Recipient, &m.ReplyTo, &m.ThreadID, &m.NodeID, &m.Body, &m.CreatedAt, &m.ReadAt,
		&m.Status, &notify, &m.RecipientActor, &m.DecidedBy, &m.DecidedAt,
	); err != nil {
		return Message{}, err
	}
	m.Notify = notify != 0
	m.From = shortActor(m.SenderActor) + "/" + m.SenderSession
	return m, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (h *Hub) persistMessage(ctx context.Context, msg Message, files []storedFile) error {
	if h.database == nil {
		h.mu.Lock()
		h.memMessages = append(h.memMessages, msg)
		if len(files) > 0 {
			h.memFiles[msg.ID] = files
		}
		h.mu.Unlock()
		return nil
	}
	return h.database.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO agent_messages (
				id, project, sender_actor, sender_session, sender_agent,
				recipient, reply_to, thread_id, node_id, body, created_at, read_at,
				status, notify, recipient_actor, decided_by, decided_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, '', 0)
		`,
			msg.ID, msg.Project, msg.SenderActor, msg.SenderSession, msg.SenderAgent,
			msg.Recipient, msg.ReplyTo, msg.ThreadID, msg.NodeID, msg.Body, msg.CreatedAt,
			msg.Status, boolInt(msg.Notify), msg.RecipientActor,
		); err != nil {
			return err
		}
		for _, f := range files {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO agent_message_files (message_id, name, size, sha256, content)
				VALUES (?, ?, ?, ?, ?)
			`, msg.ID, f.Name, f.Size, f.SHA256, f.content); err != nil {
				return err
			}
		}
		return nil
	})
}

func (h *Hub) getMessageByID(ctx context.Context, project, id string) (Message, error) {
	if h.database == nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		for _, m := range h.memMessages {
			if m.Project == project && m.ID == id {
				m.Files = fileInfos(h.memFiles[m.ID])
				return m, nil
			}
		}
		return Message{}, ErrNotFound
	}
	m, err := scanMessage(h.database.QueryRowContext(ctx, `
		SELECT `+messageColumns+`
		  FROM agent_messages
		 WHERE project = ? AND id = ?
	`, project, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	if err != nil {
		return Message{}, err
	}
	out := []Message{m}
	if err := h.attachFileInfos(ctx, out); err != nil {
		return Message{}, err
	}
	return out[0], nil
}

// recipientAliases returns the inbox keys a session or actor checks. A run's
// inbox is its own session's alone: it acts as its runner's owner, and mail
// addressed to that person, or to everyone, is for the person's own sessions,
// not for an agent working whatever the board told it to.
func recipientAliases(sessionName, actorID string, run bool) []string {
	var out []string
	add := func(v string) {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" {
			return
		}
		for _, existing := range out {
			if existing == v {
				return
			}
		}
		out = append(out, v)
	}
	add(sessionName)
	if sessionName != "" && actorID != "" {
		add(shortActor(actorID) + "/" + sessionName)
	}
	if run {
		return out
	}
	add(actorID)
	add(shortActor(actorID))
	add("*")
	return out
}

// runInbox reports whether the inbox of sessionName, asked for as actorID, is
// a run's: a "run:" actor, or a session a runner dispatched.
func (h *Hub) runInbox(project, sessionName, actorID string) bool {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(actorID)), "run:") {
		return true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.sessionByNameLocked(project, strings.TrimSpace(sessionName))
	return s != nil && s.Run && strings.EqualFold(s.ActorID, strings.TrimSpace(actorID))
}

// inboxRefused reports whether actorID is asking for the inbox of a session
// another actor holds; mail is addressed to session names, so without this any
// account could drain any agent's inbox by naming it.
func (h *Hub) inboxRefused(project, sessionName, actorID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.nameHeldByOtherLocked(project, strings.TrimSpace(sessionName), actorID)
}

// CheckInbox returns unread messages for a session/actor and marks them read.
// If wait > 0 and the mailbox is currently empty, it long-polls up to wait (or
// ctx cancellation) before returning. Held and rejected messages are never
// returned: only an approval puts a held one in the inbox.
func (h *Hub) CheckInbox(ctx context.Context, project, sessionName, actorID string, wait time.Duration) ([]Message, error) {
	return h.checkInbox(ctx, project, sessionName, actorID, wait, false)
}

// CheckRunInbox is CheckInbox for a run the transport recognised (a request
// through a runner's proxy): it reads its own session's mail only.
func (h *Hub) CheckRunInbox(ctx context.Context, project, sessionName, actorID string, wait time.Duration) ([]Message, error) {
	return h.checkInbox(ctx, project, sessionName, actorID, wait, true)
}

func (h *Hub) checkInbox(ctx context.Context, project, sessionName, actorID string, wait time.Duration, run bool) ([]Message, error) {
	project = strings.Trim(strings.TrimSpace(project), "/")
	if wait > MaxInboxWait {
		wait = MaxInboxWait
	}
	if h.inboxRefused(project, sessionName, actorID) {
		return nil, fmt.Errorf("%w: session %q belongs to another actor", ErrRefused, sessionName)
	}
	aliases := recipientAliases(sessionName, actorID, run || h.runInbox(project, sessionName, actorID))

	deadline := time.Time{}
	if wait > 0 {
		deadline = time.Now().Add(wait)
	}

	for {
		ch := h.registerInboxWaiter(project)
		msgs, err := h.popUnread(ctx, project, aliases)
		if err != nil {
			h.unregisterInboxWaiter(project, ch)
			return nil, err
		}
		if len(msgs) > 0 || wait <= 0 {
			h.unregisterInboxWaiter(project, ch)
			return msgs, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			h.unregisterInboxWaiter(project, ch)
			return nil, nil
		}
		timer := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			timer.Stop()
			h.unregisterInboxWaiter(project, ch)
			return nil, ctx.Err()
		case <-timer.C:
			h.unregisterInboxWaiter(project, ch)
			return h.popUnread(ctx, project, aliases)
		case <-ch:
			timer.Stop()
			h.unregisterInboxWaiter(project, ch)
		}
	}
}

func (h *Hub) popUnread(ctx context.Context, project string, aliases []string) ([]Message, error) {
	if len(aliases) == 0 {
		return nil, nil
	}
	now := h.nowMillis()
	if h.database == nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		aliasSet := make(map[string]bool, len(aliases))
		for _, a := range aliases {
			aliasSet[a] = true
		}
		var out []Message
		for i := range h.memMessages {
			m := &h.memMessages[i]
			if m.Project == project && m.ReadAt == 0 && m.Status == StatusDelivered &&
				aliasSet[strings.ToLower(m.Recipient)] {
				m.ReadAt = now
				copied := *m
				copied.Files = fileInfos(h.memFiles[m.ID])
				out = append(out, copied)
			}
		}
		return out, nil
	}

	var out []Message
	err := h.database.Tx(ctx, func(tx *sql.Tx) error {
		placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(aliases)), ", ")
		args := make([]any, 0, len(aliases)+1)
		args = append(args, project)
		for _, a := range aliases {
			args = append(args, a)
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT `+messageColumns+`
			  FROM agent_messages
			 WHERE project = ? AND read_at = 0 AND status = 'delivered' AND recipient IN (`+placeholders+`)
			 ORDER BY created_at ASC, id ASC
		`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			m, err := scanMessage(rows)
			if err != nil {
				rows.Close()
				return err
			}
			m.ReadAt = now
			out = append(out, m)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, m := range out {
			if _, err := tx.ExecContext(ctx,
				`UPDATE agent_messages SET read_at = ? WHERE id = ?`, now, m.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := h.attachFileInfos(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// UnreadCount returns how many unread messages are waiting for sessionName /
// actorID without marking them read. Used to append unread notices to MCP tool
// responses. Held messages do not count: the agent cannot read them yet.
func (h *Hub) UnreadCount(ctx context.Context, project, sessionName, actorID string) int {
	return h.unreadCount(ctx, project, sessionName, actorID, false)
}

// RunUnreadCount is UnreadCount for a run (CheckRunInbox).
func (h *Hub) RunUnreadCount(ctx context.Context, project, sessionName, actorID string) int {
	return h.unreadCount(ctx, project, sessionName, actorID, true)
}

func (h *Hub) unreadCount(ctx context.Context, project, sessionName, actorID string, run bool) int {
	project = strings.Trim(strings.TrimSpace(project), "/")
	if h.inboxRefused(project, sessionName, actorID) {
		return 0
	}
	aliases := recipientAliases(sessionName, actorID, run || h.runInbox(project, sessionName, actorID))
	if len(aliases) == 0 {
		return 0
	}
	if h.database == nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		aliasSet := make(map[string]bool, len(aliases))
		for _, a := range aliases {
			aliasSet[a] = true
		}
		count := 0
		for _, m := range h.memMessages {
			if m.Project == project && m.ReadAt == 0 && m.Status == StatusDelivered &&
				aliasSet[strings.ToLower(m.Recipient)] {
				count++
			}
		}
		return count
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(aliases)), ", ")
	args := make([]any, 0, len(aliases)+1)
	args = append(args, project)
	for _, a := range aliases {
		args = append(args, a)
	}
	var count int
	_ = h.database.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM agent_messages
		 WHERE project = ? AND read_at = 0 AND status = 'delivered' AND recipient IN (`+placeholders+`)
	`, args...).Scan(&count)
	return count
}

// ListMessages returns recent messages in a project (optionally filtered by
// nodeID and status) in chronological order for the browser UI.
func (h *Hub) ListMessages(ctx context.Context, project, nodeID, status string, limit int) ([]Message, error) {
	project = strings.Trim(strings.TrimSpace(project), "/")
	nodeID = strings.TrimSpace(nodeID)
	status = strings.ToLower(strings.TrimSpace(status))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if h.database == nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		var out []Message
		for _, m := range h.memMessages {
			if m.Project != project {
				continue
			}
			if nodeID != "" && m.NodeID != nodeID {
				continue
			}
			if status != "" && m.Status != status {
				continue
			}
			m.Files = fileInfos(h.memFiles[m.ID])
			out = append(out, m)
		}
		if len(out) > limit {
			out = out[len(out)-limit:]
		}
		return out, nil
	}

	query := `
		SELECT ` + messageColumns + `
		  FROM agent_messages
		 WHERE project = ?`
	args := []any{project}
	if nodeID != "" {
		query += ` AND node_id = ?`
		args = append(args, nodeID)
	}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := h.database.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Reverse into chronological order.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if err := h.attachFileInfos(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (h *Hub) registerInboxWaiter(project string) chan struct{} {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	set := h.inboxWaiters[project]
	if set == nil {
		set = make(map[chan struct{}]struct{})
		h.inboxWaiters[project] = set
	}
	set[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *Hub) unregisterInboxWaiter(project string, ch chan struct{}) {
	h.mu.Lock()
	if set := h.inboxWaiters[project]; set != nil {
		delete(set, ch)
	}
	h.mu.Unlock()
}

func (h *Hub) wakeInboxWaiters(project string) {
	h.mu.Lock()
	set := h.inboxWaiters[project]
	for ch := range set {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	h.mu.Unlock()
}
