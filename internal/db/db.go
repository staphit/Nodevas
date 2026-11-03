// Package db is the workspace's database: the operational data a workspace
// needs that is not the user's content. It is PostgreSQL: the server named by
// NODEVAS_DATABASE_URL (a NAS, a hosted deployment), or the workspace's own
// embedded cluster that serve starts on 127.0.0.1 (internal/dbconn,
// internal/embeddedpg).
//
// The line this package draws matters. Node bodies stay Markdown files and the
// graph stays YAML, because being readable in a text editor, diffable in git
// and writable by another agent is what this app is for. What lives here is
// everything that was never any of those things — accounts, the audit trail,
// server settings, the search index — which in a file layout meant a scatter
// of JSON and JSONL that could only be edited safely by this program.
//
// Until the release that introduced the embedded server a workspace kept this
// data in a SQLite file, <workspace>/.vised/nodevas.db. internal/legacysqlite
// imports such a file once; nothing here reads it.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nodevas/internal/dbconn"
	"nodevas/internal/workspace"
)

// FileName is the legacy SQLite database file inside a workspace's data
// directory: what internal/legacysqlite imports. Nothing opens it otherwise.
const FileName = "nodevas.db"

// maxReaders bounds the read pool: a workspace is served to a handful of
// people, and every connection is a server backend.
const maxReaders = 4

// searchConns is the search index's pool size: its own writer and a reader,
// so a reindex never waits behind an audit append on the main writer.
const searchConns = 2

// DB is an open workspace database: one connection for writing, a pool for
// reading, and a small pool of the search index's own.
//
// The write pool is one connection, so the transactions that count and then
// insert ("the first account is an administrator", "the last administrator
// cannot be removed") stay serialised in Go rather than depending on an
// isolation level. The read connections run default_transaction_read_only,
// which is defence in depth rather than tidiness: a read path that is ever
// tricked into carrying a mutation is refused by the server itself instead of
// relying on every caller having picked the right handle.
type DB struct {
	write  *sql.DB
	read   *sql.DB
	search *sql.DB
	path   string
	// mode is "external" or "embedded" ("" for a URL opened directly).
	mode    string
	dataDir string
	port    int
	exited  <-chan error
	release func() error
}

// Search is the search index's own small pool (two connections, its own
// writer), so reindexing never queues behind audit and session writes on the
// single main writer.
func (d *DB) Search() *sql.DB { return d.search }

// Writer is the single write connection, for a caller that needs the handle
// itself. Prefer the Exec and Tx methods.
func (d *DB) Writer() *sql.DB { return d.write }

// Reader is the read pool. Statements on it cannot write; see the type comment.
func (d *DB) Reader() *sql.DB { return d.read }

// Reads go to the pool.
func (d *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.read.QueryContext(ctx, query, args...)
}

func (d *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return d.read.QueryRowContext(ctx, query, args...)
}

func (d *DB) Query(query string, args ...any) (*sql.Rows, error) {
	return d.read.Query(query, args...)
}

func (d *DB) QueryRow(query string, args ...any) *sql.Row {
	return d.read.QueryRow(query, args...)
}

// Writes go to the single writer.
func (d *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.write.ExecContext(ctx, query, args...)
}

func (d *DB) Exec(query string, args ...any) (sql.Result, error) {
	return d.write.Exec(query, args...)
}

// BeginTx starts a write transaction. There is no read-only transaction here
// on purpose: a reader that needs a consistent snapshot across statements
// should say so by taking one from Reader itself.
func (d *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return d.write.BeginTx(ctx, opts)
}

// Close shuts the pools and then releases the embedded cluster, which the
// last holder in the process stops. It reports every failure.
func (d *DB) Close() error {
	readErr := d.read.Close()
	writeErr := d.write.Close()
	searchErr := d.search.Close()
	var releaseErr error
	if d.release != nil {
		releaseErr = d.release()
		d.release = nil
	}
	return errors.Join(writeErr, readErr, searchErr, releaseErr)
}

// Path returns the PostgreSQL server without its credentials, for an
// operator being told where the data lives.
func (d *DB) Path() string { return d.path }

// Mode is "external" for a server named by URLEnv, "embedded" for the
// workspace's own cluster, "embedded-client" for a cluster another process
// (serve) owns, and "" for a URL opened directly with OpenPostgres.
func (d *DB) Mode() string { return d.mode }

// DataDir is the embedded cluster's data directory, "" otherwise.
func (d *DB) DataDir() string { return d.dataDir }

// Port is the embedded cluster's loopback port, 0 otherwise.
func (d *DB) Port() int { return d.port }

// Exited fires when this process's embedded server died and could not be
// restarted. It is nil for an external server.
func (d *DB) Exited() <-chan error { return d.exited }

// PathFor returns where a workspace's legacy SQLite database lives.
func PathFor(root string) string {
	return filepath.Join(root, workspace.DataDir, FileName)
}

// ErrNotImplemented is no longer returned; it is kept for the tests that
// assert a refusal is a real one and not a stub.
var ErrNotImplemented = errors.New("db: not implemented")

// OpenRoot opens the workspace's database: the external server named by
// URLEnv/URLFileEnv, or the workspace's embedded cluster (internal/dbconn).
// No SQLite file, no fallback: a server unreachable within WaitEnv is an
// error wrapping ErrUnreachable. Under `go test` without a URL it refuses to
// start a cluster and says to use internal/testpg.
func OpenRoot(root string) (*DB, error) {
	rawURL, err := URLFromEnv(os.LookupEnv)
	if err != nil {
		return nil, err
	}
	if rawURL == "" && testing.Testing() {
		return nil, fmt.Errorf("db.OpenRoot under go test without %s would start a real embedded cluster; "+
			"use internal/testpg (testpg.New, or testpg.UseEnv before code that opens the workspace)", URLEnv)
	}
	wait, err := waitFromEnv(os.LookupEnv)
	if err != nil {
		return nil, err
	}
	resolved, err := dbconn.Resolve(root)
	if err != nil {
		return nil, err
	}
	mode := "external"
	if resolved.Embedded {
		// The cluster answered SELECT 1 already; a short wait covers a
		// supervisor restart in progress.
		wait, mode = 10*time.Second, "embedded"
		if resolved.Client {
			mode = "embedded-client"
		}
	}
	database, err := OpenPostgres(resolved.URL, wait)
	if err != nil {
		_ = resolved.Release()
		return nil, err
	}
	database.mode, database.dataDir, database.port = mode, resolved.DataDir, resolved.Port
	database.exited, database.release = resolved.Exited, resolved.Release
	return database, nil
}

// migration is one step. Steps are append-only and never edited once released:
// an edited migration is one that some databases ran and others did not.
type migration struct {
	name string
	stmt string
}

// migrate applies the pending steps in one transaction each.
//
// Every step, and the bookkeeping table itself, runs under the migration
// advisory lock: two processes opening one empty database at once (serve and
// a CLI command, two test binaries) would otherwise both run CREATE TABLE IF
// NOT EXISTS, which is not safe against a concurrent CREATE TABLE. Each of
// those transactions lifts the writer's timeouts (NoTimeouts).
func (d *DB) migrate(ctx context.Context) error {
	if err := d.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, NoTimeouts); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(?)`, pgMigrationLock); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, pgSchemaMigrations)
		return err
	}); err != nil {
		return fmt.Errorf("schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	rows, err := d.write.QueryContext(ctx, `SELECT name FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		applied[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, step := range pgMigrations {
		if applied[step.name] {
			continue
		}
		if err := d.inTx(ctx, func(tx *sql.Tx) error {
			// Another process may have applied this step since the read
			// above; the lock orders the two and the recheck skips it.
			if _, err := tx.ExecContext(ctx, NoTimeouts); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(?)`, pgMigrationLock); err != nil {
				return err
			}
			var done int
			err := tx.QueryRowContext(ctx,
				`SELECT 1 FROM schema_migrations WHERE name = ?`, step.name).Scan(&done)
			if err == nil {
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if _, err := tx.ExecContext(ctx, step.stmt); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx,
				`INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`,
				step.name, Now())
			return err
		}); err != nil {
			return fmt.Errorf("migration %s: %w", step.name, err)
		}
	}
	return nil
}

// inTx runs fn in a transaction, rolling back on error or panic.
func (d *DB) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// Tx runs fn in a transaction. Callers outside this package use it so the
// rollback-on-panic rule lives in one place.
func (d *DB) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	return d.inTx(ctx, fn)
}

// Now is the timestamp format every table in this database uses: RFC3339 in
// UTC, so rows sort as text and read as time.
func Now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// ErrNotFound is what a lookup returns when there is no such row, so callers
// do not each have to know about sql.ErrNoRows.
var ErrNotFound = errors.New("not found")

// A settings key is a dotted identifier this program chooses and a value is a
// short scalar or a small JSON blob. Bounding both here means a caller that
// ever starts passing something a user typed cannot turn this table into
// storage.
const (
	maxSettingKeyBytes   = 256
	maxSettingValueBytes = 64 << 10
)

// Setting reads one settings row. A missing key gives the fallback rather than
// an error: a setting nobody has changed is not a failure.
func (d *DB) Setting(ctx context.Context, key, fallback string) (string, error) {
	if key == "" || len(key) > maxSettingKeyBytes {
		return "", fmt.Errorf("setting key must be 1..%d bytes", maxSettingKeyBytes)
	}
	var value string
	err := d.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return fallback, nil
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

// SetSetting writes one settings row.
func (d *DB) SetSetting(ctx context.Context, key, value string) error {
	if key == "" || len(key) > maxSettingKeyBytes {
		return fmt.Errorf("setting key must be 1..%d bytes", maxSettingKeyBytes)
	}
	if len(value) > maxSettingValueBytes {
		return fmt.Errorf("setting %q is over %d bytes", key, maxSettingValueBytes)
	}
	_, err := d.ExecContext(ctx, `
INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, Now())
	return err
}
