package db_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"nodevas/internal/db"
	"nodevas/internal/db/dbtest"
)

func requirePostgres(t *testing.T) {
	t.Helper()
	if !dbtest.UsePostgres(t) {
		t.Skipf("set %s to run against PostgreSQL", dbtest.Env)
	}
}

// A server that never answers must not keep the workspace from starting: the
// SQLite file takes over, as it ran before there was a server.
func TestUnreachablePostgresFallsBackToSQLite(t *testing.T) {
	t.Setenv(db.URLEnv, "postgres://nodevas:x@127.0.0.1:1/nodevas?connect_timeout=1")
	t.Setenv(db.WaitEnv, "0s")
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()
	if database.Backend() != db.SQLite {
		t.Fatalf("backend = %s, want the SQLite fallback", database.Backend())
	}
	if database.Local() != database {
		t.Fatal("on SQLite the local database is the database itself")
	}
}

// A URL that cannot be parsed is a configuration mistake, not an outage, and
// must stop the server instead of quietly running it on the fallback.
func TestMalformedPostgresURLIsAnError(t *testing.T) {
	t.Setenv(db.URLEnv, "postgres://%zz")
	if database, err := db.Open(t.TempDir()); err == nil {
		database.Close()
		t.Fatal("a malformed URL opened a database")
	}
}

func TestPostgresSchemaAndReopen(t *testing.T) {
	requirePostgres(t)
	workspace := t.TempDir()
	first, err := db.Open(workspace)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if first.Backend() != db.Postgres {
		t.Fatalf("backend = %s, want postgres", first.Backend())
	}
	if first.Local().Backend() != db.SQLite {
		t.Fatal("the search index must stay in the workspace SQLite file")
	}
	if _, err := os.Stat(db.PathFor(workspace)); err != nil {
		t.Fatalf("local SQLite file: %v", err)
	}
	ctx := context.Background()
	if err := first.SetSetting(ctx, "notify.smtp.host", "smtp.example.test"); err != nil {
		t.Fatal(err)
	}
	if err := first.SetSetting(ctx, "notify.smtp.host", "mail.example.test"); err != nil {
		t.Fatal(err)
	}
	first.Close()

	second, err := db.Open(workspace)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()
	value, err := second.Setting(ctx, "notify.smtp.host", "")
	if err != nil || value != "mail.example.test" {
		t.Fatalf("setting = %q, %v", value, err)
	}
	var steps int
	if err := second.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&steps); err != nil {
		t.Fatal(err)
	}
	// 0001-0003, 0005, 0007-0023: PostgreSQL has no counterpart to the
	// SQLite-only steps (the search index, 0006's data repair).
	if steps != 21 {
		t.Fatalf("schema_migrations has %d rows, want 21", steps)
	}
	// The read pool is read-only on the server, as query_only makes it on
	// SQLite.
	if _, err := second.Reader().ExecContext(ctx, `DELETE FROM settings`); err == nil {
		t.Fatal("the read pool accepted a write")
	}
}

func TestCopyToPostgres(t *testing.T) {
	requirePostgres(t)
	ctx := context.Background()
	src, err := db.OpenAt(filepath.Join(t.TempDir(), db.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	for _, stmt := range []string{
		`INSERT INTO accounts (id, name, role, email, created_at) VALUES ('a1', 'Ann', 'admin', 'ann@example.test', '2026-01-01T00:00:00Z')`,
		`INSERT INTO accounts (id, name, role, created_at) VALUES ('b2', 'bob', 'member', '2026-01-02T00:00:00Z')`,
		`INSERT INTO audit_events (at, action) VALUES ('2026-01-01T00:00:00Z', 'user.add')`,
		`INSERT INTO audit_events (at, action) VALUES ('2026-01-01T00:00:01Z', 'user.login')`,
	} {
		if _, err := src.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := src.SetSetting(ctx, "copy.test", ""); err != nil {
		t.Fatal(err)
	}

	dst, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	counts, err := db.CopyToPostgres(ctx, src, dst, false)
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if counts["accounts"] != 2 || counts["audit_events"] != 2 || counts["settings"] != 1 {
		t.Fatalf("counts = %v", counts)
	}
	// Creation order survives, and so does the case-insensitive lookup.
	var first string
	if err := dst.QueryRowContext(ctx, `SELECT name FROM accounts ORDER BY rowid LIMIT 1`).Scan(&first); err != nil || first != "Ann" {
		t.Fatalf("first account = %q, %v", first, err)
	}
	var id string
	if err := dst.QueryRowContext(ctx,
		`SELECT id FROM accounts WHERE email = ? COLLATE NOCASE`, "ANN@Example.test").Scan(&id); err != nil || id != "a1" {
		t.Fatalf("lookup by address = %q, %v", id, err)
	}
	if _, err := db.CopyToPostgres(ctx, src, dst, false); err == nil {
		t.Fatal("a copy onto a populated database was accepted without --replace")
	}
	if counts, err = db.CopyToPostgres(ctx, src, dst, true); err != nil || counts["accounts"] != 2 {
		t.Fatalf("replace copy = %v, %v", counts, err)
	}
	var accounts int
	if err := dst.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts`).Scan(&accounts); err != nil || accounts != 2 {
		t.Fatalf("accounts after replace = %d, %v", accounts, err)
	}
}

func TestUniqueViolationOnPostgres(t *testing.T) {
	requirePostgres(t)
	database := dbtest.Open(t)
	ctx := context.Background()
	insert := `INSERT INTO accounts (id, name, role, email, created_at) VALUES (?, ?, 'member', ?, '2026-01-01T00:00:00Z')`
	if _, err := database.ExecContext(ctx, insert, "a", "ann", "ann@example.test"); err != nil {
		t.Fatal(err)
	}
	_, err := database.ExecContext(ctx, insert, "b", "bob", "ANN@example.test")
	if !db.IsUniqueViolation(err, "accounts_email_nocase") {
		t.Fatalf("duplicate address error = %v", err)
	}
	_, err = database.ExecContext(ctx, insert, "c", "ANN", "")
	if !db.IsUniqueViolation(err, "") || db.IsUniqueViolation(err, "accounts_email_nocase") {
		t.Fatalf("duplicate name error = %v", err)
	}
}
