//go:build !nosqliteimport

// REMOVE IN THE RELEASE AFTER THE ONE THAT INTRODUCES IT, with
// internal/legacysqlite (see its package comment).

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"nodevas/internal/db"
	"nodevas/internal/legacysqlite"
)

// importLegacy is serve's startup step: import the workspace's old SQLite
// database once, before anything reads the new one. It fails closed: a
// server that started empty would have its administrator re-created, after
// which the import would be refused.
func importLegacy(ctx context.Context, database *db.DB, root string) error {
	result, err := legacysqlite.MaybeImport(ctx, database, root)
	if err != nil {
		return fmt.Errorf("importing %s failed: %w\n"+
			"Nothing was imported and the file is unchanged. Compare it with the database using "+
			"`nodevas db import-sqlite --report --project %q`, fix the cause and start again, or start without it "+
			"by setting %s=1 (the file stays where it is and can be imported later with `nodevas db import-sqlite`)",
			result.Source, err, root, legacysqlite.SkipEnv)
	}
	if result.Imported {
		total := 0
		for _, n := range result.Counts {
			total += n
		}
		slog.Info("imported the workspace's SQLite database into PostgreSQL",
			slog.String("event.code", "db.legacy_import_done"), slog.Int("rows", total),
			slog.Bool("renamed", result.Renamed), slog.String("path", result.Source))
	}
	return nil
}

// importSQLiteCmd is `nodevas db import-sqlite`: import the workspace's old
// SQLite file, or with report compare it with the database.
func importSQLiteCmd(ctx context.Context, root string, report bool, w io.Writer) error {
	database, err := db.OpenRoot(root)
	if err != nil {
		return err
	}
	defer database.Close()
	atFatal = func() { database.Close() }
	source := legacysqlite.SourcePath(root)
	if report {
		result, err := legacysqlite.BuildReport(ctx, database, source)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "%s compared with %s\n", result.Source, database.Path())
		fmt.Fprintf(w, "%-24s %8s %8s  %s\n", "table", "file", "database", "only in the file")
		for _, table := range result.Tables {
			only := strings.Join(table.OnlyInFile, ", ")
			if len(table.OnlyInFile) > 20 {
				only = strings.Join(table.OnlyInFile[:20], ", ") + fmt.Sprintf(" … (%d more)", len(table.OnlyInFile)-20)
			}
			fmt.Fprintf(w, "%-24s %8d %8d  %s\n", table.Name, table.InFile, table.InDatabase, only)
		}
		return nil
	}
	result, err := legacysqlite.MaybeImport(ctx, database, root)
	if err != nil {
		return err
	}
	switch {
	case result.Imported:
		fmt.Fprintf(w, "imported %s:\n", result.Source)
		for _, table := range legacysqlite.Tables {
			if n := result.Counts[table]; n > 0 {
				fmt.Fprintf(w, "  %-24s %d\n", table, n)
			}
		}
	case result.AlreadyImported:
		fmt.Fprintf(w, "%s was imported before; renamed: %v\n", result.Source, result.Renamed)
	case result.Present:
		fmt.Fprintf(w, "%s was not imported: the database already holds data (see --report)\n", result.Source)
	case result.Skipped:
		_, err := legacysqlite.Import(ctx, database, source)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "imported %s (it had been skipped)\n", source)
	default:
		fmt.Fprintf(w, "no SQLite database at %s\n", source)
	}
	return nil
}
