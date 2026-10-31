package agents

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	// AskKindPermission is an agent CLI's tool-permission prompt, relayed by
	// the runner: allowed once, allowed for the rest of the run, or denied.
	AskKindPermission = "permission"

	// The decisions a person answers an ask with (AnswerInput.Decision).
	DecisionApprove = "approve"
	DecisionDeny    = "deny"
	DecisionAnswer  = "answer"
	// DecisionApproveRun allows a permission prompt and every later prompt
	// for the same tool in the same run (Ask.Answer records it).
	DecisionApproveRun = "approve_run"

	// AllowedForRunNote is the note on a permission prompt the hub allowed
	// without asking, because the person had allowed its tool for the run.
	AllowedForRunNote = "allowed for this run"

	// A choice offers MinAskOptions..MaxAskOptions options of at most
	// MaxAskOptionRunes characters each.
	MinAskOptions     = 2
	MaxAskOptions     = 8
	MaxAskOptionRunes = 200
	// MaxAskAnswerRunes caps a person's text answer.
	MaxAskAnswerRunes = 4000

	// MaxAskToolRunes caps a permission prompt's tool name, and
	// MaxAskToolInputBytes its input (longer input is cut).
	MaxAskToolRunes      = 200
	MaxAskToolInputBytes = 4096
	// maxPendingPermissions bounds one session's waiting permission
	// prompts: one per tool call its CLI runs in parallel.
	maxPendingPermissions = 8

	MaxAskTimeout = 10 * time.Minute
	// PermissionAskTimeout is how long a permission prompt waits at most;
	// one nobody answers expires, and the tool call is denied.
	PermissionAskTimeout = 5 * time.Minute

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
	// picked of a choice; DecisionApproveRun on a permission prompt allowed
	// for the rest of its run.
	Answer string `json:"answer,omitempty"`
	// Decision is the decision that settled the ask: approve, approve_run,
	// deny or answer; empty while pending or once expired. Derived from
	// Status and Answer (MarshalJSON fills it), so it is never stored.
	Decision  string `json:"decision,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	// ExpiresAt is when a pending ask stops waiting (unix ms).
	ExpiresAt int64 `json:"expiresAt,omitempty"`
	DecidedAt int64 `json:"decidedAt,omitempty"`
	// runKey narrows what allowing a permission prompt for the rest of its
	// run covers: "" the tool, "cmd:<hash>" one shell command (runKeyFor).
	runKey string
}

// The scopes an "allow for this run" of a permission prompt covers
// (Ask.RunScope, "runScope" in its JSON).
const (
	// RunScopeTool: every later call of the same tool in the run.
	RunScopeTool = "tool"
	// RunScopeCommand: the same shell command (and working directory) again
	// in the run; any other command still asks.
	RunScopeCommand = "command"
	// RunScopeOnce: this call only. A shell command cut to fit cannot be
	// told apart from a longer one with the same start.
	RunScopeOnce = "once"
)

// RunScope is what allowing a for the rest of its run covers, or "" for an
// ask that is not a permission prompt.
func (a Ask) RunScope() string {
	if a.Kind != AskKindPermission {
		return ""
	}
	switch {
	case strings.HasPrefix(a.runKey, runKeyCommand):
		return RunScopeCommand
	case strings.HasPrefix(a.runKey, runKeyOnce):
		return RunScopeOnce
	}
	return RunScopeTool
}

const (
	runKeyCommand = "cmd:"
	runKeyOnce    = "once:"
)

// shellTools are the agent CLIs' tools that run a command line (lower
// case): Claude Code's Bash, the Antigravity CLI's run_command and
// send_command_input, OpenCode's bash and its ACP kind execute, and the
// names other CLIs give theirs. Allowing one of them for the run allows one
// command, never the shell.
var shellTools = map[string]bool{
	"bash": true, "run_command": true, "send_command_input": true, "execute": true,
	"shell": true, "exec": true, "exec_command": true, "local_shell": true,
	"powershell": true, "terminal": true, "run_terminal_cmd": true,
}

// cosmeticInputKeys are tool-input fields (lower case) that describe a
// shell call without changing what it runs.
var cosmeticInputKeys = map[string]bool{"description": true, "timeout": true, "waitmsbeforeasync": true}

// runKeyFor is the runKey of a permission prompt for tool with input (as
// sent, before the hub cuts it): "" for a tool allowed tool-wide, one key
// per command for a shell tool, a key matching nothing for a shell command
// that may have been cut to fit.
func runKeyFor(tool, input string) string {
	if !shellTools[strings.ToLower(tool)] {
		return ""
	}
	// The runner cuts input to MaxAskToolInputBytes at a rune boundary, so
	// input this long may be the start of a longer command.
	if len(input) > MaxAskToolInputBytes-utf8.UTFMax {
		return runKeyOnce + newID()
	}
	canonical := input
	var fields map[string]any
	if json.Unmarshal([]byte(input), &fields) == nil && fields != nil {
		for key := range fields {
			if cosmeticInputKeys[strings.ToLower(key)] {
				delete(fields, key)
			}
		}
		// Marshalled maps sort their keys: the same call, the same text.
		if raw, err := json.Marshal(fields); err == nil {
			canonical = string(raw)
		}
	}
	sum := sha256.Sum256([]byte(strings.ToLower(tool) + "\x00" + canonical))
	return runKeyCommand + hex.EncodeToString(sum[:])
}

// DecisionOf is the decision that settled a: approve, approve_run, deny or
// answer, or "" while it is pending or once it expired.
func DecisionOf(a Ask) string {
	switch a.Status {
	case AskApproved:
		if a.Answer == DecisionApproveRun {
			return DecisionApproveRun
		}
		return DecisionApprove
	case AskDenied:
		return DecisionDeny
	case AskAnswered:
		return DecisionAnswer
	}
	return ""
}

// MarshalJSON writes the ask with its decision and, for a permission
// prompt, its runScope.
func (a Ask) MarshalJSON() ([]byte, error) {
	type plain Ask
	a.Decision = DecisionOf(a)
	return json.Marshal(struct {
		plain
		RunScope string `json:"runScope,omitempty"`
	}{plain(a), a.RunScope()})
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
	runKey    string
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
	case AskKindPermission:
		shape.tool = strings.TrimSpace(in.Tool)
		switch {
		case shape.tool == "":
			return askShape{}, fmt.Errorf("%w: a permission ask names its tool", ErrInvalid)
		case utf8.RuneCountInString(shape.tool) > MaxAskToolRunes:
			return askShape{}, fmt.Errorf("%w: tool exceeds %d characters", ErrInvalid, MaxAskToolRunes)
		case len(in.Options) > 0:
			return askShape{}, fmt.Errorf("%w: options belong to kind choice", ErrInvalid)
		}
		shape.toolInput = capBytes(strings.TrimSpace(in.ToolInput), MaxAskToolInputBytes)
		shape.runKey = runKeyFor(shape.tool, strings.TrimSpace(in.ToolInput))
		if shape.question == "" {
			shape.question = fmt.Sprintf("Allow %s?", shape.tool)
		}
		return shape, nil
	default:
		return askShape{}, fmt.Errorf("%w: kind must be approval, question, choice or permission", ErrInvalid)
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

// AskTimeout is how long an ask of kind waits: timeout, or the kind's
// longest wait (PermissionAskTimeout for a permission prompt, else
// MaxAskTimeout) when timeout is unset or longer.
func AskTimeout(kind string, timeout time.Duration) time.Duration {
	limit := MaxAskTimeout
	if strings.EqualFold(strings.TrimSpace(kind), AskKindPermission) {
		limit = PermissionAskTimeout
	}
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
	timeout = AskTimeout(shape.kind, timeout)

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
	if shape.kind == AskKindPermission && runID != "" {
		if decidedBy := h.runAllowedBy(ctx, project, runID, shape.tool, shape.runKey); decidedBy != "" {
			return h.allowForRun(ctx, Ask{
				ID:          newID(),
				Project:     project,
				SessionID:   sessionID,
				SessionName: sessionName,
				ActorID:     actorID,
				Agent:       strings.TrimSpace(in.Agent),
				Kind:        shape.kind,
				Question:    question,
				AboutMsgID:  aboutMsgID,
				NodeID:      strings.TrimSpace(in.NodeID),
				RunID:       runID,
				Tool:        shape.tool,
				ToolInput:   shape.toolInput,
				runKey:      shape.runKey,
			}, decidedBy)
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
		Kind:        shape.kind,
		Question:    question,
		Options:     shape.options,
		AboutMsgID:  aboutMsgID,
		NodeID:      strings.TrimSpace(in.NodeID),
		RunID:       runID,
		Tool:        shape.tool,
		ToolInput:   shape.toolInput,
		runKey:      shape.runKey,
		Status:      AskPending,
		CreatedAt:   now,
		ExpiresAt:   now + timeout.Milliseconds(),
	}

	// Waiting before the ask exists: an answer that lands the moment it is
	// stored (a run-wide allow releasing it) must find the waiter.
	waitCh := make(chan Ask, 1)
	h.mu.Lock()
	h.askWaiters[ask.ID] = waitCh
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.askWaiters, ask.ID)
		h.mu.Unlock()
	}()

	if h.beforeAskInsert != nil {
		h.beforeAskInsert()
	}
	if err := h.insertPendingAsk(ctx, ask); err != nil {
		return Ask{}, err
	}
	h.LogAction(project, sessionID, runID, ActionAsk, ask.NodeID, capBytes(question, 200))

	// The person may have allowed the tool for the run between the check
	// above and the insert, after releaseRunPrompts listed what waited:
	// looking again now that the ask is stored leaves no gap, since an
	// allow stored later releases it.
	released := false
	if shape.kind == AskKindPermission && runID != "" {
		if decidedBy := h.runAllowedBy(ctx, project, runID, shape.tool, shape.runKey); decidedBy != "" {
			updated, _, decided, err := h.decideAsk(ctx, project, ask.ID, AskApproved, DecisionApproveRun, AllowedForRunNote, decidedBy, false)
			if err == nil && decided {
				h.deliverDecision(ctx, updated, false, decidedBy)
				released = true
			}
		}
	}
	if !released {
		h.emit(project, AskEvent, ask)
	}

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

// insertPendingAsk stores a new pending ask. A session waits on one
// question (approval, question or choice) at a time. Permission prompts are
// counted apart: the CLI raises them for its own tool calls, possibly while
// ask_user waits or several at once for parallel calls, so they never wait
// behind a question; maxPendingPermissions bounds them instead.
func (h *Hub) insertPendingAsk(ctx context.Context, ask Ask) error {
	// Asks past their time wait on nobody: they never hold the session.
	h.expireStaleAsks(ctx, ask.Project)
	permission := ask.Kind == AskKindPermission
	limit := 1
	if permission {
		limit = maxPendingPermissions
	}
	if h.database == nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		count := 0
		for _, existing := range h.memAsks {
			if existing.Project == ask.Project && existing.SessionID == ask.SessionID && existing.Status == AskPending &&
				(existing.Kind == AskKindPermission) == permission {
				count++
			}
		}
		if count >= limit {
			return ErrAskBusy
		}
		copied := ask
		h.memAsks[ask.ID] = &copied
		return nil
	}
	kindFilter := `kind != 'permission'`
	if permission {
		kindFilter = `kind = 'permission'`
	}
	return h.database.Tx(ctx, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM agent_asks
			 WHERE project = ? AND session_id = ? AND status = 'pending' AND `+kindFilter,
			ask.Project, ask.SessionID).Scan(&count); err != nil {
			return err
		}
		if count >= limit {
			return ErrAskBusy
		}
		return insertAsk(ctx, tx, ask)
	})
}

// runAllowedBy is who allowed tool (with runKey, its command for a shell
// tool) for the rest of run: an approve_run permission ask of that run, tool
// and key; "" when nobody did.
func (h *Hub) runAllowedBy(ctx context.Context, project, runID, tool, runKey string) string {
	if strings.HasPrefix(runKey, runKeyOnce) {
		return ""
	}
	if h.database == nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		for _, a := range h.memAsks {
			if a.Project == project && a.RunID == runID && a.Tool == tool && a.runKey == runKey && a.Kind == AskKindPermission &&
				a.Status == AskApproved && a.Answer == DecisionApproveRun {
				return a.DecidedBy
			}
		}
		return ""
	}
	var decidedBy string
	if err := h.database.QueryRowContext(ctx, `
		SELECT decided_by FROM agent_asks
		 WHERE project = ? AND run_id = ? AND tool = ? AND run_key = ? AND kind = 'permission'
		   AND status = 'approved' AND answer = ?
		 ORDER BY decided_at LIMIT 1
	`, project, runID, tool, runKey, DecisionApproveRun).Scan(&decidedBy); err != nil {
		return ""
	}
	return decidedBy
}

// allowForRun records ask as allowed without asking, on decidedBy's earlier
// "allow for this run", and returns it.
func (h *Hub) allowForRun(ctx context.Context, ask Ask, decidedBy string) (Ask, error) {
	now := h.nowMillis()
	ask.Status = AskApproved
	ask.Answer = DecisionApproveRun
	ask.Note = AllowedForRunNote
	ask.DecidedBy = decidedBy
	ask.CreatedAt = now
	ask.DecidedAt = now
	if h.database == nil {
		h.mu.Lock()
		copied := ask
		h.memAsks[ask.ID] = &copied
		h.mu.Unlock()
	} else if err := h.database.Tx(ctx, func(tx *sql.Tx) error { return insertAsk(ctx, tx, ask) }); err != nil {
		return Ask{}, err
	}
	h.LogAction(ask.Project, ask.SessionID, ask.RunID, ActionAsk, ask.NodeID, capBytes(ask.Question, 200))
	h.emit(ask.Project, AskEvent, ask)
	return ask, nil
}

// releaseRunPrompts allows the permission prompts of run, tool and runKey
// still waiting, now that decidedBy allowed them for the run.
func (h *Hub) releaseRunPrompts(ctx context.Context, project, runID, tool, runKey, decidedBy string) {
	if strings.HasPrefix(runKey, runKeyOnce) {
		return
	}
	var ids []string
	if h.database == nil {
		h.mu.Lock()
		for _, a := range h.memAsks {
			if a.Project == project && a.RunID == runID && a.Tool == tool && a.runKey == runKey && a.Kind == AskKindPermission && a.Status == AskPending {
				ids = append(ids, a.ID)
			}
		}
		h.mu.Unlock()
	} else {
		rows, err := h.database.QueryContext(ctx, `
			SELECT id FROM agent_asks
			 WHERE project = ? AND run_id = ? AND tool = ? AND run_key = ? AND kind = 'permission' AND status = 'pending'
		`, project, runID, tool, runKey)
		if err != nil {
			return
		}
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		_ = rows.Close()
	}
	for _, id := range ids {
		updated, _, decided, err := h.decideAsk(ctx, project, id, AskApproved, DecisionApproveRun, AllowedForRunNote, decidedBy, false)
		if err == nil && decided {
			h.deliverDecision(ctx, updated, false, decidedBy)
		}
	}
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
			tool_input, expires_at, run_key
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		ask.ID, ask.Project, ask.SessionID, ask.SessionName, ask.ActorID, ask.Agent,
		ask.Question, ask.AboutMsgID, ask.NodeID, ask.Status, ask.DecidedBy, ask.Note,
		ask.CreatedAt, ask.DecidedAt, ask.Kind, options, ask.Answer, ask.RunID, ask.Tool,
		ask.ToolInput, ask.ExpiresAt, ask.runKey,
	)
	return err
}

// askColumns are the agent_asks columns scanAsk reads, in its order.
const askColumns = `id, project, session_id, session_name, actor_id, agent,
		       question, about_msg_id, node_id, status, decided_by, note,
		       created_at, decided_at, kind, options, answer, run_id, tool,
		       tool_input, expires_at, run_key`

// scanAsk reads one row of askColumns.
func scanAsk(row rowScanner) (Ask, error) {
	var a Ask
	var options string
	if err := row.Scan(
		&a.ID, &a.Project, &a.SessionID, &a.SessionName, &a.ActorID, &a.Agent,
		&a.Question, &a.AboutMsgID, &a.NodeID, &a.Status, &a.DecidedBy, &a.Note,
		&a.CreatedAt, &a.DecidedAt, &a.Kind, &options, &a.Answer, &a.RunID, &a.Tool,
		&a.ToolInput, &a.ExpiresAt, &a.runKey,
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

// expireStaleAsks expires the pending asks of projects (of every project
// when none is named) whose time is up: one still waited on is about to
// give up anyway, and one a stopped hub left pending waits on nobody, so it
// must not hold its session's question or permission slots, nor show in a
// listing as waiting. Asks from before expiry times are up MaxAskTimeout
// after they were made. Each project whose asks changed is told.
func (h *Hub) expireStaleAsks(ctx context.Context, projects ...string) {
	now := h.nowMillis()
	legacy := now - MaxAskTimeout.Milliseconds()
	changed := map[string]bool{}
	if h.database == nil {
		want := make(map[string]bool, len(projects))
		for _, p := range projects {
			want[p] = true
		}
		h.mu.Lock()
		for _, a := range h.memAsks {
			if a.Status != AskPending || (len(want) > 0 && !want[a.Project]) {
				continue
			}
			if (a.ExpiresAt > 0 && a.ExpiresAt <= now) || (a.ExpiresAt == 0 && a.CreatedAt <= legacy) {
				a.Status = AskExpired
				a.DecidedAt = now
				changed[a.Project] = true
			}
		}
		h.mu.Unlock()
	} else {
		where := `
			 WHERE status = 'pending'
			   AND ((expires_at > 0 AND expires_at <= ?) OR (expires_at = 0 AND created_at <= ?))`
		args := []any{now, legacy}
		if len(projects) > 0 {
			where += ` AND project IN (?` + strings.Repeat(", ?", len(projects)-1) + `)`
			for _, p := range projects {
				args = append(args, p)
			}
		}
		// Listings call this all the time: the writer is only bothered
		// when there is something to expire.
		var one int
		if err := h.database.QueryRowContext(ctx, `SELECT 1 FROM agent_asks`+where+` LIMIT 1`, args...).Scan(&one); err != nil {
			return
		}
		// A write with RETURNING: on the writer, not the read pool.
		_ = h.database.Tx(ctx, func(tx *sql.Tx) error {
			rows, err := tx.QueryContext(ctx, `UPDATE agent_asks SET status = 'expired', decided_at = ?`+where+` RETURNING project`,
				append([]any{now}, args...)...)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var project string
				if rows.Scan(&project) == nil {
					changed[project] = true
				}
			}
			return rows.Err()
		})
	}
	for project := range changed {
		h.emit(project, AskEvent, nil)
	}
}

// expireOrphanedAsks expires every ask left pending in the database: a hub
// that has just attached it waits on none of them, so nobody's answer could
// reach their tool calls, which ended with the hub before.
func (h *Hub) expireOrphanedAsks() {
	h.mu.Lock()
	database := h.database
	h.mu.Unlock()
	if database == nil {
		return
	}
	_, _ = database.ExecContext(context.Background(), `
		UPDATE agent_asks SET status = 'expired', decided_at = ? WHERE status = 'pending'
	`, h.nowMillis())
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
	case DecisionApproveRun:
		return DecisionApproveRun
	}
	return ""
}

// resolveDecision is the status and answer decision gives ask, or ErrInvalid
// when the decision does not fit the ask's kind.
func resolveDecision(ask Ask, decision, answer string) (status, answerOut string, err error) {
	answer = strings.TrimSpace(answer)
	switch ask.Kind {
	case AskKindPermission:
		switch decision {
		case DecisionApprove:
			return AskApproved, "", nil
		case DecisionApproveRun:
			return AskApproved, DecisionApproveRun, nil
		case DecisionDeny:
			return AskDenied, "", nil
		}
		return "", "", fmt.Errorf("%w: a permission prompt is decided approve, approve_run or deny", ErrInvalid)
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
// speaks for them. A permission prompt is its runner owner's alone
// (Viewer.MayAnswer). To anyone else the ask does not exist. An ask already
// decided stays as it is and is returned unchanged.
func (h *Hub) AnswerWith(ctx context.Context, project, askID string, in AnswerInput, viewer Viewer) (Ask, error) {
	project = strings.Trim(strings.TrimSpace(project), "/")
	askID = strings.TrimSpace(askID)
	decision := normalizeDecision(in.Decision)
	if decision == "" {
		return Ask{}, fmt.Errorf("%w: decision must be approve, deny, answer or approve_run", ErrInvalid)
	}
	note := strings.TrimSpace(in.Note)
	if viewer.Agent {
		return Ask{}, fmt.Errorf("%w: only a person may answer ask_user requests", ErrRefused)
	}
	decidedBy := strings.TrimSpace(viewer.ID)
	if decidedBy == "" {
		decidedBy = "local"
	}
	// An ask past its time is answered as an expired one.
	h.expireStaleAsks(ctx, project)
	existing, err := h.lookupAsk(ctx, project, askID)
	if err != nil {
		return Ask{}, err
	}
	if !(Viewer{ID: decidedBy, Admin: viewer.Admin}).MayAnswer(existing) {
		return Ask{}, ErrNotFound
	}
	// A permission prompt that expired was denied to the CLI already; a
	// late answer to it could change nothing.
	lateAllowed := existing.Kind != AskKindPermission
	if existing.Status != AskPending && (existing.Status != AskExpired || !lateAllowed) {
		return existing, nil
	}
	status, answer, err := resolveDecision(existing, decision, in.Answer)
	if err != nil {
		return Ask{}, err
	}
	updated, wasExpired, decided, err := h.decideAsk(ctx, project, askID, status, answer, note, decidedBy, lateAllowed)
	if err != nil || !decided {
		return updated, err
	}
	h.deliverDecision(ctx, updated, wasExpired, decidedBy)
	if answer == DecisionApproveRun && updated.RunID != "" {
		h.releaseRunPrompts(ctx, project, updated.RunID, updated.Tool, updated.runKey, decidedBy)
	}
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

// decideAsk moves a pending ask, or with lateAllowed an expired one, to
// status. decided is false when it was neither (somebody else decided it
// first); the ask is then returned as they left it.
func (h *Hub) decideAsk(ctx context.Context, project, askID, status, answer, note, decidedBy string, lateAllowed bool) (updated Ask, wasExpired, decided bool, err error) {
	now := h.nowMillis()
	if h.database == nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		a, ok := h.memAsks[askID]
		if !ok || a.Project != project {
			return Ask{}, false, false, ErrNotFound
		}
		if a.Status != AskPending && (a.Status != AskExpired || !lateAllowed) {
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
		if previous != AskPending && (previous != AskExpired || !lateAllowed) {
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
	decided = previous == AskPending || (previous == AskExpired && lateAllowed)
	return updated, decided && previous == AskExpired, decided, nil
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

// maxSettledAsks caps the settled asks an unfiltered ListAsks returns.
const maxSettledAsks = 100

// ListAsks returns ask requests for a project, newest first: those of status
// when it is non-empty, else every pending ask and the newest settled ones.
// Every relayed permission prompt adds a row, so a cap over all statuses
// would push a question still waiting out of the listing.
func (h *Hub) ListAsks(ctx context.Context, project, status string) ([]Ask, error) {
	project = strings.Trim(strings.TrimSpace(project), "/")
	status = strings.ToLower(strings.TrimSpace(status))
	h.expireStaleAsks(ctx, project)
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
	switch status {
	case "":
		query += ` AND (status = 'pending' OR id IN (
			SELECT id FROM agent_asks
			 WHERE project = ? AND status != 'pending'
			 ORDER BY created_at DESC, id DESC LIMIT ?))
		 ORDER BY created_at DESC, id DESC`
		args = append(args, project, maxSettledAsks)
	case AskPending:
		// Bounded by the sessions themselves (one question and
		// maxPendingPermissions prompts each).
		query += ` AND status = ? ORDER BY created_at DESC, id DESC`
		args = append(args, status)
	default:
		query += ` AND status = ? ORDER BY created_at DESC, id DESC LIMIT ?`
		args = append(args, status, maxSettledAsks)
	}

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
	h.expireStaleAsks(ctx, projects...)
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
