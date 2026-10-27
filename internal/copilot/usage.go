package copilot

import "context"

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
	// TODO(green): implement.
	return nil
}

// UsageRows lists project's recorded turns, oldest first.
func (s *Store) UsageRows(ctx context.Context, project string) ([]UsageRow, error) {
	// TODO(green): implement.
	return nil, nil
}
