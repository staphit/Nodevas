// Package db is the workspace's SQLite database: the operational data a
// workspace needs that is not the user's content.
//
// The line this package draws matters. Node bodies stay Markdown files and the
// graph stays YAML, because being readable in a text editor, diffable in git
// and writable by another agent is what this app is for. What lives here is
// everything that was never any of those things — accounts, the audit trail,
// server settings, the search index — which in a file layout meant a scatter
// of JSON and JSONL that could only be edited safely by this program.
//
// One file, at <workspace>/.vised/nodevas.db, so an operator can open it in DB
// Browser for SQLite, TablePlus or DBeaver and look at or fix a row without
// this program's help. The driver is pure Go (modernc.org/sqlite): a cgo one
// would need a C toolchain on Windows and would complicate the iOS framework
// build, which is built with cgo disabled.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"nodevas/internal/workspace"
)

// FileName is the database file inside a workspace's data directory.
const FileName = "nodevas.db"

// busyTimeout is how long a statement waits for another writer before giving
// up. The other writer is usually the operator's DB tool holding a transaction
// open while they look at a row; a few seconds outlasts that without hanging a
// request forever.
const busyTimeout = 5 * time.Second

// maxReaders bounds the read pool. WAL lets readers run alongside the writer,
// so this is about not opening a file handle per in-flight request rather than
// about correctness; a workspace is served to a handful of people.
const maxReaders = 4

// DB is an open workspace database: one connection for writing, a pool for
// reading.
//
// SQLite admits a single writer, so the write handle is capped at one
// connection and the queueing happens in Go rather than as a pile of
// SQLITE_BUSY errors. WAL lets readers run concurrently with that writer, so
// the read pool is several connections and a search no longer waits behind an
// audit row being appended.
//
// The read connections are opened PRAGMA query_only, which is defence in
// depth rather than tidiness: a read path that is ever tricked into carrying a
// mutation — an injection, a mistake, a stray Exec — is refused by SQLite
// itself instead of relying on every caller having picked the right handle.
type DB struct {
	write *sql.DB
	read  *sql.DB
	path  string
}

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

// Close shuts both handles, reporting the first failure.
func (d *DB) Close() error {
	readErr := d.read.Close()
	writeErr := d.write.Close()
	if writeErr != nil {
		return writeErr
	}
	return readErr
}

// Path returns the database file, for an operator being told where to point
// their tools.
func (d *DB) Path() string { return d.path }

// PathFor returns where a workspace's database lives.
func PathFor(root string) string {
	return filepath.Join(root, workspace.DataDir, FileName)
}

// Open opens (creating if needed) the database for a workspace and brings its
// schema up to date.
func Open(root string) (*DB, error) {
	path := PathFor(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("database directory: %w", err)
	}
	return OpenAt(path)
}

// OpenAt opens a database by path. Tests use it; Open is what the server calls.
func OpenAt(path string) (*DB, error) {
	// Pragmas travel in the DSN rather than being executed once after opening,
	// because a pragma is per connection: a pool would apply them to the first
	// connection and quietly run the rest without them.
	//
	// _txlock=immediate makes a write transaction take its lock at BEGIN
	// rather than at the first write. Without it, two transactions that both
	// start by reading and then write deadlock into SQLITE_BUSY, which is the
	// classic way a SQLite application starts failing under concurrency.
	//
	// synchronous=NORMAL trades a crash-window of the last transaction for not
	// fsyncing on every commit. The content of record is still the Markdown
	// and YAML on disk; this database holds operational data whose tail a
	// crash may lose.
	writeDSN := "file:" + path + "?_txlock=immediate" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(ON)" +
		fmt.Sprintf("&_pragma=busy_timeout(%d)", busyTimeout.Milliseconds())

	writer, err := sql.Open("sqlite", writeDSN)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// One writer, always. Letting database/sql open several does not give
	// SQLite more than one, it only turns a queue into failures.
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)
	writer.SetConnMaxLifetime(0)

	// query_only comes last so the pragmas before it still apply, and
	// journal_mode is absent because setting it is itself a write.
	readDSN := "file:" + path + "?_pragma=foreign_keys(ON)" +
		fmt.Sprintf("&_pragma=busy_timeout(%d)", busyTimeout.Milliseconds()) +
		"&_pragma=query_only(1)"
	reader, err := sql.Open("sqlite", readDSN)
	if err != nil {
		writer.Close()
		return nil, fmt.Errorf("open %s for reading: %w", path, err)
	}
	reader.SetMaxOpenConns(maxReaders)
	reader.SetMaxIdleConns(maxReaders)
	reader.SetConnMaxLifetime(0)

	database := &DB{write: writer, read: reader, path: path}

	// The writer runs first: it is what creates the file and puts it in WAL
	// mode, and a reader opened against a database that does not exist yet
	// would create an empty one in the default journal mode.
	if err := database.migrate(context.Background()); err != nil {
		database.Close()
		return nil, err
	}

	// The file holds password and PIN hashes. On a shared machine it must not
	// be world-readable, like the other workspace credentials. Done after the
	// migration so the WAL sidecars exist to be tightened too.
	if err := restrictPermissions(path); err != nil {
		database.Close()
		return nil, err
	}
	return database, nil
}

// restrictPermissions tightens the database and its WAL sidecars. Chmod is a
// no-op on Windows, where the file inherits the directory's ACL instead.
func restrictPermissions(path string) error {
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(name, 0o600); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("secure %s: %w", name, err)
		}
	}
	return nil
}

// migration is one step. Steps are append-only and never edited once released:
// an edited migration is one that some workspaces ran and others did not.
type migration struct {
	name string
	stmt string
}

// migrations run in order, once each, recorded in schema_migrations.
//
// Every table here is meant to be readable and, where it makes sense,
// editable by hand in a database tool. That is why the timestamps are ISO-8601
// text rather than Unix integers: an operator scanning the audit table should
// see when something happened without converting anything.
var migrations = []migration{
	{
		name: "0001_accounts",
		stmt: `
CREATE TABLE accounts (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    role        TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    -- argon2id, PHC format. Never a plaintext password or PIN: this column is
    -- one-way on purpose, so pasting a password in here does not create a
    -- working login, it creates an account nobody can use.
    password_hash TEXT NOT NULL DEFAULT '',
    pin_hash      TEXT NOT NULL DEFAULT '',
    -- Where this account's one-time passcodes are sent. Changing it changes
    -- who can complete a sign-in, so it is a credential, not a contact field.
    email       TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL
) STRICT;
CREATE UNIQUE INDEX accounts_name_nocase ON accounts (name COLLATE NOCASE);
`,
	},
	{
		name: "0002_audit",
		stmt: `
CREATE TABLE audit_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    at          TEXT NOT NULL,
    -- The project this happened in, or '' for server-wide events such as a
    -- sign-in. Not a foreign key: projects come and go on disk, and losing the
    -- audit trail because a project was deleted defeats the point of it.
    project     TEXT NOT NULL DEFAULT '',
    actor_id    TEXT NOT NULL DEFAULT '',
    actor_name  TEXT NOT NULL DEFAULT '',
    action      TEXT NOT NULL,
    target      TEXT NOT NULL DEFAULT '',
    client_ip   TEXT NOT NULL DEFAULT '',
    -- Free-form JSON for whatever the action needs. Kept as text so a reader
    -- can see it without this program.
    detail      TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX audit_events_at ON audit_events (at DESC);
CREATE INDEX audit_events_actor ON audit_events (actor_id, at DESC);
CREATE INDEX audit_events_project ON audit_events (project, at DESC);
`,
	},
	{
		name: "0003_settings",
		stmt: `
CREATE TABLE settings (
    -- Dotted keys, e.g. 'notify.smtp.host'. A flat key/value table rather than
    -- a column per setting: settings are added often, and a migration per
    -- checkbox is not worth it.
    key         TEXT PRIMARY KEY,
    value       TEXT NOT NULL,
    updated_at  TEXT NOT NULL
) STRICT;
`,
	},
	{
		name: "0004_search_index",
		stmt: `
-- Derived data: everything here can be rebuilt from the Markdown on disk, so
-- it is safe to delete this table's contents if it ever disagrees with them.
CREATE TABLE search_documents (
    project     TEXT NOT NULL,
    node_id     TEXT NOT NULL,
    path        TEXT NOT NULL DEFAULT '',
    title       TEXT NOT NULL DEFAULT '',
    -- Modification time of the file this row was built from, so a rescan can
    -- skip what has not changed.
    modified_at TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (project, node_id, path)
) STRICT;

-- The columns hold *pre-tokenised* text, not the original: internal/searchindex
-- already tokenises for a corpus that is mostly Chinese, where whitespace
-- carries no word boundaries and FTS5's own unicode61 would index a whole
-- paragraph as one term. So the app emits its bigrams and single characters
-- separated by spaces, and FTS5 is left doing what it is good at — posting
-- lists — rather than guessing where Chinese words end.
CREATE VIRTUAL TABLE search_fts USING fts5 (
    title,
    body,
    content = '',
    tokenize = 'unicode61'
);
`,
	},
	{
		name: "0005_sessions",
		stmt: `
-- Signed-in browsers. Sessions used to live only in memory, which logged
-- everyone out on every restart; with people editing the same board at once
-- that is a deploy-shaped interruption rather than a tidy default.
--
-- Outliving the process is safe because a session is not authority by itself:
-- every request re-reads the account's revision and refuses a session whose
-- account has since changed or gone. See auth.SessionAuth.Authenticate.
CREATE TABLE sessions (
    -- sha256 of the cookie value, hex. Never the cookie itself: this file is
    -- copied to object storage three times a day, and a backup must not be a
    -- bag of working credentials.
    token_hash  TEXT PRIMARY KEY,
    -- Not a foreign key to accounts: the visitor session belongs to no account,
    -- and a removed account is already handled by the revision check.
    user_id     TEXT NOT NULL,
    user_name   TEXT NOT NULL DEFAULT '',
    user_role   TEXT NOT NULL DEFAULT '',
    -- The account revision this session was opened against.
    revision    TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    expires_at  TEXT NOT NULL
) STRICT;
CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expires ON sessions (expires_at);
`,
	},
}

// migrate applies the pending steps in one transaction each.
func (d *DB) migrate(ctx context.Context) error {
	if _, err := d.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    name       TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL
) STRICT`); err != nil {
		return fmt.Errorf("schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	rows, err := d.QueryContext(ctx, `SELECT name FROM schema_migrations`)
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

	for _, step := range migrations {
		if applied[step.name] {
			continue
		}
		if err := d.inTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, step.stmt); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx,
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
