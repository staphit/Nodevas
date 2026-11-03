package auth

import (
	"os"
	"testing"

	"nodevas/internal/db"
	"nodevas/internal/testpg"
)

func TestMain(m *testing.M) { os.Exit(testpg.Main(m)) }

// openUserStore is what the removed NewUserStore(workspace) did, for tests
// that open a workspace by its directory (after testpg.UseEnv): open the
// workspace database, wrap it, make sure there is an administrator.
func openUserStore(t testing.TB, workspace string) (*UserStore, error) {
	t.Helper()
	database, err := db.OpenRoot(workspace)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = database.Close() })
	users := NewUserStoreDB(database)
	if err := users.EnsureAdmin(); err != nil {
		return nil, err
	}
	return users, nil
}
