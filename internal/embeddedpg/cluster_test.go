package embeddedpg

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ---------- no binaries needed ----------

func TestPostgresIsInOwnProcessGroup(t *testing.T) {
	assertOwnGroup(t, spawnAttrs())
}

func TestSecretFilesOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := WriteSecretFile(path, []byte("s3cret-password")); err != nil {
		t.Fatalf("WriteSecretFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "s3cret-password" {
		t.Fatalf("read back %q, %v", data, err)
	}
	assertOwnerOnly(t, path)
	// Rewriting keeps it owner-only (an existing looser file is not reused).
	_ = os.Chmod(path, 0o644)
	if err := WriteSecretFile(path, []byte("rotated")); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	assertOwnerOnly(t, path)
}

// deadPID is the pid of a process that has exited.
func deadPID(t *testing.T) int {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatalf("helper: %v", err)
	}
	pid := cmd.Process.Pid
	if processAlive(pid) {
		t.Skip("pid reused immediately")
	}
	return pid
}

func writePostmasterPID(t *testing.T, dataDir string, pid int) string {
	t.Helper()
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dataDir, "postmaster.pid")
	content := strconv.Itoa(pid) + "\n" + dataDir + "\n1761600000\n54321\n\n127.0.0.1\n  5432001         0\nready   \n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStalePidOfDeadProcessRecovered(t *testing.T) {
	home, id := t.TempDir(), randomID(t)
	release, err := Lock(home, id)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	defer release()
	dataDir := filepath.Join(ClusterDir(home, id), "data")
	writePostmasterPID(t, dataDir, deadPID(t))
	recovery, err := RecoverStalePID(context.Background(), "", dataDir)
	if err != nil || recovery != RecoveryDeadPID {
		t.Fatalf("RecoverStalePID = %q, %v; want %q", recovery, err, RecoveryDeadPID)
	}
}

// Windows reuses pids quickly: a live process that is not our postmaster must
// not keep the cluster from starting.
func TestStalePidOfUnrelatedLiveProcessRecovered(t *testing.T) {
	home, id := t.TempDir(), randomID(t)
	release, err := Lock(home, id)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	defer release()
	dataDir := filepath.Join(ClusterDir(home, id), "data")
	path := writePostmasterPID(t, dataDir, os.Getpid())
	recovery, err := RecoverStalePID(context.Background(), "", dataDir)
	if err != nil || recovery != RecoveryUnrelatedPID {
		t.Fatalf("RecoverStalePID = %q, %v; want %q", recovery, err, RecoveryUnrelatedPID)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("postmaster.pid of an unrelated process was kept: %v", err)
	}
	if !processAlive(os.Getpid()) {
		t.Fatal("recovery touched the unrelated process")
	}
}

func TestSecondOwnerRefused(t *testing.T) {
	home, id := t.TempDir(), randomID(t)
	release, err := Lock(home, id)
	if err != nil {
		t.Fatalf("first Lock: %v", err)
	}
	if _, err := Lock(home, id); !errors.Is(err, ErrClusterBusy) {
		t.Fatalf("second Lock = %v, want ErrClusterBusy", err)
	}
	release()
	again, err := Lock(home, id)
	if err != nil {
		t.Fatalf("Lock after release: %v", err)
	}
	again()
	// A different cluster id is a different lock.
	other, err := Lock(home, randomID(t))
	if err != nil {
		t.Fatalf("Lock of another id: %v", err)
	}
	other()
}

func writeRun(t *testing.T, home, id string, info RunInfo) {
	t.Helper()
	dir := ClusterDir(home, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(info)
	if err := os.WriteFile(filepath.Join(dir, "run.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secret"), []byte("app-password"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestClientDiscoversRunningCluster(t *testing.T) {
	home, id := t.TempDir(), randomID(t)
	release, err := Lock(home, id)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	defer release()
	writeRun(t, home, id, RunInfo{PID: os.Getpid(), Port: 54999, Version: PinnedVersion, StartedAt: time.Now().UTC().Format(time.RFC3339)})
	info, url, ok, err := Discover(home, id)
	if err != nil || !ok {
		t.Fatalf("Discover = %v, %v", ok, err)
	}
	if info.PID != os.Getpid() || info.Port != 54999 {
		t.Fatalf("info = %+v", info)
	}
	if !strings.Contains(url, "127.0.0.1:54999") || !strings.Contains(url, "nodevas") {
		t.Fatalf("url does not name the loopback port and app role")
	}
}

func TestStaleRunJSONIsIgnored(t *testing.T) {
	home, id := t.TempDir(), randomID(t)
	// Nobody holds the lock: run.json is a crashed owner's leftover, whatever
	// its pid says.
	writeRun(t, home, id, RunInfo{PID: os.Getpid(), Port: 54998, Version: PinnedVersion})
	if _, _, ok, err := Discover(home, id); err != nil || ok {
		t.Fatalf("Discover without an owner = %v, %v; want not found", ok, err)
	}
	writeRun(t, home, id, RunInfo{PID: deadPID(t), Port: 54997, Version: PinnedVersion})
	if _, _, ok, err := Discover(home, id); err != nil || ok {
		t.Fatalf("Discover of a dead pid = %v, %v; want not found", ok, err)
	}
}

// ---------- real binaries ----------

func hbaLines(t *testing.T, dataDir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dataDir, "pg_hba.conf"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, strings.Join(strings.Fields(line), " "))
	}
	return out
}

func TestClusterListensOnLoopbackOnly(t *testing.T) {
	c := startCluster(t)
	app := connect(t, c.URL())
	if got := scalar(t, app, `SHOW listen_addresses`); got != "127.0.0.1" {
		t.Fatalf("listen_addresses = %q", got)
	}
	if got := scalar(t, app, `SHOW unix_socket_directories`); got != "" {
		t.Fatalf("unix_socket_directories = %q, want none", got)
	}
	if got := hbaLines(t, c.DataDir()); len(got) != 1 || got[0] != "host all nodevas,nodevas_admin 127.0.0.1/32 scram-sha-256" {
		t.Fatalf("pg_hba.conf rules = %q", got)
	}
	addrs, _ := net.InterfaceAddrs()
	for _, addr := range addrs {
		ip, _, _ := net.ParseCIDR(addr.String())
		if ip == nil || ip.IsLoopback() || ip.To4() == nil {
			continue
		}
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(ip.String(), strconv.Itoa(c.Port())), time.Second)
		if err == nil {
			conn.Close()
			t.Fatalf("the cluster answered on %s", ip)
		}
	}
}

func TestAlterSystemCannotWidenListen(t *testing.T) {
	c := startCluster(t)
	admin := connect(t, c.AdminURL())
	if _, err := admin.Exec(`ALTER SYSTEM SET listen_addresses = '*'`); err != nil {
		t.Fatalf("ALTER SYSTEM: %v", err)
	}
	admin.Close()
	ctx := context.Background()
	if err := c.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := c.Start(ctx); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if got := scalar(t, connect(t, c.URL()), `SHOW listen_addresses`); got != "127.0.0.1" {
		t.Fatalf("listen_addresses after ALTER SYSTEM = %q", got)
	}
}

func TestAppRoleIsNotSuperuser(t *testing.T) {
	c := startCluster(t)
	app := connect(t, c.URL())
	if got := scalar(t, app, `SELECT current_user`); got != "nodevas" {
		t.Fatalf("app connects as %q", got)
	}
	if got := scalar(t, app, `SELECT rolsuper::text FROM pg_roles WHERE rolname = current_user`); got != "false" {
		t.Fatalf("rolsuper = %s", got)
	}
	for _, statement := range []string{
		`COPY (SELECT 1) TO PROGRAM 'echo pwned'`,
		`ALTER SYSTEM SET work_mem = '1MB'`,
		`SELECT pg_read_file('postgresql.conf')`,
		`CREATE ROLE intruder SUPERUSER`,
	} {
		if _, err := app.Exec(statement); err == nil {
			t.Errorf("app role may run %s", statement)
		}
	}
}

func TestCredentialsNeverLogged(t *testing.T) {
	c := startCluster(t)
	secrets := map[string]string{}
	for _, name := range []string{"secret", "admin-secret"} {
		data, err := os.ReadFile(filepath.Join(c.Dir(), name))
		if err != nil || len(strings.TrimSpace(string(data))) < 32 {
			t.Fatalf("%s: %q, %v", name, data, err)
		}
		secrets[name] = strings.TrimSpace(string(data))
		assertOwnerOnly(t, filepath.Join(c.Dir(), name))
	}
	// A failed sign-in, which is when a careless server logs the most.
	wrong := strings.Replace(c.URL(), secrets["secret"], "wrong-password", 1)
	if wrong == c.URL() {
		t.Fatal("the app password is not the one in the secret file")
	}
	if handle, err := sqlOpen(wrong); err == nil {
		if err := handle.Ping(); err == nil {
			t.Fatal("a wrong password was accepted")
		}
		handle.Close()
	}
	if err := c.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	haystacks := map[string]string{"slog": c.logs.String()}
	logs, _ := filepath.Glob(filepath.Join(c.DataDir(), "log", "*"))
	for _, path := range logs {
		data, _ := os.ReadFile(path)
		haystacks[path] = string(data)
	}
	for where, text := range haystacks {
		for name, secret := range secrets {
			if strings.Contains(text, secret) {
				t.Errorf("%s holds the %s password", where, name)
			}
		}
	}
}

func readLogs(t *testing.T, dataDir string) string {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(dataDir, "log", "*"))
	var out strings.Builder
	for _, path := range paths {
		data, _ := os.ReadFile(path)
		out.Write(data)
	}
	return out.String()
}

func TestGracefulStopLeavesCleanShutdown(t *testing.T) {
	c := startCluster(t)
	ctx := context.Background()
	if err := c.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := os.Stat(filepath.Join(c.DataDir(), "postmaster.pid")); !os.IsNotExist(err) {
		t.Fatalf("postmaster.pid survived a graceful stop: %v", err)
	}
	if err := c.Start(ctx); err != nil {
		t.Fatalf("restart: %v", err)
	}
	logs := readLogs(t, c.DataDir())
	for _, bad := range []string{"not properly shut down", "was interrupted", "automatic recovery in progress"} {
		if strings.Contains(logs, bad) {
			t.Fatalf("restart after a graceful stop logged %q", bad)
		}
	}
}

func TestOrphanPostmasterIsStoppedAndRestarted(t *testing.T) {
	c := startCluster(t)
	orphan := c.PID()
	c.abandon()
	if !processAlive(orphan) {
		t.Fatal("abandon stopped the postmaster")
	}
	next := New(c.opts)
	if err := next.Start(context.Background()); err != nil {
		t.Fatalf("Start over an orphan: %v", err)
	}
	t.Cleanup(func() { _ = next.Stop(context.Background()) })
	waitFor(t, 15*time.Second, "the orphan to exit", func() bool { return !processAlive(orphan) })
	if next.PID() == orphan || next.PID() == 0 {
		t.Fatalf("postmaster pid %d, orphan was %d", next.PID(), orphan)
	}
	_ = scalar(t, connect(t, next.URL()), `SELECT 1::text`)
}

func TestPostmasterDiesWithOwner(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS has no parent-death signal; the next start recovers the orphan instead")
	}
	binaries := realBinaries(t)
	home, id := t.TempDir(), randomID(t)
	helper, postmaster := startHelper(t, home, id, binaries)
	if !processAlive(postmaster) {
		t.Fatal("postmaster not running")
	}
	_ = helper.Process.Kill() // TerminateProcess / SIGKILL: no shutdown code runs
	_ = helper.Wait()
	waitFor(t, 5*time.Second, "the postmaster to die with its owner", func() bool { return !processAlive(postmaster) })
}

func TestUnexpectedExitRestartsOnSamePort(t *testing.T) {
	c := startCluster(t)
	port, first := c.Port(), c.PID()
	process, err := os.FindProcess(first)
	if err != nil {
		t.Fatal(err)
	}
	_ = process.Kill()
	waitFor(t, 30*time.Second, "a restarted postmaster", func() bool {
		return c.PID() != 0 && c.PID() != first && processAlive(c.PID())
	})
	if c.Port() != port {
		t.Fatalf("restarted on port %d, was %d", c.Port(), port)
	}
	_ = scalar(t, connect(t, c.URL()), `SELECT 1::text`)
	if !strings.Contains(c.logs.String(), "db.embedded_exited") {
		t.Fatal("the unexpected exit was not logged as db.embedded_exited")
	}
}

func TestPortConflictRetries(t *testing.T) {
	c := startCluster(t)
	ctx := context.Background()
	port := c.Port()
	if err := c.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Someone else takes the port run.json remembers.
	squatter, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Skipf("port %d not free for the squatter: %v", port, err)
	}
	defer squatter.Close()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start with the last port taken: %v", err)
	}
	if c.Port() == port || c.Port() == 0 {
		t.Fatalf("port %d after a conflict on %d", c.Port(), port)
	}
	_ = scalar(t, connect(t, c.URL()), `SELECT 1::text`)
}

func TestReadyEventReportsTimingAndDisk(t *testing.T) {
	c := startCluster(t)
	var ready map[string]any
	for _, line := range strings.Split(c.logs.String(), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) == nil && record["event.code"] == "db.embedded_ready" {
			ready = record
		}
	}
	if ready == nil {
		t.Fatalf("no db.embedded_ready event in:\n%s", c.logs.String())
	}
	for _, key := range []string{"initdb_ms", "start_ms", "data_bytes", "port", "version"} {
		if _, ok := ready[key]; !ok {
			t.Errorf("db.embedded_ready lacks %s: %v", key, ready)
		}
	}
	if bytes, _ := ready["data_bytes"].(float64); bytes <= 0 {
		t.Errorf("data_bytes = %v", ready["data_bytes"])
	}
	if ready["version"] != PinnedVersion {
		t.Errorf("version = %v", ready["version"])
	}
}

func TestICUCollationAvailable(t *testing.T) {
	c := startCluster(t)
	app := connect(t, c.URL())
	if _, err := app.Exec(`CREATE COLLATION nocase_probe (provider = icu, locale = 'und-u-ks-level2', deterministic = false)`); err != nil {
		t.Fatalf("ICU collation (migration 0001 needs it): %v", err)
	}
	if got := scalar(t, app, `SELECT ('ABC' = 'abc' COLLATE nocase_probe)::text`); got != "true" {
		t.Fatalf("case-insensitive compare = %s", got)
	}
}
