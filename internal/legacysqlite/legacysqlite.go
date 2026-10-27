//go:build !nosqliteimport

// REMOVE IN THE RELEASE AFTER THE ONE THAT INTRODUCES IT (Postgres only).
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
// RED PHASE: compile-only stubs.
package legacysqlite

import (
	"context"
	"errors"

	_ "modernc.org/sqlite"

	"nodevas/internal/db"
)

// SkipEnv lets serve start although an import failed or is pending, marking
// the file skipped.
const SkipEnv = "NODEVAS_SKIP_SQLITE_IMPORT"

// ImportedSuffix is appended to the file and its sidecars after an import.
const ImportedSuffix = ".imported"

var (
	// ErrNotImplemented is what every stub returns.
	ErrNotImplemented = errors.New("legacysqlite: not implemented")
	// ErrTargetNotEmpty refuses an import into a database that already holds
	// operational rows.
	ErrTargetNotEmpty = errors.New("legacysqlite: the PostgreSQL database already holds data")
)

// Tables is every operational table, parents before children: the import
// order. search_* (rebuilt from Markdown), schema_migrations and
// legacy_imports are not imported.
var Tables []string

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

// MaybeImport is serve's startup step: import the workspace's SQLite file if
// there is one to import, retry a rename an earlier import could not finish,
// and otherwise do nothing.
func MaybeImport(ctx context.Context, target *db.DB, root string) (Result, error) {
	return Result{}, ErrNotImplemented
}

// Import copies source into target unconditionally of legacy_imports, but
// refuses a non-empty target with ErrTargetNotEmpty.
func Import(ctx context.Context, target *db.DB, source string) (Result, error) {
	return Result{}, ErrNotImplemented
}

// BuildReport compares source with target table by table.
func BuildReport(ctx context.Context, target *db.DB, source string) (Report, error) {
	return Report{}, ErrNotImplemented
}
