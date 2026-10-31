package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"nodevas/internal/runner"
	"nodevas/internal/runnerwire"
)

// A standalone `serve` (loopback --listen, no proxy, no TLS, no Cloudflare
// Access) is the one-machine setup: the hub, and a runner for it started as
// its own process, `nodevas runner start`, exactly as a person would start
// one. The hub process still never runs an agent. The runner signs in with an
// ordinary runner token over loopback, asks at this terminal before every run
// its auto-accept policy (off unless turned on) does not cover, and keeps its
// settings in a file of its own, so a login to another hub in the usual
// runner.json is left alone. A networked hub never starts one: its runners
// are linked by their owners.

// repoFlags collects --repo, which may be given more than once.
type repoFlags []string

func (r *repoFlags) String() string { return strings.Join(*r, ",") }

func (r *repoFlags) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("--repo needs a path")
	}
	*r = append(*r, value)
	return nil
}

// localRunnerName is the local runner's name on the hub: a fixed id the web
// translates, never shown as stored.
const localRunnerName = runnerwire.LocalRunnerName

// localRunnerMode is whether serve starts a runner beside its hub, and how
// much a failure to start it matters.
type localRunnerMode int

const (
	// noLocalRunner: --no-runner, or a hub that is not standalone.
	noLocalRunner localRunnerMode = iota
	// defaultLocalRunner: a standalone hub nobody asked anything of. A runner
	// that cannot start is a warning; the hub serves on without it.
	defaultLocalRunner
	// explicitLocalRunner: --with-runner or --repo. A runner that cannot
	// start stops serve, as asked-for things that fail do.
	explicitLocalRunner
)

// planLocalRunner decides the local runner. Asked for (--with-runner, --repo)
// where its runner would be owned by nobody, it is an error: on a server with
// accounts the local actor is not a person. Not asked for, it starts only on
// a standalone hub; note says why it did not, when that is worth a line.
func planLocalRunner(withRunner, noRunner bool, repos []string, remote, useTLS, access bool) (mode localRunnerMode, note string, err error) {
	explicit := withRunner || len(repos) > 0
	if noRunner {
		if explicit {
			return noLocalRunner, "", errors.New("--no-runner cannot be combined with --with-runner or --repo")
		}
		return noLocalRunner, "", nil
	}
	if explicit {
		flag := "--with-runner"
		if !withRunner {
			flag = "--repo"
		}
		if remote {
			return noLocalRunner, "", errors.New(flag + " is for a server on this machine only (a loopback --listen, no proxy); " +
				"on a networked hub, link each runner from the Runners page")
		}
		if useTLS {
			return noLocalRunner, "", errors.New(flag + " talks to its runner over loopback http; drop --tls-cert/--tls-key, " +
				"or start `nodevas runner` yourself")
		}
		return explicitLocalRunner, "", nil
	}
	switch {
	case remote:
		// A networked hub: its runners are linked from the Runners page.
		return noLocalRunner, "", nil
	case useTLS:
		return noLocalRunner, "local runner not started: it talks to the hub over loopback http, and this hub serves TLS; " +
			"start `nodevas runner` yourself", nil
	case access:
		return noLocalRunner, "local runner not started: Cloudflare Access is on, so this hub is reachable off this machine; " +
			"link runners from the Runners page", nil
	}
	return defaultLocalRunner, "", nil
}

// localRunnerConfigPath is where the local runner of one workspace keeps its
// settings: beside the rest of this user's Nodevas state, outside the
// workspace (which may be synced or backed up), one file per workspace
// because each workspace has its own database of runners.
func localRunnerConfigPath(catalogRoot, workspace string) string {
	sum := sha256.Sum256([]byte(workspace))
	return filepath.Join(catalogRoot, "local-runners", hex.EncodeToString(sum[:8])+".json")
}

// localHubURL is the hub as its local runner reaches it.
func localHubURL(listen string, port int) string {
	return "http://" + net.JoinHostPort(strings.Trim(listen, "[]"), strconv.Itoa(port))
}

// tokenMinter is Server.LocalRunnerToken.
type tokenMinter func(ctx context.Context, existing, name string) (token string, created bool, err error)

// prepareLocalRunner writes the local runner's settings: the hub, a runner
// token (the stored one while it is still good), and each --repo. Naming a
// repository on the command line is the consent `runner repo add` asks for;
// every run in it is still offered at the terminal. Without --repo the runner
// keeps the repositories its settings already allow; none are added for it,
// the current directory included, because each is a decision of its own.
func prepareLocalRunner(ctx context.Context, path, hub string, repos []string, mint tokenMinter, stderr io.Writer) error {
	hub, err := runner.ValidateHub(hub)
	if err != nil {
		return err
	}
	_, cfg, err := loadRunnerConfig(path, stderr)
	if err != nil {
		return err
	}
	token, created, err := mint(ctx, cfg.Token, localRunnerName)
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintf(stderr, "local runner: registered %q on this hub\n", localRunnerName)
	}
	cfg.Hub, cfg.Token = hub, token
	cfg.AccessClientID, cfg.AccessClientSecret = "", ""
	for _, p := range repos {
		repo, err := runner.InspectRepo(ctx, runnerCommands, p)
		if err != nil {
			return err
		}
		known := false
		for _, existing := range cfg.Repos {
			known = known || existing.ID == repo.ID
		}
		if known {
			continue
		}
		if err := cfg.AddRepo(repo); err != nil {
			return err
		}
		fmt.Fprintf(stderr, "local runner: agent runs may now work in %s (id %s)\n", repo.Path, repo.ID)
	}
	if len(cfg.Repos) == 0 {
		fmt.Fprintf(stderr, "local runner: no repositories yet, so no run can start; allow one with "+
			"`nodevas serve --repo <dir>` or `nodevas runner repo add <dir> --config %q`\n", path)
	}
	return runner.SaveConfig(path, cfg)
}

// startLocalRunner starts `nodevas runner start --config path` on this
// terminal: its questions go where serve's log goes, and its answers come
// from serve's stdin, which serve itself never reads. The returned stop asks
// it to finish and waits.
func startLocalRunner(path string, stdin *os.File, stderr *os.File) (stop func(), err error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, "runner", "start", "--config", path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stderr, stderr
	// Serve owns Ctrl+C on the terminal they share (it asks for a second
	// one); the runner ends when serve stops it.
	cmd.Env = append(os.Environ(), localRunnerUnderServeEnv+"=1")
	endWithServe(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start the local runner: %w", err)
	}
	tieToServe(cmd)
	fmt.Fprintf(stderr, "local runner started (settings %s; change them with `nodevas runner ... --config %q`)\n", path, path)
	if !isTerminal(stdin) {
		fmt.Fprintln(stderr, "local runner: stdin is not a terminal, so nobody can accept a run here; "+
			"runs its auto-accept policy does not cover are declined")
	}
	done := make(chan struct{})
	stopping := make(chan struct{})
	go func() {
		err := cmd.Wait()
		select {
		case <-stopping:
		default:
			fmt.Fprintf(stderr, "local runner exited (%v); the hub keeps serving, restart serve to bring it back\n", err)
		}
		close(done)
	}()
	return func() {
		close(stopping)
		// SIGTERM, since the runner leaves Ctrl+C to serve. Windows has no
		// signal to send another process, so there it is killed.
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}, nil
}
