// Package dbtest runs database tests against PostgreSQL as well as SQLite.
//
// Set NODEVAS_TEST_DATABASE_URL to a server the tests may create and drop
// databases on, for example
//
//	docker run -d -p 55432:5432 -e POSTGRES_PASSWORD=test postgres:17
//	NODEVAS_TEST_DATABASE_URL=postgres://postgres:test@127.0.0.1:55432/postgres go test ./internal/...
//
// Unset, every helper here is a no-op and the tests run on SQLite as before.
package dbtest

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"nodevas/internal/db"
)

// Env names the server the tests use.
const Env = "NODEVAS_TEST_DATABASE_URL"

var sequence atomic.Int64

// UsePostgres points db.Open at a fresh, empty PostgreSQL database for the rest
// of t, dropped when t ends, and reports whether it did. Every handle the test
// opens through db.Open — including a second "outside" one — reaches the same
// database, as two handles on one workspace reach the same SQLite file.
func UsePostgres(t testing.TB) bool {
	t.Helper()
	base := os.Getenv(Env)
	if base == "" {
		return false
	}
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatalf("%s: %v", Env, err)
	}
	name := fmt.Sprintf("nodevas_test_%d_%d", time.Now().UnixNano(), sequence.Add(1))
	if _, err := admin.Exec(`CREATE DATABASE ` + name); err != nil {
		admin.Close()
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() {
		defer admin.Close()
		if _, err := admin.Exec(`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`); err != nil {
			t.Errorf("drop test database %s: %v", name, err)
		}
	})

	location, err := url.Parse(base)
	if err != nil {
		t.Fatalf("%s must be a postgres:// URL: %v", Env, err)
	}
	location.Path = "/" + name
	t.Setenv(db.URLEnv, location.String())
	t.Setenv(db.WaitEnv, "0s")
	return true
}

// Open is db.Open on a fresh workspace, on PostgreSQL when Env is set.
func Open(t testing.TB) *db.DB {
	t.Helper()
	postgres := UsePostgres(t)
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	// A silent fallback would run the whole suite on SQLite and pass.
	if postgres && database.Backend() != db.Postgres {
		t.Fatalf("%s is set but the database fell back to %s", Env, database.Backend())
	}
	return database
}
