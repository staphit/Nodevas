package server

import (
	"os"
	"sync"
	"testing"

	"nodevas/internal/db"
	"nodevas/internal/testpg"
)

func TestMain(m *testing.M) { os.Exit(testpg.Main(m)) }

// testDatabases remembers the tests that already pointed NODEVAS_DATABASE_URL
// at a database of their own, so every handle a test opens on its workspace
// reaches the same one, as two handles on one workspace do in production.
var testDatabases sync.Map

func useTestDatabase(t *testing.T) {
	t.Helper()
	if _, loaded := testDatabases.LoadOrStore(t, true); !loaded {
		testpg.UseEnv(t)
		t.Cleanup(func() { testDatabases.Delete(t) })
	}
}

// dbtestOpenForServer opens the workspace's database the way serve does.
func dbtestOpenForServer(t *testing.T, workspace string) *db.DB {
	t.Helper()
	useTestDatabase(t)
	database, err := db.OpenRoot(workspace)
	if err != nil {
		t.Fatalf("db.OpenRoot: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}
