//go:build !nosqliteimport

// REMOVE IN THE RELEASE AFTER THE ONE THAT INTRODUCES IT (Postgres only),
// together with modernc.org/sqlite in go.mod and cmd/nodevas/legacy_import.go.
//
// Package legacysqlite is the one-time importer of a workspace's old SQLite
// database (<workspace>/.vised/nodevas.db) into PostgreSQL. It is the only
// package that links modernc.org/sqlite, and only into a binary built without
// the nosqliteimport tag; serve calls it in-process, before the hub starts,
// and `nodevas db import-sqlite` runs it by hand.
//
// It imports when the file exists, it has not been imported before
// (legacy_imports), and every importable table in the target is empty. All
// rows and the legacy_imports marker commit in one transaction; then the file
// and its -wal/-shm sidecars are renamed *.imported, never deleted.
//
// The file itself is never opened: it (and its WAL, which can hold rows not
// yet checkpointed into it) is copied to a temporary directory and the copy
// is read, so the original stays byte for byte what it was.
package legacysqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"

	"nodevas/internal/db"
)

// SkipEnv lets serve start although an import failed or is pending, marking
// the file skipped.
const SkipEnv = "NODEVAS_SKIP_SQLITE_IMPORT"

// ImportedSuffix is appended to the file and its sidecars after an import.
const ImportedSuffix = ".imported"

var (
	// ErrNotImplemented is no longer returned; it is kept for tests that
	// assert a result is a real one and not a stub.
	ErrNotImplemented = errors.New("legacysqlite: not implemented")
	// ErrTargetNotEmpty refuses an import into a database that already holds
	// operational rows.
	ErrTargetNotEmpty = errors.New("legacysqlite: the PostgreSQL database already holds data")
)

// Tables is every operational table, parents before children: the import
// order. search_* (rebuilt from Markdown), schema_migrations and
// legacy_imports are not imported.
var Tables = []string{
	"accounts",
	"audit_events",
	"settings",
	"sessions",
	"copilot_conversations",
	"copilot_entries",
	"agent_messages",
	"agent_asks",
	"runners",
	"runner_tokens",
	"runs",
	"run_events",
	"agent_message_files",
	"run_changes",
	"run_merges",
	"run_budgets",
	"run_routes",
	"run_pipelines",
	"session_changes",
	"agent_session_usage",
	"usage_prices",
	"project_members",
	"autopilot_runs",
	"autopilot_tasks",
	"review_votes",
	"account_totp",
	"account_recovery_codes",
	"copilot_followups",
	"run_role_settings",
}

// Result is what MaybeImport or Import did.
type Result struct {
	// Source is the SQLite file.
	Source string
	// Imported is true when rows were copied by this call.
	Imported bool
	// AlreadyImported is true when legacy_imports already named the file.
	AlreadyImported bool
	// Renamed is true when the file and its sidecars now end in .imported.
	Renamed bool
	// Present is true when the file exists but the target is not empty (the
	// NAS case): nothing was imported, db.legacy_sqlite_present was logged.
	Present bool
	// Skipped is true when SkipEnv kept the file from being imported.
	Skipped bool
	// Counts is rows copied per table.
	Counts map[string]int
}

// TableReport compares one table between the file and the database.
type TableReport struct {
	Name       string
	InFile     int
	InDatabase int
	// OnlyInFile lists primary keys present in the file and not in the
	// database: rows written during a fallback, for example.
	OnlyInFile []string
}

// Report is `nodevas db import-sqlite --report`.
type Report struct {
	Source string
	Tables []TableReport
}

// testHookAfterTable, when set, runs after each table is copied inside the
// import transaction; an error aborts the import. Tests only.
var testHookAfterTable func(table string) error

// SourcePath is the workspace's legacy SQLite file.
func SourcePath(root string) string { return db.PathFor(root) }

// sidecars are the file and the SQLite files that travel with it.
var sidecars = []string{"", "-wal", "-shm"}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// key is how legacy_imports names a source: its absolute path.
func key(source string) string {
	if abs, err := filepath.Abs(source); err == nil {
		return abs
	}
	return source
}

// MaybeImport is serve's startup step: import the workspace's SQLite file if
// there is one to import, retry a rename an earlier import could not finish,
// and otherwise do nothing.
func MaybeImport(ctx context.Context, target *db.DB, root string) (Result, error) {
	source := SourcePath(root)
	result := Result{Source: source}
	if !fileExists(source) {
		return result, nil
	}
	status, err := marker(ctx, target, source)
	if err != nil {
		return result, err
	}
	switch status {
	case "imported":
		result.AlreadyImported = true
		result.Renamed, err = rename(source)
		return result, err
	case "skipped":
		result.Skipped = true
		return result, nil
	}
	if value := strings.TrimSpace(os.Getenv(SkipEnv)); value != "" && value != "0" && value != "false" {
		result.Skipped = true
		return result, markSkipped(ctx, target, source)
	}
	empty, err := targetEmpty(ctx, target.Writer())
	if err != nil {
		return result, err
	}
	if !empty {
		result.Present = true
		slog.Warn("a legacy SQLite database is present but the PostgreSQL database already holds data; it was not imported. "+
			"Compare them with `nodevas db import-sqlite --report` before deleting the file",
			slog.String("event.code", "db.legacy_sqlite_present"), slog.String("path", source))
		return result, nil
	}
	imported, err := Import(ctx, target, source)
	if err != nil {
		return imported, err
	}
	imported.Renamed, err = rename(source)
	return imported, err
}

// marker is the file's legacy_imports status: "imported", "skipped" or "".
func marker(ctx context.Context, target *db.DB, source string) (string, error) {
	var status string
	err := target.Writer().QueryRowContext(ctx,
		`SELECT status FROM legacy_imports WHERE source = $1`, key(source)).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("legacy_imports: %w", err)
	}
	return status, nil
}

func markSkipped(ctx context.Context, target *db.DB, source string) error {
	_, err := target.Writer().ExecContext(ctx, `
INSERT INTO legacy_imports (source, source_sha256, status, imported_at, counts)
VALUES ($1, '', 'skipped', $2, '{}')
ON CONFLICT (source) DO NOTHING`, key(source), db.Now())
	if err != nil {
		return fmt.Errorf("legacy_imports: %w", err)
	}
	slog.Warn("the legacy SQLite database was not imported ("+SkipEnv+" is set); "+
		"import it by hand with `nodevas db import-sqlite`",
		slog.String("event.code", "db.legacy_sqlite_skipped"), slog.String("path", source))
	return nil
}

// rename moves the file and its sidecars aside. A file already renamed is
// not an error.
func rename(source string) (bool, error) {
	var errs []error
	for _, suffix := range sidecars {
		from := source + suffix
		if !fileExists(from) {
			continue
		}
		if err := os.Rename(from, from+ImportedSuffix); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return false, fmt.Errorf("the import is committed but %s could not be renamed (it will be retried at the next start): %w", source, err)
	}
	return !fileExists(source), nil
}

func targetEmpty(ctx context.Context, handle interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (bool, error) {
	for _, table := range Tables {
		var any int
		err := handle.QueryRowContext(ctx, `SELECT 1 FROM `+table+` LIMIT 1`).Scan(&any)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("%s: %w", table, err)
		}
		return false, nil
	}
	return true, nil
}

// snapshot copies source and its WAL into a temporary directory and opens
// the copy. The caller removes the directory.
func snapshot(source string) (*sql.DB, string, error) {
	dir, err := os.MkdirTemp("", "nodevas-legacy-")
	if err != nil {
		return nil, "", err
	}
	copyPath := filepath.Join(dir, "nodevas.db")
	for _, suffix := range []string{"", "-wal"} {
		if !fileExists(source + suffix) {
			continue
		}
		if err := copyFile(source+suffix, copyPath+suffix); err != nil {
			os.RemoveAll(dir)
			return nil, "", err
		}
	}
	handle, err := sql.Open("sqlite", "file:"+filepath.ToSlash(copyPath)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		os.RemoveAll(dir)
		return nil, "", err
	}
	handle.SetMaxOpenConns(1)
	var n int
	if err := handle.QueryRow(`SELECT count(*) FROM sqlite_master`).Scan(&n); err != nil {
		handle.Close()
		os.RemoveAll(dir)
		return nil, "", fmt.Errorf("%s is not a readable SQLite database: %w", source, err)
	}
	return handle, dir, nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// pgColumn is one column of a target table.
type pgColumn struct {
	name     string
	dataType string
	identity bool
}

func pgColumns(ctx context.Context, tx *sql.Tx, table string) ([]pgColumn, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT column_name, data_type, identity_generation IS NOT NULL
FROM information_schema.columns
WHERE table_schema = 'public' AND table_name = $1
ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pgColumn
	for rows.Next() {
		var column pgColumn
		if err := rows.Scan(&column.name, &column.dataType, &column.identity); err != nil {
			return nil, err
		}
		out = append(out, column)
	}
	return out, rows.Err()
}

// sqliteColumns is the table's columns in the file, nil when the table does
// not exist there (an older schema).
func sqliteColumns(handle *sql.DB, table string) (map[string]bool, error) {
	rows, err := handle.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	if len(columns) == 0 {
		return nil, rows.Err()
	}
	return columns, rows.Err()
}

func quote(identifier string) string { return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"` }

// convert turns a SQLite value into what the PostgreSQL column takes.
func convert(value any, dataType string) (any, error) {
	if value == nil {
		return nil, nil
	}
	text := func() string {
		switch v := value.(type) {
		case string:
			return v
		case []byte:
			return string(v)
		case int64:
			return strconv.FormatInt(v, 10)
		case float64:
			return strconv.FormatFloat(v, 'g', -1, 64)
		case bool:
			return strconv.FormatBool(v)
		}
		return fmt.Sprint(value)
	}
	switch dataType {
	case "integer", "bigint", "smallint":
		switch v := value.(type) {
		case int64:
			return v, nil
		case float64:
			return int64(v), nil
		case bool:
			if v {
				return int64(1), nil
			}
			return int64(0), nil
		}
		return strconv.ParseInt(strings.TrimSpace(text()), 10, 64)
	case "double precision", "real", "numeric":
		switch v := value.(type) {
		case float64:
			return v, nil
		case int64:
			return float64(v), nil
		}
		return strconv.ParseFloat(strings.TrimSpace(text()), 64)
	case "boolean":
		switch v := value.(type) {
		case int64:
			return v != 0, nil
		case bool:
			return v, nil
		}
		return strconv.ParseBool(strings.TrimSpace(text()))
	case "bytea":
		if b, ok := value.([]byte); ok {
			return b, nil
		}
		return []byte(text()), nil
	}
	// PostgreSQL text cannot hold NUL; SQLite text can.
	return strings.ReplaceAll(text(), "\x00", ""), nil
}

// Import copies source into target unconditionally of legacy_imports, but
// refuses a non-empty target with ErrTargetNotEmpty.
func Import(ctx context.Context, target *db.DB, source string) (Result, error) {
	result := Result{Source: source, Counts: map[string]int{}}
	if !fileExists(source) {
		return result, fmt.Errorf("legacysqlite: %s does not exist", source)
	}
	digest, err := fileSHA256(source)
	if err != nil {
		return result, err
	}
	handle, dir, err := snapshot(source)
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(dir)
	defer handle.Close()

	tx, err := target.Writer().BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	empty, err := targetEmpty(ctx, tx)
	if err != nil {
		return result, err
	}
	if !empty {
		return result, ErrTargetNotEmpty
	}
	for _, table := range Tables {
		n, err := copyTable(ctx, handle, tx, table)
		if err != nil {
			return Result{Source: source}, fmt.Errorf("legacysqlite: import %s: %w", table, err)
		}
		if n > 0 {
			result.Counts[table] = n
		}
		if testHookAfterTable != nil {
			if err := testHookAfterTable(table); err != nil {
				return Result{Source: source}, err
			}
		}
	}
	counts, _ := json.Marshal(result.Counts)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO legacy_imports (source, source_sha256, status, imported_at, counts)
VALUES ($1, $2, 'imported', $3, $4)
ON CONFLICT (source) DO UPDATE SET source_sha256 = excluded.source_sha256, status = 'imported',
    imported_at = excluded.imported_at, counts = excluded.counts`,
		key(source), digest, db.Now(), string(counts)); err != nil {
		return Result{Source: source}, fmt.Errorf("legacy_imports: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Result{Source: source}, err
	}
	committed = true
	result.Imported = true
	slog.Info("imported the legacy SQLite database",
		slog.String("event.code", "db.legacy_sqlite_imported"), slog.String("path", source),
		slog.Any("counts", result.Counts))
	return result, nil
}

// copyTable copies one table's rows and checks the count.
func copyTable(ctx context.Context, handle *sql.DB, tx *sql.Tx, table string) (int, error) {
	present, err := sqliteColumns(handle, table)
	if err != nil {
		return 0, err
	}
	if present == nil {
		return 0, nil // the file predates this table
	}
	columns, err := pgColumns(ctx, tx, table)
	if err != nil {
		return 0, err
	}
	var selected []pgColumn
	var selectList []string
	identity := false
	for _, column := range columns {
		switch {
		case present[column.name]:
			selectList = append(selectList, quote(column.name))
		case column.name == "rowid":
			// SQLite's implicit rowid: the creation order accounts.rowid
			// keeps.
			selectList = append(selectList, "rowid")
		default:
			continue // added after the file's schema: the column default
		}
		selected = append(selected, column)
		identity = identity || column.identity
	}
	if len(selected) == 0 {
		return 0, nil
	}
	rows, err := handle.QueryContext(ctx, `SELECT `+strings.Join(selectList, ", ")+` FROM `+quote(table)+` ORDER BY rowid`)
	if err != nil {
		// A WITHOUT ROWID table has no rowid to order by.
		rows, err = handle.QueryContext(ctx, `SELECT `+strings.Join(selectList, ", ")+` FROM `+quote(table))
		if err != nil {
			return 0, err
		}
	}
	defer rows.Close()

	names := make([]string, len(selected))
	for i, column := range selected {
		names[i] = quote(column.name)
	}
	prefix := `INSERT INTO ` + quote(table) + ` (` + strings.Join(names, ", ") + `)`
	if identity {
		prefix += ` OVERRIDING SYSTEM VALUE`
	}
	prefix += ` VALUES `
	batchRows := min(500, 60000/len(selected))

	copied := 0
	var batch []any
	pending := 0
	flush := func() error {
		if pending == 0 {
			return nil
		}
		var values strings.Builder
		n := 1
		for r := 0; r < pending; r++ {
			if r > 0 {
				values.WriteString(", ")
			}
			values.WriteByte('(')
			for c := range selected {
				if c > 0 {
					values.WriteString(", ")
				}
				values.WriteString("$" + strconv.Itoa(n))
				n++
			}
			values.WriteByte(')')
		}
		if _, err := tx.ExecContext(ctx, prefix+values.String(), batch...); err != nil {
			return err
		}
		copied += pending
		batch, pending = batch[:0], 0
		return nil
	}
	for rows.Next() {
		raw := make([]any, len(selected))
		pointers := make([]any, len(selected))
		for i := range raw {
			pointers[i] = &raw[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return 0, err
		}
		for i, column := range selected {
			value, err := convert(raw[i], column.dataType)
			if err != nil {
				return 0, fmt.Errorf("%s.%s: %w", table, column.name, err)
			}
			batch = append(batch, value)
		}
		pending++
		if pending >= batchRows {
			if err := flush(); err != nil {
				return 0, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if err := flush(); err != nil {
		return 0, err
	}

	var inFile, inTarget int
	if err := handle.QueryRowContext(ctx, `SELECT count(*) FROM `+quote(table)).Scan(&inFile); err != nil {
		return 0, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM `+quote(table)).Scan(&inTarget); err != nil {
		return 0, err
	}
	if inFile != inTarget || inTarget != copied {
		return 0, fmt.Errorf("%d rows in the file, %d copied, %d in the database", inFile, copied, inTarget)
	}
	// Identity sequences continue after the largest imported value.
	for _, column := range selected {
		if !column.identity {
			continue
		}
		if _, err := tx.ExecContext(ctx, `SELECT setval(pg_get_serial_sequence($1, $2), coalesce(max(`+quote(column.name)+`), 0) + 1, false) FROM `+quote(table),
			table, column.name); err != nil {
			return 0, fmt.Errorf("sequence %s.%s: %w", table, column.name, err)
		}
	}
	return copied, nil
}

// BuildReport compares source with target table by table.
func BuildReport(ctx context.Context, target *db.DB, source string) (Report, error) {
	report := Report{Source: source}
	handle, dir, err := snapshot(source)
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(dir)
	defer handle.Close()
	for _, table := range Tables {
		present, err := sqliteColumns(handle, table)
		if err != nil {
			return report, err
		}
		if present == nil {
			continue
		}
		entry := TableReport{Name: table}
		if err := handle.QueryRowContext(ctx, `SELECT count(*) FROM `+quote(table)).Scan(&entry.InFile); err != nil {
			return report, err
		}
		if err := target.Writer().QueryRowContext(ctx, `SELECT count(*) FROM `+quote(table)).Scan(&entry.InDatabase); err != nil {
			return report, err
		}
		keys, err := primaryKey(ctx, target, table)
		if err != nil {
			return report, err
		}
		usable := len(keys) > 0
		for _, k := range keys {
			usable = usable && present[k]
		}
		if usable {
			inFile, err := keyValues(ctx, handle, table, keys)
			if err != nil {
				return report, err
			}
			inDB, err := keyValues(ctx, target.Writer(), table, keys)
			if err != nil {
				return report, err
			}
			seen := map[string]bool{}
			for _, k := range inDB {
				seen[k] = true
			}
			for _, k := range inFile {
				if !seen[k] {
					entry.OnlyInFile = append(entry.OnlyInFile, k)
				}
			}
			sort.Strings(entry.OnlyInFile)
		}
		report.Tables = append(report.Tables, entry)
	}
	return report, nil
}

func primaryKey(ctx context.Context, target *db.DB, table string) ([]string, error) {
	rows, err := target.Writer().QueryContext(ctx, `
SELECT a.attname
FROM pg_index i
JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
WHERE i.indrelid = ('public.' || $1)::regclass AND i.indisprimary
ORDER BY array_position(i.indkey, a.attnum)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		keys = append(keys, name)
	}
	return keys, rows.Err()
}

// keyValues lists a table's primary keys as text, columns joined with "/".
func keyValues(ctx context.Context, handle *sql.DB, table string, keys []string) ([]string, error) {
	quoted := make([]string, len(keys))
	for i, k := range keys {
		quoted[i] = quote(k)
	}
	rows, err := handle.QueryContext(ctx, `SELECT `+strings.Join(quoted, ", ")+` FROM `+quote(table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		raw := make([]any, len(keys))
		pointers := make([]any, len(keys))
		for i := range raw {
			pointers[i] = &raw[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		parts := make([]string, len(keys))
		for i, value := range raw {
			text, _ := convert(value, "text")
			parts[i] = fmt.Sprint(text)
		}
		out = append(out, strings.Join(parts, "/"))
	}
	return out, rows.Err()
}
