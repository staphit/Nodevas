package db_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/testpg"
)

// A URL that cannot be parsed is a configuration mistake, not an outage; the
// error must not echo it.
func TestMalformedPostgresURLIsAnError(t *testing.T) {
	t.Setenv(db.URLEnv, "postgres://user:hunter2-secret@%zz")
	database, err := db.OpenRoot(t.TempDir())
	if err == nil {
		database.Close()
		t.Fatal("a malformed URL opened a database")
	}
	if strings.Contains(err.Error(), "hunter2-secret") {
		t.Fatalf("the error echoes the URL: %v", err)
	}
}

// Running the migrations again must be a no-op, because that is what every
// restart does.
func TestReopeningIsANoOp(t *testing.T) {
	url := testpg.NewURL(t)
	first, err := db.OpenPostgres(url, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.SetSetting(context.Background(), "notify.enabled", "true"); err != nil {
		t.Fatal(err)
	}
	first.Close()
	second, err := db.OpenPostgres(url, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if value, err := second.Setting(context.Background(), "notify.enabled", "false"); err != nil || value != "true" {
		t.Fatalf("value = %q, %v; want the one written before the reopen", value, err)
	}
	var steps int
	if err := second.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&steps); err != nil {
		t.Fatal(err)
	}
	if steps != len(db.PGMigrationNames()) {
		t.Fatalf("schema_migrations has %d rows, chain has %d", steps, len(db.PGMigrationNames()))
	}
}

// A write in flight must not stop a read: they use different pools.
func TestAReadRunsWhileAWriteTransactionIsOpen(t *testing.T) {
	database := testpg.New(t)
	ctx := context.Background()
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES ('held', 'open', ?)`, db.Now()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		var count int
		done <- database.QueryRowContext(ctx, `SELECT count(*) FROM settings WHERE key = 'held'`).Scan(&count)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("read while a write was open: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the read blocked behind an open write transaction")
	}
}

func TestDescribeURLDropsCredentials(t *testing.T) {
	got := db.DescribeURL("postgres://nodevas:hunter2@db.example:5433/nodevas?sslmode=disable")
	if got != "postgres://db.example:5433/nodevas" {
		t.Fatalf("DescribeURL = %q", got)
	}
	if got := db.DescribeURL("postgres://u:hunter2@%zz"); strings.Contains(got, "hunter2") {
		t.Fatalf("an unparseable URL was echoed: %q", got)
	}
}
