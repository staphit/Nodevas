package copilot

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"nodevas/internal/db"
)

// ErrNotFound is what every lookup answers for a row that does not exist and
// for a row that belongs to somebody else. The two are deliberately the same:
// "that conversation is not yours" would confirm it exists.
var ErrNotFound = errors.New("not found")

// Entry kinds, as stored in copilot_entries.kind.
const (
	EntryUser      = "user"
	EntryAssistant = "assistant"
	EntryTool      = "tool"
	EntryNotice    = "notice"
	// EntryEvent is something that happened while nobody was talking: a plan
	// run the conversation started has ended (followup.go). Its message is a
	// user-role note for the model, written by the server, never the person.
	EntryEvent = "event"
	// EntryDraft is proposed nodes from the drafter (draft.go): its data holds
	// the draft, its issues and whether the person created or discarded it;
	// its message tells the model, in later turns, what was proposed.
	EntryDraft = "draft"
)

// Tool entry outcomes, as stored in copilot_entries.tool_status.
const (
	ToolOK     = "ok"
	ToolError  = "error"
	ToolDenied = "denied"
)

// maxConversationsListed bounds one listing. The panel shows the recent ones;
// a person with thousands of conversations does not need them all in one
// response, and nothing pages this list yet.
const maxConversationsListed = 200

// Conversation is one chat between a person and the copilot, about one
// project.
type Conversation struct {
	ID        string `json:"id"`
	AccountID string `json:"-"`
	Project   string `json:"project"`
	Title     string `json:"title"`
	Mode      Mode   `json:"mode"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	// Unread: a follow-up (followup.go) landed in it that the person has not
	// opened yet. Set by ListConversations only.
	Unread bool `json:"unread,omitempty"`
}

// Entry is one line of a transcript.
type Entry struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversationId"`
	Seq            int64  `json:"seq"`
	Kind           string `json:"kind"`
	Source         string `json:"source,omitempty"`
	Text           string `json:"text,omitempty"`
	ToolName       string `json:"toolName,omitempty"`
	ToolInput      string `json:"toolInput,omitempty"`
	ToolOutput     string `json:"toolOutput,omitempty"`
	ToolStatus     string `json:"toolStatus,omitempty"`
	// Message is the provider.Message JSON the next turn replays. It never
	// goes to the browser: it can hold a vendor's raw reasoning blocks, and
	// the display columns already say everything the panel shows.
	Message string `json:"-"`
	// Data is a kind's own JSON for the panel (a draft's DraftData).
	Data      string `json:"data,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

// Store keeps conversations and transcripts in the workspace database.
type Store struct {
	database *db.DB
}

// NewStore wraps an open workspace database.
func NewStore(database *db.DB) *Store {
	return &Store{database: database}
}

// newID is a random hex identifier. Random rather than sequential so an id
// says nothing about how many conversations other people have.
func newID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto/rand does not fail on any platform this runs on; if it ever
		// does, carrying on with a predictable id would be worse than stopping.
		panic(fmt.Sprintf("copilot: random id: %v", err))
	}
	return hex.EncodeToString(raw[:])
}

func nowMillis() int64 { return time.Now().UnixMilli() }

const conversationColumns = `id, account_id, project, title, mode, created_at, updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanConversation(row rowScanner) (Conversation, error) {
	var conversation Conversation
	var mode string
	err := row.Scan(&conversation.ID, &conversation.AccountID, &conversation.Project,
		&conversation.Title, &mode, &conversation.CreatedAt, &conversation.UpdatedAt)
	conversation.Mode = Mode(mode)
	return conversation, err
}

// ListConversations returns an account's conversations, newest first. An
// empty project lists every project's.
func (s *Store) ListConversations(ctx context.Context, accountID, project string) ([]Conversation, error) {
	query := `SELECT ` + conversationColumns + `, CASE WHEN EXISTS (SELECT 1 FROM copilot_followups f
WHERE f.conversation_id = copilot_conversations.id AND f.status = 'delivered' AND f.seen = 0) THEN 1 ELSE 0 END
FROM copilot_conversations WHERE account_id = ?`
	args := []any{accountID}
	if project != "" {
		query += ` AND project = ?`
		args = append(args, project)
	}
	query += fmt.Sprintf(` ORDER BY updated_at DESC, id LIMIT %d`, maxConversationsListed)
	rows, err := s.database.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	conversations := []Conversation{}
	for rows.Next() {
		var conversation Conversation
		var mode string
		var unread int64
		if err := rows.Scan(&conversation.ID, &conversation.AccountID, &conversation.Project,
			&conversation.Title, &mode, &conversation.CreatedAt, &conversation.UpdatedAt, &unread); err != nil {
			return nil, err
		}
		conversation.Mode, conversation.Unread = Mode(mode), unread != 0
		conversations = append(conversations, conversation)
	}
	return conversations, rows.Err()
}

// CreateConversation starts an empty conversation.
func (s *Store) CreateConversation(ctx context.Context, accountID, project string, mode Mode) (Conversation, error) {
	now := nowMillis()
	conversation := Conversation{
		ID: newID(), AccountID: accountID, Project: project, Mode: mode,
		CreatedAt: now, UpdatedAt: now,
	}
	_, err := s.database.ExecContext(ctx, `
INSERT INTO copilot_conversations (id, account_id, project, title, mode, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		conversation.ID, accountID, project, "", string(mode), now, now)
	if err != nil {
		return Conversation{}, err
	}
	return conversation, nil
}

// Conversation returns one conversation, or ErrNotFound when it does not exist
// or belongs to another account.
func (s *Store) Conversation(ctx context.Context, accountID, id string) (Conversation, error) {
	row := s.database.QueryRowContext(ctx,
		`SELECT `+conversationColumns+` FROM copilot_conversations WHERE id = ? AND account_id = ?`,
		id, accountID)
	conversation, err := scanConversation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, ErrNotFound
	}
	return conversation, err
}

// ConversationPatch is a partial update. A nil field is left as it is.
type ConversationPatch struct {
	Mode  *Mode
	Title *string
}

// UpdateConversation applies a patch and returns the result.
func (s *Store) UpdateConversation(ctx context.Context, accountID, id string, patch ConversationPatch) (Conversation, error) {
	var sets []string
	var args []any
	if patch.Mode != nil {
		sets = append(sets, "mode = ?")
		args = append(args, string(*patch.Mode))
	}
	if patch.Title != nil {
		sets = append(sets, "title = ?")
		args = append(args, *patch.Title)
	}
	if len(sets) > 0 {
		sets = append(sets, "updated_at = ?")
		args = append(args, nowMillis(), id, accountID)
		result, err := s.database.ExecContext(ctx,
			`UPDATE copilot_conversations SET `+strings.Join(sets, ", ")+` WHERE id = ? AND account_id = ?`,
			args...)
		if err != nil {
			return Conversation{}, err
		}
		if changed, err := result.RowsAffected(); err == nil && changed == 0 {
			return Conversation{}, ErrNotFound
		}
	}
	return s.Conversation(ctx, accountID, id)
}

// setTitleIfEmpty names a conversation after its first words, unless somebody
// already named it.
func (s *Store) setTitleIfEmpty(ctx context.Context, id, title string) error {
	_, err := s.database.ExecContext(ctx,
		`UPDATE copilot_conversations SET title = ? WHERE id = ? AND title = ''`, title, id)
	return err
}

// DeleteConversation removes a conversation and, by cascade, its transcript.
func (s *Store) DeleteConversation(ctx context.Context, accountID, id string) error {
	result, err := s.database.ExecContext(ctx,
		`DELETE FROM copilot_conversations WHERE id = ? AND account_id = ?`, id, accountID)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err == nil && changed == 0 {
		return ErrNotFound
	}
	return nil
}

const entryColumns = `id, conversation_id, seq, kind, source, text, tool_name, tool_input, tool_output, tool_status, message, data, created_at`

// Entries returns a conversation's transcript in order. The caller has
// already established that the conversation is the caller's own.
func (s *Store) Entries(ctx context.Context, conversationID string) ([]Entry, error) {
	rows, err := s.database.QueryContext(ctx,
		`SELECT `+entryColumns+` FROM copilot_entries WHERE conversation_id = ? ORDER BY seq`,
		conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []Entry{}
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(&entry.ID, &entry.ConversationID, &entry.Seq, &entry.Kind,
			&entry.Source, &entry.Text, &entry.ToolName, &entry.ToolInput, &entry.ToolOutput,
			&entry.ToolStatus, &entry.Message, &entry.Data, &entry.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// openPlanDrafts lists the account's draft entries in the project whose
// data has an origin on goal and state open: a coding agent's plan (P6) the
// person has neither created nor discarded, in any of their conversations of
// the project. Decoded and filtered here, not in SQL: data is plain text.
func (s *Store) openPlanDrafts(ctx context.Context, accountID, project, goal string) ([]Entry, error) {
	columns := "e." + strings.ReplaceAll(entryColumns, ", ", ", e.")
	rows, err := s.database.QueryContext(ctx, `
SELECT `+columns+` FROM copilot_entries e
  JOIN copilot_conversations c ON c.id = e.conversation_id
 WHERE c.account_id = ? AND c.project = ? AND e.kind = ?
 ORDER BY e.seq`, accountID, project, EntryDraft)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(&entry.ID, &entry.ConversationID, &entry.Seq, &entry.Kind,
			&entry.Source, &entry.Text, &entry.ToolName, &entry.ToolInput, &entry.ToolOutput,
			&entry.ToolStatus, &entry.Message, &entry.Data, &entry.CreatedAt); err != nil {
			return nil, err
		}
		var data DraftData
		if json.Unmarshal([]byte(entry.Data), &data) != nil || data.Origin == nil ||
			data.Origin.GoalID != goal || data.State != DraftOpen {
			continue
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

// SetEntryData replaces the data of one entry of the given kind, in a
// conversation the account owns, and returns the entry. Anything else,
// including another account's entry, is ErrNotFound.
func (s *Store) SetEntryData(ctx context.Context, accountID, conversationID, entryID, kind, data string) (Entry, error) {
	result, err := s.database.ExecContext(ctx, `
UPDATE copilot_entries SET data = ?
WHERE id = ? AND conversation_id = ? AND kind = ?
  AND conversation_id IN (SELECT id FROM copilot_conversations WHERE id = ? AND account_id = ?)`,
		data, entryID, conversationID, kind, conversationID, accountID)
	if err != nil {
		return Entry{}, err
	}
	if n, err := result.RowsAffected(); err != nil {
		return Entry{}, err
	} else if n == 0 {
		return Entry{}, ErrNotFound
	}
	var entry Entry
	err = s.database.QueryRowContext(ctx,
		`SELECT `+entryColumns+` FROM copilot_entries WHERE id = ?`, entryID).Scan(
		&entry.ID, &entry.ConversationID, &entry.Seq, &entry.Kind,
		&entry.Source, &entry.Text, &entry.ToolName, &entry.ToolInput, &entry.ToolOutput,
		&entry.ToolStatus, &entry.Message, &entry.Data, &entry.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, ErrNotFound
	}
	return entry, err
}

// AppendEntry stores an entry at the end of a conversation and touches the
// conversation, so the list orders by last activity. ID, Seq and CreatedAt are
// assigned here.
func (s *Store) AppendEntry(ctx context.Context, entry Entry) (Entry, error) {
	stored, err := s.AppendEntries(ctx, []Entry{entry})
	if err != nil {
		return Entry{}, err
	}
	return stored[0], nil
}

// AppendEntries stores entries at the end of their conversations, in order,
// in one transaction: all of them or none. A follow-up that writes several
// (a plan draft's note, card and reply) can then be retried without leaving
// a second copy of what the failed attempt wrote.
func (s *Store) AppendEntries(ctx context.Context, entries []Entry) ([]Entry, error) {
	stored := make([]Entry, len(entries))
	copy(stored, entries)
	now := nowMillis()
	for i := range stored {
		stored[i].ID = newID()
		stored[i].CreatedAt = now
	}
	err := s.database.Tx(ctx, func(tx *sql.Tx) error {
		for i := range stored {
			entry := &stored[i]
			// The next seq is read inside the write transaction: the single
			// writer on either engine is what keeps two appends from taking
			// the same one, and UNIQUE (conversation_id, seq) is the backstop
			// if that ever changes.
			if err := tx.QueryRowContext(ctx,
				`SELECT COALESCE(MAX(seq), 0) + 1 FROM copilot_entries WHERE conversation_id = ?`,
				entry.ConversationID).Scan(&entry.Seq); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
INSERT INTO copilot_entries (`+entryColumns+`)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				entry.ID, entry.ConversationID, entry.Seq, entry.Kind, entry.Source, entry.Text,
				entry.ToolName, entry.ToolInput, entry.ToolOutput, entry.ToolStatus, entry.Message,
				entry.Data, entry.CreatedAt); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE copilot_conversations SET updated_at = ? WHERE id = ?`,
				entry.CreatedAt, entry.ConversationID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return stored, nil
}
