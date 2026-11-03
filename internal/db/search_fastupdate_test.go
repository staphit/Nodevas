package db_test

// P5: the search index's GIN indexes run without a pending list
// (fastupdate=off). With the default, a bulk load leaves thousands of
// entries in the unsorted pending list, the planner prices the index scan off
// it, and the ranked query flips to a sequential scan of every tsvector.

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"nodevas/internal/db"
	"nodevas/internal/testpg"
)

var searchGINIndexes = []string{"search_documents_meta_tokens", "search_documents_body_tokens"}

func reloptions(t *testing.T, handle interface {
	QueryRow(string, ...any) *sql.Row
}, index string) string {
	t.Helper()
	var options sql.NullString
	if err := handle.QueryRow(
		`SELECT array_to_string(reloptions, ',') FROM pg_class WHERE oid = to_regclass($1)`, index).Scan(&options); err != nil {
		t.Fatalf("reloptions of %s: %v", index, err)
	}
	return options.String
}

func TestSearchGINIndexesHaveFastupdateOff(t *testing.T) {
	database := testpg.New(t)
	for _, index := range searchGINIndexes {
		if got := reloptions(t, database.Reader(), index); !strings.Contains(got, "fastupdate=off") {
			t.Errorf("%s reloptions = %q, want fastupdate=off", index, got)
		}
	}
}

// A server that last ran 0028 gets the setting in place.
func TestFastupdateOffAppliedOnUpgradeFrom0028(t *testing.T) {
	ctx := context.Background()
	location := testpg.NewEmptyURL(t)
	handle := rawHandle(t, location)
	if err := db.ApplyPGMigrationsThrough(ctx, handle, "0028_agent_analytics"); err != nil {
		t.Fatal(err)
	}
	for _, index := range searchGINIndexes {
		if got := reloptions(t, handle, index); strings.Contains(got, "fastupdate=off") {
			t.Fatalf("%s already has fastupdate=off before the upgrade", index)
		}
	}
	upgraded, err := db.OpenPostgres(location, 0)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	upgraded.Close()
	for _, index := range searchGINIndexes {
		if got := reloptions(t, handle, index); !strings.Contains(got, "fastupdate=off") {
			t.Errorf("%s reloptions after upgrade = %q, want fastupdate=off", index, got)
		}
	}
}

// The search table can be dropped by an operator and recreated by
// internal/searchindex; the migration must not fail on a database where it
// is gone.
func TestFastupdateMigrationToleratesADroppedSearchTable(t *testing.T) {
	ctx := context.Background()
	location := testpg.NewEmptyURL(t)
	handle := rawHandle(t, location)
	if err := db.ApplyPGMigrationsThrough(ctx, handle, "0028_agent_analytics"); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Exec(`DROP TABLE search_documents`); err != nil {
		t.Fatal(err)
	}
	upgraded, err := db.OpenPostgres(location, 0)
	if err != nil {
		t.Fatalf("upgrade without search_documents: %v", err)
	}
	upgraded.Close()
}
