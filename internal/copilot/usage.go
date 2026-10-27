package copilot

import (
	"context"
	"time"
)

// Copilot token use (agent analytics A): one row per turn that used tokens,
// kept when its conversation is deleted, for the usage report.

// UsageRow is one recorded turn's tokens.
type UsageRow struct {
	ConversationID string
	AccountID      string
	Project        string
	Provider       string
	Model          string
	InputTokens    int64
	OutputTokens   int64
	CreatedAt      string
}

// RecordUsage stores one turn's tokens; a turn with none stores nothing.
func (s *Store) RecordUsage(ctx context.Context, conversationID, accountID, project, provider, model string, in, out int) error {
	if s == nil || s.database == nil || in+out <= 0 {
		return nil
	}
	_, err := s.database.ExecContext(ctx, `
INSERT INTO copilot_usage (conversation_id, account_id, project, provider, model, input_tokens, output_tokens, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, conversationID, accountID, project, provider, model, max(in, 0), max(out, 0),
		time.Now().UTC().Format("2006-01-02T15:04:05.000000Z"))
	return err
}

// UsageRows lists project's recorded turns, oldest first.
func (s *Store) UsageRows(ctx context.Context, project string) ([]UsageRow, error) {
	rows, err := s.database.QueryContext(ctx, `
SELECT conversation_id, account_id, project, provider, model, input_tokens, output_tokens, created_at
  FROM copilot_usage WHERE project = ? ORDER BY id`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageRow
	for rows.Next() {
		var r UsageRow
		if err := rows.Scan(&r.ConversationID, &r.AccountID, &r.Project, &r.Provider, &r.Model,
			&r.InputTokens, &r.OutputTokens, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
