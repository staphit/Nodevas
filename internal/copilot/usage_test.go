package copilot

import (
	"context"
	"testing"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/usage"
)

// B-08: a copilot turn's tokens are kept for the usage report; a turn with
// none keeps nothing.
func TestCopilotTurnRecordsItsTokens(t *testing.T) {
	var database *db.DB
	h := newHarness(t, func(o *Options) { database = o.Database })
	conversation := h.conversation(alice, ModeDefault)
	h.model.steps = []step{answer("Hello")}
	h.start(h.turn(conversation, ModeDefault)).finish()

	ctx := context.Background()
	rows, err := h.service.store.UsageRows(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ConversationID != conversation.ID || rows[0].AccountID != alice.ID ||
		rows[0].Model != "claude-test" || rows[0].Provider != "anthropic" || rows[0].InputTokens != 10 || rows[0].OutputTokens != 2 {
		t.Fatalf("copilot usage rows = %+v, want one 10/2 row", rows)
	}

	if err := h.service.store.RecordUsage(ctx, conversation.ID, alice.ID, "demo", "anthropic", "claude-test", 0, 0); err != nil {
		t.Fatal(err)
	}
	if rows, _ := h.service.store.UsageRows(ctx, "demo"); len(rows) != 1 {
		t.Fatalf("a zero-token turn was recorded: %+v", rows)
	}

	reports := usage.NewStore(database)
	reports.SetClock(func() time.Time { return time.Now().Add(time.Minute) })
	report, err := reports.Report(ctx, usage.Query{Project: "demo", Days: 1})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, b := range report.ByAgent {
		if b.Key == "copilot" && b.InputTokens == 10 && b.OutputTokens == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("usage byAgent = %+v, want a copilot entry", report.ByAgent)
	}
}
