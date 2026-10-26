package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

// URLEnv names the variable that moves the workspace's operational data from
// the SQLite file to a PostgreSQL server. It is an environment variable and
// not a nodevas.yaml key for the same reason the SMTP password is: the URL
// carries a password, and nodevas.yaml sits in the workspace beside the notes.
const URLEnv = "NODEVAS_DATABASE_URL"

// URLFileEnv names a file holding the URL instead, for a container that gets
// its secrets as mounted files, as NODEVAS_SMTP_PASSWORD_FILE does.
const URLFileEnv = URLEnv + "_FILE"

// URLFromEnv returns the configured PostgreSQL URL, or "" for none. URLEnv
// wins over URLFileEnv when both are set.
func URLFromEnv(lookup func(string) (string, bool)) (string, error) {
	if raw, _ := lookup(URLEnv); strings.TrimSpace(raw) != "" {
		return strings.TrimSpace(raw), nil
	}
	path, _ := lookup(URLFileEnv)
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	data, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return "", fmt.Errorf("%s: %w", URLFileEnv, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// WaitEnv bounds how long Open keeps retrying an unreachable server before it
// falls back to the SQLite file, as a Go duration ("30s", "2m"). On a NAS that
// has just booted the database container usually comes up after this one, and
// giving up at the first refused connection would leave the server on the
// fallback until somebody restarts it.
const WaitEnv = "NODEVAS_DATABASE_WAIT"

// defaultWait is WaitEnv's value when it is unset.
const defaultWait = 30 * time.Second

// Backend names the engine a DB talks to.
type Backend string

const (
	SQLite   Backend = "sqlite"
	Postgres Backend = "postgres"
)

// ErrUnreachable wraps a failure to reach the PostgreSQL server at all, as
// opposed to reaching it and being refused something. Only this kind falls
// back to SQLite: a server that answers and rejects the schema is a problem to
// surface, not one to route around.
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
// date, retrying an unreachable server for up to wait.
//
// The handles mirror the SQLite ones on purpose. The write pool is one
// connection, so the transactions that count and then insert ("the first
// account is an administrator", "the last administrator cannot be removed")
// stay serialised exactly as SQLite's single writer serialised them, rather
// than depending on an isolation level. The read pool is opened with
// default_transaction_read_only, the server-side counterpart of query_only.
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

	database := &DB{write: writer, read: reader, path: describeURL(writeConfig), backend: Postgres}
	if err := database.migrate(context.Background()); err != nil {
		database.Close()
		return nil, err
	}
	return database, nil
}

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
// constraint or index, on either engine. A non-empty name narrows it to that
// constraint: PostgreSQL reports the index name, SQLite prints the indexed
// columns or the index name depending on the index, so callers that care pass
// the index name and match the column form themselves.
func IsUniqueViolation(err error, name string) bool {
	if err == nil {
		return false
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code == "23505" && (name == "" || pgErr.ConstraintName == name)
	}
	message := err.Error()
	return strings.Contains(message, "UNIQUE constraint failed") &&
		(name == "" || strings.Contains(message, name))
}

// rebindConnector lets the SQL in this program keep SQLite's ? placeholders on
// PostgreSQL, which wants $1, $2, …, by rewriting each statement on its way to
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

// pgSchemaMigrations is the bookkeeping table, as schema_migrations is on
// SQLite.
const pgSchemaMigrations = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    name       TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL
)`

// pgMigrations is the PostgreSQL schema. It follows the SQLite one table for
// table, so the queries in the rest of the program run unchanged on either:
//
//   - nocase is an ICU collation that ignores case, so the queries' own
//     "COLLATE NOCASE" (folded to nocase by the parser) finds it by name.
//   - accounts keeps a rowid column, which is what "ORDER BY rowid" means on
//     SQLite: the order the rows were created in.
//   - Timestamp and hash columns use the C collation, so they compare and sort
//     byte by byte as they do on SQLite, whatever locale the server runs in.
//   - The search index has no counterpart here. It is derived from the
//     Markdown, built on SQLite's FTS5, and stays in the workspace's SQLite
//     file; see DB.Local.
//
// The names line up with the SQLite steps they mirror. A server starts from
// the current SQLite shape, so 0006's data repair has nothing to repair and
// only its index is created, in 0001.
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
}

// pgMigrationLock is the advisory lock key migrations hold on PostgreSQL, so a
// CLI command and a server starting at the same moment do not both apply a
// step. SQLite gets the same exclusion from its single writer.
const pgMigrationLock = 0x6e6f6465 // "node"
