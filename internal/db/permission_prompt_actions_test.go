package db_test

import (
	"context"
	"strings"
	"testing"

	"nodevas/internal/db"
	"nodevas/internal/testpg"
)

const permissionPromptDetails = "0033_permission_prompt_action_details"

// Before the action log wrote a permission prompt as "Allow <tool>?", it
// wrote the prompt's question, which quotes the tool's input (a path, a
// command). An upgrade rewrites those rows, and nothing else.
func TestUpgradeRewritesQuotedPermissionPromptsInTheActionLog(t *testing.T) {
	ctx := context.Background()
	url := testpg.NewEmptyURL(t)
	handle := rawHandle(t, url)
	if err := db.ApplyPGMigrationsThrough(ctx, handle, "0032_agent_ask_input_truncated"); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := handle.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	ask := func(id, session, kind, tool, question string) {
		exec(`INSERT INTO agent_asks (id, project, session_id, session_name, actor_id, question, created_at, kind, tool)
VALUES ($1, 'alpha', $2, $2, 'alice', $3, 1, $4, $5)`, id, session, question, kind, tool)
	}
	action := func(session, runID, kind, detail string) {
		exec(`INSERT INTO agent_session_actions (project, session_id, run_id, at, action, detail)
VALUES ('alpha', $1, $2, '2026-10-30T00:00:00.000000Z', $3, $4)`, session, runID, kind, detail)
	}
	long := `claude wants to run Read: {"file_path":"/home/alice/.ssh/SECRET-LONG` + strings.Repeat("x", 300) + `"}`
	// A relayed prompt whose ask is still stored, one cut to 200 bytes as
	// the log cut it, and one whose ask retention already pruned.
	ask("a1", "r1", "permission", "Read", `claude wants to run Read: {"file_path":"/home/alice/.ssh/SECRET-1"}`)
	action("r1", "run-1", "ask", `claude wants to run Read: {"file_path":"/home/alice/.ssh/SECRET-1"}`)
	ask("a2", "r1", "permission", "Read", long)
	action("r1", "run-1", "ask", long[:200])
	action("r2", "run-2", "ask", "codex wants to run shell: cat ~/.aws/SECRET-PRUNED")
	action("r2", "run-2", "ask", "The agent wants to run Bash")
	// A permission prompt with its own wording, still stored.
	ask("a3", "s3", "permission", "Write", "Allow Write to /etc/SECRET-CUSTOM?")
	action("s3", "", "ask", "Allow Write to /etc/SECRET-CUSTOM?")
	// Rows that are not permission prompts stay as they are.
	ask("a4", "s4", "question", "", "claude wants to run tests: shall I?")
	action("s4", "", "ask", "claude wants to run tests: shall I?")
	action("s5", "", "ask", "Should I deploy to staging?")
	action("r6", "run-6", "doing", "claude wants to run Bash: make")
	action("r7", "run-7", "ask", "Allow Edit?")

	upgraded, err := db.OpenPostgres(url, 0)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	upgraded.Close()

	want := []string{
		"Allow Read?", "Allow Read?", "Allow shell?", "Allow Bash?", "Allow Write?",
		"claude wants to run tests: shall I?", "Should I deploy to staging?", "claude wants to run Bash: make", "Allow Edit?",
	}
	check := func(when string) {
		t.Helper()
		rows, err := handle.QueryContext(ctx, `SELECT detail FROM agent_session_actions ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var got []string
		for rows.Next() {
			var detail string
			if err := rows.Scan(&detail); err != nil {
				t.Fatal(err)
			}
			got = append(got, detail)
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s: details\n%q\nwant\n%q", when, got, want)
		}
	}
	check("after the upgrade")
	// Running it again changes nothing.
	statement := db.PGMigrationStatement(permissionPromptDetails)
	if statement == "" {
		t.Fatalf("no step %s", permissionPromptDetails)
	}
	exec(statement)
	check("run twice")
}
