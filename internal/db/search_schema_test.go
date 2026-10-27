package db_test

import (
	"strings"
	"testing"

	"nodevas/internal/db"
	"nodevas/internal/searchindex"
)

// Migration 0026 and searchindex.Schema (which recreates a dropped table)
// must create the same objects, or a recreated table would differ from a
// migrated one.
func TestSearchMigrationMatchesSearchindexSchema(t *testing.T) {
	normalize := func(sql string) string { return strings.Join(strings.Fields(sql), " ") }
	if normalize(db.SearchIndexSchema) != normalize(searchindex.Schema) {
		t.Fatalf("migration 0026 differs from searchindex.Schema:\n%s\n---\n%s", db.SearchIndexSchema, searchindex.Schema)
	}
}
