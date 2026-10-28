package db_test

// P2: server-side timeouts on every pool, so one lock wait or one transaction
// left open on a shared server cannot stall every write in the process.

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"nodevas/internal/db"
	"nodevas/internal/testpg"
)

func show(t *testing.T, handle *sql.DB, setting string) string {
	t.Helper()
	var value string
	if err := handle.QueryRow(`SHOW ` + setting).Scan(&value); err != nil {
		t.Fatalf("SHOW %s: %v", setting, err)
	}
	return value
}

func TestPoolsCarryServerSideTimeouts(t *testing.T) {
	database := testpg.New(t)
	want := []struct {
		pool    string
		handle  *sql.DB
		setting string
		value   string
	}{
		{"writer", database.Writer(), "statement_timeout", "15s"},
		{"writer", database.Writer(), "lock_timeout", "5s"},
		{"writer", database.Writer(), "idle_in_transaction_session_timeout", "30s"},
		{"search", database.Search(), "statement_timeout", "15s"},
		{"search", database.Search(), "lock_timeout", "5s"},
		{"search", database.Search(), "idle_in_transaction_session_timeout", "30s"},
		{"reader", database.Reader(), "statement_timeout", "30s"},
	}
	for _, w := range want {
		if got := show(t, w.handle, w.setting); got != w.value {
			t.Errorf("%s %s = %q, want %q", w.pool, w.setting, got, w.value)
		}
	}
}

// An operator who needs a different limit puts it in the URL, and the URL wins.
func TestURLTimeoutOverridesTheDefault(t *testing.T) {
	location, err := url.Parse(testpg.NewURL(t))
	if err != nil {
		t.Fatal(err)
	}
	query := location.Query()
	query.Set("statement_timeout", "60s")
	location.RawQuery = query.Encode()
	database, err := db.OpenPostgres(location.String(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if got := show(t, database.Writer(), "statement_timeout"); got != "1min" {
		t.Fatalf("writer statement_timeout = %q, want the URL's 1min", got)
	}
	if got := show(t, database.Writer(), "lock_timeout"); got != "5s" {
		t.Fatalf("writer lock_timeout = %q, want the default 5s", got)
	}
}

// A row lock held by someone else (another process on a shared server) fails
// the write with lock_not_available after lock_timeout instead of hanging it.
func TestBlockedWriteTimesOutInsteadOfHanging(t *testing.T) {
	ctx := context.Background()
	location := testpg.NewURL(t)
	database, err := db.OpenPostgres(location, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES ('held', 'v', 'now')`); err != nil {
		t.Fatal(err)
	}
	other := rawHandle(t, location)
	tx, err := other.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE settings SET value = 'other' WHERE key = 'held'`); err != nil {
		t.Fatal(err)
	}

	guard, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	started := time.Now()
	_, err = database.ExecContext(guard, `UPDATE settings SET value = 'mine' WHERE key = 'held'`)
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("the blocked write succeeded while the row was locked")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("blocked write failed with %v (after %s), want lock_not_available (55P03)", err, elapsed)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("blocked write took %s to fail", elapsed)
	}
	// The writer is usable again right away.
	if _, err := database.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES ('after', 'v', 'now')`); err != nil {
		t.Fatalf("writer after the timeout: %v", err)
	}
}

// Migrations are exempt: a second process waiting longer than lock_timeout
// for the migration lock, or a step slower than statement_timeout, must still
// finish rather than fail the server's start.
func TestMigrationWaitsPastLockTimeout(t *testing.T) {
	ctx := context.Background()
	location := testpg.NewEmptyURL(t)
	other := rawHandle(t, location)
	tx, err := other.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1852793957)`); err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		defer close(released)
		time.Sleep(6500 * time.Millisecond)
		_ = tx.Rollback()
	}()
	database, err := db.OpenPostgres(location, 0)
	<-released
	if err != nil {
		t.Fatalf("migration behind a held lock: %v", err)
	}
	database.Close()
}
