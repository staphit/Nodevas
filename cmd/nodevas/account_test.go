package main

import (
	"context"
	"strings"
	"testing"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
	"nodevas/internal/db"
)

func TestAccountTOTPResetTurnsTheFactorOffAndAudits(t *testing.T) {
	root := t.TempDir()
	users, err := auth.NewUserStore(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := users.AddWithEmail(ctx, "ann", "correct-horse-battery", "", "ann@example.test"); err != nil {
		t.Fatal(err)
	}
	id, _, _ := users.FindAccount(ctx, "ann")
	_ = users.Close()

	database, err := db.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	// An enabled authenticator as the enrollment would leave it; the reset
	// does not need to read the secret, only to remove it.
	if _, err := database.Exec(
		`INSERT INTO account_totp (user_id, secret, created_at, enabled_at, last_counter) VALUES (?, 'sealed', ?, ?, 1)`,
		id, db.Now(), db.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		`INSERT INTO account_recovery_codes (user_id, code_hash, created_at) VALUES (?, 'digest', ?)`, id, db.Now()); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	// The account may come before the flags.
	if code := accountCmd([]string{"totp-reset", "ANN@example.test", "--project", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ann: two-factor authentication turned off") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	var left int
	if err := database.QueryRow(
		`SELECT (SELECT COUNT(*) FROM account_totp) + (SELECT COUNT(*) FROM account_recovery_codes)`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("%d rows survived the reset", left)
	}
	events, err := audit.New(database).Query(ctx, audit.Filter{ActionPrefix: audit.ActionTOTPReset})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Target != id || events[0].Detail["wasEnabled"] != true {
		t.Fatalf("audit = %+v", events)
	}

	stdout.Reset()
	if code := accountCmd([]string{"totp-reset", "--project", root, id}, &stdout, &stderr); code != 0 ||
		!strings.Contains(stdout.String(), "was not on") {
		t.Fatalf("second reset: exit %d, %q", code, stdout.String())
	}
	if code := accountCmd([]string{"totp-reset", "--project", root, "nobody@example.test"}, &stdout, &stderr); code != 1 {
		t.Fatalf("unknown account exit = %d", code)
	}
	if code := accountCmd([]string{"totp-reset", "--project", root}, &stdout, &stderr); code != 2 {
		t.Fatalf("missing account exit = %d", code)
	}
}
