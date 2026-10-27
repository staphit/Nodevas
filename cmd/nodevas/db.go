package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"

	"nodevas/internal/db"
	"nodevas/internal/dbconn"
	"nodevas/internal/embeddedpg"
)

// database is `nodevas db`: where a workspace's data lives, backups, the
// embedded PostgreSQL's binaries, and the one-time SQLite import. The hidden
// actions (test-server, create-database, drop-database) exist for the e2e run.
func database(args []string) {
	if len(args) == 0 {
		usage()
		exit(2)
	}
	action := args[0]
	fs := flag.NewFlagSet("db "+action, flag.ExitOnError)
	projectFlag := fs.String("project", ".", "workspace directory")
	out := fs.String("out", "", "dump: the file to write (pg_dump custom format); install-postgres: install into this directory")
	in := fs.String("in", "", "restore: the dump to restore")
	archive := fs.String("archive", "", "install-postgres: install from this archive instead of downloading it")
	targetName := fs.String("target", "", "install-postgres: the platform to install (default: this one), e.g. windows-amd64")
	report := fs.Bool("report", false, "import-sqlite: compare the file with the database and import nothing")
	dryRun := fs.Bool("dry-run", false, "import-sqlite: same as --report")
	dir := fs.String("dir", "", "test-server: the scratch server's directory")
	urlFile := fs.String("url-file", "", "test-server: where to write the server's admin URL")
	adminURL := fs.String("url", "", "create-database, drop-database: the server's admin URL")
	name := fs.String("name", "", "create-database, drop-database: the database name")
	_ = fs.Parse(args[1:])

	root, err := filepath.Abs(*projectFlag)
	if err != nil {
		fatal(err)
	}
	ctx := context.Background()

	switch action {
	case "status":
		err = dbStatus(root, os.Stdout)
	case "dump":
		err = dbDump(ctx, root, *out)
	case "restore":
		err = dbRestore(ctx, root, *in)
	case "install-postgres":
		err = dbInstallPostgres(ctx, *archive, *targetName, *out)
	case "import-sqlite":
		err = importSQLiteCmd(ctx, root, *report || *dryRun, os.Stdout)
	case "detach":
		var id string
		if id, err = dbconn.Detach(root); err == nil {
			fmt.Printf("%s now has a database of its own (id %s); it is created empty at the next start\n", root, id)
		}
	case "test-server":
		err = dbTestServer(ctx, *dir, *urlFile)
	case "create-database":
		err = dbAdmin(ctx, *adminURL, *name, true)
	case "drop-database":
		err = dbAdmin(ctx, *adminURL, *name, false)
	default:
		usage()
		exit(2)
	}
	if err != nil {
		fatalf("db %s: %v", action, err)
	}
}

// dbStatus says where the workspace's data lives, without credentials.
func dbStatus(root string, w io.Writer) error {
	handle, err := db.OpenRoot(root)
	if err != nil {
		return err
	}
	defer handle.Close()
	atFatal = func() { handle.Close() }
	mode := handle.Mode()
	fmt.Fprintf(w, "mode: %s\n", strings.TrimSuffix(mode, "-client"))
	if mode == "external" {
		fmt.Fprintf(w, "server: %s\n", handle.Path())
	} else {
		fmt.Fprintf(w, "data dir: %s\n", handle.DataDir())
		fmt.Fprintf(w, "port: %d\n", handle.Port())
		fmt.Fprintf(w, "version: %s\n", embeddedpg.PinnedVersion)
		if mode == "embedded-client" {
			fmt.Fprintln(w, "owner: another process (a running serve) holds the server")
		}
	}
	ctx := context.Background()
	var version, size string
	if err := handle.QueryRowContext(ctx, `SELECT current_setting('server_version'), pg_size_pretty(pg_database_size(current_database()))`).Scan(&version, &size); err == nil {
		fmt.Fprintf(w, "server version: %s\nsize: %s\n", version, size)
	}
	var steps int
	var last string
	if err := handle.QueryRowContext(ctx, `SELECT count(*), coalesce(max(name), '') FROM schema_migrations`).Scan(&steps, &last); err == nil {
		fmt.Fprintf(w, "schema: %d steps, last %s\n", steps, last)
	}
	legacy := db.PathFor(root)
	for _, path := range []string{legacy, legacy + ".imported"} {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		var status string
		_ = handle.QueryRowContext(ctx, `SELECT status FROM legacy_imports WHERE source = ?`, legacy).Scan(&status)
		if status == "" {
			status = "not imported"
		}
		fmt.Fprintf(w, "legacy file: %s (%s)\n", path, status)
	}
	return nil
}

// pgTool runs a bundled PostgreSQL client program against the workspace's
// database. The password reaches it through a temporary 0600 pgpass file,
// never argv (which every process on the machine can read) or a
// PGPASSWORD environment variable.
func pgTool(ctx context.Context, root, program string, args ...string) error {
	handle, err := db.OpenRoot(root)
	if err != nil {
		return err
	}
	defer handle.Close()
	atFatal = func() { handle.Close() }
	rawURL, err := connectionURL(root, handle)
	if err != nil {
		return err
	}
	config, err := pgx.ParseConfig(rawURL)
	if err != nil {
		return fmt.Errorf("%s is not a valid PostgreSQL connection string", db.URLEnv)
	}
	binaries, err := embeddedpg.Binaries(ctx, os.Stderr)
	if err != nil {
		return err
	}
	passDir, err := os.MkdirTemp("", "nodevas-pgpass-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(passDir)
	escape := func(s string) string { return strings.NewReplacer(`\`, `\\`, `:`, `\:`).Replace(s) }
	passFile := filepath.Join(passDir, "pgpass")
	line := fmt.Sprintf("%s:%d:%s:%s:%s\n", escape(config.Host), config.Port, escape(config.Database), escape(config.User), escape(config.Password))
	if err := embeddedpg.WriteSecretFile(passFile, []byte(line)); err != nil {
		return err
	}
	exe := filepath.Join(binaries, "bin", program)
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	full := append([]string{"--host", config.Host, "--port", strconv.Itoa(int(config.Port)),
		"--username", config.User, "--dbname", config.Database, "--no-password"}, args...)
	cmd := exec.CommandContext(ctx, exe, full...)
	cmd.Env = append(scrubbedEnv(), "PGPASSFILE="+passFile)
	if mode := sslMode(rawURL); mode != "" {
		cmd.Env = append(cmd.Env, "PGSSLMODE="+mode)
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// connectionURL is the URL OpenRoot used: the environment's, or the
// embedded cluster's app URL.
func connectionURL(root string, handle *db.DB) (string, error) {
	if raw, err := db.URLFromEnv(os.LookupEnv); err != nil || raw != "" {
		return raw, err
	}
	resolved, err := dbconn.Resolve(root)
	if err != nil {
		return "", err
	}
	_ = resolved.Release() // the handle above still holds the cluster
	return resolved.URL, nil
}

func sslMode(rawURL string) string {
	if i := strings.Index(rawURL, "sslmode="); i >= 0 {
		mode := rawURL[i+len("sslmode="):]
		if j := strings.IndexAny(mode, "& "); j >= 0 {
			mode = mode[:j]
		}
		return mode
	}
	return ""
}

func scrubbedEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(strings.ToUpper(name), "PG") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

func dbDump(ctx context.Context, root, out string) error {
	if out == "" {
		return errors.New("--out <file> is required")
	}
	abs, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	if err := pgTool(ctx, root, "pg_dump", "--format=custom", "--no-owner", "--no-privileges", "--file", abs); err != nil {
		return err
	}
	fmt.Printf("dumped to %s (restore with `nodevas db restore --in %q`)\n", abs, abs)
	return nil
}

func dbRestore(ctx context.Context, root, in string) error {
	if in == "" {
		return errors.New("--in <file> is required")
	}
	abs, err := filepath.Abs(in)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		return err
	}
	return pgTool(ctx, root, "pg_restore", "--clean", "--if-exists", "--no-owner", "--no-privileges",
		"--single-transaction", abs)
}

func dbInstallPostgres(ctx context.Context, archive, targetName, out string) error {
	target, err := embeddedpg.CurrentTarget()
	if targetName != "" {
		var ok bool
		if target, ok = embeddedpg.TargetNamed(targetName); !ok {
			return fmt.Errorf("no pinned PostgreSQL for %s", targetName)
		}
		err = nil
	}
	if err != nil {
		return err
	}
	opts := embeddedpg.InstallOptions{Target: target, Archive: archive, Progress: os.Stderr,
		Mirror: os.Getenv(embeddedpg.MirrorEnv)}
	if out != "" {
		if opts.Dest, err = filepath.Abs(out); err != nil {
			return err
		}
	} else if opts.Home, err = embeddedpg.DefaultHome(); err != nil {
		return err
	}
	dir, err := embeddedpg.Install(ctx, opts)
	if err != nil {
		return err
	}
	fmt.Printf("PostgreSQL %s for %s (sha256 %s) is in %s\n", embeddedpg.PinnedVersion, target.Name, target.SHA256, dir)
	return nil
}

// dbTestServer runs a scratch PostgreSQL in the foreground for the e2e run:
// it writes the admin URL to urlFile (owner-only) and stops when stdin
// closes or on Ctrl+C.
func dbTestServer(ctx context.Context, dir, urlFile string) error {
	if dir == "" || urlFile == "" {
		return errors.New("--dir and --url-file are required")
	}
	binaries, err := embeddedpg.Binaries(ctx, os.Stderr)
	if err != nil {
		return err
	}
	target, err := embeddedpg.CurrentTarget()
	if err != nil {
		return err
	}
	cache, err := embeddedpg.ScratchCache(target)
	if err != nil {
		return err
	}
	server, err := embeddedpg.StartScratch(ctx, embeddedpg.ScratchOptions{Binaries: binaries, Dir: dir, TemplateCache: cache})
	if err != nil {
		return err
	}
	stopCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		stop()
	}()
	if err := embeddedpg.WriteSecretFile(urlFile, []byte(server.AdminURL()+"\n")); err != nil {
		_ = server.Stop(context.Background())
		return err
	}
	fmt.Fprintf(os.Stderr, "test PostgreSQL on 127.0.0.1:%d; close stdin to stop it\n", server.Port())
	select {
	case <-stopCtx.Done():
	case <-server.Done():
		return errors.New("the test server exited")
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return server.Stop(shutdown)
}

var databaseName = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// dbAdmin creates or drops a database on a server the caller administers.
func dbAdmin(ctx context.Context, adminURL, name string, create bool) error {
	if adminURL == "" || name == "" {
		return errors.New("--url and --name are required")
	}
	if !databaseName.MatchString(name) {
		return fmt.Errorf("database name %q must be lower-case letters, digits and _", name)
	}
	handle, err := sql.Open("pgx", adminURL)
	if err != nil {
		return errors.New("--url is not a valid PostgreSQL connection string")
	}
	defer handle.Close()
	statement := `DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`
	if create {
		statement = `CREATE DATABASE ` + name
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if _, err := handle.ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("%s: %w", db.DescribeURL(adminURL), err)
	}
	return nil
}
