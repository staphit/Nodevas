package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"

	"nodevas/internal/dbconn"
)

// URLEnv names an external PostgreSQL server (a NAS, a hosted deployment).
// Unset, the workspace runs its own embedded server. It is an environment
// variable and not a nodevas.yaml key for the same reason the SMTP password
// is: the URL carries a password, and nodevas.yaml sits in the workspace
// beside the notes.
const URLEnv = dbconn.URLEnv

// URLFileEnv names a file holding the URL instead, for a container that gets
// its secrets as mounted files, as NODEVAS_SMTP_PASSWORD_FILE does.
const URLFileEnv = dbconn.URLFileEnv

// URLFromEnv returns the configured PostgreSQL URL, or "" for none. URLEnv
// wins over URLFileEnv when both are set.
func URLFromEnv(lookup func(string) (string, bool)) (string, error) {
	return dbconn.URLFromEnv(lookup)
}

// WaitEnv bounds how long OpenRoot keeps retrying an unreachable external
// server before it gives up, as a Go duration ("30s", "2m"). On a NAS that has
// just booted the database container usually comes up after this one. Past
// it, serve exits and the container's restart policy tries again: there is no
// fallback database.
const WaitEnv = "NODEVAS_DATABASE_WAIT"

// defaultWait is WaitEnv's value when it is unset.
const defaultWait = 30 * time.Second

// ErrUnreachable wraps a failure to reach the PostgreSQL server at all, as
// opposed to reaching it and being refused something.
var ErrUnreachable = errors.New("postgres unreachable")

// waitFromEnv reads WaitEnv.
func waitFromEnv(lookup func(string) (string, bool)) (time.Duration, error) {
	raw, ok := lookup(WaitEnv)
	if !ok || strings.TrimSpace(raw) == "" {
		return defaultWait, nil
	}
	wait, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || wait < 0 {
		return 0, fmt.Errorf("%s must be a non-negative duration such as 30s, got %q", WaitEnv, raw)
	}
	return wait, nil
}

// OpenPostgres connects to a PostgreSQL server and brings its schema up to
// date, retrying an unreachable server for up to wait. See DB for the pools.
func OpenPostgres(rawURL string, wait time.Duration) (*DB, error) {
	writeConfig, err := pgx.ParseConfig(rawURL)
	if err != nil {
		// The message from the parser can echo the URL, password included.
		return nil, fmt.Errorf("%s is not a valid PostgreSQL connection string", URLEnv)
	}
	if writeConfig.ConnectTimeout == 0 {
		writeConfig.ConnectTimeout = 5 * time.Second
	}
	readConfig := writeConfig.Copy()
	readConfig.RuntimeParams["default_transaction_read_only"] = "on"
	setDefaults(writeConfig.RuntimeParams, writerTimeouts)
	setDefaults(readConfig.RuntimeParams, readerTimeouts)

	writer := sql.OpenDB(rebindConnector{stdlib.GetConnector(*writeConfig)})
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)
	writer.SetConnMaxLifetime(0)

	if err := pingUntil(writer, wait); err != nil {
		writer.Close()
		return nil, fmt.Errorf("%w: %s: %w", ErrUnreachable, describeURL(writeConfig), err)
	}

	reader := sql.OpenDB(rebindConnector{stdlib.GetConnector(*readConfig)})
	reader.SetMaxOpenConns(maxReaders)
	reader.SetMaxIdleConns(maxReaders)
	reader.SetConnMaxLifetime(0)

	search := sql.OpenDB(rebindConnector{stdlib.GetConnector(*writeConfig.Copy())})
	search.SetMaxOpenConns(searchConns)
	search.SetMaxIdleConns(searchConns)
	search.SetConnMaxLifetime(0)

	database := &DB{write: writer, read: reader, search: search, path: describeURL(writeConfig)}
	if err := database.migrate(context.Background()); err != nil {
		database.Close()
		return nil, err
	}
	return database, nil
}

// writerTimeouts bound every session on the write pools (the main writer and
// the search pool). The main writer is one connection: without these, one
// lock wait or one transaction left open on a shared server would stall every
// audit, session and run write in the process indefinitely. A lock wait fails
// with lock_not_available (55P03), a runaway statement with query_canceled
// (57014), and a session idle inside a transaction is closed by the server.
var writerTimeouts = map[string]string{
	"statement_timeout":                   "15s",
	"lock_timeout":                        "5s",
	"idle_in_transaction_session_timeout": "30s",
}

// readerTimeouts bound the read pool: reads take no locks worth waiting on,
// but an analytics query over a large history must not hold a connection
// forever.
var readerTimeouts = map[string]string{
	"statement_timeout": "30s",
}

// setDefaults sets each param the connection string did not: a value in the
// URL (postgres://…?statement_timeout=60s) is the operator's and wins.
func setDefaults(params, defaults map[string]string) {
	for key, value := range defaults {
		if _, set := params[key]; !set {
			params[key] = value
		}
	}
}

// NoTimeouts lifts the pool's timeouts for the rest of one transaction. A
// migration may wait for another process's migration lock longer than
// lock_timeout, and a step over a large table may run longer than
// statement_timeout; neither should fail a server's start.
const NoTimeouts = `SET LOCAL statement_timeout = 0; SET LOCAL lock_timeout = 0`

// pingUntil retries a ping with a short backoff until it succeeds or wait has
// passed. A zero wait is a single attempt.
func pingUntil(handle *sql.DB, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	delay := 500 * time.Millisecond
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := handle.PingContext(ctx)
		cancel()
		if err == nil {
			return nil
		}
		if !time.Now().Add(delay).Before(deadline) {
			return err
		}
		time.Sleep(delay)
		if delay < 5*time.Second {
			delay *= 2
		}
	}
}

// DescribeURL is a connection string's server and database without the
// credentials, for logs and for an operator being told where the data lives.
// A URL that does not parse is described as such, never echoed.
func DescribeURL(rawURL string) string {
	config, err := pgx.ParseConfig(rawURL)
	if err != nil {
		return "(an unparseable PostgreSQL URL)"
	}
	return describeURL(config)
}

// describeURL is the server's address without its credentials, for logs and
// for an operator being told where the data went.
func describeURL(config *pgx.ConnConfig) string {
	location := url.URL{
		Scheme: "postgres",
		Host:   config.Host + ":" + strconv.Itoa(int(config.Port)),
		Path:   "/" + config.Database,
	}
	return location.String()
}

// IsUniqueViolation reports whether err is a write refused by a unique
// constraint or index. A non-empty name narrows it to that constraint (the
// index name PostgreSQL reports).
func IsUniqueViolation(err error, name string) bool {
	if err == nil {
		return false
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code == "23505" && (name == "" || pgErr.ConstraintName == name)
	}
	return false
}

// rebindConnector lets the SQL in this program keep the ? placeholders it was
// written with (when it also ran on SQLite) on PostgreSQL, which wants $1, $2,
// …, by rewriting each statement on its way to
// the driver. Doing it under database/sql instead of at every call site keeps
// *sql.DB and *sql.Tx the types the rest of the program already passes around.
type rebindConnector struct{ inner driver.Connector }

func (c rebindConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	pgConn, ok := conn.(*stdlib.Conn)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("unexpected PostgreSQL driver connection %T", conn)
	}
	return rebindConn{pgConn}, nil
}

func (c rebindConnector) Driver() driver.Driver { return c.inner.Driver() }

// rebindConn embeds the pgx connection so every optional driver interface it
// implements — transactions, pinging, value checking, session reset — still
// reaches database/sql, and overrides only the methods that carry SQL text.
type rebindConn struct{ *stdlib.Conn }

func (c rebindConn) Prepare(query string) (driver.Stmt, error) {
	return c.Conn.Prepare(rebind(query))
}

func (c rebindConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	return c.Conn.PrepareContext(ctx, rebind(query))
}

// A statement with no arguments is passed through untouched. pgx sends those
// over the simple protocol, which is what lets a migration be several
// statements in one string, and there is nothing to rebind in them anyway.
func (c rebindConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if len(args) > 0 {
		query = rebind(query)
	}
	return c.Conn.ExecContext(ctx, query, args)
}

func (c rebindConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if len(args) > 0 {
		query = rebind(query)
	}
	return c.Conn.QueryContext(ctx, query, args)
}

// rebind numbers the ? placeholders in query as $1, $2, … . A ? inside a
// quoted string, a quoted identifier or a comment is left alone.
func rebind(query string) string {
	if !strings.Contains(query, "?") {
		return query
	}
	var out strings.Builder
	out.Grow(len(query) + 8)
	n := 0
	for i := 0; i < len(query); i++ {
		ch := query[i]
		switch {
		case ch == '\'' || ch == '"':
			end := strings.IndexByte(query[i+1:], ch)
			if end < 0 {
				out.WriteString(query[i:])
				return out.String()
			}
			out.WriteString(query[i : i+end+2])
			i += end + 1
		case ch == '-' && i+1 < len(query) && query[i+1] == '-':
			end := strings.IndexByte(query[i:], '\n')
			if end < 0 {
				out.WriteString(query[i:])
				return out.String()
			}
			out.WriteString(query[i : i+end+1])
			i += end
		case ch == '?':
			n++
			out.WriteByte('$')
			out.WriteString(strconv.Itoa(n))
		default:
			out.WriteByte(ch)
		}
	}
	return out.String()
}

// pgSchemaMigrations is the bookkeeping table: one row per applied step,
// matched by name.
const pgSchemaMigrations = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    name       TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL
)`

// pgMigrations is the schema. It was written to mirror the SQLite schema this
// program used to keep beside it, which is why some of it reads the way it
// does:
//
//   - nocase is an ICU collation that ignores case, so the queries' own
//     "COLLATE NOCASE" (folded to nocase by the parser) finds it by name.
//   - accounts keeps a rowid column: the order the rows were created in.
//   - Timestamp and hash columns use the C collation, so they compare and sort
//     byte by byte whatever locale the server runs in.
//
// Steps are matched by name in schema_migrations, and databases in the field
// (the NAS) have these names recorded, so none is ever renamed or
// renumbered. The gaps (0004, 0006) are SQLite-only steps that never had a
// PostgreSQL counterpart: 0004 was the FTS5 index, 0006 a SQLite data repair.
var pgMigrations = []migration{
	{
		name: "0001_accounts",
		stmt: `
CREATE COLLATION IF NOT EXISTS nocase (provider = icu, locale = 'und-u-ks-level2', deterministic = false);
CREATE TABLE accounts (
    rowid         BIGINT GENERATED ALWAYS AS IDENTITY UNIQUE,
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    role          TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    password_hash TEXT NOT NULL DEFAULT '',
    pin_hash      TEXT NOT NULL DEFAULT '',
    email         TEXT NOT NULL DEFAULT '',
    created_at    TEXT COLLATE "C" NOT NULL
);
CREATE UNIQUE INDEX accounts_name_nocase ON accounts (name COLLATE nocase);
CREATE UNIQUE INDEX accounts_email_nocase ON accounts (email COLLATE nocase) WHERE email != '';
`,
	},
	{
		name: "0002_audit",
		stmt: `
CREATE TABLE audit_events (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at          TEXT COLLATE "C" NOT NULL,
    project     TEXT NOT NULL DEFAULT '',
    actor_id    TEXT NOT NULL DEFAULT '',
    actor_name  TEXT NOT NULL DEFAULT '',
    action      TEXT COLLATE "C" NOT NULL,
    target      TEXT NOT NULL DEFAULT '',
    client_ip   TEXT NOT NULL DEFAULT '',
    detail      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX audit_events_at ON audit_events (at DESC);
CREATE INDEX audit_events_actor ON audit_events (actor_id, at DESC);
CREATE INDEX audit_events_project ON audit_events (project, at DESC);
`,
	},
	{
		name: "0003_settings",
		stmt: `
CREATE TABLE settings (
    key         TEXT PRIMARY KEY,
    value       TEXT NOT NULL,
    updated_at  TEXT COLLATE "C" NOT NULL
);
`,
	},
	{
		name: "0005_sessions",
		stmt: `
CREATE TABLE sessions (
    token_hash  TEXT COLLATE "C" PRIMARY KEY,
    user_id     TEXT NOT NULL,
    user_name   TEXT NOT NULL DEFAULT '',
    user_role   TEXT NOT NULL DEFAULT '',
    revision    TEXT NOT NULL DEFAULT '',
    created_at  TEXT COLLATE "C" NOT NULL,
    expires_at  TEXT COLLATE "C" NOT NULL
);
CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expires ON sessions (expires_at);
`,
	},
	{
		// BIGINT where SQLite says INTEGER: SQLite's INTEGER is 64-bit, and a
		// Unix millisecond timestamp does not fit PostgreSQL's 32-bit one.
		name: "0007_copilot",
		stmt: `
CREATE TABLE copilot_conversations (
    id          TEXT COLLATE "C" PRIMARY KEY,
    account_id  TEXT NOT NULL,
    project     TEXT NOT NULL,
    title       TEXT NOT NULL DEFAULT '',
    mode        TEXT NOT NULL DEFAULT 'default',
    created_at  BIGINT NOT NULL,
    updated_at  BIGINT NOT NULL
);
CREATE INDEX copilot_conversations_owner ON copilot_conversations (account_id, project, updated_at);

CREATE TABLE copilot_entries (
    id               TEXT COLLATE "C" PRIMARY KEY,
    conversation_id  TEXT COLLATE "C" NOT NULL REFERENCES copilot_conversations(id) ON DELETE CASCADE,
    seq              BIGINT NOT NULL,
    kind             TEXT NOT NULL,
    source           TEXT NOT NULL DEFAULT '',
    text             TEXT NOT NULL DEFAULT '',
    tool_name        TEXT NOT NULL DEFAULT '',
    tool_input       TEXT NOT NULL DEFAULT '',
    tool_output      TEXT NOT NULL DEFAULT '',
    tool_status      TEXT NOT NULL DEFAULT '',
    message          TEXT NOT NULL DEFAULT '',
    created_at       BIGINT NOT NULL,
    UNIQUE (conversation_id, seq)
);
`,
	},
	{
		name: "0008_agent_network",
		stmt: `
CREATE TABLE agent_messages (
    id              TEXT COLLATE "C" PRIMARY KEY,
    project         TEXT NOT NULL,
    sender_actor    TEXT NOT NULL,
    sender_session  TEXT NOT NULL DEFAULT '',
    sender_agent    TEXT NOT NULL DEFAULT '',
    recipient       TEXT NOT NULL,
    reply_to        TEXT NOT NULL DEFAULT '',
    thread_id       TEXT NOT NULL DEFAULT '',
    node_id         TEXT NOT NULL DEFAULT '',
    body            TEXT NOT NULL,
    created_at      BIGINT NOT NULL,
    read_at         BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX agent_messages_inbox ON agent_messages (project, recipient, read_at, created_at);
CREATE INDEX agent_messages_project ON agent_messages (project, created_at);

CREATE TABLE agent_asks (
    id            TEXT COLLATE "C" PRIMARY KEY,
    project       TEXT NOT NULL,
    session_id    TEXT NOT NULL,
    session_name  TEXT NOT NULL,
    actor_id      TEXT NOT NULL,
    agent         TEXT NOT NULL DEFAULT '',
    question      TEXT NOT NULL,
    about_msg_id  TEXT NOT NULL DEFAULT '',
    node_id       TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'denied', 'expired')),
    decided_by    TEXT NOT NULL DEFAULT '',
    note          TEXT NOT NULL DEFAULT '',
    created_at    BIGINT NOT NULL,
    decided_at    BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX agent_asks_project ON agent_asks (project, status, created_at);
`,
	},
	{
		name: "0009_runners",
		stmt: `
CREATE TABLE runners (
    id            TEXT COLLATE "C" PRIMARY KEY,
    owner_id      TEXT NOT NULL,
    owner_name    TEXT NOT NULL DEFAULT '',
    name          TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    last_seen_at  TEXT NOT NULL DEFAULT '',
    info          TEXT NOT NULL DEFAULT '',
    revoked_at    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX runners_owner ON runners (owner_id, created_at);

CREATE TABLE runner_tokens (
    token_hash  TEXT COLLATE "C" PRIMARY KEY,
    runner_id   TEXT NOT NULL REFERENCES runners(id),
    created_at  TEXT NOT NULL,
    revoked_at  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX runner_tokens_runner ON runner_tokens (runner_id);
`,
	},
	{
		name: "0010_runs",
		stmt: `
CREATE TABLE runs (
    id               TEXT COLLATE "C" PRIMARY KEY,
    project          TEXT COLLATE "C" NOT NULL,
    node_id          TEXT COLLATE "C" NOT NULL,
    runner_id        TEXT COLLATE "C" NOT NULL,
    runner_name      TEXT NOT NULL DEFAULT '',
    owner_id         TEXT COLLATE "C" NOT NULL,
    agent            TEXT NOT NULL,
    model            TEXT NOT NULL DEFAULT '',
    repo_id          TEXT NOT NULL,
    repo_name        TEXT NOT NULL DEFAULT '',
    base             TEXT NOT NULL DEFAULT '',
    branch           TEXT NOT NULL DEFAULT '',
    prompt           TEXT NOT NULL DEFAULT '',
    node_title       TEXT NOT NULL DEFAULT '',
    job_prompt       TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL CHECK (status IN ('queued', 'offered', 'declined', 'running', 'succeeded', 'failed', 'cancelled')),
    error            TEXT NOT NULL DEFAULT '',
    summary          TEXT NOT NULL DEFAULT '',
    exit_code        INTEGER NOT NULL DEFAULT 0,
    usage            TEXT NOT NULL DEFAULT '',
    pr_url           TEXT NOT NULL DEFAULT '',
    ci_status        TEXT NOT NULL DEFAULT '',
    created_by       TEXT COLLATE "C" NOT NULL,
    created_by_name  TEXT NOT NULL DEFAULT '',
    created_at       TEXT COLLATE "C" NOT NULL,
    started_at       TEXT COLLATE "C" NOT NULL DEFAULT '',
    finished_at      TEXT COLLATE "C" NOT NULL DEFAULT ''
);
CREATE INDEX runs_node ON runs (project, node_id, created_at);
CREATE INDEX runs_runner ON runs (runner_id, status);

CREATE TABLE run_events (
    run_id   TEXT COLLATE "C" NOT NULL REFERENCES runs(id),
    seq      BIGINT NOT NULL,
    type     TEXT NOT NULL,
    at       TEXT COLLATE "C" NOT NULL DEFAULT '',
    payload  TEXT NOT NULL,
    PRIMARY KEY (run_id, seq)
);
`,
	},
	{
		name: "0011_agent_network_w15",
		stmt: `
ALTER TABLE agent_messages ADD COLUMN status TEXT NOT NULL DEFAULT 'delivered' CHECK (status IN ('delivered', 'held', 'rejected'));
ALTER TABLE agent_messages ADD COLUMN notify INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agent_messages ADD COLUMN recipient_actor TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_messages ADD COLUMN decided_by TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_messages ADD COLUMN decided_at BIGINT NOT NULL DEFAULT 0;
CREATE INDEX agent_messages_status ON agent_messages (project, status, created_at);

CREATE TABLE agent_message_files (
    message_id  TEXT COLLATE "C" NOT NULL REFERENCES agent_messages(id),
    name        TEXT COLLATE "C" NOT NULL,
    size        BIGINT NOT NULL,
    sha256      TEXT COLLATE "C" NOT NULL,
    content     TEXT NOT NULL,
    PRIMARY KEY (message_id, name)
);
`,
	},
	{
		name: "0012_run_changes",
		stmt: `
ALTER TABLE runs ADD COLUMN commit_sha    TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN files_changed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN additions     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN deletions     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN pr_number     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN pr_state      TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN pr_error      TEXT NOT NULL DEFAULT '';

CREATE TABLE run_changes (
    run_id          TEXT COLLATE "C" PRIMARY KEY REFERENCES runs(id),
    commit_sha      TEXT COLLATE "C" NOT NULL DEFAULT '',
    base            TEXT NOT NULL DEFAULT '',
    files           TEXT NOT NULL DEFAULT '[]',
    additions       INTEGER NOT NULL DEFAULT 0,
    deletions       INTEGER NOT NULL DEFAULT 0,
    patch           TEXT NOT NULL DEFAULT '',
    patch_truncated INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT COLLATE "C" NOT NULL
);
`,
	},
	{
		name: "0013_run_merges_budgets",
		stmt: `
ALTER TABLE runs ADD COLUMN pr_head_sha TEXT COLLATE "C" NOT NULL DEFAULT '';
CREATE INDEX runs_owner_created ON runs (owner_id, created_at);

CREATE TABLE run_merges (
    id                TEXT COLLATE "C" PRIMARY KEY,
    run_id            TEXT COLLATE "C" NOT NULL REFERENCES runs(id),
    head_sha          TEXT COLLATE "C" NOT NULL,
    status            TEXT NOT NULL CHECK (status IN ('pending', 'merged', 'failed', 'expired')),
    reason            TEXT NOT NULL DEFAULT '',
    requested_by      TEXT COLLATE "C" NOT NULL,
    requested_by_name TEXT NOT NULL DEFAULT '',
    created_at        TEXT COLLATE "C" NOT NULL,
    finished_at       TEXT COLLATE "C" NOT NULL DEFAULT ''
);
CREATE INDEX run_merges_run ON run_merges (run_id, created_at);
CREATE UNIQUE INDEX run_merges_one_pending ON run_merges (run_id) WHERE status = 'pending';

CREATE TABLE run_budgets (
    owner_id        TEXT COLLATE "C" PRIMARY KEY,
    daily_runs      INTEGER NOT NULL DEFAULT 0,
    daily_cost_usd  DOUBLE PRECISION NOT NULL DEFAULT 0,
    daily_tokens    BIGINT NOT NULL DEFAULT 0,
    updated_at      TEXT COLLATE "C" NOT NULL
);
`,
	},
	{
		name: "0014_run_routing",
		stmt: `
ALTER TABLE runs ADD COLUMN kind         TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN failure_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN verdict      TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN retry_of     TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN pipeline_id  TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN fallbacks    TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN job_branch   TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN job_pr_url   TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN review       TEXT NOT NULL DEFAULT '';
CREATE INDEX runs_pipeline ON runs (pipeline_id, created_at);

CREATE TABLE run_routes (
    owner_id    TEXT COLLATE "C" NOT NULL,
    position    INTEGER NOT NULL,
    rule        TEXT NOT NULL,
    updated_at  TEXT COLLATE "C" NOT NULL,
    PRIMARY KEY (owner_id, position)
);

CREATE TABLE run_pipelines (
    id          TEXT COLLATE "C" PRIMARY KEY,
    owner_id    TEXT COLLATE "C" NOT NULL,
    created_by  TEXT COLLATE "C" NOT NULL,
    project     TEXT COLLATE "C" NOT NULL,
    node_id     TEXT COLLATE "C" NOT NULL,
    pipeline    TEXT NOT NULL,
    fallbacks   TEXT NOT NULL DEFAULT '',
    job_prompt  TEXT NOT NULL DEFAULT '',
    prompt      TEXT NOT NULL DEFAULT '',
    branch      TEXT NOT NULL DEFAULT '',
    pr_url      TEXT NOT NULL DEFAULT '',
    round       INTEGER NOT NULL DEFAULT 1,
    status      TEXT NOT NULL CHECK (status IN ('running', 'approved', 'changes_left', 'failed', 'cancelled')),
    reason      TEXT NOT NULL DEFAULT '',
    created_at  TEXT COLLATE "C" NOT NULL,
    updated_at  TEXT COLLATE "C" NOT NULL
);
CREATE INDEX run_pipelines_node ON run_pipelines (project, node_id, created_at);
`,
	},
	{
		name: "0015_run_mode",
		stmt: `
ALTER TABLE runs ADD COLUMN mode TEXT NOT NULL DEFAULT '' CHECK (mode IN ('', 'interactive'));
`,
	},
	{
		name: "0016_w6",
		stmt: `
ALTER TABLE sessions ADD COLUMN stepped_up_at TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN stepped_up_at TEXT COLLATE "C" NOT NULL DEFAULT '';

CREATE TABLE session_changes (
    id            TEXT COLLATE "C" PRIMARY KEY,
    project       TEXT COLLATE "C" NOT NULL,
    node_id       TEXT COLLATE "C" NOT NULL,
    session_id    TEXT COLLATE "C" NOT NULL,
    session_name  TEXT NOT NULL DEFAULT '',
    agent         TEXT NOT NULL DEFAULT '',
    actor_id      TEXT COLLATE "C" NOT NULL DEFAULT '',
    repo          TEXT NOT NULL DEFAULT '',
    branch        TEXT NOT NULL DEFAULT '',
    files         TEXT NOT NULL DEFAULT '[]',
    snapshot      TEXT NOT NULL DEFAULT '',
    created_at    TEXT COLLATE "C" NOT NULL,
    updated_at    TEXT COLLATE "C" NOT NULL
);
CREATE UNIQUE INDEX session_changes_key ON session_changes (project, node_id, session_id);
CREATE INDEX session_changes_node ON session_changes (project, node_id, updated_at);
`,
	},
	{
		name: "0017_usage",
		stmt: `
CREATE TABLE agent_session_usage (
    project            TEXT COLLATE "C" NOT NULL,
    session_id         TEXT COLLATE "C" NOT NULL,
    model              TEXT COLLATE "C" NOT NULL,
    actor_id           TEXT COLLATE "C" NOT NULL DEFAULT '',
    actor_name         TEXT NOT NULL DEFAULT '',
    session_name       TEXT NOT NULL DEFAULT '',
    agent              TEXT NOT NULL DEFAULT '',
    input_tokens       BIGINT NOT NULL DEFAULT 0,
    output_tokens      BIGINT NOT NULL DEFAULT 0,
    cache_read_tokens  BIGINT NOT NULL DEFAULT 0,
    cache_write_tokens BIGINT NOT NULL DEFAULT 0,
    reported_cost_usd  DOUBLE PRECISION NOT NULL DEFAULT 0,
    first_at           TEXT COLLATE "C" NOT NULL,
    updated_at         TEXT COLLATE "C" NOT NULL,
    PRIMARY KEY (project, session_id, model)
);
CREATE INDEX agent_session_usage_updated ON agent_session_usage (project, updated_at);
CREATE INDEX runs_project_created ON runs (project, created_at);

CREATE TABLE usage_prices (
    model                 TEXT COLLATE "C" PRIMARY KEY,
    input_per_mtok        DOUBLE PRECISION NOT NULL DEFAULT 0,
    output_per_mtok       DOUBLE PRECISION NOT NULL DEFAULT 0,
    cache_read_per_mtok   DOUBLE PRECISION NOT NULL DEFAULT 0,
    cache_write_per_mtok  DOUBLE PRECISION NOT NULL DEFAULT 0,
    updated_at            TEXT COLLATE "C" NOT NULL,
    updated_by            TEXT NOT NULL DEFAULT ''
);
`,
	},
	{
		name: "0018_project_members",
		stmt: `
CREATE TABLE project_members (
    project    TEXT COLLATE "C" NOT NULL,
    user_id    TEXT COLLATE "C" NOT NULL,
    role       TEXT NOT NULL CHECK (role IN ('owner', 'editor', 'viewer')),
    added_by   TEXT NOT NULL DEFAULT '',
    created_at TEXT COLLATE "C" NOT NULL,
    PRIMARY KEY (project, user_id)
);
CREATE INDEX project_members_user ON project_members (user_id);
`,
	},
	{
		name: "0019_autopilot",
		stmt: `
CREATE TABLE autopilot_runs (
    id              TEXT COLLATE "C" PRIMARY KEY,
    project         TEXT COLLATE "C" NOT NULL,
    goal_node_id    TEXT COLLATE "C" NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('running', 'paused', 'succeeded', 'failed', 'cancelled')),
    policy          TEXT NOT NULL,
    reason          TEXT NOT NULL DEFAULT '',
    created_by      TEXT COLLATE "C" NOT NULL,
    created_by_name TEXT NOT NULL DEFAULT '',
    stepped_up_at   TEXT COLLATE "C" NOT NULL DEFAULT '',
    created_at      TEXT COLLATE "C" NOT NULL,
    updated_at      TEXT COLLATE "C" NOT NULL,
    finished_at     TEXT COLLATE "C" NOT NULL DEFAULT ''
);
CREATE INDEX autopilot_runs_goal ON autopilot_runs (project, goal_node_id, created_at);
CREATE UNIQUE INDEX autopilot_runs_live ON autopilot_runs (project, goal_node_id)
    WHERE status IN ('running', 'paused');

CREATE TABLE autopilot_tasks (
    autopilot_id TEXT COLLATE "C" NOT NULL REFERENCES autopilot_runs(id),
    node_id      TEXT COLLATE "C" NOT NULL,
    state        TEXT NOT NULL,
    run_id       TEXT COLLATE "C" NOT NULL DEFAULT '',
    pipeline_id  TEXT COLLATE "C" NOT NULL DEFAULT '',
    data         TEXT NOT NULL,
    updated_at   TEXT COLLATE "C" NOT NULL,
    PRIMARY KEY (autopilot_id, node_id)
);
CREATE INDEX autopilot_tasks_run ON autopilot_tasks (run_id);
CREATE INDEX autopilot_tasks_pipeline ON autopilot_tasks (pipeline_id);
`,
	},
	{
		name: "0020_review_votes",
		stmt: `
ALTER TABLE run_pipelines ADD COLUMN awaiting TEXT NOT NULL DEFAULT '';

CREATE TABLE review_votes (
    pipeline_id TEXT COLLATE "C" NOT NULL,
    round       INTEGER NOT NULL,
    reviewer    TEXT COLLATE "C" NOT NULL,
    agent       TEXT NOT NULL DEFAULT '',
    model       TEXT NOT NULL DEFAULT '',
    verdict     TEXT NOT NULL DEFAULT '' CHECK (verdict IN ('', 'approve', 'changes', 'abstain')),
    run_id      TEXT COLLATE "C" NOT NULL DEFAULT '',
    comment     TEXT NOT NULL DEFAULT '',
    actor_id    TEXT COLLATE "C" NOT NULL DEFAULT '',
    created_at  TEXT COLLATE "C" NOT NULL,
    updated_at  TEXT COLLATE "C" NOT NULL,
    PRIMARY KEY (pipeline_id, round, reviewer)
);
CREATE INDEX review_votes_run ON review_votes (run_id);
`,
	},
	{
		name: "0021_account_totp",
		stmt: `
CREATE TABLE account_totp (
    user_id      TEXT COLLATE "C" PRIMARY KEY,
    secret       TEXT NOT NULL,
    created_at   TEXT COLLATE "C" NOT NULL,
    enabled_at   TEXT COLLATE "C" NOT NULL DEFAULT '',
    last_counter BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE account_recovery_codes (
    user_id    TEXT COLLATE "C" NOT NULL,
    code_hash  TEXT COLLATE "C" NOT NULL,
    created_at TEXT COLLATE "C" NOT NULL,
    used_at    TEXT COLLATE "C" NOT NULL DEFAULT '',
    PRIMARY KEY (user_id, code_hash)
);
`,
	},
	{
		name: "0022_copilot_followups",
		stmt: `
CREATE TABLE copilot_followups (
    id               TEXT COLLATE "C" PRIMARY KEY,
    conversation_id  TEXT COLLATE "C" NOT NULL REFERENCES copilot_conversations(id) ON DELETE CASCADE,
    account_id       TEXT NOT NULL,
    goal_node_id     TEXT NOT NULL,
    run_id           TEXT COLLATE "C" NOT NULL,
    language         TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL DEFAULT 'pending',
    seen             BIGINT NOT NULL DEFAULT 0,
    created_at       BIGINT NOT NULL,
    delivered_at     BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX copilot_followups_run ON copilot_followups (run_id, status);
CREATE INDEX copilot_followups_conversation ON copilot_followups (conversation_id, status);
`,
	},
	{
		name: "0023_copilot_followup_revisions",
		stmt: `
ALTER TABLE copilot_followups ADD COLUMN kind TEXT NOT NULL DEFAULT 'breakdown';
ALTER TABLE copilot_followups ADD COLUMN revision TEXT NOT NULL DEFAULT '';
`,
	},
	{
		name: "0024_run_handoff",
		stmt: `
ALTER TABLE runs ADD COLUMN handoff_branch     TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN handoff_note       TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN job_handoff_branch TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN job_handoff_note   TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN retry_at           TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN limit_waits        INTEGER NOT NULL DEFAULT 0;
CREATE INDEX runs_retry_at ON runs (retry_at) WHERE retry_at <> '';
`,
	},
	{
		name: "0025_agent_roles",
		stmt: `
ALTER TABLE runs ADD COLUMN role     TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN verify   TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN checks   TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN override TEXT NOT NULL DEFAULT '';
-- What a role's job is offered besides the prompt (tools, the Tester's
-- files, acceptance items, a verify spec), as JSON; '' for a legacy run.
ALTER TABLE runs ADD COLUMN job_roles TEXT NOT NULL DEFAULT '';
CREATE INDEX runs_owner_role ON runs (owner_id, role, status);
ALTER TABLE run_pipelines ADD COLUMN stages     TEXT NOT NULL DEFAULT '';
ALTER TABLE run_pipelines ADD COLUMN phase      TEXT NOT NULL DEFAULT '';
ALTER TABLE run_pipelines ADD COLUMN test_files TEXT NOT NULL DEFAULT '';
ALTER TABLE run_pipelines ADD COLUMN guard      TEXT NOT NULL DEFAULT '';
ALTER TABLE run_pipelines ADD COLUMN tries      TEXT NOT NULL DEFAULT '';
ALTER TABLE autopilot_runs ADD COLUMN stages     TEXT NOT NULL DEFAULT '';
ALTER TABLE autopilot_runs ADD COLUMN goal_phase TEXT NOT NULL DEFAULT '';
ALTER TABLE autopilot_runs ADD COLUMN qa_round   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE autopilot_runs ADD COLUMN qa_run_id  TEXT NOT NULL DEFAULT '';
CREATE TABLE run_role_settings (
    owner_id       TEXT COLLATE "C" NOT NULL,
    project        TEXT COLLATE "C" NOT NULL DEFAULT '',
    role           TEXT COLLATE "C" NOT NULL,
    max_concurrent INTEGER NOT NULL DEFAULT 0,
    daily_cost_usd DOUBLE PRECISION NOT NULL DEFAULT 0,
    daily_tokens   BIGINT NOT NULL DEFAULT 0,
    daily_runs     INTEGER NOT NULL DEFAULT 0,
    updated_at     TEXT COLLATE "C" NOT NULL,
    PRIMARY KEY (owner_id, project, role)
);
`,
	},
	{
		// The search index, which lived in a SQLite FTS5 table until the
		// database became PostgreSQL only. It is derived from the Markdown and
		// rebuilt lazily, so an upgraded server starts with it empty. The text
		// must stay the same objects as searchindex.Schema, which recreates a
		// dropped table (search_schema_test.go compares them).
		name: "0026_search_index",
		stmt: SearchIndexSchema,
	},
	{
		// One row per legacy SQLite file internal/legacysqlite imported (or was
		// told to skip), so the import happens once and a rename it could not
		// finish is retried.
		name: "0027_legacy_imports",
		stmt: `
CREATE TABLE legacy_imports (
    source        TEXT COLLATE "C" PRIMARY KEY,
    source_sha256 TEXT COLLATE "C" NOT NULL DEFAULT '',
    status        TEXT COLLATE "C" NOT NULL DEFAULT 'imported' CHECK (status IN ('imported', 'skipped')),
    imported_at   TEXT COLLATE "C" NOT NULL,
    counts        JSONB NOT NULL DEFAULT '{}'
);
`,
	},
	{
		name: "0028_agent_analytics",
		stmt: `
ALTER TABLE runs ADD COLUMN cost_usd         DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN cost_estimated   BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE runs ADD COLUMN cost_settled     BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE runs ADD COLUMN node_type        TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN acceptance_count INTEGER NOT NULL DEFAULT 0;
CREATE INDEX runs_project_role_created ON runs (project, role, created_at);
CREATE INDEX runs_retry_of ON runs (retry_of) WHERE retry_of <> '';
CREATE INDEX runs_finished ON runs (finished_at) WHERE finished_at <> '';

ALTER TABLE agent_session_usage ADD COLUMN run_id TEXT COLLATE "C" NOT NULL DEFAULT '';

CREATE TABLE copilot_usage (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    conversation_id TEXT COLLATE "C" NOT NULL,
    account_id      TEXT COLLATE "C" NOT NULL,
    project         TEXT COLLATE "C" NOT NULL,
    provider        TEXT NOT NULL DEFAULT '',
    model           TEXT NOT NULL DEFAULT '',
    input_tokens    BIGINT NOT NULL DEFAULT 0,
    output_tokens   BIGINT NOT NULL DEFAULT 0,
    created_at      TEXT COLLATE "C" NOT NULL
);
CREATE INDEX copilot_usage_project ON copilot_usage (project, created_at);

CREATE TABLE agent_session_actions (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project    TEXT COLLATE "C" NOT NULL,
    session_id TEXT COLLATE "C" NOT NULL,
    run_id     TEXT COLLATE "C" NOT NULL DEFAULT '',
    at         TEXT COLLATE "C" NOT NULL,
    action     TEXT NOT NULL CHECK (action IN ('joined','left','doing','message','ask','changes','usage')),
    node_id    TEXT COLLATE "C" NOT NULL DEFAULT '',
    detail     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX agent_session_actions_session ON agent_session_actions (project, session_id, at);
CREATE INDEX agent_session_actions_at ON agent_session_actions (at);

CREATE TABLE run_evidence (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id      TEXT COLLATE "C" NOT NULL,
    pipeline_id TEXT COLLATE "C" NOT NULL DEFAULT '',
    project     TEXT COLLATE "C" NOT NULL,
    node_id     TEXT COLLATE "C" NOT NULL,
    kind        TEXT NOT NULL,
    outcome     TEXT NOT NULL DEFAULT 'info' CHECK (outcome IN ('pass','fail','skip','info')),
    summary     TEXT NOT NULL DEFAULT '',
    at          TEXT COLLATE "C" NOT NULL
);
CREATE INDEX run_evidence_run ON run_evidence (run_id);
CREATE INDEX run_evidence_pipeline ON run_evidence (pipeline_id, at);
CREATE INDEX run_evidence_node ON run_evidence (project, node_id, at);

CREATE TABLE autopilot_qa_rounds (
    autopilot_id TEXT COLLATE "C" NOT NULL,
    round        INTEGER NOT NULL,
    run_id       TEXT COLLATE "C" NOT NULL DEFAULT '',
    outcome      TEXT NOT NULL CHECK (outcome IN ('pass','fail','none')),
    fix_tasks    INTEGER NOT NULL DEFAULT 0,
    reason       TEXT NOT NULL DEFAULT '',
    created_at   TEXT COLLATE "C" NOT NULL,
    PRIMARY KEY (autopilot_id, round)
);

CREATE TABLE run_verdicts (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id         TEXT COLLATE "C" NOT NULL UNIQUE REFERENCES runs(id),
    project        TEXT COLLATE "C" NOT NULL,
    node_id        TEXT COLLATE "C" NOT NULL,
    pipeline_id    TEXT COLLATE "C" NOT NULL DEFAULT '',
    role           TEXT COLLATE "C" NOT NULL CHECK (role IN ('reviewer','qa')),
    round          INTEGER NOT NULL DEFAULT 1,
    subject_run_id TEXT COLLATE "C" NOT NULL DEFAULT '',
    agent          TEXT NOT NULL DEFAULT '',
    model          TEXT NOT NULL DEFAULT '',
    outcome        TEXT NOT NULL CHECK (outcome IN ('pass','fail')),
    severity       TEXT NOT NULL CHECK (severity IN ('none','low','medium','high','critical')),
    reasons        TEXT NOT NULL DEFAULT '[]',
    note           TEXT NOT NULL DEFAULT '',
    source         TEXT NOT NULL DEFAULT 'mcp' CHECK (source IN ('mcp','finish')),
    created_at     TEXT COLLATE "C" NOT NULL,
    updated_at     TEXT COLLATE "C" NOT NULL
);
CREATE INDEX run_verdicts_node ON run_verdicts (project, node_id, created_at);
CREATE INDEX run_verdicts_pipeline ON run_verdicts (pipeline_id, round);

CREATE TABLE analytics_settings (
    owner_id        TEXT COLLATE "C" NOT NULL,
    project         TEXT COLLATE "C" NOT NULL DEFAULT '',
    auto_routing    BOOLEAN NOT NULL DEFAULT false,
    exploration_pct INTEGER NOT NULL DEFAULT 10 CHECK (exploration_pct BETWEEN 0 AND 50),
    required_tiers  TEXT NOT NULL DEFAULT '{}',
    updated_at      TEXT COLLATE "C" NOT NULL,
    PRIMARY KEY (owner_id, project)
);

CREATE TABLE route_picks (
    id            TEXT COLLATE "C" PRIMARY KEY,
    owner_id      TEXT COLLATE "C" NOT NULL,
    project       TEXT COLLATE "C" NOT NULL,
    node_id       TEXT COLLATE "C" NOT NULL,
    role          TEXT COLLATE "C" NOT NULL DEFAULT '',
    source        TEXT NOT NULL CHECK (source IN ('manual','auto','none')),
    rule_id       TEXT NOT NULL DEFAULT '',
    runner_id     TEXT COLLATE "C" NOT NULL DEFAULT '',
    agent         TEXT NOT NULL DEFAULT '',
    model         TEXT NOT NULL DEFAULT '',
    tier          TEXT NOT NULL DEFAULT '',
    required_tier TEXT NOT NULL DEFAULT '',
    explore       BOOLEAN NOT NULL DEFAULT false,
    reason        TEXT NOT NULL,
    run_id        TEXT COLLATE "C" NOT NULL DEFAULT '',
    created_at    TEXT COLLATE "C" NOT NULL
);
CREATE INDEX route_picks_project ON route_picks (project, created_at);
CREATE INDEX route_picks_at ON route_picks (created_at);

CREATE TABLE analytics_dismissed (
    owner_id     TEXT COLLATE "C" NOT NULL,
    key          TEXT COLLATE "C" NOT NULL,
    dismissed_at TEXT COLLATE "C" NOT NULL,
    PRIMARY KEY (owner_id, key)
);
`,
	},
	{
		// The search index's GIN indexes without a pending list. See
		// SearchIndexTuning.
		name: "0029_search_gin_fastupdate",
		stmt: SearchIndexTuning,
	},
	{
		// An ask's kind (approval, question, choice, permission), a
		// choice's options (a JSON array), the person's text answer, the
		// run it came from, a permission prompt's tool and input, and when
		// it expires. Rows from before read as approvals; "answered" is the
		// status of a question or choice a person answered. Every statement
		// tolerates a rerun.
		name: "0030_agent_ask_kinds",
		stmt: `
ALTER TABLE agent_asks ADD COLUMN IF NOT EXISTS kind       TEXT NOT NULL DEFAULT 'approval';
ALTER TABLE agent_asks ADD COLUMN IF NOT EXISTS options    TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_asks ADD COLUMN IF NOT EXISTS answer     TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_asks ADD COLUMN IF NOT EXISTS run_id     TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE agent_asks ADD COLUMN IF NOT EXISTS tool       TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_asks ADD COLUMN IF NOT EXISTS tool_input TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_asks ADD COLUMN IF NOT EXISTS expires_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE agent_asks DROP CONSTRAINT IF EXISTS agent_asks_kind_check;
ALTER TABLE agent_asks ADD CONSTRAINT agent_asks_kind_check CHECK (kind IN ('approval', 'question', 'choice', 'permission'));
ALTER TABLE agent_asks DROP CONSTRAINT IF EXISTS agent_asks_status_check;
ALTER TABLE agent_asks ADD CONSTRAINT agent_asks_status_check CHECK (status IN ('pending', 'approved', 'denied', 'answered', 'expired'));
CREATE INDEX IF NOT EXISTS agent_asks_run ON agent_asks (project, run_id, tool, status) WHERE run_id != '';
`,
	},
	{
		// What allowing a permission prompt for the rest of its run covers:
		// '' the tool, 'cmd:<hash>' one shell command, 'once:<id>' nothing
		// more (agents.Ask.RunScope). Rows from before allowed their tool.
		name: "0031_agent_ask_run_key",
		stmt: `
ALTER TABLE agent_asks ADD COLUMN IF NOT EXISTS run_key TEXT COLLATE "C" NOT NULL DEFAULT '';
`,
	},
	{
		// Whether a permission prompt's tool input was cut to fit, by the
		// hub or by whoever relayed it (agents.Ask.ToolInputTruncated). Rows
		// from before read as whole.
		name: "0032_agent_ask_input_truncated",
		stmt: `
ALTER TABLE agent_asks ADD COLUMN IF NOT EXISTS tool_input_truncated BOOLEAN NOT NULL DEFAULT FALSE;
`,
	},
}

// SearchIndexSchema is migration 0026: the search index's table, as
// internal/searchindex defines it (searchindex.Schema).
const SearchIndexSchema = `
CREATE TABLE IF NOT EXISTS search_documents (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project     TEXT COLLATE "C" NOT NULL,
    node_id     TEXT COLLATE "C" NOT NULL,
    path        TEXT COLLATE "C" NOT NULL DEFAULT '',
    title       TEXT NOT NULL DEFAULT '',
    modified_at TEXT COLLATE "C" NOT NULL DEFAULT '',
    meta        TEXT NOT NULL DEFAULT '',
    body        TEXT NOT NULL DEFAULT '',
    meta_tokens TSVECTOR NOT NULL,
    body_tokens TSVECTOR NOT NULL,
    token_count INTEGER NOT NULL,
    oversize    BOOLEAN NOT NULL DEFAULT false,
    UNIQUE (project, node_id, path)
);
CREATE INDEX IF NOT EXISTS search_documents_meta_tokens ON search_documents USING gin (meta_tokens);
CREATE INDEX IF NOT EXISTS search_documents_body_tokens ON search_documents USING gin (body_tokens);
CREATE INDEX IF NOT EXISTS search_documents_oversize ON search_documents (project) WHERE oversize;
`

// SearchIndexTuning is migration 0029, and what internal/searchindex runs
// after recreating a dropped table (searchindex.SchemaTuning): the two GIN
// indexes with fastupdate off, and whatever an older server left in their
// pending lists merged into the index proper.
//
// With fastupdate on (PostgreSQL's default) new entries go to an unsorted
// pending list that every probe scans in full and that is merged only by
// (auto)vacuum or when it outgrows gin_pending_list_limit. A bulk reindex of
// a few thousand documents leaves the list large, the planner prices each
// index probe off it, and the ranked query flips to a sequential scan that
// reads every tsvector: seconds instead of milliseconds on 5,000 documents.
// Writes to this table are batched and asynchronous already, so the cheaper
// insert the pending list buys is not worth it.
//
// Every statement tolerates the table being gone (an operator may drop it;
// the search index recreates it on first use). Setting a GIN index's
// fastupdate takes an exclusive lock on it, which a migration can afford.
const SearchIndexTuning = `
ALTER INDEX IF EXISTS search_documents_meta_tokens SET (fastupdate = off);
ALTER INDEX IF EXISTS search_documents_body_tokens SET (fastupdate = off);
SELECT gin_clean_pending_list(v.idx)
FROM (VALUES (to_regclass('search_documents_meta_tokens')), (to_regclass('search_documents_body_tokens'))) AS v(idx)
WHERE v.idx IS NOT NULL;
`

// pgMigrationLock is the advisory lock key migrations hold, so a CLI command
// and a server starting at the same moment do not both apply a step.
const pgMigrationLock = 0x6e6f6465 // "node"
