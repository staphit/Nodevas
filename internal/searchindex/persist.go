// The index's storage: SQLite FTS5.
//
// Everything here is derived data. Every row can be rebuilt from the Markdown
// and YAML on disk, which is why nothing in this file reports a corrupt or
// missing table to the user: it wipes and rescans instead (see ensureSchema).
//
// Three tables, two of them created by migration 0004 in internal/db:
//
//   - search_documents — one row per node, the source-file bookkeeping. Its
//     rowid is the identity of a document everywhere else here.
//   - search_fts — the posting lists, contentless. Its columns hold the
//     *tokens* this package emits, space separated, never the prose; see
//     tokenize.go for why the corpus cannot be left to FTS5's own tokeniser.
//   - search_content — the prose, kept because the tokens cannot be turned back
//     into it and two things need it: the substring verification that makes
//     bigram over-matching invisible (see projectIndex.query) and the snippet
//     the caller cuts out of Match.Text.
//
// search_content is created here rather than in internal/db's migration list on
// purpose: a contentless FTS5 row can only be deleted by handing FTS5 the exact
// text that was indexed, so this table is part of how search_fts is written,
// not a separate concern, and it is recreated by the same code that recreates
// the other two when an operator has deleted them.

package searchindex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"

	"nodevas/internal/db"
)

// maxQueryTokens bounds the FTS5 expression a query is turned into.
//
// Tokens are only a candidate filter, and dropping some of them can only widen
// the candidate set — never lose a document — because the verification pass
// decides the result either way. So a pasted paragraph costs a bounded MATCH
// instead of a thousand-term boolean expression.
const maxQueryTokens = 16

// maxCandidates bounds how many documents one search pulls out of the
// database. See candidates for why a ceiling here cannot produce a wrong
// result.
const maxCandidates = 2000

// docStore is the package's view of the workspace database.
//
// It holds *sql.DB rather than *db.DB so a Manager built without a database can
// back itself with a private in-memory one (see NewManager); the schema below
// is created on demand, so the two are the same code path.
type docStore struct {
	// sql is the write handle; read is the pool queries go to. They are the
	// same handle for an in-memory store, and two for the workspace database,
	// where WAL lets a search run alongside a row being written instead of
	// queueing behind it. Searching is the hot path, so it must not wait on
	// whatever else the server happened to be writing.
	sql  *sql.DB
	read *sql.DB
	once sync.Once
	err  error
	// owned is closed by Manager.Close; a database passed in by the caller is
	// the caller's to close.
	owned bool
}

// storedDoc is one row of search_documents joined to its text.
type storedDoc struct {
	nodeID string
	title  string
	path   string
	stamp  string
	meta   string
	body   string
}

// text is the haystack as the old in-memory index held it: the graph metadata
// line, a newline, then the markdown and the text subpages.
func (d *storedDoc) text() string { return d.meta + "\n" + d.body }

// newDocStore wraps the workspace database.
func newDocStore(database *db.DB) *docStore {
	return &docStore{sql: database.Writer(), read: database.Reader()}
}

// newMemoryDocStore backs the index with a private in-memory database, so a
// Manager built without one still searches correctly and simply rebuilds its
// index in the next process. The driver is registered by internal/db.
func newMemoryDocStore() (*docStore, error) {
	handle, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("open in-memory index: %w", err)
	}
	// One connection, because closing the last one would drop the database.
	handle.SetMaxOpenConns(1)
	handle.SetMaxIdleConns(1)
	handle.SetConnMaxLifetime(0)
	if _, err := handle.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		handle.Close()
		return nil, fmt.Errorf("in-memory foreign keys: %w", err)
	}
	return &docStore{sql: handle, owned: true}, nil
}

// reader is the handle queries go to: the read pool when there is one, and the
// single handle otherwise. An in-memory store has nothing to gain from a
// second pool — closing its last connection would drop the database.
func (s *docStore) reader() *sql.DB {
	if s.read != nil {
		return s.read
	}
	return s.sql
}

func (s *docStore) close() error {
	if s == nil || !s.owned {
		return nil
	}
	return s.sql.Close()
}

// schema mirrors migration 0004 so that a table an operator dropped comes back.
// Dropping them is a supported thing to do — this is derived data — and the
// alternative is a workspace whose search stays broken after an operator
// drops the derived tables, which never happens because the schema step is
// already recorded.
const schema = `
CREATE TABLE IF NOT EXISTS search_documents (
    project     TEXT NOT NULL,
    node_id     TEXT NOT NULL,
    path        TEXT NOT NULL DEFAULT '',
    title       TEXT NOT NULL DEFAULT '',
    modified_at TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (project, node_id, path)
) STRICT;

CREATE VIRTUAL TABLE IF NOT EXISTS search_fts USING fts5 (
    title,
    body,
    content = '',
    tokenize = 'unicode61'
);

CREATE TABLE IF NOT EXISTS search_content (
    -- search_documents.rowid, which is also the search_fts rowid: one integer
    -- identifies a document in all three tables, and because the upsert in put
    -- keeps the primary key it keeps the rowid, so reindexing replaces a
    -- document instead of adding a second copy of it.
    doc_id  INTEGER PRIMARY KEY,
    project TEXT NOT NULL,
    node_id TEXT NOT NULL,
    path    TEXT NOT NULL,
    meta    TEXT NOT NULL,
    body    TEXT NOT NULL,
    FOREIGN KEY (project, node_id, path) REFERENCES search_documents (project, node_id, path) ON DELETE CASCADE
) STRICT;
CREATE INDEX IF NOT EXISTS search_content_project ON search_content (project, node_id);
`

// ensure creates the schema and checks that the three tables still agree,
// once per store.
func (s *docStore) ensure(ctx context.Context) error {
	s.once.Do(func() {
		if _, err := s.sql.ExecContext(ctx, schema); err != nil {
			s.err = fmt.Errorf("search schema: %w", err)
			return
		}
		s.err = s.repairLocked(ctx)
	})
	return s.err
}

// repairLocked wipes the index when the three tables disagree about how many
// documents exist.
//
// That happens when an operator empties or drops one of them, which they are
// entitled to do. The count is the only check available: a contentless FTS5
// table cannot be scanned, so its row count has to come from the shadow table
// that records document sizes, and if that read fails the check is simply
// skipped rather than turned into an error nobody can act on.
func (s *docStore) repairLocked(ctx context.Context) error {
	var documents, content int
	if err := s.reader().QueryRowContext(ctx, `SELECT count(*) FROM search_documents`).Scan(&documents); err != nil {
		return fmt.Errorf("count search_documents: %w", err)
	}
	if err := s.reader().QueryRowContext(ctx, `SELECT count(*) FROM search_content`).Scan(&content); err != nil {
		return fmt.Errorf("count search_content: %w", err)
	}
	consistent := documents == content
	var indexed int
	if err := s.reader().QueryRowContext(ctx, `SELECT count(*) FROM search_fts_docsize`).Scan(&indexed); err == nil {
		consistent = consistent && indexed == documents
	}
	if consistent {
		return nil
	}
	return s.reset(ctx)
}

// reset empties the whole index, for every project, and is what a rescan is
// recovered through. It is all-or-nothing because a contentless FTS5 table can
// only be emptied wholesale: once its rows have lost the text they were built
// from, there is no way to delete just one project's.
func (s *docStore) reset(ctx context.Context) error {
	return withTx(ctx, s.sql, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO search_fts (search_fts) VALUES ('delete-all')`); err != nil {
			return fmt.Errorf("empty search_fts: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM search_content`); err != nil {
			return fmt.Errorf("empty search_content: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM search_documents`); err != nil {
			return fmt.Errorf("empty search_documents: %w", err)
		}
		return nil
	})
}

// withTx runs fn in a transaction, rolling back on error or panic. It is
// db.DB.Tx, repeated for the *sql.DB an in-memory store holds.
func withTx(ctx context.Context, handle *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := handle.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
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
		return fmt.Errorf("commit: %w", err)
	}
	committed = true
	return nil
}

// stored returns what the database already holds for a project, keyed by node,
// so a rescan can decide per node whether anything has to be read at all.
func (s *docStore) stored(ctx context.Context, project string) (map[string]*storedDoc, error) {
	rows, err := s.reader().QueryContext(ctx, `
SELECT d.node_id, d.title, d.path, d.modified_at, c.meta
FROM search_documents d
JOIN search_content c ON c.doc_id = d.rowid
WHERE d.project = ?`, project)
	if err != nil {
		return nil, fmt.Errorf("read search_documents: %w", err)
	}
	defer rows.Close()
	out := make(map[string]*storedDoc, 64)
	for rows.Next() {
		doc := &storedDoc{}
		if err := rows.Scan(&doc.nodeID, &doc.title, &doc.path, &doc.stamp, &doc.meta); err != nil {
			return nil, fmt.Errorf("scan search_documents: %w", err)
		}
		out[doc.nodeID] = doc
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read search_documents: %w", err)
	}
	return out, nil
}

// body returns the stored text of a node, so a metadata-only change can be
// written back without re-reading the node's files.
func (s *docStore) body(ctx context.Context, project, nodeID string) (string, error) {
	var body string
	err := s.reader().QueryRowContext(ctx,
		`SELECT body FROM search_content WHERE project = ? AND node_id = ?`, project, nodeID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read search_content: %w", err)
	}
	return body, nil
}

// put writes one node, replacing whatever was indexed for it.
//
// The upsert is what makes reindexing idempotent: the primary key is unchanged,
// so the row keeps its rowid, and the rowid is what search_fts is keyed by.
func (s *docStore) put(ctx context.Context, project string, doc *storedDoc, stamp string) error {
	return withTx(ctx, s.sql, func(tx *sql.Tx) error {
		// A node that moved between folders is the same node under a different
		// path, and the path is part of the key. Its old row has to go, or the
		// node is in the index twice and every hit on it is doubled.
		if err := pruneOtherPaths(ctx, tx, project, doc.nodeID, doc.path); err != nil {
			return err
		}
		var rowid int64
		err := tx.QueryRowContext(ctx, `
INSERT INTO search_documents (project, node_id, path, title, modified_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (project, node_id, path) DO UPDATE SET title = excluded.title, modified_at = excluded.modified_at
RETURNING rowid`, project, doc.nodeID, doc.path, doc.title, stamp).Scan(&rowid)
		if err != nil {
			return fmt.Errorf("upsert search_documents: %w", err)
		}
		if err := deleteIndexed(ctx, tx, rowid); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO search_fts (rowid, title, body) VALUES (?, ?, ?)`,
			rowid, indexTokens(doc.meta), indexTokens(doc.body)); err != nil {
			return fmt.Errorf("write search_fts: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO search_content (doc_id, project, node_id, path, meta, body) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (doc_id) DO UPDATE SET meta = excluded.meta, body = excluded.body`,
			rowid, project, doc.nodeID, doc.path, doc.meta, doc.body); err != nil {
			return fmt.Errorf("write search_content: %w", err)
		}
		return nil
	})
}

func pruneOtherPaths(ctx context.Context, tx *sql.Tx, project, nodeID, keep string) error {
	rowids, err := nodeRowids(ctx, tx,
		`SELECT rowid FROM search_documents WHERE project = ? AND node_id = ? AND path <> ?`,
		project, nodeID, keep)
	if err != nil || len(rowids) == 0 {
		return err
	}
	for _, rowid := range rowids {
		if err := deleteIndexed(ctx, tx, rowid); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM search_documents WHERE project = ? AND node_id = ? AND path <> ?`,
		project, nodeID, keep); err != nil {
		return fmt.Errorf("delete search_documents: %w", err)
	}
	return nil
}

func nodeRowids(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read search_documents: %w", err)
	}
	defer rows.Close()
	var rowids []int64
	for rows.Next() {
		var rowid int64
		if err := rows.Scan(&rowid); err != nil {
			return nil, fmt.Errorf("scan search_documents: %w", err)
		}
		rowids = append(rowids, rowid)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read search_documents: %w", err)
	}
	return rowids, nil
}

// deleteIndexed removes a document's postings.
//
// A contentless FTS5 table cannot be deleted from by rowid: the only way to
// retract a row is to hand FTS5 back the exact column values it indexed, which
// is why search_content exists and why indexTokens has to be deterministic.
// Skipping this and inserting over a live rowid would leave the document in the
// posting lists twice.
func deleteIndexed(ctx context.Context, tx *sql.Tx, rowid int64) error {
	var meta, body string
	err := tx.QueryRowContext(ctx, `SELECT meta, body FROM search_content WHERE doc_id = ?`, rowid).Scan(&meta, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read search_content: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO search_fts (search_fts, rowid, title, body) VALUES ('delete', ?, ?, ?)`,
		rowid, indexTokens(meta), indexTokens(body)); err != nil {
		return fmt.Errorf("delete from search_fts: %w", err)
	}
	return nil
}

// remove drops every row a node owns. The content row goes with the document
// row through the foreign key, but its postings have to be retracted first,
// while the text they were built from is still readable.
func (s *docStore) remove(ctx context.Context, project, nodeID string) error {
	return withTx(ctx, s.sql, func(tx *sql.Tx) error {
		rowids, err := nodeRowids(ctx, tx,
			`SELECT rowid FROM search_documents WHERE project = ? AND node_id = ?`, project, nodeID)
		if err != nil {
			return err
		}
		for _, rowid := range rowids {
			if err := deleteIndexed(ctx, tx, rowid); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM search_documents WHERE project = ? AND node_id = ?`, project, nodeID); err != nil {
			return fmt.Errorf("delete search_documents: %w", err)
		}
		return nil
	})
}

// prune drops every project that is not in keep.
//
// One database now holds what used to be a cache file inside each project, and
// a file went away when its project did. Nothing else notices that a project
// was deleted or moved, so without this the rows of every project a workspace
// ever had would stay forever.
func (s *docStore) prune(ctx context.Context, keep map[string]struct{}) error {
	rows, err := s.reader().QueryContext(ctx, `SELECT DISTINCT project FROM search_documents`)
	if err != nil {
		return fmt.Errorf("read search_documents: %w", err)
	}
	var stale []string
	for rows.Next() {
		var project string
		if err := rows.Scan(&project); err != nil {
			rows.Close()
			return fmt.Errorf("scan search_documents: %w", err)
		}
		if _, ok := keep[project]; !ok {
			stale = append(stale, project)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read search_documents: %w", err)
	}
	for _, project := range stale {
		if err := s.dropProject(ctx, project); err != nil {
			return err
		}
	}
	return nil
}

func (s *docStore) dropProject(ctx context.Context, project string) error {
	return withTx(ctx, s.sql, func(tx *sql.Tx) error {
		rowids, err := nodeRowids(ctx, tx, `SELECT rowid FROM search_documents WHERE project = ?`, project)
		if err != nil {
			return err
		}
		for _, rowid := range rowids {
			if err := deleteIndexed(ctx, tx, rowid); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM search_documents WHERE project = ?`, project); err != nil {
			return fmt.Errorf("delete search_documents: %w", err)
		}
		return nil
	})
}

// candidates returns the documents of a project that hold every one of the
// given tokens, or all of them when the query produced no tokens at all.
func (s *docStore) candidates(ctx context.Context, project string, tokens []string) ([]*storedDoc, error) {
	query := `
SELECT d.node_id, d.title, c.meta, c.body
FROM search_documents d
JOIN search_content c ON c.doc_id = d.rowid
WHERE d.project = ?`
	args := []any{project}
	if len(tokens) > 0 {
		query = `
SELECT d.node_id, d.title, c.meta, c.body
FROM search_fts
JOIN search_documents d ON d.rowid = search_fts.rowid
JOIN search_content c ON c.doc_id = search_fts.rowid
WHERE search_fts MATCH ? AND d.project = ?`
		args = []any{matchExpression(tokens), project}
	}
	// A ceiling, because both branches select the full prose of every document
	// they match and the HTTP layer's own cap applies only after that. The
	// shortest query the API accepts is two characters, which is one very
	// common bigram, so without this a large project answers it by loading
	// every node's body into memory.
	//
	// It can cost recall on a pathological query and cannot cost correctness:
	// postings are a candidate filter and the verification pass decides the
	// result either way — the same argument maxQueryTokens already makes.
	query += ` LIMIT ?`
	args = append(args, maxCandidates)

	rows, err := s.reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()
	out := make([]*storedDoc, 0, 16)
	for rows.Next() {
		doc := &storedDoc{}
		if err := rows.Scan(&doc.nodeID, &doc.title, &doc.meta, &doc.body); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		out = append(out, doc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	return out, nil
}

// matchExpression builds the FTS5 query for a token set.
//
// No part of the user's query reaches FTS5 as syntax. What is interpolated here
// are tokens, and a token is one or two letters or digits by construction (see
// forEachRun), so a quote, a star, a minus, a parenthesis or the word NEAR in
// what someone typed has already been dropped as a separator before this point.
// Each token is still quoted, and an embedded quote still doubled, because the
// cost of that is nothing and the cost of being wrong about it is an attacker
// choosing the query plan: a bareword that happens to spell AND, OR, NOT or
// NEAR would otherwise be read as an operator rather than as a term.
func matchExpression(tokens []string) string {
	if len(tokens) > maxQueryTokens {
		tokens = tokens[:maxQueryTokens]
	}
	var out strings.Builder
	for i, token := range tokens {
		if i > 0 {
			out.WriteString(" AND ")
		}
		out.WriteByte('"')
		out.WriteString(strings.ReplaceAll(token, `"`, `""`))
		out.WriteByte('"')
	}
	return out.String()
}
