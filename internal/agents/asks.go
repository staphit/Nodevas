package agents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	AskPending  = "pending"
	AskApproved = "approved"
	AskDenied   = "denied"
	// AskAnswered: a person answered a question or picked a choice's option
	// (Ask.Answer).
	AskAnswered = "answered"
	AskExpired  = "expired"

	// The kinds of ask. An approval is decided approve or deny; a question
	// is answered with text; a choice with one of its options. Asks from
	// before kinds existed are approvals.
	AskKindApproval = "approval"
	AskKindQuestion = "question"
	AskKindChoice   = "choice"

	// The decisions a person answers an ask with (AnswerInput.Decision).
	DecisionApprove = "approve"
	DecisionDeny    = "deny"
	DecisionAnswer  = "answer"

	// A choice offers MinAskOptions..MaxAskOptions options of at most
	// MaxAskOptionRunes characters each.
	MinAskOptions     = 2
	MaxAskOptions     = 8
	MaxAskOptionRunes = 200
	// MaxAskAnswerRunes caps a person's text answer.
	MaxAskAnswerRunes = 4000

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
	// Kind is one of the AskKind values; never empty in what the hub
	// returns.
	Kind     string `json:"kind"`
	Question string `json:"question"`
	// Options are a choice's options, in the order the agent gave them.
	Options    []string `json:"options,omitempty"`
	AboutMsgID string   `json:"aboutMsgId,omitempty"`
	NodeID     string   `json:"nodeId,omitempty"`
	// RunID is the run the asking session belongs to, if any.
	RunID string `json:"runId,omitempty"`
	// Tool and ToolInput are a permission prompt's tool, as the agent CLI
	// names it, and its input (JSON or text, at most MaxAskToolInputBytes).
	Tool      string `json:"tool,omitempty"`
	ToolInput string `json:"toolInput,omitempty"`
	Status    string `json:"status"`
	DecidedBy string `json:"decidedBy,omitempty"`
	Note      string `json:"note,omitempty"`
	// Answer is the person's text answer to a question or the option they
	// picked of a choice.
	Answer    string `json:"answer,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	// ExpiresAt is when a pending ask stops waiting (unix ms).
	ExpiresAt int64 `json:"expiresAt,omitempty"`
	DecidedAt int64 `json:"decidedAt,omitempty"`
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
	// Kind defaults to AskKindApproval.
	Kind string
	// Options: a choice's options, MinAskOptions..MaxAskOptions.
	Options []string
	// RunID: the run the transport vouches the ask comes from. Empty means
	// the asking session's own run, if it has one.
	RunID string
	// Tool and ToolInput belong to permission prompts only.
	Tool      string
	ToolInput string
	// FromRun: the asker is a run (the transport recognised it), whose
	// inbox is its own session's alone (recipientAliases).
	FromRun bool
}

// AnswerInput is a person's answer to an ask.
type AnswerInput struct {
	// Decision is DecisionApprove or DecisionDeny for an approval;
	// DecisionAnswer (with Answer) or DecisionDeny for a question or a
	// choice. The past tenses (approved, denied, answered) are accepted too.
	Decision string
	Note     string
	// Answer is a question's text answer or the chosen option of a choice.
	Answer string
}

// askShape is an ask's validated kind and the fields that go with it.
type askShape struct {
	kind      string
	options   []string
	tool      string
	toolInput string
	question  string
}

// shapeAsk validates in's kind and the fields that go with it.
func shapeAsk(in AskInput) (askShape, error) {
	shape := askShape{kind: strings.ToLower(strings.TrimSpace(in.Kind)), question: strings.TrimSpace(in.Question)}
	if shape.kind == "" {
		shape.kind = AskKindApproval
	}
	switch shape.kind {
	case AskKindApproval, AskKindQuestion, AskKindChoice:
	default:
		return askShape{}, fmt.Errorf("%w: kind must be approval, question or choice", ErrInvalid)
	}
	if strings.TrimSpace(in.Tool) != "" || strings.TrimSpace(in.ToolInput) != "" {
		return askShape{}, fmt.Errorf("%w: tool and toolInput belong to permission asks", ErrInvalid)
	}
	if shape.kind != AskKindChoice {
		if len(in.Options) > 0 {
			return askShape{}, fmt.Errorf("%w: options belong to kind choice", ErrInvalid)
		}
		return shape, nil
	}
	if len(in.Options) < MinAskOptions || len(in.Options) > MaxAskOptions {
		return askShape{}, fmt.Errorf("%w: a choice needs %d to %d options", ErrInvalid, MinAskOptions, MaxAskOptions)
	}
	seen := make(map[string]bool, len(in.Options))
	for _, option := range in.Options {
		option = strings.TrimSpace(option)
		switch {
		case option == "":
			return askShape{}, fmt.Errorf("%w: a choice's options must not be blank", ErrInvalid)
		case utf8.RuneCountInString(option) > MaxAskOptionRunes:
			return askShape{}, fmt.Errorf("%w: an option exceeds %d characters", ErrInvalid, MaxAskOptionRunes)
		case seen[option]:
			return askShape{}, fmt.Errorf("%w: option %q is listed twice", ErrInvalid, option)
		}
		seen[option] = true
		shape.options = append(shape.options, option)
	}
	return shape, nil
}

// askTimeout is how long an ask of kind waits: timeout, or the kind's
// longest wait when timeout is unset or longer.
func askTimeout(kind string, timeout time.Duration) time.Duration {
	limit := MaxAskTimeout
	if timeout <= 0 || timeout > limit {
		return limit
	}
	return timeout
}

// CreateAndWait records a pending ask, broadcasts it to the UI, and blocks
// until a human answers or timeout (or ctx) expires.
func (h *Hub) CreateAndWait(ctx context.Context, in AskInput, timeout time.Duration) (Ask, error) {
	project := strings.Trim(strings.TrimSpace(in.Project), "/")
	sessionID := strings.TrimSpace(in.SessionID)
	sessionName := strings.TrimSpace(in.SessionName)
	shape, err := shapeAsk(in)
	if err != nil {
		return Ask{}, err
	}
	question := shape.question
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
	timeout = askTimeout(shape.kind, timeout)

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
	runID := strings.TrimSpace(in.RunID)
	if runID == "" {
		runID = h.sessionRunID(project, sessionID)
	}
	now := h.nowMillis()
	ask := Ask{
		ID:          newID(),
		Project:     project,
		SessionID:   sessionID,
		SessionName: sessionName,
		ActorID:     actorID,
		Agent:       strings.TrimSpace(in.Agent),
		Kind:        shape.kind,
		Question:    question,
		Options:     shape.options,
		AboutMsgID:  aboutMsgID,
		NodeID:      strings.TrimSpace(in.NodeID),
		RunID:       runID,
		Tool:        shape.tool,
		ToolInput:   shape.toolInput,
		Status:      AskPending,
		CreatedAt:   now,
		ExpiresAt:   now + timeout.Milliseconds(),
	}

	if err := h.insertPendingAsk(ctx, ask); err != nil {
		return Ask{}, err
	}
	h.LogAction(project, sessionID, runID, ActionAsk, ask.NodeID, capBytes(question, 200))

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
		return insertAsk(ctx, tx, ask)
	})
}

// insertAsk writes ask as it is.
func insertAsk(ctx context.Context, tx *sql.Tx, ask Ask) error {
	options := ""
	if len(ask.Options) > 0 {
		raw, err := json.Marshal(ask.Options)
		if err != nil {
			return err
		}
		options = string(raw)
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO agent_asks (
			id, project, session_id, session_name, actor_id, agent,
			question, about_msg_id, node_id, status, decided_by, note,
			created_at, decided_at, kind, options, answer, run_id, tool,
			tool_input, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		ask.ID, ask.Project, ask.SessionID, ask.SessionName, ask.ActorID, ask.Agent,
		ask.Question, ask.AboutMsgID, ask.NodeID, ask.Status, ask.DecidedBy, ask.Note,
		ask.CreatedAt, ask.DecidedAt, ask.Kind, options, ask.Answer, ask.RunID, ask.Tool,
		ask.ToolInput, ask.ExpiresAt,
	)
	return err
}

// askColumns are the agent_asks columns scanAsk reads, in its order.
const askColumns = `id, project, session_id, session_name, actor_id, agent,
		       question, about_msg_id, node_id, status, decided_by, note,
		       created_at, decided_at, kind, options, answer, run_id, tool,
		       tool_input, expires_at`

// scanAsk reads one row of askColumns.
func scanAsk(row rowScanner) (Ask, error) {
	var a Ask
	var options string
	if err := row.Scan(
		&a.ID, &a.Project, &a.SessionID, &a.SessionName, &a.ActorID, &a.Agent,
		&a.Question, &a.AboutMsgID, &a.NodeID, &a.Status, &a.DecidedBy, &a.Note,
		&a.CreatedAt, &a.DecidedAt, &a.Kind, &options, &a.Answer, &a.RunID, &a.Tool,
		&a.ToolInput, &a.ExpiresAt,
	); err != nil {
		return Ask{}, err
	}
	if options != "" {
		if err := json.Unmarshal([]byte(options), &a.Options); err != nil {
			return Ask{}, fmt.Errorf("ask %s: options: %w", a.ID, err)
		}
	}
	if a.Kind == "" {
		a.Kind = AskKindApproval
	}
	return a, nil
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

// Answer records a human decision on an approval ask: decision is
// approve(d) or deny/denied. It is AnswerWith without a text answer, kept for
// the callers that only ever approve or deny.
func (h *Hub) Answer(ctx context.Context, project, askID, decision, note string, viewer Viewer) (Ask, error) {
	return h.AnswerWith(ctx, project, askID, AnswerInput{Decision: decision, Note: note}, viewer)
}

// normalizeDecision is decision's canonical name, or "" when it is none.
func normalizeDecision(decision string) string {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case DecisionApprove, AskApproved:
		return DecisionApprove
	case DecisionDeny, AskDenied:
		return DecisionDeny
	case DecisionAnswer, AskAnswered:
		return DecisionAnswer
	}
	return ""
}

// resolveDecision is the status and answer decision gives ask, or ErrInvalid
// when the decision does not fit the ask's kind.
func resolveDecision(ask Ask, decision, answer string) (status, answerOut string, err error) {
	answer = strings.TrimSpace(answer)
	switch ask.Kind {
	case AskKindQuestion, AskKindChoice:
		switch decision {
		case DecisionDeny:
			return AskDenied, "", nil
		case DecisionAnswer:
		default:
			return "", "", fmt.Errorf("%w: a %s is answered (decision answer) or dismissed (deny)", ErrInvalid, ask.Kind)
		}
		if answer == "" {
			return "", "", fmt.Errorf("%w: answer is required", ErrInvalid)
		}
		if utf8.RuneCountInString(answer) > MaxAskAnswerRunes {
			return "", "", fmt.Errorf("%w: answer exceeds %d characters", ErrInvalid, MaxAskAnswerRunes)
		}
		if ask.Kind == AskKindChoice {
			found := false
			for _, option := range ask.Options {
				if option == answer {
					found = true
					break
				}
			}
			if !found {
				return "", "", fmt.Errorf("%w: answer must be one of the choice's options", ErrInvalid)
			}
		}
		return AskAnswered, answer, nil
	default:
		switch decision {
		case DecisionApprove:
			return AskApproved, "", nil
		case DecisionDeny:
			return AskDenied, "", nil
		}
		return "", "", fmt.Errorf("%w: an approval is decided approve or deny", ErrInvalid)
	}
}

// AnswerWith records a person's answer to an ask: approve or deny for an
// approval, a text answer (or deny, "won't answer") for a question, one of
// the options (or deny) for a choice. If the ask was still waiting, the
// blocked tool call receives the result immediately. If the ask had already
// expired, the answer is delivered to the session's inbox as a message so
// the agent still receives it on its next turn. When `aboutMsgId` was set on
// the ask, a verified `<nodevas-approval>` notice is also sent back to the
// sender of that message.
//
// Only the person whose session asked, or an administrator, may answer: the
// answer is that person's decision, and an approval forwarded about a message
// speaks for them. To anyone else the ask does not exist. An ask already
// decided stays as it is and is returned unchanged.
func (h *Hub) AnswerWith(ctx context.Context, project, askID string, in AnswerInput, viewer Viewer) (Ask, error) {
	project = strings.Trim(strings.TrimSpace(project), "/")
	askID = strings.TrimSpace(askID)
	decision := normalizeDecision(in.Decision)
	if decision == "" {
		return Ask{}, fmt.Errorf("%w: decision must be approve, deny or answer", ErrInvalid)
	}
	note := strings.TrimSpace(in.Note)
	if viewer.Agent {
		return Ask{}, fmt.Errorf("%w: only a person may answer ask_user requests", ErrRefused)
	}
	decidedBy := strings.TrimSpace(viewer.ID)
	if decidedBy == "" {
		decidedBy = "local"
	}
	existing, err := h.lookupAsk(ctx, project, askID)
	if err != nil {
		return Ask{}, err
	}
	if !viewer.Admin && !strings.EqualFold(existing.ActorID, decidedBy) {
		return Ask{}, ErrNotFound
	}
	if existing.Status != AskPending && existing.Status != AskExpired {
		return existing, nil
	}
	status, answer, err := resolveDecision(existing, decision, in.Answer)
	if err != nil {
		return Ask{}, err
	}
	updated, wasExpired, decided, err := h.decideAsk(ctx, project, askID, status, answer, note, decidedBy)
	if err != nil || !decided {
		return updated, err
	}
	h.deliverDecision(ctx, updated, wasExpired, decidedBy)
	return updated, nil
}

// lookupAsk is one ask of project, from the database or memory.
func (h *Hub) lookupAsk(ctx context.Context, project, askID string) (Ask, error) {
	if h.database != nil {
		return h.getAskByID(ctx, project, askID)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	a, ok := h.memAsks[askID]
	if !ok || a.Project != project {
		return Ask{}, ErrNotFound
	}
	return *a, nil
}

// decideAsk moves a pending or expired ask to status. decided is false when
// somebody else decided it first; the ask is then returned as they left it.
func (h *Hub) decideAsk(ctx context.Context, project, askID, status, answer, note, decidedBy string) (updated Ask, wasExpired, decided bool, err error) {
	now := h.nowMillis()
	if h.database == nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		a, ok := h.memAsks[askID]
		if !ok || a.Project != project {
			return Ask{}, false, false, ErrNotFound
		}
		if a.Status != AskPending && a.Status != AskExpired {
			return *a, false, false, nil
		}
		wasExpired = a.Status == AskExpired
		a.Status = status
		a.Answer = answer
		a.Note = note
		a.DecidedBy = decidedBy
		a.DecidedAt = now
		return *a, wasExpired, true, nil
	}
	var previous string
	err = h.database.Tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `
			SELECT status FROM agent_asks WHERE project = ? AND id = ? FOR UPDATE
		`, project, askID).Scan(&previous); err != nil {
			return err
		}
		if previous != AskPending && previous != AskExpired {
			return nil
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE agent_asks
			   SET status = ?, answer = ?, note = ?, decided_by = ?, decided_at = ?
			 WHERE project = ? AND id = ?
		`, status, answer, note, decidedBy, now, project, askID)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Ask{}, false, false, ErrNotFound
	}
	if err != nil {
		return Ask{}, false, false, err
	}
	updated, err = h.getAskByID(ctx, project, askID)
	if err != nil {
		return Ask{}, false, false, err
	}
	decided = previous == AskPending || previous == AskExpired
	return updated, previous == AskExpired, decided, nil
}

// deliverDecision hands a decided ask to its waiting tool call, or as mail
// when it had expired, forwards the verified approval its aboutMsgId asks
// for, and tells the UI.
func (h *Hub) deliverDecision(ctx context.Context, updated Ask, wasExpired bool, decidedBy string) {
	project := updated.Project
	h.mu.Lock()
	waitCh, waiting := h.askWaiters[updated.ID]
	h.mu.Unlock()
	if waiting {
		select {
		case waitCh <- updated:
		default:
		}
	} else if wasExpired && updated.SessionName != "" {
		// Late answer: deliver to the asking session's mailbox.
		body := fmt.Sprintf("[Late answer to ask %s (%q): %s]", updated.ID, updated.Question, updated.Status)
		if updated.Answer != "" {
			body += "\nAnswer: " + updated.Answer
		}
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
			text := updated.Note
			if updated.Answer != "" {
				text = strings.TrimSpace("Answer: " + updated.Answer + "\n" + updated.Note)
			}
			approvalBody := fmt.Sprintf(
				"<nodevas-approval from=%q about=%q decision=%q verified>\n%s\n</nodevas-approval>",
				shortActor(decidedBy), updated.AboutMsgID, updated.Status, text,
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
}

func (h *Hub) getAskByID(ctx context.Context, project, id string) (Ask, error) {
	a, err := scanAsk(h.database.QueryRowContext(ctx, `
		SELECT `+askColumns+`
		  FROM agent_asks
		 WHERE project = ? AND id = ?
	`, project, id))
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
		SELECT ` + askColumns + `
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
		a, err := scanAsk(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// maxBuildingRows caps the cross-project listings Station's building reads:
// counts beyond it would not change what a person sees at a glance.
const maxBuildingRows = 2000

// normalizeProjectKeys trims each key the way the hub stores it and drops
// empties and repeats.
func normalizeProjectKeys(projects []string) []string {
	seen := make(map[string]bool, len(projects))
	out := make([]string, 0, len(projects))
	for _, p := range projects {
		p = strings.Trim(strings.TrimSpace(p), "/")
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// PendingAsksIn returns the pending asks of several projects in one query,
// newest first. Project keys are store roots, as for ListAsks.
func (h *Hub) PendingAsksIn(ctx context.Context, projects []string) ([]Ask, error) {
	projects = normalizeProjectKeys(projects)
	out := []Ask{}
	if len(projects) == 0 {
		return out, nil
	}
	if h.database == nil {
		want := make(map[string]bool, len(projects))
		for _, p := range projects {
			want[p] = true
		}
		h.mu.Lock()
		for _, a := range h.memAsks {
			if want[a.Project] && a.Status == AskPending {
				out = append(out, *a)
			}
		}
		h.mu.Unlock()
		sort.Slice(out, func(i, j int) bool {
			if out[i].CreatedAt != out[j].CreatedAt {
				return out[i].CreatedAt > out[j].CreatedAt
			}
			return out[i].ID > out[j].ID
		})
		return out, nil
	}
	args := make([]any, 0, len(projects)+2)
	for _, p := range projects {
		args = append(args, p)
	}
	args = append(args, AskPending, maxBuildingRows)
	rows, err := h.database.QueryContext(ctx, `
		SELECT `+askColumns+`
		  FROM agent_asks
		 WHERE project IN (?`+strings.Repeat(", ?", len(projects)-1)+`) AND status = ?
		 ORDER BY created_at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanAsk(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
