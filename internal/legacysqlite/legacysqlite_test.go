//go:build !nosqliteimport

package legacysqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"nodevas/internal/db"
	"nodevas/internal/testpg"
)

func TestMain(m *testing.M) { os.Exit(testpg.Main(m)) }

const fixtures = "testdata/legacy"

func copyFixture(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// legacyWorkspace is a workspace holding a fixture as its old database,
// WAL included when the fixture has one.
func legacyWorkspace(t *testing.T, fixture string) string {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(fixtures, fixture)
	copyFixture(t, source, SourcePath(root))
	if _, err := os.Stat(source + "-wal"); err == nil {
		copyFixture(t, source+"-wal", SourcePath(root)+"-wal")
	}
	return root
}

// fileCounts reads per-table row counts from a private copy of a fixture
// (WAL included), never from the file under test.
func fileCounts(t *testing.T, fixture string) map[string]int {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "copy.db")
	copyFixture(t, filepath.Join(fixtures, fixture), path)
	if _, err := os.Stat(filepath.Join(fixtures, fixture) + "-wal"); err == nil {
		copyFixture(t, filepath.Join(fixtures, fixture)+"-wal", path+"-wal")
	}
	handle, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	rows, err := handle.Query(`SELECT name FROM sqlite_master WHERE type = 'table'
		AND name NOT LIKE 'sqlite_%' AND name NOT LIKE 'search_%' AND name <> 'schema_migrations'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		_ = rows.Scan(&name)
		tables = append(tables, name)
	}
	rows.Close()
	out := map[string]int{}
	for _, table := range tables {
		var n int
		if err := handle.QueryRow(`SELECT count(*) FROM "` + table + `"`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		out[table] = n
	}
	return out
}

func pgCounts(t *testing.T, target *db.DB) map[string]int {
	t.Helper()
	counts := testpg.Counts(t, target.Writer())
	delete(counts, "legacy_imports")
	delete(counts, "search_documents")
	return counts
}

func markers(t *testing.T, target *db.DB) int {
	t.Helper()
	var n int
	if err := target.QueryRow(`SELECT count(*) FROM legacy_imports`).Scan(&n); err != nil {
		t.Fatalf("legacy_imports: %v", err)
	}
	return n
}

func int64s(t *testing.T, target *db.DB, query string) []int64 {
	t.Helper()
	rows, err := target.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var v int64
		_ = rows.Scan(&v)
		out = append(out, v)
	}
	return out
}

func sha(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// 51.
func TestImportCopiesEveryTableAndPreservesIDs(t *testing.T) {
	ctx := context.Background()
	root := legacyWorkspace(t, "nodevas-0025.db")
	target := testpg.New(t)
	result, err := MaybeImport(ctx, target, root)
	if err != nil || !result.Imported {
		t.Fatalf("MaybeImport = %+v, %v", result, err)
	}
	want := fileCounts(t, "nodevas-0025.db")
	got := pgCounts(t, target)
	for table, n := range want {
		if got[table] != n || result.Counts[table] != n {
			t.Errorf("%s: %d rows in PostgreSQL (reported %d), %d in the file", table, got[table], result.Counts[table], n)
		}
	}
	if ids := int64s(t, target, `SELECT id FROM audit_events ORDER BY id`); !slices.Equal(ids, []int64{1, 3, 4}) {
		t.Errorf("audit ids = %v, want the file's [1 3 4]", ids)
	}
	if rowids := int64s(t, target, `SELECT rowid FROM accounts ORDER BY rowid`); !slices.Equal(rowids, []int64{1, 3}) {
		t.Errorf("account rowids = %v, want the file's [1 3]", rowids)
	}
	// Identities continue after the imported maximum.
	if _, err := target.Exec(`INSERT INTO audit_events (at, action) VALUES ('2026-10-29T00:00:00Z', 'after')`); err != nil {
		t.Fatalf("insert after import: %v", err)
	}
	if ids := int64s(t, target, `SELECT max(id) FROM audit_events`); ids[0] <= 4 {
		t.Errorf("next audit id %d reuses an imported one", ids[0])
	}
	if _, err := target.Exec(`INSERT INTO accounts (id, name, role, created_at) VALUES ('new', 'new', 'member', 'now')`); err != nil {
		t.Fatalf("account after import: %v", err)
	}
	if rowids := int64s(t, target, `SELECT rowid FROM accounts WHERE id = 'new'`); rowids[0] <= 3 {
		t.Errorf("next account rowid %d reuses an imported one", rowids[0])
	}
	var value string
	if err := target.QueryRow(`SELECT value FROM settings WHERE key = 'nul.inside'`).Scan(&value); err != nil || value != "beforeafter" {
		t.Errorf("NUL text = %q, %v; want the NUL stripped", value, err)
	}
	if markers(t, target) != 1 {
		t.Error("no legacy_imports marker")
	}
}

// 52.
func TestImportTableListCoversPGSchema(t *testing.T) {
	target := testpg.New(t)
	var schema []string
	position := map[string]int{}
	for i, table := range Tables {
		position[table] = i
	}
	for _, table := range testpg.Tables(t, target.Writer()) {
		switch table.Name {
		case "legacy_imports", "search_documents":
			if slices.Contains(Tables, table.Name) {
				t.Errorf("%s must not be imported", table.Name)
			}
			continue
		}
		schema = append(schema, table.Name)
		for _, parent := range table.Parents {
			if parent != table.Name && position[parent] >= position[table.Name] {
				t.Errorf("%s is imported before its parent %s", table.Name, parent)
			}
		}
	}
	listed := slices.Clone(Tables)
	sort.Strings(schema)
	sort.Strings(listed)
	if !slices.Equal(schema, listed) {
		t.Fatalf("Tables = %v\nschema = %v", listed, schema)
	}
}

// 53.
func TestImportOlderSchemaUsesDefaults(t *testing.T) {
	ctx := context.Background()
	root := legacyWorkspace(t, "nodevas-0018.db")
	target := testpg.New(t)
	result, err := MaybeImport(ctx, target, root)
	if err != nil || !result.Imported {
		t.Fatalf("MaybeImport = %+v, %v", result, err)
	}
	want := fileCounts(t, "nodevas-0018.db")
	got := pgCounts(t, target)
	for table, n := range want {
		if got[table] != n {
			t.Errorf("%s: %d rows, file has %d", table, got[table], n)
		}
	}
	for table, n := range got {
		if _, inFile := want[table]; !inFile && n != 0 {
			t.Errorf("%s did not exist in 0018 but has %d rows", table, n)
		}
	}
	var role string
	if err := target.QueryRow(`SELECT role FROM runs LIMIT 1`).Scan(&role); err != nil || role != "" {
		t.Errorf("runs.role (added in 0025) = %q, %v; want the column default", role, err)
	}
}

// 54.
func TestImportIncludesUncheckpointedWAL(t *testing.T) {
	root := legacyWorkspace(t, "nodevas-0025.db")
	target := testpg.New(t)
	if _, err := MaybeImport(context.Background(), target, root); err != nil {
		t.Fatalf("MaybeImport: %v", err)
	}
	var value string
	if err := target.QueryRow(`SELECT value FROM settings WHERE key = 'wal.only'`).Scan(&value); err != nil || value != "from the wal" {
		t.Fatalf("WAL-only setting = %q, %v", value, err)
	}
	var n int
	_ = target.QueryRow(`SELECT count(*) FROM audit_events WHERE action = 'wal.only'`).Scan(&n)
	if n != 1 {
		t.Fatalf("WAL-only audit event: %d rows", n)
	}
}

// 55.
func TestImportDoesNotModifySource(t *testing.T) {
	root := legacyWorkspace(t, "nodevas-0025.db")
	path := SourcePath(root)
	before := map[string]string{"": sha(t, path), "-wal": sha(t, path+"-wal")}
	if _, err := MaybeImport(context.Background(), testpg.New(t), root); err != nil {
		t.Fatalf("MaybeImport: %v", err)
	}
	for suffix, digest := range before {
		if got := sha(t, path+suffix+ImportedSuffix); got != digest {
			t.Errorf("nodevas.db%s changed during the import", suffix)
		}
	}
}

// 56.
func TestImportRefusesNonEmptyTarget(t *testing.T) {
	ctx := context.Background()
	root := legacyWorkspace(t, "nodevas-0025.db")
	target := testpg.New(t)
	if _, err := target.Exec(`INSERT INTO accounts (id, name, role, created_at) VALUES ('nas', 'nas', 'admin', 'now')`); err != nil {
		t.Fatal(err)
	}
	result, err := MaybeImport(ctx, target, root)
	if err != nil || result.Imported || !result.Present {
		t.Fatalf("MaybeImport into a used database = %+v, %v; want nothing imported, file reported present", result, err)
	}
	if _, err := Import(ctx, target, SourcePath(root)); !errors.Is(err, ErrTargetNotEmpty) {
		t.Fatalf("Import = %v, want ErrTargetNotEmpty", err)
	}
	if !exists(SourcePath(root)) || markers(t, target) != 0 {
		t.Fatal("a refused import renamed the file or left a marker")
	}
	if got := pgCounts(t, target)["accounts"]; got != 1 {
		t.Fatalf("accounts = %d", got)
	}
}

// 57.
func TestImportIsAtomicOnMidwayFailure(t *testing.T) {
	root := legacyWorkspace(t, "nodevas-0025.db")
	target := testpg.New(t)
	injected := errors.New("injected failure")
	testHookAfterTable = func(table string) error {
		if table == "runs" {
			return injected
		}
		return nil
	}
	t.Cleanup(func() { testHookAfterTable = nil })
	if _, err := MaybeImport(context.Background(), target, root); !errors.Is(err, injected) {
		t.Fatalf("MaybeImport = %v, want the injected failure", err)
	}
	for table, n := range pgCounts(t, target) {
		if n != 0 {
			t.Errorf("%s kept %d rows from a failed import", table, n)
		}
	}
	if markers(t, target) != 0 {
		t.Error("a failed import was marked done")
	}
	if !exists(SourcePath(root)) || exists(SourcePath(root)+ImportedSuffix) {
		t.Error("a failed import renamed the file")
	}
}

// 58.
func TestImportRenamesFileAndSidecars(t *testing.T) {
	root := legacyWorkspace(t, "nodevas-0025.db")
	path := SourcePath(root)
	result, err := MaybeImport(context.Background(), testpg.New(t), root)
	if err != nil || !result.Renamed {
		t.Fatalf("MaybeImport = %+v, %v", result, err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if exists(path + suffix) {
			t.Errorf("nodevas.db%s is still in place", suffix)
		}
	}
	for _, suffix := range []string{"", "-wal"} {
		if !exists(path + suffix + ImportedSuffix) {
			t.Errorf("nodevas.db%s%s is missing: the file must be kept, renamed", suffix, ImportedSuffix)
		}
	}
}

// 59. The rows committed but the rename failed (a Windows lock): the next
// start only retries the rename.
func TestRenameRetriedWhenMarkerExists(t *testing.T) {
	ctx := context.Background()
	root := legacyWorkspace(t, "nodevas-0025.db")
	path := SourcePath(root)
	target := testpg.New(t)
	if _, err := MaybeImport(ctx, target, root); err != nil {
		t.Fatalf("first: %v", err)
	}
	before := pgCounts(t, target)
	for _, suffix := range []string{"", "-wal"} {
		if err := os.Rename(path+suffix+ImportedSuffix, path+suffix); err != nil {
			t.Fatal(err)
		}
	}
	result, err := MaybeImport(ctx, target, root)
	if err != nil || result.Imported || !result.AlreadyImported || !result.Renamed {
		t.Fatalf("retry = %+v, %v; want only the rename", result, err)
	}
	if after := pgCounts(t, target); !mapsEqual(before, after) {
		t.Fatalf("the retry copied rows again: %v -> %v", before, after)
	}
	if exists(path) || !exists(path+ImportedSuffix) {
		t.Fatal("the rename was not retried")
	}
}

func mapsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// 60.
func TestImportIsIdempotent(t *testing.T) {
	ctx := context.Background()
	root := legacyWorkspace(t, "nodevas-0025.db")
	target := testpg.New(t)
	if _, err := MaybeImport(ctx, target, root); err != nil {
		t.Fatalf("first: %v", err)
	}
	before := pgCounts(t, target)
	second, err := MaybeImport(ctx, target, root)
	if err != nil || second.Imported {
		t.Fatalf("second = %+v, %v", second, err)
	}
	if after := pgCounts(t, target); !mapsEqual(before, after) || markers(t, target) != 1 {
		t.Fatalf("a second start changed the database: %v -> %v", before, after)
	}
}

// 61. The NAS case: the file is not imported, and the report says what it
// holds that the server does not.
func TestReportListsRowsOnlyInFile(t *testing.T) {
	root := legacyWorkspace(t, "nodevas-0025.db")
	target := testpg.New(t)
	if _, err := target.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('wal.only', 'x', 'now')`); err != nil {
		t.Fatal(err)
	}
	report, err := BuildReport(context.Background(), target, SourcePath(root))
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	var settings *TableReport
	for i := range report.Tables {
		if report.Tables[i].Name == "settings" {
			settings = &report.Tables[i]
		}
	}
	if settings == nil {
		t.Fatalf("no settings in %+v", report)
	}
	want := fileCounts(t, "nodevas-0025.db")["settings"]
	if settings.InFile != want || settings.InDatabase != 1 {
		t.Fatalf("settings in file %d (want %d), in database %d (want 1)", settings.InFile, want, settings.InDatabase)
	}
	if !slices.Contains(settings.OnlyInFile, "nul.inside") || slices.Contains(settings.OnlyInFile, "wal.only") {
		t.Fatalf("OnlyInFile = %v", settings.OnlyInFile)
	}
	if !exists(SourcePath(root)) {
		t.Fatal("a report renamed the file")
	}
}
