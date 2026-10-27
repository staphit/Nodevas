package embeddedpg

import (
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"nodevas/internal/lockfile"
)

// Role names. The app connects as appRole, which is not a superuser: it
// cannot COPY ... PROGRAM, ALTER SYSTEM or read server files. adminRole
// exists for initdb and for an operator.
const (
	appRole   = "nodevas"
	adminRole = "nodevas_admin"
	appDB     = "nodevas"
)

// hbaRule is the whole of pg_hba.conf: password (SCRAM) logins over IPv4
// loopback for the two roles, nothing else — no Unix socket, no ::1, no
// trust.
const hbaRule = "host all " + appRole + "," + adminRole + " 127.0.0.1/32 scram-sha-256"

// Files in a cluster directory.
const (
	secretFile      = "secret"
	adminSecretFile = "admin-secret"
	runFile         = "run.json"
	lockFile        = "owner.lock"
	workspaceFile   = "workspace.txt"
	initializedFile = "initialized"
)

// Restart budget for a postmaster that exits on its own.
const (
	maxRestarts   = 3
	restartWindow = time.Minute
)

// Options describes one workspace's cluster.
type Options struct {
	// Home is the embedded root; the cluster lives in Home/clusters/ID.
	Home string
	// ID is the workspace's database id (<workspace>/.vised/database-id).
	ID string
	// Binaries is an installed, verified binaries directory.
	Binaries string
	// Workspace is recorded in workspace.txt for an operator.
	Workspace string
	// Logger receives db.embedded_* events; nil is slog.Default.
	Logger *slog.Logger
	// ReadyTimeout bounds a start; zero is 60s for a first start, 30s after.
	ReadyTimeout time.Duration
}

// RunInfo is run.json: who runs the cluster and where.
type RunInfo struct {
	PID       int    `json:"pid"`
	Port      int    `json:"port"`
	Version   string `json:"version"`
	StartedAt string `json:"startedAt"`
	// Owner is the process that started the postmaster (serve).
	Owner int `json:"owner,omitempty"`
}

// Cluster is a started (or startable) embedded server.
type Cluster struct {
	opts   Options
	exited chan error

	mu        sync.Mutex
	lock      *lockfile.Lock
	proc      *postmaster
	port      int
	appPass   string
	adminPass string
	stopping  bool
	restarts  []time.Time
}

// ClusterDir is where cluster id lives under home.
func ClusterDir(home, id string) string { return filepath.Join(home, "clusters", id) }

// New prepares a cluster; nothing happens until Start.
func New(opts Options) *Cluster { return &Cluster{opts: opts, exited: make(chan error, 1)} }

func (c *Cluster) logger() *slog.Logger {
	if c.opts.Logger != nil {
		return c.opts.Logger
	}
	return slog.Default()
}

// Dir is ClusterDir(Home, ID).
func (c *Cluster) Dir() string { return ClusterDir(c.opts.Home, c.opts.ID) }

// DataDir is the PostgreSQL data directory.
func (c *Cluster) DataDir() string { return filepath.Join(c.Dir(), "data") }

// Exited reports a postmaster supervision gave up on: it died more often than
// the restart budget allows. Serve cancels itself when it fires.
func (c *Cluster) Exited() <-chan error { return c.exited }

// URL is the app role's connection string (the non-superuser nodevas).
func (c *Cluster) URL() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.port == 0 || c.appPass == "" {
		return ""
	}
	return loopbackURL(appRole, c.appPass, c.port, appDB)
}

// AdminURL is the cluster administrator's connection string.
func (c *Cluster) AdminURL() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.port == 0 || c.adminPass == "" {
		return ""
	}
	return loopbackURL(adminRole, c.adminPass, c.port, "postgres")
}

// Port is the loopback port the postmaster listens on.
func (c *Cluster) Port() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.port
}

// PID is the running postmaster's process id.
func (c *Cluster) PID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.proc == nil {
		return 0
	}
	return c.proc.pid
}

func loopbackURL(role, password string, port int, database string) string {
	// The passwords are hex, so nothing in them needs escaping.
	return fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/%s?sslmode=disable&connect_timeout=5", role, password, port, database)
}

// Start takes the owner lock, initdbs on first use, recovers a stale
// postmaster.pid, starts the postmaster on 127.0.0.1 and waits until the app
// role can run SELECT 1.
func (c *Cluster) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.proc != nil && !c.proc.exited() {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	if err := refuseRoot(); err != nil {
		return err
	}
	if c.opts.Home == "" || c.opts.ID == "" || c.opts.Binaries == "" {
		return errors.New("embeddedpg: a cluster needs a home, an id and the binaries")
	}
	began := time.Now()
	dir := c.Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := restrictDir(dir); err != nil {
		return fmt.Errorf("embeddedpg: restrict %s: %w", dir, err)
	}
	lock, err := acquire(c.opts.Home, c.opts.ID)
	if err != nil {
		return err
	}
	started := false
	defer func() {
		if !started {
			_ = lock.Release()
		}
	}()
	if c.opts.Workspace != "" {
		_ = WriteSecretFile(filepath.Join(dir, workspaceFile), []byte(c.opts.Workspace+"\n"))
	}

	first := !fileExists(filepath.Join(dir, initializedFile))
	var initdbTook time.Duration
	if first {
		t := time.Now()
		if err := c.initdb(ctx); err != nil {
			return err
		}
		initdbTook = time.Since(t)
	}
	appPass, err := readSecret(filepath.Join(dir, secretFile))
	if err != nil {
		return err
	}
	adminPass, err := readSecret(filepath.Join(dir, adminSecretFile))
	if err != nil {
		return err
	}
	// Rewritten on every start: whatever edited it, the rule is this one.
	if err := writeHBA(c.DataDir(), hbaRule); err != nil {
		return err
	}
	recovery, err := RecoverStalePID(ctx, c.opts.Binaries, c.DataDir())
	if err != nil {
		return err
	}
	if recovery != RecoveryNone {
		c.logger().Info("embedded PostgreSQL recovered a stale postmaster.pid",
			slog.String("event.code", "db.embedded_recovered"), slog.String("recovery", string(recovery)))
	}

	timeout := c.opts.ReadyTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
		if first {
			timeout = 60 * time.Second
		}
	}
	startedAt := time.Now()
	proc, port, err := c.launchOnFreePort(ctx, lastPort(dir), timeout, adminPass, first)
	if err != nil {
		return err
	}
	if first {
		if err := createAppRole(ctx, port, adminPass, appPass); err != nil {
			_ = stopPostmaster(context.Background(), proc)
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, initializedFile), []byte(PinnedVersion+"\n"), 0o600); err != nil {
			_ = stopPostmaster(context.Background(), proc)
			return err
		}
	}
	if err := waitReady(ctx, proc, loopbackURL(appRole, appPass, port, appDB), timeout); err != nil {
		_ = stopPostmaster(context.Background(), proc)
		return err
	}
	startTook := time.Since(startedAt)

	c.mu.Lock()
	c.lock, c.proc, c.port, c.appPass, c.adminPass = lock, proc, port, appPass, adminPass
	c.stopping, c.restarts = false, nil
	c.mu.Unlock()
	started = true
	c.writeRun(proc, port)
	go c.supervise(proc)

	c.logger().Info("embedded PostgreSQL ready",
		slog.String("event.code", "db.embedded_ready"),
		slog.String("version", PinnedVersion),
		slog.Int("port", port),
		slog.Int("pid", proc.pid),
		slog.Bool("first_start", first),
		slog.Int64("initdb_ms", initdbTook.Milliseconds()),
		slog.Int64("start_ms", startTook.Milliseconds()),
		slog.Int64("total_ms", time.Since(began).Milliseconds()),
		slog.Int64("data_bytes", dirSize(c.DataDir())),
		slog.String("data_dir", c.DataDir()))
	return nil
}

// launchOnFreePort starts the postmaster on the preferred port, or on fresh
// free ports when that one is taken, and waits for it to accept the admin
// role.
func (c *Cluster) launchOnFreePort(ctx context.Context, preferred int, timeout time.Duration, adminPass string, first bool) (*postmaster, int, error) {
	port := preferred
	if port == 0 || !portFree(port) {
		var err error
		if port, err = freePort(); err != nil {
			return nil, 0, err
		}
	}
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		proc, err := startPostmaster(c.opts.Binaries, c.DataDir(), serverArgs(c.DataDir(), port, clusterSettings))
		if err != nil {
			return nil, 0, err
		}
		err = waitReady(ctx, proc, loopbackURL(adminRole, adminPass, port, "postgres"), timeout)
		if err == nil {
			return proc, port, nil
		}
		_ = stopPostmaster(context.Background(), proc)
		lastErr = err
		if !isBindError(err) {
			return nil, 0, err
		}
		if port, err = freePort(); err != nil {
			return nil, 0, err
		}
	}
	return nil, 0, lastErr
}

// clusterSettings are the command-line settings of a workspace cluster. A
// command-line setting outranks postgresql.conf and postgresql.auto.conf, so
// nothing an ALTER SYSTEM writes can widen what the server listens on or who
// it lets in.
var clusterSettings = []string{
	"listen_addresses=127.0.0.1",
	"unix_socket_directories=",
	"password_encryption=scram-sha-256",
	"ssl=off",
	"max_connections=40",
	"shared_buffers=32MB",
	"jit=off",
	"logging_collector=on",
	"log_directory=log",
	"log_filename=postgresql-%a.log",
	"log_rotation_age=1d",
	"log_rotation_size=10MB",
	"log_truncate_on_rotation=on",
	"log_statement=none",
	"log_min_error_statement=panic",
	"log_connections=off",
	"log_disconnections=off",
	"max_wal_size=256MB",
	"min_wal_size=32MB",
	"lc_messages=C",
}

func serverArgs(dataDir string, port int, settings []string) []string {
	args := []string{"-D", dataDir, "-p", strconv.Itoa(port),
		"-c", "hba_file=" + filepath.ToSlash(filepath.Join(dataDir, "pg_hba.conf"))}
	for _, setting := range settings {
		args = append(args, "-c", setting)
	}
	return args
}

// initdb creates the data directory, the administrator and the passwords.
// A directory left half-made by an interrupted first start is wiped: nothing
// but initdb's own files can be in it yet.
func (c *Cluster) initdb(ctx context.Context) error {
	dir := c.Dir()
	if err := os.RemoveAll(c.DataDir()); err != nil {
		return fmt.Errorf("embeddedpg: clear %s: %w", c.DataDir(), err)
	}
	appPass, adminPass := randomHex(32), randomHex(32)
	if err := WriteSecretFile(filepath.Join(dir, secretFile), []byte(appPass+"\n")); err != nil {
		return err
	}
	if err := WriteSecretFile(filepath.Join(dir, adminSecretFile), []byte(adminPass+"\n")); err != nil {
		return err
	}
	if err := initdb(ctx, c.opts.Binaries, c.DataDir(), adminPass, nil); err != nil {
		return err
	}
	c.logger().Info("embedded PostgreSQL initialized",
		slog.String("event.code", "db.embedded_initdb"), slog.String("data_dir", c.DataDir()))
	return nil
}

// initdb runs initdb for adminRole with a password passed through a file that
// exists only for the call (never argv, never the environment).
func initdb(ctx context.Context, binaries, dataDir, adminPass string, extra []string) error {
	pwfile := filepath.Join(filepath.Dir(dataDir), ".pwfile-"+randomHex(6))
	if err := WriteSecretFile(pwfile, []byte(adminPass+"\n")); err != nil {
		return err
	}
	defer os.Remove(pwfile)
	args := append([]string{"-D", dataDir, "-U", adminRole, "--pwfile=" + pwfile,
		"-A", "scram-sha-256", "-E", "UTF8", "--locale=C", "--no-instructions"}, extra...)
	if err := run(ctx, binaries, "initdb", args...); err != nil {
		return fmt.Errorf("embeddedpg: %w", err)
	}
	return nil
}

func writeHBA(dataDir, rule string) error {
	content := "# Written by Nodevas on every start; edits are replaced.\n" + rule + "\n"
	return os.WriteFile(filepath.Join(dataDir, "pg_hba.conf"), []byte(content), 0o600)
}

// createAppRole makes the non-superuser app role and its database. The
// password reaches the server only as a SCRAM verifier computed here, so not
// even a statement log could hold it.
func createAppRole(ctx context.Context, port int, adminPass, appPass string) error {
	conn, err := connectOnce(ctx, loopbackURL(adminRole, adminPass, port, "postgres"))
	if err != nil {
		return fmt.Errorf("embeddedpg: connect as administrator: %w", err)
	}
	defer conn.Close(context.Background())
	verifier, err := scramVerifier(appPass)
	if err != nil {
		return err
	}
	for _, statement := range []string{
		`CREATE ROLE ` + appRole + ` LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '` + verifier + `'`,
		`CREATE DATABASE ` + appDB + ` OWNER ` + appRole + ` ENCODING 'UTF8' TEMPLATE template0`,
		`REVOKE ALL ON DATABASE postgres FROM PUBLIC`,
		// Read-only access to the server settings, so the app (and an operator
		// connected as it) can check what the server listens on.
		`GRANT pg_read_all_settings TO ` + appRole,
	} {
		if _, err := conn.Exec(ctx, statement); err != nil {
			// The statement is not echoed: the first one carries the verifier.
			return fmt.Errorf("embeddedpg: create the app role and database: %w", pgErrorOnly(err))
		}
	}
	return nil
}

// pgErrorOnly keeps a server error's message and drops anything else.
func pgErrorOnly(err error) error {
	message := err.Error()
	if i := strings.Index(message, "PASSWORD"); i >= 0 {
		message = message[:i]
	}
	return errors.New(message)
}

// scramVerifier is PostgreSQL's stored form of a SCRAM-SHA-256 password.
func scramVerifier(password string) (string, error) {
	const iterations = 4096
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	salted, err := pbkdf2.Key(sha256.New, password, salt, iterations, sha256.Size)
	if err != nil {
		return "", err
	}
	mac := func(key []byte, message string) []byte {
		h := hmac.New(sha256.New, key)
		h.Write([]byte(message))
		return h.Sum(nil)
	}
	clientKey := mac(salted, "Client Key")
	storedKey := sha256.Sum256(clientKey)
	serverKey := mac(salted, "Server Key")
	b64 := base64.StdEncoding.EncodeToString
	return fmt.Sprintf("SCRAM-SHA-256$%d:%s$%s:%s", iterations, b64(salt), b64(storedKey[:]), b64(serverKey)), nil
}

func connectOnce(ctx context.Context, url string) (*pgx.Conn, error) {
	attempt, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return pgx.Connect(attempt, url)
}

// errBind is a postmaster that could not listen on its port.
var errBind = errors.New("embeddedpg: the port is in use")

func isBindError(err error) bool { return errors.Is(err, errBind) }

// waitReady polls url with SELECT 1 until it answers, the process exits or
// the timeout passes.
func waitReady(ctx context.Context, proc *postmaster, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if proc.exited() {
			return exitError(proc)
		}
		conn, err := connectOnce(ctx, url)
		if err == nil {
			var one int
			err = conn.QueryRow(ctx, `SELECT 1`).Scan(&one)
			conn.Close(context.Background())
			if err == nil {
				return nil
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("embeddedpg: PostgreSQL did not accept connections within %s: %v%s", timeout, describeConnectError(err), outputSuffix(proc))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// describeConnectError drops anything but the reason: a pgx connect error
// names the user and host, never the password, but keep it short anyway.
func describeConnectError(err error) string {
	if err == nil {
		return "no answer"
	}
	return err.Error()
}

// exitError explains a postmaster that ended before it was ready.
func exitError(proc *postmaster) error {
	output := proc.output.String() + "\n" + latestLog(proc.dataDir)
	if strings.Contains(output, "could not bind") || strings.Contains(output, "Address already in use") ||
		strings.Contains(output, "could not create any TCP/IP sockets") {
		return fmt.Errorf("%w: %s", errBind, lastLines(output, 5))
	}
	return fmt.Errorf("embeddedpg: PostgreSQL exited during startup (%v)%s", proc.waitErr, outputSuffix(proc))
}

func outputSuffix(proc *postmaster) string {
	output := strings.TrimSpace(proc.output.String() + "\n" + latestLog(proc.dataDir))
	if output == "" {
		return ""
	}
	return ":\n" + lastLines(output, 12)
}

func lastLines(text string, n int) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// latestLog is the tail of the most recently written server log file.
func latestLog(dataDir string) string {
	paths, _ := filepath.Glob(filepath.Join(dataDir, "log", "*"))
	var newest string
	var newestTime time.Time
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && info.ModTime().After(newestTime) {
			newest, newestTime = path, info.ModTime()
		}
	}
	if newest == "" {
		return ""
	}
	data, err := os.ReadFile(newest)
	if err != nil {
		return ""
	}
	if len(data) > 8<<10 {
		data = data[len(data)-8<<10:]
	}
	return string(data)
}

// supervise waits on a postmaster and restarts it on the same port when it
// exits without being asked to: up to maxRestarts in restartWindow, then it
// gives up and reports on Exited.
func (c *Cluster) supervise(proc *postmaster) {
	<-proc.done
	c.mu.Lock()
	if c.stopping || c.proc != proc {
		c.mu.Unlock()
		return
	}
	port, appPass := c.port, c.appPass
	c.mu.Unlock()
	c.logger().Warn("embedded PostgreSQL exited unexpectedly",
		slog.String("event.code", "db.embedded_exited"),
		slog.Int("pid", proc.pid), slog.String("exit", fmt.Sprint(proc.waitErr)))
	releaseProc(proc)

	var lastErr error
	for {
		c.mu.Lock()
		now := time.Now()
		recent := c.restarts[:0]
		for _, at := range c.restarts {
			if now.Sub(at) < restartWindow {
				recent = append(recent, at)
			}
		}
		c.restarts = recent
		if len(c.restarts) >= maxRestarts || c.stopping || c.proc != proc {
			giveUp := !c.stopping && c.proc == proc
			c.mu.Unlock()
			if giveUp {
				if lastErr == nil {
					lastErr = fmt.Errorf("PostgreSQL exited %d times within %s", maxRestarts, restartWindow)
				}
				c.logger().Error("embedded PostgreSQL keeps exiting; giving up",
					slog.String("event.code", "db.embedded_failed"), slog.String("error", lastErr.Error()))
				select {
				case c.exited <- lastErr:
				default:
				}
			}
			return
		}
		c.restarts = append(c.restarts, now)
		attempt := len(c.restarts)
		c.mu.Unlock()

		time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		next, err := c.restartOn(port, appPass)
		if err != nil {
			lastErr = err
			continue
		}
		c.mu.Lock()
		if c.stopping || c.proc != proc {
			c.mu.Unlock()
			_ = stopPostmaster(context.Background(), next)
			return
		}
		c.proc = next
		c.mu.Unlock()
		c.writeRun(next, port)
		c.logger().Info("embedded PostgreSQL restarted",
			slog.String("event.code", "db.embedded_restarted"), slog.Int("pid", next.pid), slog.Int("port", port))
		go c.supervise(next)
		return
	}
}

// restartOn starts the server again on the same port, so open pools
// reconnect where they were. Backends of the dead postmaster may hold its
// shared memory for a moment; the start is retried until they are gone.
func (c *Cluster) restartOn(port int, appPass string) (*postmaster, error) {
	deadline := time.Now().Add(20 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if _, err := RecoverStalePID(context.Background(), c.opts.Binaries, c.DataDir()); err != nil {
			lastErr = err
		}
		proc, err := startPostmaster(c.opts.Binaries, c.DataDir(), serverArgs(c.DataDir(), port, clusterSettings))
		if err != nil {
			return nil, err
		}
		err = waitReady(context.Background(), proc, loopbackURL(appRole, appPass, port, appDB), 30*time.Second)
		if err == nil {
			return proc, nil
		}
		_ = stopPostmaster(context.Background(), proc)
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	return nil, lastErr
}

// Stop shuts the postmaster down fast and releases the owner lock.
func (c *Cluster) Stop(ctx context.Context) error {
	c.mu.Lock()
	proc, lock := c.proc, c.lock
	c.stopping = true
	c.mu.Unlock()
	var err error
	if proc != nil {
		err = stopPostmaster(ctx, proc)
		c.logger().Info("embedded PostgreSQL stopped",
			slog.String("event.code", "db.embedded_stopped"), slog.Int("pid", proc.pid))
	}
	c.mu.Lock()
	c.proc = nil
	c.lock = nil
	c.mu.Unlock()
	// run.json stays: it names the port to try first next time, and without
	// the owner lock nobody mistakes it for a running server.
	if lock != nil {
		_ = lock.Release()
	}
	return err
}

// abandon drops supervision and the owner lock without stopping the
// postmaster, leaving the orphan a crashed serve on macOS would leave. Tests
// only.
func (c *Cluster) abandon() {
	c.mu.Lock()
	lock := c.lock
	c.proc, c.lock = nil, nil
	c.mu.Unlock()
	if lock != nil {
		_ = lock.Release()
	}
}

func (c *Cluster) writeRun(proc *postmaster, port int) {
	info := RunInfo{PID: proc.pid, Port: port, Version: PinnedVersion,
		StartedAt: time.Now().UTC().Format(time.RFC3339), Owner: os.Getpid()}
	data, _ := json.MarshalIndent(info, "", "  ")
	if err := WriteSecretFile(filepath.Join(c.Dir(), runFile), append(data, '\n')); err != nil {
		c.logger().Warn("could not write run.json", slog.String("error", err.Error()))
	}
}

func readRun(dir string) (RunInfo, bool) {
	data, err := os.ReadFile(filepath.Join(dir, runFile))
	if err != nil {
		return RunInfo{}, false
	}
	var info RunInfo
	if json.Unmarshal(data, &info) != nil || info.Port <= 0 {
		return RunInfo{}, false
	}
	return info, true
}

func lastPort(dir string) int {
	info, ok := readRun(dir)
	if !ok {
		return 0
	}
	return info.Port
}

func readSecret(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("embeddedpg: %s: %w", filepath.Base(path), err)
	}
	secret := strings.TrimSpace(string(data))
	if secret == "" {
		return "", fmt.Errorf("embeddedpg: %s is empty", path)
	}
	return secret, nil
}

func acquire(home, id string) (*lockfile.Lock, error) {
	dir := ClusterDir(home, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	lock, err := lockfile.Acquire(filepath.Join(dir, lockFile))
	if errors.Is(err, lockfile.ErrLocked) {
		return nil, fmt.Errorf("%w (%s)", ErrClusterBusy, dir)
	}
	return lock, err
}

// Lock takes cluster id's owner lock without starting anything. It returns
// ErrClusterBusy when another holder has it.
func Lock(home, id string) (release func(), err error) {
	lock, err := acquire(home, id)
	if err != nil {
		return nil, err
	}
	return func() { _ = lock.Release() }, nil
}

// Discover finds a cluster another live process owns: its lock is held and
// run.json names a live pid. It returns the app URL to connect to.
func Discover(home, id string) (info RunInfo, url string, ok bool, err error) {
	state, info, url, err := Probe(home, id)
	return info, url, state == StateRunning, err
}

// State is what Probe found.
type State int

const (
	// StateFree: nobody owns the cluster (or it does not exist yet).
	StateFree State = iota
	// StateStarting: the owner lock is held but no live server is recorded
	// yet: an owner in the middle of its start.
	StateStarting
	// StateRunning: another process owns a running server.
	StateRunning
)

// Probe reports whether cluster id is owned and running.
func Probe(home, id string) (State, RunInfo, string, error) {
	dir := ClusterDir(home, id)
	if _, err := os.Stat(filepath.Join(dir, lockFile)); errors.Is(err, fs.ErrNotExist) {
		return StateFree, RunInfo{}, "", nil
	}
	lock, err := lockfile.Acquire(filepath.Join(dir, lockFile))
	if err == nil {
		_ = lock.Release()
		return StateFree, RunInfo{}, "", nil
	}
	if !errors.Is(err, lockfile.ErrLocked) {
		return StateFree, RunInfo{}, "", err
	}
	info, ok := readRun(dir)
	if !ok || !pidAlive(info.PID) {
		return StateStarting, RunInfo{}, "", nil
	}
	secret, err := readSecret(filepath.Join(dir, secretFile))
	if err != nil {
		return StateStarting, RunInfo{}, "", nil
	}
	return StateRunning, info, loopbackURL(appRole, secret, info.Port, appDB), nil
}

// Workspace is the workspace path a cluster records, "" when unknown.
func Workspace(home, id string) string {
	data, err := os.ReadFile(filepath.Join(ClusterDir(home, id), workspaceFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// Recovery is what RecoverStalePID did.
type Recovery string

const (
	// RecoveryNone: no postmaster.pid.
	RecoveryNone Recovery = "none"
	// RecoveryDeadPID: the pid is dead; PostgreSQL clears the file itself.
	RecoveryDeadPID Recovery = "dead-pid"
	// RecoveryUnrelatedPID: the pid is alive but is not our postmaster (pid
	// reuse); the file was removed under the owner lock.
	RecoveryUnrelatedPID Recovery = "unrelated-pid"
	// RecoveryOrphanStopped: our own orphaned postmaster was stopped.
	RecoveryOrphanStopped Recovery = "orphan-stopped"
)

// RecoverStalePID inspects dataDir/postmaster.pid before a start. The caller
// holds the owner lock.
func RecoverStalePID(ctx context.Context, binaries, dataDir string) (Recovery, error) {
	path := filepath.Join(dataDir, "postmaster.pid")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return RecoveryNone, nil
	}
	if err != nil {
		return RecoveryNone, err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	pid, _ := strconv.Atoi(strings.TrimSpace(lines[0]))
	if pid <= 0 || !pidAlive(pid) {
		return RecoveryDeadPID, nil
	}
	recordedDir := ""
	if len(lines) > 1 {
		recordedDir = strings.TrimSpace(lines[1])
	}
	image := strings.ToLower(strings.TrimSuffix(strings.ToLower(processImage(pid)), ".exe"))
	if image == "postgres" && pid != os.Getpid() && samePath(recordedDir, dataDir) {
		if err := stopOrphan(ctx, binaries, dataDir, pid); err != nil {
			return RecoveryNone, err
		}
		return RecoveryOrphanStopped, nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return RecoveryNone, fmt.Errorf("embeddedpg: remove stale %s: %w", path, err)
	}
	return RecoveryUnrelatedPID, nil
}

func samePath(a, b string) bool {
	clean := func(p string) string {
		p = filepath.Clean(filepath.FromSlash(p))
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			p = resolved
		}
		return p
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(clean(a), clean(b))
	}
	return clean(a) == clean(b)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func portFree(port int) bool {
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	listener.Close()
	return true
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if info, err := entry.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
