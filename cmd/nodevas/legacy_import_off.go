//go:build nosqliteimport

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"

	"nodevas/internal/db"
)

// importLegacy is a no-op in a build without the SQLite importer; it says so
// when there is a file it would have imported.
func importLegacy(_ context.Context, _ *db.DB, root string) error {
	if _, err := os.Stat(db.PathFor(root)); err == nil {
		slog.Warn("this build cannot import the workspace's SQLite database; run `nodevas db import-sqlite` from the previous release",
			slog.String("event.code", "db.legacy_sqlite_present"), slog.String("path", db.PathFor(root)))
	}
	return nil
}

func importSQLiteCmd(context.Context, string, bool, io.Writer) error {
	return errors.New("this build has no SQLite importer (built with -tags nosqliteimport)")
}
