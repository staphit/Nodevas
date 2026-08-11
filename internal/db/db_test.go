package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openForTest(t *testing.T) *DB {
	t.Helper()
	database, err := OpenAt(filepath.Join(t.TempDir(), FileName))
	if err != nil {
		t.Fatalf("OpenAt: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestOpenCreatesEverySchemaObject(t *testing.T) {
	database := openForTest(t)

	rows, err := database.Query(
		`SELECT name FROM sqlite_master WHERE type IN ('table','view') ORDER BY name`)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	defer rows.Close()
	present := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		present[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("scan schema: %v", err)
	}
	for _, want := range []string{
		"accounts", "audit_events", "settings", "search_documents", "search_fts",
		"schema_migrations",
	} {
		if !present[want] {
			t.Fatalf("table %q missing; have %v", want, present)
		}
	}
}

// Running the migrations twice must be a no-op, because that is what every
// restart does.
func TestOpeningAnExistingDatabaseIsANoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	first, err := OpenAt(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := first.SetSetting(context.Background(), "notify.enabled", "true"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	first.Close()

	second, err := OpenAt(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer second.Close()

	value, err := second.Setting(context.Background(), "notify.enabled", "false")
	if err != nil {
		t.Fatalf("Setting: %v", err)
	}
	if value != "true" {
		t.Fatalf("value = %q, want the value written before the reopen", value)
	}
}

// Defence in depth: a read path that is ever tricked into carrying a mutation
// is refused by SQLite itself rather than by everyone having picked the right
// handle. This is the assertion that keeps that true.
func TestTheReadPoolCannotWrite(t *testing.T) {
	database := openForTest(t)

	_, err := database.Reader().Exec(
		`INSERT INTO settings (key, value, updated_at) VALUES ('x', 'y', ?)`, Now())
	if err == nil {
		t.Fatal("a write through the read pool succeeded")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "readonly") &&
		!strings.Contains(strings.ToLower(err.Error()), "read-only") {
		t.Fatalf("err = %v, want a read-only refusal", err)
	}

	// And the same statement through the writer still works, so the refusal is
	// the pool's doing and not a broken database.
	if _, err := database.Exec(
		`INSERT INTO settings (key, value, updated_at) VALUES ('x', 'y', ?)`, Now()); err != nil {
		t.Fatalf("write through the writer: %v", err)
	}
}

// A write in flight must not stop a search. Before the pool was split, both
// went through one connection and a search queued behind whatever the server
// happened to be writing.
func TestAReadRunsWhileAWriteTransactionIsOpen(t *testing.T) {
	database := openForTest(t)
	ctx := context.Background()

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES ('held', 'open', ?)`, Now()); err != nil {
		t.Fatalf("write inside the transaction: %v", err)
	}

	// Uncommitted, so the reader must not see it — and must not block on it.
	done := make(chan error, 1)
	go func() {
		var count int
		done <- database.QueryRowContext(ctx,
			`SELECT count(*) FROM settings WHERE key = 'held'`).Scan(&count)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("read while a write was open: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the read blocked behind an open write transaction")
	}
}

func TestSettingReturnsTheFallbackWhenNobodyHasChangedIt(t *testing.T) {
	database := openForTest(t)

	value, err := database.Setting(context.Background(), "never.set", "the-default")
	if err != nil {
		t.Fatalf("Setting: %v", err)
	}
	if value != "the-default" {
		t.Fatalf("value = %q, want the fallback", value)
	}
}

func TestSetSettingOverwritesRatherThanAccumulating(t *testing.T) {
	database := openForTest(t)
	ctx := context.Background()

	for _, value := range []string{"first", "second", "third"} {
		if err := database.SetSetting(ctx, "smtp.host", value); err != nil {
			t.Fatalf("SetSetting %q: %v", value, err)
		}
	}
	var rows int
	if err := database.QueryRow(`SELECT count(*) FROM settings WHERE key = 'smtp.host'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("rows = %d, want 1", rows)
	}
	value, err := database.Setting(ctx, "smtp.host", "")
	if err != nil {
		t.Fatal(err)
	}
	if value != "third" {
		t.Fatalf("value = %q, want the last write", value)
	}
}

// An account row's role is constrained in the schema, so a hand-edit in a
// database tool cannot invent a role the server does not understand.
func TestAccountRoleIsConstrainedBySchema(t *testing.T) {
	database := openForTest(t)

	_, err := database.Exec(
		`INSERT INTO accounts (id, name, role, created_at) VALUES ('1', 'ann', 'superuser', ?)`, Now())
	if err == nil {
		t.Fatal("an unknown role was accepted")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "constraint") {
		t.Fatalf("err = %v, want a constraint failure", err)
	}
}

// Names are unique without regard to case: a sign-in prompt that tells "Ann"
// from "ann" only creates lockouts.
func TestAccountNamesAreUniqueIgnoringCase(t *testing.T) {
	database := openForTest(t)

	if _, err := database.Exec(
		`INSERT INTO accounts (id, name, role, created_at) VALUES ('1', 'ann', 'admin', ?)`, Now()); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO accounts (id, name, role, created_at) VALUES ('2', 'ANN', 'member', ?)`, Now()); err == nil {
		t.Fatal("a name differing only in case was accepted")
	}
}

// A transaction that fails halfway must leave nothing behind, or a partial
// account is worse than no account.
func TestTxRollsBackOnError(t *testing.T) {
	database := openForTest(t)
	ctx := context.Background()

	wanted := errors.New("deliberate")
	err := database.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO accounts (id, name, role, created_at) VALUES ('1', 'ann', 'admin', ?)`,
			Now()); err != nil {
			return err
		}
		return wanted
	})
	if !errors.Is(err, wanted) {
		t.Fatalf("err = %v, want the callback's error", err)
	}

	var rows int
	if err := database.QueryRow(`SELECT count(*) FROM accounts`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("rows = %d, want 0 after a rollback", rows)
	}
}

func TestTxRollsBackOnPanic(t *testing.T) {
	database := openForTest(t)
	ctx := context.Background()

	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic was swallowed")
			}
		}()
		_ = database.Tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO accounts (id, name, role, created_at) VALUES ('1', 'ann', 'admin', ?)`,
				Now()); err != nil {
				return err
			}
			panic("deliberate")
		})
	}()

	var rows int
	if err := database.QueryRow(`SELECT count(*) FROM accounts`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("rows = %d, want 0 after a panic", rows)
	}
}

// The columns hold tokens the app produced, not prose: FTS5's own tokeniser
// would index a Chinese paragraph as a single term. This is the shape the
// search index writes and queries in.
func TestSearchIndexMatchesTheTokensTheAppSupplies(t *testing.T) {
	database := openForTest(t)

	if _, err := database.Exec(
		`INSERT INTO search_fts (rowid, title, body) VALUES (1, '設計 計稿 設 計 稿', '初稿 稿內 內容 初 稿 內 容')`,
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var rowid int
	if err := database.QueryRow(
		`SELECT rowid FROM search_fts WHERE search_fts MATCH '初稿'`).Scan(&rowid); err != nil {
		t.Fatalf("match: %v", err)
	}
	if rowid != 1 {
		t.Fatalf("rowid = %d, want 1", rowid)
	}
}
