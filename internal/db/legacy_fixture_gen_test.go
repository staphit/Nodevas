package db

// Step 0 of the PostgreSQL move: write the SQLite workspace databases the
// one-time importer (internal/legacysqlite) is tested against, with the
// SQLite code that still exists. Delete this file with the SQLite chain; the
// fixtures stay.
//
//	NODEVAS_WRITE_LEGACY_FIXTURES=1 go test ./internal/db -run TestWriteLegacyFixtures
//
// nodevas-0025.db is every operational table at the current schema, three
// rows each, with an identity gap in audit_events and accounts, a NUL inside
// a text value, and two rows (a setting and an audit event) that live only in
// nodevas-0025.db-wal, never checkpointed. nodevas-0018.db stops at
// 0018_project_members, the shape of an older workspace.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const legacyFixtureDir = "../legacysqlite/testdata/legacy"

func TestWriteLegacyFixtures(t *testing.T) {
	if os.Getenv("NODEVAS_WRITE_LEGACY_FIXTURES") != "1" {
		t.Skip("set NODEVAS_WRITE_LEGACY_FIXTURES=1 to regenerate the importer fixtures")
	}
	if err := os.MkdirAll(legacyFixtureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeLegacy0025(t)
	writeLegacy0018(t)
}

func removeFixture(t *testing.T, path string) {
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}

func writeLegacy0025(t *testing.T) {
	ctx := context.Background()
	work := filepath.Join(t.TempDir(), FileName)
	database, err := OpenAt(work)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	seedSQLite(t, database.Writer(), 3)

	// Gaps: identity values that must survive the import as they are.
	mustExec(t, database.Writer(), `DELETE FROM audit_events WHERE id = 2`)
	mustExec(t, database.Writer(), `DELETE FROM accounts WHERE rowid = (SELECT rowid FROM accounts ORDER BY rowid LIMIT 1 OFFSET 1)`)
	// A NUL inside text, which PostgreSQL text cannot hold.
	mustExec(t, database.Writer(), `INSERT INTO settings (key, value, updated_at) VALUES ('nul.inside', ?, '2026-10-28T00:00:00Z')`,
		"before\x00after")

	// Everything so far goes into the main file; what follows stays in the WAL.
	mustExec(t, database.Writer(), `PRAGMA wal_autocheckpoint = 0`)
	mustExec(t, database.Writer(), `PRAGMA wal_checkpoint(TRUNCATE)`)
	mustExec(t, database.Writer(), `INSERT INTO settings (key, value, updated_at) VALUES ('wal.only', 'from the wal', '2026-10-28T00:00:00Z')`)
	mustExec(t, database.Writer(), `INSERT INTO audit_events (at, action, detail) VALUES ('2026-10-28T00:00:01Z', 'wal.only', 'from the wal')`)
	var walRows int
	if err := database.Writer().QueryRowContext(ctx, `SELECT count(*) FROM settings WHERE key = 'wal.only'`).Scan(&walRows); err != nil || walRows != 1 {
		t.Fatalf("wal row: %d, %v", walRows, err)
	}

	target := filepath.Join(legacyFixtureDir, "nodevas-0025.db")
	removeFixture(t, target)
	copyFile(t, work, target)
	copyFile(t, work+"-wal", target+"-wal")
	info, err := os.Stat(target + "-wal")
	if err != nil || info.Size() == 0 {
		t.Fatalf("the fixture's WAL is empty: %v", err)
	}
}

func writeLegacy0018(t *testing.T) {
	saved := migrations
	defer func() { migrations = saved }()
	for i, step := range saved {
		if step.name == "0018_project_members" {
			migrations = saved[:i+1]
		}
	}
	if len(migrations) == len(saved) {
		t.Fatal("no 0018_project_members step")
	}
	work := filepath.Join(t.TempDir(), FileName)
	database, err := OpenAt(work)
	if err != nil {
		t.Fatal(err)
	}
	seedSQLite(t, database.Writer(), 2)
	mustExec(t, database.Writer(), `PRAGMA wal_checkpoint(TRUNCATE)`)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(legacyFixtureDir, "nodevas-0018.db")
	removeFixture(t, target)
	copyFile(t, work, target)
}

func mustExec(t *testing.T, handle *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := handle.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

var sqliteCheckIn = regexp.MustCompile(`CHECK\s*\(\s*(\w+)\s+IN\s*\(([^)]*)\)\s*\)`)
var sqliteQuoted = regexp.MustCompile(`'((?:[^']|'')*)'`)

type sqliteColumn struct {
	name, kind string
	pk         int
	allowed    []string
	refTable   string
	refCol     string
}

// seedSQLite inserts n rows into every operational table, parents first.
func seedSQLite(t *testing.T, handle *sql.DB, n int) {
	t.Helper()
	rows, err := handle.Query(`SELECT name, sql FROM sqlite_master WHERE type = 'table'
		AND name NOT LIKE 'sqlite_%' AND name NOT LIKE 'search_%' AND name <> 'schema_migrations'`)
	if err != nil {
		t.Fatal(err)
	}
	tableSQL := map[string]string{}
	for rows.Next() {
		var name, text string
		if err := rows.Scan(&name, &text); err != nil {
			t.Fatal(err)
		}
		tableSQL[name] = text
	}
	rows.Close()

	columns := map[string][]sqliteColumn{}
	parents := map[string][]string{}
	for table, text := range tableSQL {
		allowed := map[string][]string{}
		for _, match := range sqliteCheckIn.FindAllStringSubmatch(text, -1) {
			for _, value := range sqliteQuoted.FindAllStringSubmatch(match[2], -1) {
				allowed[match[1]] = append(allowed[match[1]], strings.ReplaceAll(value[1], "''", "'"))
			}
		}
		refs := map[string][2]string{}
		fk, err := handle.Query(`SELECT "table", "from", coalesce("to", '') FROM pragma_foreign_key_list(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		for fk.Next() {
			var parent, from, to string
			if err := fk.Scan(&parent, &from, &to); err != nil {
				t.Fatal(err)
			}
			refs[from] = [2]string{parent, to}
			parents[table] = append(parents[table], parent)
		}
		fk.Close()
		info, err := handle.Query(`SELECT name, type, pk FROM pragma_table_info(?) ORDER BY cid`, table)
		if err != nil {
			t.Fatal(err)
		}
		for info.Next() {
			var column sqliteColumn
			if err := info.Scan(&column.name, &column.kind, &column.pk); err != nil {
				t.Fatal(err)
			}
			column.allowed = allowed[column.name]
			if ref, ok := refs[column.name]; ok {
				column.refTable, column.refCol = ref[0], ref[1]
			}
			columns[table] = append(columns[table], column)
		}
		info.Close()
	}

	names := make([]string, 0, len(tableSQL))
	for name := range tableSQL {
		names = append(names, name)
	}
	sort.Strings(names)
	var order []string
	done := map[string]bool{}
	var visit func(string)
	visit = func(name string) {
		if done[name] {
			return
		}
		done[name] = true
		for _, parent := range parents[name] {
			if parent != name {
				visit(parent)
			}
		}
		order = append(order, name)
	}
	for _, name := range names {
		visit(name)
	}

	values := map[string][]map[string]any{}
	for _, table := range order {
		for i := 0; i < n; i++ {
			row := map[string]any{}
			var cols, marks []string
			var args []any
			for _, column := range columns[table] {
				var value any
				switch {
				case column.refTable != "":
					parent := values[column.refTable]
					refCol := column.refCol
					if refCol == "" {
						refCol = "id"
					}
					value = parent[i%len(parent)][refCol]
				case len(column.allowed) > 0:
					value = column.allowed[(i+1)%len(column.allowed)]
				default:
					switch strings.ToUpper(column.kind) {
					case "INTEGER":
						value = int64(i + 1)
					case "REAL":
						value = float64(i) + 0.5
					default:
						value = fmt.Sprintf("%s.%s.%d", table, column.name, i)
					}
				}
				row[column.name] = value
				cols = append(cols, `"`+column.name+`"`)
				marks = append(marks, "?")
				args = append(args, value)
			}
			if _, err := handle.Exec(fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s)`, table,
				strings.Join(cols, ", "), strings.Join(marks, ", ")), args...); err != nil {
				t.Fatalf("seed %s: %v", table, err)
			}
			values[table] = append(values[table], row)
		}
	}
}
