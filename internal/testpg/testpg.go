// Package testpg gives tests a PostgreSQL database: a fresh, migrated one per
// test, cloned from a template migrated once per test binary.
//
// The server is NODEVAS_TEST_DATABASE_URL when it is set (CI's service
// container, or a developer's own server), for example
//
//	NODEVAS_TEST_DATABASE_URL=postgres://postgres:test@127.0.0.1:5432/postgres?sslmode=disable go test ./...
//
// Unset, the package starts one embedded PostgreSQL (internal/embeddedpg) per
// test binary from a cached template data directory, with fsync off. A package
// that uses this one calls Main from its TestMain so that server and the
// template database are stopped and dropped when the binary ends.
package testpg

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"nodevas/internal/db"
)

// Env names the server the tests use instead of an embedded one.
const Env = "NODEVAS_TEST_DATABASE_URL"

var (
	serverOnce sync.Once
	serverURL  string
	serverErr  error

	templateOnce sync.Once
	templateName string
	templateErr  error

	sequence atomic.Int64
	runID    = randomSuffix()
)

func randomSuffix() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Main runs the tests and then drops the template database and stops an
// embedded server. Call it from TestMain: os.Exit(testpg.Main(m)).
func Main(m *testing.M) int {
	code := m.Run()
	if templateName != "" && serverURL != "" {
		if admin, err := sql.Open("pgx", serverURL); err == nil {
			_, _ = admin.Exec(`DROP DATABASE IF EXISTS ` + templateName + ` WITH (FORCE)`)
			_ = admin.Close()
		}
	}
	stopEmbedded()
	return code
}

// AdminURL is the server's maintenance URL: a role that may create and drop
// databases.
func AdminURL(t testing.TB) string {
	t.Helper()
	serverOnce.Do(func() {
		if raw := os.Getenv(Env); raw != "" {
			serverURL = raw
			return
		}
		serverURL, serverErr = startEmbedded()
	})
	if serverErr != nil {
		t.Fatalf("testpg: %v", serverErr)
	}
	return serverURL
}

func withDatabase(raw, name string) (string, error) {
	location, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%s must be a postgres:// URL", Env)
	}
	location.Path = "/" + name
	return location.String(), nil
}

func admin(t testing.TB) *sql.DB {
	t.Helper()
	handle, err := sql.Open("pgx", AdminURL(t))
	if err != nil {
		t.Fatalf("testpg: %v", err)
	}
	return handle
}

// template migrates one database per test binary for the others to copy.
func template(t testing.TB) string {
	t.Helper()
	base := AdminURL(t)
	templateOnce.Do(func() {
		name := fmt.Sprintf("tmpl_%d_%s", os.Getpid(), runID)
		handle, err := sql.Open("pgx", base)
		if err != nil {
			templateErr = err
			return
		}
		defer handle.Close()
		if _, err := handle.Exec(`CREATE DATABASE ` + name); err != nil {
			templateErr = fmt.Errorf("create template: %w", err)
			return
		}
		templateName = name
		target, err := withDatabase(base, name)
		if err != nil {
			templateErr = err
			return
		}
		migrated, err := db.OpenPostgres(target, 0)
		if err != nil {
			templateErr = fmt.Errorf("migrate template: %w", err)
			return
		}
		templateErr = migrated.Close()
	})
	if templateErr != nil {
		t.Fatalf("testpg: %v", templateErr)
	}
	return templateName
}

func create(t testing.TB, from string) string {
	t.Helper()
	handle := admin(t)
	name := fmt.Sprintf("t_%d_%s_%d", os.Getpid(), runID, sequence.Add(1))
	statement := `CREATE DATABASE ` + name
	if from != "" {
		statement += ` TEMPLATE ` + from + ` STRATEGY FILE_COPY`
	}
	if _, err := handle.Exec(statement); err != nil {
		handle.Close()
		t.Fatalf("testpg: create database: %v", err)
	}
	t.Cleanup(func() {
		defer handle.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := handle.ExecContext(ctx, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
			t.Errorf("testpg: drop %s: %v", name, err)
		}
	})
	location, err := withDatabase(AdminURL(t), name)
	if err != nil {
		t.Fatal(err)
	}
	return location
}

// NewURL is a fresh database with the current schema, dropped when t ends.
func NewURL(t testing.TB) string {
	t.Helper()
	return create(t, template(t))
}

// NewEmptyURL is a fresh database with no schema at all, for migration tests.
func NewEmptyURL(t testing.TB) string {
	t.Helper()
	return create(t, "")
}

// New opens a fresh migrated database for t and closes it when t ends.
func New(t testing.TB) *db.DB {
	t.Helper()
	database, err := db.OpenPostgres(NewURL(t), 0)
	if err != nil {
		t.Fatalf("testpg: open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// UseEnv points NODEVAS_DATABASE_URL at a fresh migrated database for the rest
// of t, for code that resolves its own database (db.OpenRoot, a child serve),
// and returns that URL.
func UseEnv(t testing.TB) string {
	t.Helper()
	location := NewURL(t)
	t.Setenv(db.URLEnv, location)
	t.Setenv(db.WaitEnv, "0s")
	return location
}
