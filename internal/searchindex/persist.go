// The index's storage: one PostgreSQL table, search_documents.
//
// Everything here is derived data. Every row can be rebuilt from the Markdown
// and YAML on disk, which is why nothing in this file reports a missing table
// to the user: ensure recreates it and the next search rescans.
//
// A row is one node: its source-file bookkeeping, its text (the metadata line
// and the markdown), and the tokens tokenize.go cut out of each, as two
// tsvectors built with array_to_tsvector. Nothing passes through PostgreSQL's
// own text-search parser, so the lexemes are exactly this package's tokens and
// a Chinese paragraph is not one giant "word" — see tokenize.go.
//
// The text has to be kept because the tokens cannot be turned back into it and
// two things need it: the substring verification that makes bigram
// over-matching invisible (see projectIndex.query) and the excerpt the caller
// cuts out of Match.Text.
//
// Ranking is FTS5's bm25() computed in SQL (see rankedCandidates): the index
// used to live in SQLite FTS5, and the move to PostgreSQL keeps its scores.
// PostgreSQL's ts_rank has no inverse document frequency, so a whole question
// full of 的 and 是 would rank by its commonest characters.

package searchindex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgconn"

	"nodevas/internal/db"
)

// maxQueryTokens bounds the expression a plain search is turned into.
//
// Tokens are only a candidate filter, and dropping some of them can only widen
// the candidate set — never lose a document — because the verification pass
// decides the result either way. So a pasted paragraph costs a bounded query
// instead of a thousand-term boolean expression.
const maxQueryTokens = 16

// maxCandidates bounds how many documents one search pulls out of the
// database. See candidates for why a ceiling here cannot produce a wrong
// result.
const maxCandidates = 2000

// maxLexemeBytes bounds the lexemes of one tsvector. PostgreSQL refuses a
// tsvector whose lexemes add up to a megabyte or more; a megabyte of Chinese
// holds about 350,000 distinct bigrams at six bytes each, so the biggest
// documents do not fit whole. See budgetTokens for what is kept.
const maxLexemeBytes = 1_000_000

// Schema is the search index's table. Migration 0026_search_index in
// internal/db creates it from this same text (db.SearchIndexSchema); ensure
// runs it again, every statement IF NOT EXISTS, so a table an operator dropped
// comes back.
//
//   - meta_tokens and body_tokens are the distinct tokens of the metadata line
//     and of the markdown, one GIN index each: a ranked search needs to know
//     which of the two holds a token (FTS5's bm25 counts the columns), and a
//     per-column index answers that without reading the tsvector itself.
//   - token_count is the document length bm25 normalises by: the number of
//     distinct tokens in both columns, counted before any were dropped to fit
//     the size limit.
//   - oversize marks a document whose tsvectors had to drop tokens to fit. It
//     is a candidate for every plain search, so the substring check still
//     finds text in the part whose bigrams were not kept.
const Schema = db.SearchIndexSchema

// schemaLock is the advisory lock ensure holds while it runs Schema. It is the
// key internal/db's migrations hold, so recreating the table can never race a
// migration creating it, nor two processes race each other: CREATE TABLE IF
// NOT EXISTS is not safe against a concurrent CREATE TABLE.
const schemaLock = 0x6e6f6465 // "node", db.pgMigrationLock

// docStore is the package's view of the workspace database.
type docStore struct {
	// sql is the pool writes go to (db.DB.Search: a small pool of its own, so
	// reindexing never queues behind an audit append on the main writer); read
	// is the read-only pool queries go to.
	sql  *sql.DB
	read *sql.DB
	once sync.Once
	err  error
}

// storedDoc is one row of search_documents.
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
	return &docStore{sql: database.Search(), read: database.Reader()}
}

// reader is the handle queries go to.
func (s *docStore) reader() *sql.DB {
	if s.read != nil {
		return s.read
	}
	return s.sql
}

// ensure creates the table and its indexes, once per store. When they are all
// there already — the usual case, migration 0026 made them — it only looks.
func (s *docStore) ensure(ctx context.Context) error {
	s.once.Do(func() {
		var present bool
		err := s.reader().QueryRowContext(ctx, `
SELECT to_regclass('search_documents') IS NOT NULL
   AND to_regclass('search_documents_meta_tokens') IS NOT NULL
   AND to_regclass('search_documents_body_tokens') IS NOT NULL
   AND to_regclass('search_documents_oversize') IS NOT NULL`).Scan(&present)
		if err == nil && present {
			return
		}
		s.err = withTx(ctx, s.sql, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(?)`, schemaLock); err != nil {
				return fmt.Errorf("search schema lock: %w", err)
			}
			if _, err := tx.ExecContext(ctx, Schema); err != nil {
				return fmt.Errorf("search schema: %w", err)
			}
			return nil
		})
	})
	return s.err
}

// isUndefinedTable reports a query that failed because search_documents is
// gone, which is what an operator dropping it mid-run looks like.
func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}

// reset empties the whole index, for every project, and is what a rescan is
// recovered through.
func (s *docStore) reset(ctx context.Context) error {
	if _, err := s.sql.ExecContext(ctx, `DELETE FROM search_documents`); err != nil {
		return fmt.Errorf("empty search_documents: %w", err)
	}
	return nil
}

// withTx runs fn in a transaction, rolling back on error or panic.
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
SELECT node_id, title, path, modified_at, meta
FROM search_documents
WHERE project = ?`, project)
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
		`SELECT body FROM search_documents WHERE project = ? AND node_id = ? LIMIT 1`, project, nodeID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read search_documents: %w", err)
	}
	return body, nil
}

// storableText is text PostgreSQL will accept: no NUL, valid UTF-8. A file is
// whatever an editor wrote, and one stray byte must not fail the project's
// whole index into the direct scan.
func storableText(text string) string {
	if strings.IndexByte(text, 0) >= 0 {
		text = strings.ReplaceAll(text, "\x00", "")
	}
	return strings.ToValidUTF8(text, "�")
}

// put writes one node, replacing whatever was indexed for it.
func (s *docStore) put(ctx context.Context, project string, doc *storedDoc, stamp string) error {
	return s.putMany(ctx, project, []pendingWrite{{doc: doc, stamp: stamp}})
}

// putMany writes a batch of nodes in one transaction, each replacing whatever
// was indexed for it.
//
// The upsert is what makes reindexing idempotent, and atomic against a second
// writer indexing the same node at the same moment: one row per key, whoever
// commits last wins. The batch is sorted by key so two writers lock rows in
// the same order, and a deadlock between them anyway is retried once.
//
// The commit is asynchronous. This is derived data: a crash can lose the last
// few batches, which then still carry their old freshness stamps and are
// written again by the next search. Waiting for the disk on every batch would
// spend a cold build of thousands of nodes on fsync.
func (s *docStore) putMany(ctx context.Context, project string, batch []pendingWrite) error {
	if len(batch) == 0 {
		return nil
	}
	rows := make([]putRow, 0, len(batch))
	for _, write := range batch {
		doc := write.doc
		row := putRow{
			nodeID: doc.nodeID, path: doc.path, stamp: write.stamp,
			title: storableText(doc.title), meta: storableText(doc.meta), body: storableText(doc.body),
		}
		var metaCount, bodyCount int
		var metaCut, bodyCut bool
		row.metaTokens, metaCount, metaCut = documentTokens(row.meta)
		row.bodyTokens, bodyCount, bodyCut = documentTokens(row.body)
		row.count, row.oversize = metaCount+bodyCount, metaCut || bodyCut
		rows = append(rows, row)
	}
	sort.Slice(rows, func(a, b int) bool {
		if rows[a].nodeID != rows[b].nodeID {
			return rows[a].nodeID < rows[b].nodeID
		}
		return rows[a].path < rows[b].path
	})
	err := s.writeRows(ctx, project, rows)
	if isDeadlock(err) {
		err = s.writeRows(ctx, project, rows)
	}
	return err
}

// putRow is one node ready for the database.
type putRow struct {
	nodeID, path, stamp, title, meta, body string
	metaTokens, bodyTokens                 []string
	count                                  int
	oversize                               bool
}

func (s *docStore) writeRows(ctx context.Context, project string, rows []putRow) error {
	nodeIDs := make([]string, len(rows))
	paths := make([]string, len(rows))
	for i, row := range rows {
		nodeIDs[i], paths[i] = row.nodeID, row.path
	}
	var insert strings.Builder
	insert.WriteString(`
INSERT INTO search_documents
    (project, node_id, path, title, modified_at, meta, body, meta_tokens, body_tokens, token_count, oversize)
VALUES `)
	args := make([]any, 0, len(rows)*11)
	for i, row := range rows {
		if i > 0 {
			insert.WriteString(", ")
		}
		insert.WriteString("(?, ?, ?, ?, ?, ?, ?, array_to_tsvector(?::text[]), array_to_tsvector(?::text[]), ?, ?)")
		args = append(args, project, row.nodeID, row.path, row.title, row.stamp, row.meta, row.body,
			row.metaTokens, row.bodyTokens, row.count, row.oversize)
	}
	insert.WriteString(`
ON CONFLICT (project, node_id, path) DO UPDATE SET
    title = excluded.title, modified_at = excluded.modified_at, meta = excluded.meta, body = excluded.body,
    meta_tokens = excluded.meta_tokens, body_tokens = excluded.body_tokens,
    token_count = excluded.token_count, oversize = excluded.oversize`)
	return withTx(ctx, s.sql, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT set_config('synchronous_commit', 'off', true)`); err != nil {
			return fmt.Errorf("search write: %w", err)
		}
		// A node that moved between folders is the same node under a different
		// path, and the path is part of the key. Its old row has to go, or the
		// node is in the index twice and every hit on it is doubled.
		if _, err := tx.ExecContext(ctx, `
DELETE FROM search_documents d
USING unnest(?::text[], ?::text[]) AS k(node_id, path)
WHERE d.project = ? AND d.node_id = k.node_id AND d.path <> k.path`,
			nodeIDs, paths, project); err != nil {
			return fmt.Errorf("delete search_documents: %w", err)
		}
		if _, err := tx.ExecContext(ctx, insert.String(), args...); err != nil {
			return fmt.Errorf("upsert search_documents: %w", err)
		}
		return nil
	})
}

// isDeadlock reports PostgreSQL breaking a deadlock by failing this side.
func isDeadlock(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40P01"
}

// remove drops every row a node owns.
func (s *docStore) remove(ctx context.Context, project, nodeID string) error {
	if _, err := s.sql.ExecContext(ctx,
		`DELETE FROM search_documents WHERE project = ? AND node_id = ?`, project, nodeID); err != nil {
		return fmt.Errorf("delete search_documents: %w", err)
	}
	return nil
}

// prune drops every project that is not in keep.
//
// One database holds what used to be a cache file inside each project, and a
// file went away when its project did. Nothing else notices that a project
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
		if _, err := s.sql.ExecContext(ctx, `DELETE FROM search_documents WHERE project = ?`, project); err != nil {
			return fmt.Errorf("delete search_documents: %w", err)
		}
	}
	return nil
}

// tsqueryLiteral is one token as tsquery input: a quoted lexeme, matched
// as-is.
//
// No part of the user's query reaches PostgreSQL as syntax. What is quoted
// here is a token, and a token is one or two letters or digits by
// construction (see forEachRun), so a quote, an ampersand, a bang, a colon or
// a parenthesis in what someone typed was dropped as a separator before this
// point. The quote and the backslash are still doubled, because the cost of
// that is nothing and the cost of being wrong about it is someone choosing the
// query plan.
func tsqueryLiteral(token string) string {
	token = strings.ReplaceAll(token, `\`, `\\`)
	token = strings.ReplaceAll(token, `'`, `''`)
	return "'" + token + "'"
}

// candidates returns the documents of a project that hold every one of the
// given tokens (in either column), plus every oversize document, or all of
// them when the query produced no tokens at all.
func (s *docStore) candidates(ctx context.Context, project string, tokens []string) ([]*storedDoc, error) {
	if len(tokens) > maxQueryTokens {
		tokens = tokens[:maxQueryTokens]
	}
	query := `
SELECT node_id, title, meta, body
FROM search_documents
WHERE project = ?`
	args := []any{project}
	if len(tokens) > 0 {
		var where strings.Builder
		for i, token := range tokens {
			if i > 0 {
				where.WriteString(" AND ")
			}
			where.WriteString("(meta_tokens @@ ?::tsquery OR body_tokens @@ ?::tsquery)")
			literal := tsqueryLiteral(token)
			args = append(args, literal, literal)
		}
		query = `
SELECT node_id, title, meta, body
FROM search_documents
WHERE id IN (
    SELECT id FROM search_documents WHERE project = ? AND ` + where.String() + `
    UNION
    SELECT id FROM search_documents WHERE project = ? AND oversize
)`
		args = append(args, project)
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

// rankedDoc is a candidate with its BM25 score, higher is better.
type rankedDoc struct {
	storedDoc
	score float64
}

// rankedQuery scores a project's documents against a set of tokens with
// SQLite FTS5's bm25(search_fts, 1.0, 1.0), sign flipped so higher is better:
//
//	Σ over tokens  idf · f·(k1+1) / (f + k1·(1 − b + b·D/avgdl)),  k1 = 1.2, b = 0.75
//	idf = max(ln((N − df + 0.5) / (df + 0.5)), 1e-6)
//
// where f is how many of the two columns hold the token (each column's tokens
// are distinct, so f is 0, 1 or 2 — the column weights are both 1), D is the
// document's token_count, and N, df and avgdl are the project's. FTS5 took N
// and avgdl over the whole table, every project in the workspace; per project
// is the same number for a workspace of one, and the right one otherwise.
//
// Hits come from the per-column GIN indexes, one probe per token and column,
// so no tsvector is read to score a document; only the rows that survive the
// LIMIT are fetched whole. The probes are LATERAL subqueries behind OFFSET 0
// on purpose: joined directly, the planner may scan the project once and test
// every row's tsvector against every token, which reads (and decompresses)
// each document's tokens once per token — 40 times slower on 5,000 documents.
// Behind the fence each probe has one qualification, the token, and an index
// that answers it.
const rankedQuery = `
WITH q AS (
    SELECT u.ord, u.lit::tsquery AS tq FROM unnest(?::text[]) WITH ORDINALITY AS u(lit, ord)
),
hits AS (
    SELECT q.ord, h.id, h.token_count FROM q CROSS JOIN LATERAL (
        SELECT id, project, token_count FROM search_documents WHERE meta_tokens @@ q.tq OFFSET 0
    ) h WHERE h.project = ?
    UNION ALL
    SELECT q.ord, h.id, h.token_count FROM q CROSS JOIN LATERAL (
        SELECT id, project, token_count FROM search_documents WHERE body_tokens @@ q.tq OFFSET 0
    ) h WHERE h.project = ?
),
tf AS (
    SELECT ord, id, token_count, count(*)::float8 AS f FROM hits GROUP BY ord, id, token_count
),
df AS (
    SELECT ord, count(*)::float8 AS df FROM tf GROUP BY ord
),
st AS (
    SELECT count(*)::float8 AS n, coalesce(avg(token_count), 1)::float8 AS avgdl
    FROM search_documents WHERE project = ?
),
scored AS (
    SELECT tf.id,
           sum(greatest(ln((st.n - df.df + 0.5) / (df.df + 0.5)), 1e-6)
               * (tf.f * 2.2) / (tf.f + 1.2 * (0.25 + 0.75 * tf.token_count / greatest(st.avgdl, 1e-9)))) AS score
    FROM tf JOIN df ON df.ord = tf.ord CROSS JOIN st
    GROUP BY tf.id
    ORDER BY score DESC, tf.id
    LIMIT ?
)
SELECT d.node_id, d.title, d.meta, d.body, s.score
FROM scored s JOIN search_documents d ON d.id = s.id
ORDER BY s.score DESC, s.id`

// rankedCandidates returns the project's documents holding any of the tokens,
// best BM25 first, a few times more than limit so the tiers above BM25 (an
// exact title, the whole phrase) have something to reorder.
func (s *docStore) rankedCandidates(ctx context.Context, project string, tokens []string, limit int) ([]*rankedDoc, error) {
	if len(tokens) > maxRankTokens {
		tokens = tokens[:maxRankTokens]
	}
	if len(tokens) == 0 {
		return nil, nil
	}
	literals := make([]string, len(tokens))
	for i, token := range tokens {
		literals[i] = tsqueryLiteral(token)
	}
	fetch := min(max(limit*3, 50), 300)
	rows, err := s.reader().QueryContext(ctx, rankedQuery, literals, project, project, project, fetch)
	if err != nil {
		return nil, fmt.Errorf("ranked search: %w", err)
	}
	defer rows.Close()
	out := make([]*rankedDoc, 0, 16)
	for rows.Next() {
		doc := &rankedDoc{}
		if err := rows.Scan(&doc.nodeID, &doc.title, &doc.meta, &doc.body, &doc.score); err != nil {
			return nil, fmt.Errorf("scan ranked result: %w", err)
		}
		out = append(out, doc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ranked search: %w", err)
	}
	return out, nil
}

// document reads one node's row, for an exact-title hit the token query did
// not rank high enough to fetch. A node without a row yields nil.
func (s *docStore) document(ctx context.Context, project, nodeID string) (*storedDoc, error) {
	doc := &storedDoc{}
	err := s.reader().QueryRowContext(ctx, `
SELECT node_id, title, meta, body
FROM search_documents
WHERE project = ? AND node_id = ?
LIMIT 1`, project, nodeID).Scan(&doc.nodeID, &doc.title, &doc.meta, &doc.body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read search document: %w", err)
	}
	return doc, nil
}
