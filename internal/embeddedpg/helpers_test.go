package embeddedpg

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// realEnv is "1" in the embedded-pg CI job: the tests that need the real
// pinned binaries must run there (downloading them if needed) instead of
// skipping.
const realEnv = "NODEVAS_PG_REAL"

// helperEnv turns the test binary into a serve stand-in that starts a cluster,
// prints its postmaster pid and blocks until killed.
const helperEnv = "EMBEDDEDPG_TEST_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "own-cluster" {
		os.Exit(helperOwnCluster())
	}
	os.Exit(m.Run())
}

func helperOwnCluster() int {
	cluster := New(Options{
		Home: os.Getenv("EMBEDDEDPG_TEST_HOME"), ID: os.Getenv("EMBEDDEDPG_TEST_ID"),
		Binaries: os.Getenv(BinariesEnv),
	})
	if err := cluster.Start(context.Background()); err != nil {
		fmt.Println("ERROR", err)
		return 1
	}
	fmt.Println("PID", cluster.PID())
	select {}
}

// realBinaries returns verified pinned binaries, or skips the test when they
// are not installed and realEnv is not set.
func realBinaries(t *testing.T) string {
	t.Helper()
	required := os.Getenv(realEnv) == "1"
	fail := func(format string, args ...any) {
		t.Helper()
		if required {
			t.Fatalf(format, args...)
		}
		t.Skipf(format+" (set "+realEnv+"=1 to require the real binaries)", args...)
	}
	target, err := CurrentTarget()
	if err != nil {
		fail("real PostgreSQL: %v", err)
	}
	if dir := os.Getenv(BinariesEnv); dir != "" {
		if err := VerifyBinaries(dir, target); err != nil {
			t.Fatalf("%s=%s: %v", BinariesEnv, dir, err)
		}
		return dir
	}
	home, err := DefaultHome()
	if err != nil {
		fail("real PostgreSQL: %v", err)
	}
	dir, err := Install(context.Background(), InstallOptions{
		Home: home, Target: target, Offline: !required, Progress: os.Stderr,
		Mirror: os.Getenv(MirrorEnv),
	})
	if err != nil {
		fail("real PostgreSQL binaries not installed: %v", err)
	}
	return dir
}

// logBuffer is a goroutine-safe slog sink.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func randomID(t *testing.T) string {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return "test-" + hex.EncodeToString(b[:])
}

type started struct {
	*Cluster
	home string
	logs *logBuffer
	opts Options
}

// startCluster starts a real cluster in a temporary home and stops it when
// the test ends.
func startCluster(t *testing.T) *started {
	t.Helper()
	binaries := realBinaries(t)
	logs := &logBuffer{}
	opts := Options{
		Home: t.TempDir(), ID: randomID(t), Binaries: binaries, Workspace: t.TempDir(),
		Logger: slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	cluster := New(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := cluster.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = cluster.Stop(ctx)
	})
	return &started{Cluster: cluster, home: opts.Home, logs: logs, opts: opts}
}

func connect(t *testing.T, url string) *sql.DB {
	t.Helper()
	if url == "" {
		t.Fatal("empty connection URL")
	}
	handle, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { handle.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := handle.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return handle
}

func scalar(t *testing.T, handle *sql.DB, query string) string {
	t.Helper()
	var value string
	if err := handle.QueryRow(query).Scan(&value); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return value
}

// waitFor polls cond until it holds or the timeout passes.
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

// startHelper runs the test binary as a serve stand-in owning a cluster and
// returns it with the postmaster pid it printed.
func startHelper(t *testing.T, home, id, binaries string) (*exec.Cmd, int) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^$")
	cmd.Env = append(os.Environ(), helperEnv+"=own-cluster",
		"EMBEDDEDPG_TEST_HOME="+home, "EMBEDDEDPG_TEST_ID="+id, BinariesEnv+"="+binaries)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	lines := bufio.NewScanner(stdout)
	pid := 0
	done := make(chan string, 1)
	go func() {
		for lines.Scan() {
			line := lines.Text()
			if strings.HasPrefix(line, "PID ") || strings.HasPrefix(line, "ERROR ") {
				done <- line
				return
			}
		}
		done <- "EOF"
	}()
	select {
	case line := <-done:
		if _, err := fmt.Sscanf(line, "PID %d", &pid); err != nil || pid == 0 {
			t.Fatalf("helper: %s", line)
		}
	case <-time.After(2 * time.Minute):
		t.Fatal("helper never reported a postmaster")
	}
	return cmd, pid
}

func sqlOpen(url string) (*sql.DB, error) { return sql.Open("pgx", url) }
