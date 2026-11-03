package dbconn

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"nodevas/internal/embeddedpg"
	"nodevas/internal/workspace"
)

func env(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("%s is not empty: nothing embedded may be installed or started", dir)
	}
}

func TestURLWinsOverEmbedded(t *testing.T) {
	const external = "postgres://nodevas:pw@db.example:5432/nodevas?sslmode=disable"
	urlFile := filepath.Join(t.TempDir(), "url")
	if err := os.WriteFile(urlFile, []byte(external+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, values := range map[string]map[string]string{
		"URL":     {"NODEVAS_DATABASE_URL": external},
		"URLFile": {"NODEVAS_DATABASE_URL_FILE": urlFile},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			resolved, err := ResolveWith(Options{Root: t.TempDir(), Lookup: env(values), PGHome: home})
			if err != nil {
				t.Fatalf("ResolveWith: %v", err)
			}
			if resolved.URL != external || resolved.Embedded || resolved.Client {
				t.Fatalf("resolved = embedded %v client %v", resolved.Embedded, resolved.Client)
			}
			if resolved.Release == nil || resolved.Release() != nil {
				t.Fatal("Release must be a no-op for an external server")
			}
			assertEmptyDir(t, home)
		})
	}
}

func TestRequireURLRefusesEmbedded(t *testing.T) {
	home := t.TempDir()
	_, err := ResolveWith(Options{Root: t.TempDir(), Lookup: env(map[string]string{RequireURLEnv: "1"}), PGHome: home})
	if err == nil || errors.Is(err, ErrNotImplemented) {
		t.Fatalf("ResolveWith = %v, want a refusal", err)
	}
	for _, want := range []string{"NODEVAS_DATABASE_URL", "NODEVAS_DATABASE_URL_FILE", RequireURLEnv} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q: %v", want, err)
		}
	}
	assertEmptyDir(t, home)
}

func TestDatabaseIDSurvivesWorkspaceMove(t *testing.T) {
	parent := t.TempDir()
	original := filepath.Join(parent, "ws")
	if err := os.MkdirAll(original, 0o755); err != nil {
		t.Fatal(err)
	}
	id, err := DatabaseID(original)
	if err != nil || len(id) < 32 {
		t.Fatalf("DatabaseID = %q, %v", id, err)
	}
	again, err := DatabaseID(original)
	if err != nil || again != id {
		t.Fatalf("second DatabaseID = %q, %v; want the same id", again, err)
	}
	if _, err := os.Stat(filepath.Join(original, workspace.DataDir, DatabaseIDFile)); err != nil {
		t.Fatalf("id file: %v", err)
	}
	moved := filepath.Join(parent, "moved")
	if err := os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	after, err := DatabaseID(moved)
	if err != nil || after != id {
		t.Fatalf("DatabaseID after a move = %q, %v; want %q", after, err, id)
	}
	other, err := DatabaseID(t.TempDir())
	if err != nil || other == id {
		t.Fatalf("a second workspace got id %q (%v)", other, err)
	}
}

// A copied workspace carries the original's id. While the original's serve
// owns the cluster, the copy must not silently share its database: it is
// refused with a hint, and `nodevas db detach` gives it a cluster of its own.
// The busy check runs before anything is installed or started.
func TestCopiedWorkspaceIsBusyUntilDetach(t *testing.T) {
	home := t.TempDir()
	original, copied := t.TempDir(), t.TempDir()
	id, err := DatabaseID(original)
	if err != nil {
		t.Fatalf("DatabaseID: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(original, workspace.DataDir, DatabaseIDFile))
	_ = os.MkdirAll(filepath.Join(copied, workspace.DataDir), 0o700)
	if err := os.WriteFile(filepath.Join(copied, workspace.DataDir, DatabaseIDFile), data, 0o600); err != nil {
		t.Fatal(err)
	}

	// The original's serve: owner lock, run.json with a live pid, its path.
	release, err := embeddedpg.Lock(home, id)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	defer release()
	dir := embeddedpg.ClusterDir(home, id)
	run, _ := json.Marshal(embeddedpg.RunInfo{PID: os.Getpid(), Port: 54990, Version: embeddedpg.PinnedVersion})
	_ = os.WriteFile(filepath.Join(dir, "run.json"), run, 0o600)
	_ = os.WriteFile(filepath.Join(dir, "secret"), []byte("pw"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "workspace.txt"), []byte(original), 0o600)

	_, err = ResolveWith(Options{Root: copied, Lookup: env(nil), PGHome: home, Binaries: filepath.Join(home, "none")})
	if !errors.Is(err, embeddedpg.ErrClusterBusy) || !strings.Contains(err.Error(), "nodevas db detach") {
		t.Fatalf("ResolveWith(copy) = %v, want ErrClusterBusy naming `nodevas db detach`", err)
	}

	newID, err := Detach(copied)
	if err != nil || newID == "" || newID == id {
		t.Fatalf("Detach = %q, %v", newID, err)
	}
	if got, _ := DatabaseID(copied); got != newID {
		t.Fatalf("copy id = %q after detach, want %q", got, newID)
	}
	if got, _ := DatabaseID(original); got != id {
		t.Fatalf("the original's id changed to %q", got)
	}
}

// ---------- real binaries ----------

func realBinaries(t *testing.T) string {
	t.Helper()
	dir := os.Getenv(embeddedpg.BinariesEnv)
	if dir == "" {
		if os.Getenv("NODEVAS_PG_REAL") == "1" {
			t.Fatalf("%s must name the pinned binaries in this job", embeddedpg.BinariesEnv)
		}
		t.Skipf("set %s to run against the real embedded PostgreSQL", embeddedpg.BinariesEnv)
	}
	return dir
}

func TestTwoOpensInOneProcessShareCluster(t *testing.T) {
	binaries := realBinaries(t)
	opts := Options{Root: t.TempDir(), Lookup: env(nil), PGHome: t.TempDir(), Binaries: binaries}
	first, err := ResolveWith(opts)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := ResolveWith(opts)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !first.Embedded || first.URL != second.URL || first.Port != second.Port || first.Port == 0 {
		t.Fatalf("two opens did not share one cluster: ports %d and %d", first.Port, second.Port)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("first release: %v", err)
	}
	handle, err := sql.Open("pgx", second.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := handle.PingContext(ctx); err != nil {
		t.Fatalf("the cluster stopped while a hold remained: %v", err)
	}
	handle.Close()
	if err := second.Release(); err != nil {
		t.Fatalf("last release: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(second.Port)), time.Second)
		if err != nil {
			break
		}
		conn.Close()
		if time.Now().After(deadline) {
			t.Fatal("the last release did not stop the cluster")
		}
		time.Sleep(200 * time.Millisecond)
	}
}
