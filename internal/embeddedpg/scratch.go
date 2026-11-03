package embeddedpg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"nodevas/internal/lockfile"
)

// Scratch is a throwaway PostgreSQL for tests and the e2e run: one superuser,
// loopback only, durability off (fsync, synchronous_commit and
// full_page_writes), and a data directory nobody needs after it stops. It is
// never what serve uses.
type Scratch struct {
	dir      string
	binaries string
	proc     *postmaster
	port     int
	password string
}

// ScratchOptions says where a scratch server lives.
type ScratchOptions struct {
	// Binaries is an installed, verified binaries directory.
	Binaries string
	// Dir holds the data directory and the password. An existing one is
	// started again.
	Dir string
	// TemplateCache, when set, is a directory where an initdb'd data
	// directory is kept and copied from, so initdb runs once per machine
	// rather than once per test binary.
	TemplateCache string
}

// scratchRule lets the superuser in to any database over IPv4 loopback.
const scratchRule = "host all " + adminRole + " 127.0.0.1/32 scram-sha-256"

var scratchSettings = []string{
	"listen_addresses=127.0.0.1",
	"unix_socket_directories=",
	"password_encryption=scram-sha-256",
	"ssl=off",
	"max_connections=300",
	"shared_buffers=32MB",
	"jit=off",
	"fsync=off",
	"synchronous_commit=off",
	"full_page_writes=off",
	"logging_collector=off",
	"log_min_messages=warning",
	"log_statement=none",
	"log_min_error_statement=panic",
	"lc_messages=C",
}

// StartScratch prepares (or reuses) a scratch data directory and starts it on
// a free loopback port.
func StartScratch(ctx context.Context, opts ScratchOptions) (*Scratch, error) {
	if err := refuseRoot(); err != nil {
		return nil, err
	}
	dataDir := filepath.Join(opts.Dir, "data")
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return nil, err
	}
	if !fileExists(filepath.Join(opts.Dir, initializedFile)) {
		if err := prepareScratch(ctx, opts); err != nil {
			return nil, err
		}
	}
	password, err := readSecret(filepath.Join(opts.Dir, adminSecretFile))
	if err != nil {
		return nil, err
	}
	if err := writeHBA(dataDir, scratchRule); err != nil {
		return nil, err
	}
	if _, err := RecoverStalePID(ctx, opts.Binaries, dataDir); err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		port, err := freePort()
		if err != nil {
			return nil, err
		}
		proc, err := startPostmaster(opts.Binaries, dataDir, serverArgs(dataDir, port, scratchSettings))
		if err != nil {
			return nil, err
		}
		err = waitReady(ctx, proc, loopbackURL(adminRole, password, port, "postgres"), 2*time.Minute)
		if err == nil {
			return &Scratch{dir: opts.Dir, binaries: opts.Binaries, proc: proc, port: port, password: password}, nil
		}
		_ = stopPostmaster(context.Background(), proc)
		lastErr = err
		if !isBindError(err) {
			break
		}
	}
	return nil, lastErr
}

// prepareScratch fills opts.Dir with a data directory: copied from the
// template cache when there is one (initdb'ing the cache first if needed),
// else initdb'd in place.
func prepareScratch(ctx context.Context, opts ScratchOptions) error {
	dataDir := filepath.Join(opts.Dir, "data")
	if err := os.RemoveAll(dataDir); err != nil {
		return err
	}
	if opts.TemplateCache == "" {
		if err := initScratch(ctx, opts.Binaries, opts.Dir); err != nil {
			return err
		}
	} else {
		if err := ensureTemplate(ctx, opts.Binaries, opts.TemplateCache); err != nil {
			return err
		}
		if err := copyTree(filepath.Join(opts.TemplateCache, "data"), dataDir); err != nil {
			return fmt.Errorf("embeddedpg: copy the template data directory: %w", err)
		}
		secret, err := os.ReadFile(filepath.Join(opts.TemplateCache, adminSecretFile))
		if err != nil {
			return err
		}
		if err := WriteSecretFile(filepath.Join(opts.Dir, adminSecretFile), secret); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(opts.Dir, initializedFile), []byte(PinnedVersion+"\n"), 0o600)
}

func initScratch(ctx context.Context, binaries, dir string) error {
	password := randomHex(32)
	if err := WriteSecretFile(filepath.Join(dir, adminSecretFile), []byte(password+"\n")); err != nil {
		return err
	}
	// One-megabyte WAL segments keep a copied template small; nothing here
	// needs to survive a power cut, so initdb skips its fsyncs.
	return initdb(ctx, binaries, filepath.Join(dir, "data"), password, []string{"--wal-segsize=1", "--no-sync"})
}

// ensureTemplate initdbs the cache once, under a lock so concurrent test
// binaries wait for the first.
func ensureTemplate(ctx context.Context, binaries, cache string) error {
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return err
	}
	if fileExists(filepath.Join(cache, initializedFile)) {
		return nil
	}
	lock, err := lockfile.AcquireWait(filepath.Join(cache, "template.lock"), 5*time.Minute)
	if err != nil {
		return err
	}
	defer lock.Release()
	if fileExists(filepath.Join(cache, initializedFile)) {
		return nil
	}
	if err := os.RemoveAll(filepath.Join(cache, "data")); err != nil {
		return err
	}
	if err := initScratch(ctx, binaries, cache); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cache, initializedFile), []byte(PinnedVersion+"\n"), 0o600)
}

// AdminURL is the superuser's connection string to the postgres database.
func (s *Scratch) AdminURL() string { return loopbackURL(adminRole, s.password, s.port, "postgres") }

// Port is the loopback port.
func (s *Scratch) Port() int { return s.port }

// Done is closed when the server exits.
func (s *Scratch) Done() <-chan struct{} { return s.proc.done }

// Stop shuts the server down.
func (s *Scratch) Stop(ctx context.Context) error {
	if s == nil || s.proc == nil {
		return nil
	}
	return stopPostmaster(ctx, s.proc)
}

// copyTree copies a directory (regular files and directories only).
func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(to, rel)
		if entry.IsDir() {
			return os.MkdirAll(dest, 0o700)
		}
		if !entry.Type().IsRegular() {
			return errors.New("not a regular file: " + path)
		}
		source, err := os.Open(path)
		if err != nil {
			return err
		}
		defer source.Close()
		target, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(target, source); err != nil {
			target.Close()
			return err
		}
		return target.Close()
	})
}

// ScratchCache is where test and e2e servers keep their initdb'd template:
// os.UserCacheDir()/nodevas-testpg/<version>-<target>.
func ScratchCache(target Target) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "nodevas-testpg", PinnedVersion+"-"+target.Name), nil
}

// Binaries returns verified binaries for this platform: NODEVAS_PG_BINARIES
// when set, else the pinned download under DefaultHome (fetched on first use
// unless NODEVAS_PG_OFFLINE is set).
func Binaries(ctx context.Context, progress io.Writer) (string, error) {
	target, err := CurrentTarget()
	if err != nil {
		return "", err
	}
	if dir := os.Getenv(BinariesEnv); dir != "" {
		if err := VerifyBinaries(dir, target); err != nil {
			return "", fmt.Errorf("%s: %w", BinariesEnv, err)
		}
		return dir, nil
	}
	home, err := DefaultHome()
	if err != nil {
		return "", err
	}
	offline := os.Getenv(OfflineEnv)
	return Install(ctx, InstallOptions{
		Home: home, Target: target, Progress: progress, Mirror: os.Getenv(MirrorEnv),
		Offline: offline != "" && offline != "0" && offline != "false",
	})
}
