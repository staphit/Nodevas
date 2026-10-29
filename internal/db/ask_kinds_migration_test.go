package db_test

import (
	"context"
	"testing"

	"nodevas/internal/testpg"
)

// 0030_agent_ask_kinds: an ask's kind, options, answer, run, tool and expiry,
// and the "answered" status. Rows written before it read as approvals.
func TestMigration0030AgentAskKinds(t *testing.T) {
	database := testpg.New(t)
	ctx := context.Background()
	var applied int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE name = '0030_agent_ask_kinds'`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("0030_agent_ask_kinds applied %d times", applied)
	}
	// A row as the code before kinds wrote it.
	if _, err := database.ExecContext(ctx, `
		INSERT INTO agent_asks (id, project, session_id, session_name, actor_id, question, status, created_at)
		VALUES ('old', 'p', 's', 's', 'a', 'q', 'pending', 1)`); err != nil {
		t.Fatal(err)
	}
	var kind, options, answer, runID, tool, toolInput string
	var expiresAt int64
	if err := database.QueryRowContext(ctx, `
		SELECT kind, options, answer, run_id, tool, tool_input, expires_at FROM agent_asks WHERE id = 'old'`).
		Scan(&kind, &options, &answer, &runID, &tool, &toolInput, &expiresAt); err != nil {
		t.Fatal(err)
	}
	if kind != "approval" || options != "" || answer != "" || runID != "" || tool != "" || toolInput != "" || expiresAt != 0 {
		t.Fatalf("old row reads kind=%q options=%q answer=%q run=%q tool=%q input=%q expires=%d",
			kind, options, answer, runID, tool, toolInput, expiresAt)
	}
	if _, err := database.ExecContext(ctx, `UPDATE agent_asks SET status = 'answered' WHERE id = 'old'`); err != nil {
		t.Fatalf("answered is not a status: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE agent_asks SET status = 'maybe' WHERE id = 'old'`); err == nil {
		t.Fatal("an unknown status was stored")
	}
	if _, err := database.ExecContext(ctx, `UPDATE agent_asks SET kind = 'poll' WHERE id = 'old'`); err == nil {
		t.Fatal("an unknown kind was stored")
	}
}
