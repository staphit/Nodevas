package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"nodevas/internal/project"
)

// state is the supervisor's view of the child. It exists so that two clicks
// arriving together cannot both decide to spawn a server: everything that
// changes the child takes the lock, checks the state, and claims it before it
// does anything slow.
type state string

const (
	stateStopped  state = "stopped"
	stateStarting state = "starting"
	stateRunning  state = "running"
	stateStopping state = "stopping"
)

// stopGrace is how long a graceful stop is given before the process is killed.
// The server's own shutdown path allows five seconds for connections and
// thirty for the final remote backup, so this has to be longer than both or a
// graceful stop would be cut short exactly when it was doing the work that
// makes it worth asking for.
const stopGrace = 40 * time.Second

type supervisorConfig struct {
	Binary     string
	Workspace  string
	ServerHost string
	ServerPort int
}

type supervisor struct {
	cfg  supervisorConfig
	logs *logRing

	mu       sync.Mutex
	state    state
	cmd      *exec.Cmd
	pid      int
	since    time.Time
	exited   chan struct{} // closed when the current child has been reaped
	lastExit string        // how the child last ended, including "on its own"
	conflict *portConflict // the port holder from the most recent failed start
	// serving is the workspace the child reported opening. It is not always
	// the one passed on --project: internal/project keeps a catalog of
	// registered workspace roots in the user config directory and reopens the
	// last active one, so --project only decides anything on a machine that
	// has never opened a workspace. Reading it back from the child's own log
	// is the only answer that cannot be wrong.
	serving string
	// problem is a sentence lifted out of the child's output when it failed
	// for a reason the child explained better than an exit status can.
	problem string
}

func newSupervisor(cfg supervisorConfig) *supervisor {
	return &supervisor{cfg: cfg, state: stateStopped, logs: newLogRing(maxLogLines)}
}

// status is the whole document the page renders. It is one struct because a
// panel that showed state and logs from two different reads could show a
// server that is both stopped and printing.
type status struct {
	State       string        `json:"state"`
	PID         int           `json:"pid"`
	Since       string        `json:"since"`
	Workspace   string        `json:"workspace"`
	Serving     string        `json:"serving"`
	Problem     string        `json:"problem"`
	Binary      string        `json:"binary"`
	ServerPort  int           `json:"serverPort"`
	AppURL      string        `json:"appURL"`
	LastExit    string        `json:"lastExit"`
	Conflict    *portConflict `json:"conflict"`
	ActionError string        `json:"actionError,omitempty"`
	Build       buildStatus   `json:"build"`
	Lines       []logLine     `json:"lines"`
}

func (s *supervisor) Snapshot(build buildStatus) status {
	s.mu.Lock()
	out := status{
		State:      string(s.state),
		PID:        s.pid,
		Workspace:  s.cfg.Workspace,
		Serving:    s.serving,
		Problem:    s.problem,
		Binary:     s.cfg.Binary,
		ServerPort: s.cfg.ServerPort,
		AppURL:     "http://" + net.JoinHostPort(s.cfg.ServerHost, strconv.Itoa(s.cfg.ServerPort)) + "/",
		LastExit:   s.lastExit,
		Conflict:   s.conflict,
		Build:      build,
	}
	if !s.since.IsZero() && s.state == stateRunning {
		out.Since = s.since.Format("15:04:05")
	}
	s.mu.Unlock()
	out.Lines = s.logs.Lines()
	return out
}

// Start spawns the server after checking the two things that make a start fail
// in practice: something else on the port, and something else holding the
// workspace.
func (s *supervisor) Start() error {
	s.mu.Lock()
	if s.state != stateStopped {
		current := s.state
		s.mu.Unlock()
		return fmt.Errorf("the server is already %s; stop it first", current)
	}
	// Claim the state before the preflight checks so a second click cannot run
	// the same checks and spawn a second child.
	s.state = stateStarting
	s.conflict = nil
	s.problem = ""
	s.serving = ""
	s.mu.Unlock()

	if err := s.preflight(); err != nil {
		s.mu.Lock()
		s.state = stateStopped
		s.problem = err.Error()
		s.mu.Unlock()
		s.logs.Add("launcher", "start refused: "+err.Error())
		return err
	}
	if err := s.spawn(); err != nil {
		s.mu.Lock()
		s.state = stateStopped
		s.problem = err.Error()
		s.mu.Unlock()
		s.logs.Add("launcher", "start failed: "+err.Error())
		return err
	}
	return nil
}

// preflight reports the conflicts that would otherwise kill the child a second
// after it starts.
func (s *supervisor) preflight() error {
	if conflict := checkPortFree(s.cfg.ServerHost, s.cfg.ServerPort); conflict != nil {
		s.mu.Lock()
		s.conflict = conflict
		s.mu.Unlock()
		return conflict
	}
	if _, err := os.Stat(s.cfg.Workspace); err != nil {
		return fmt.Errorf("workspace %s: %w", s.cfg.Workspace, err)
	}
	// Taking the workspace lock and letting it go immediately is a check, not
	// a reservation: the child takes it a moment later. Something else could
	// slip in between, and then the child reports the same condition itself.
	// Checking anyway is what turns "exit status 1" into the sentence the CLI
	// would have printed to a terminal nobody is watching.
	lock, err := project.AcquireWorkspaceLock(s.cfg.Workspace)
	if err != nil {
		var busy *project.WorkspaceBusyError
		if errors.As(err, &busy) {
			return fmt.Errorf("%v. Close the other Nodevas window or stop the running server, then start again", busy)
		}
		return fmt.Errorf("workspace lock: %w", err)
	}
	_ = lock.Release()
	return nil
}

func (s *supervisor) spawn() error {
	// The flags are `nodevas serve`'s contract; see cmd/nodevas/main.go.
	cmd := exec.Command(s.cfg.Binary, "serve",
		"--project", s.cfg.Workspace,
		"--port", strconv.Itoa(s.cfg.ServerPort),
		"--listen", s.cfg.ServerHost,
	)
	cmd.Dir = s.cfg.Workspace
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	// The server logs ECS JSON to stderr, so this is the stream that matters.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	exited := make(chan struct{})
	s.mu.Lock()
	s.cmd = cmd
	s.pid = cmd.Process.Pid
	s.since = time.Now()
	s.state = stateRunning
	s.exited = exited
	s.lastExit = ""
	s.mu.Unlock()
	s.logs.Add("launcher", fmt.Sprintf("started pid %d on port %d, workspace %s",
		cmd.Process.Pid, s.cfg.ServerPort, s.cfg.Workspace))

	var pipes sync.WaitGroup
	pipes.Add(2)
	go s.pump(&pipes, "stdout", stdout)
	go s.pump(&pipes, "stderr", stderr)

	go func() {
		// Drain both pipes before Wait, which closes them.
		pipes.Wait()
		err := cmd.Wait()
		s.mu.Lock()
		// A stop that is in progress writes its own explanation; anything else
		// is the child deciding to leave, which the page must show rather than
		// keep claiming a running server.
		if s.state == stateStopping {
			s.lastExit = "stopped by the launcher"
		} else if err != nil {
			s.lastExit = "the server exited on its own: " + err.Error()
		} else {
			s.lastExit = "the server exited on its own"
		}
		s.state = stateStopped
		s.cmd = nil
		s.pid = 0
		s.exited = nil
		message := s.lastExit
		s.mu.Unlock()
		s.logs.Add("launcher", message)
		close(exited)
	}()
	return nil
}

func (s *supervisor) pump(done *sync.WaitGroup, source string, reader io.Reader) {
	defer done.Done()
	scanner := bufio.NewScanner(reader)
	// A structured log record with a long message is still one line, and the
	// default 64KiB token limit would end the scan rather than truncate it.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		s.logs.Add(source, line)
		s.inspect(line)
	}
}

// servingLine is the child's own announcement of what it opened and where,
// "nodevas serving <workspace> on http://127.0.0.1:5666".
var servingLine = regexp.MustCompile(`nodevas serving (.+) on (https?://\S+)`)

// inspect lifts the two things out of the child's output that the panel cannot
// work out for itself: which workspace it actually opened, and the sentence
// cmd/nodevas prints when the workspace is held by another instance.
//
// The workspace check in preflight cannot cover that second case on its own,
// because the child may open a different workspace than the one we asked for
// (see the serving field). The child does explain itself when that happens --
// it just explains itself onto a stderr stream that would otherwise scroll
// past as one more log line above a bare "exit status 1".
func (s *supervisor) inspect(line string) {
	if match := servingLine.FindStringSubmatch(line); match != nil {
		// The line arrives inside a JSON log record, where a Windows path has
		// its separators doubled. No real path contains "\\", so undoing that
		// here is safe and saves parsing the whole record.
		s.mu.Lock()
		s.serving = strings.ReplaceAll(match[1], `\\`, `\`)
		s.mu.Unlock()
		return
	}
	if strings.Contains(line, "is already open in another Nodevas instance") {
		s.mu.Lock()
		s.problem = strings.TrimSpace(line) +
			". Close the other Nodevas window or stop the running server, then start again."
		s.mu.Unlock()
	}
}

// Stop ends the child and waits for it to be reaped. Waiting is not politeness:
// Restart depends on it, because a new server started before the old one has
// released the port and the workspace hits both conflicts at once.
func (s *supervisor) Stop() error {
	s.mu.Lock()
	if s.state != stateRunning {
		current := s.state
		s.mu.Unlock()
		if current == stateStopped {
			return nil
		}
		return fmt.Errorf("the server is %s; wait for that to finish", current)
	}
	process := s.cmd.Process
	exited := s.exited
	s.state = stateStopping
	s.mu.Unlock()

	graceful, err := terminateChild(process)
	if err != nil {
		s.logs.Add("launcher", "stop signal failed: "+err.Error())
	}
	if !graceful && ungracefulStopNote != "" {
		s.logs.Add("launcher", ungracefulStopNote)
	}

	select {
	case <-exited:
		return nil
	case <-time.After(stopGrace):
	}
	// It did not go. Killing is the only remaining option, and it is worse
	// than the signal above in exactly the ways described at terminateChild.
	s.logs.Add("launcher", fmt.Sprintf("still running after %s; killing pid %d", stopGrace, process.Pid))
	_ = process.Kill()
	select {
	case <-exited:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("the server did not exit after being killed; look for the process by hand")
	}
}

// Restart stops and starts, in that order and never overlapping.
func (s *supervisor) Restart() error {
	if err := s.Stop(); err != nil {
		return err
	}
	return s.Start()
}

// ReleasePort kills the process holding the server port, and only that one.
// It refuses any pid other than the one the last failed start reported, so a
// mistyped or forged request cannot turn this endpoint into a general kill.
// Nothing calls it on its own: killing something the operator did not ask
// about is how a dev tool ends up killing the wrong thing.
func (s *supervisor) ReleasePort(pid int) error {
	s.mu.Lock()
	conflict := s.conflict
	s.mu.Unlock()
	if conflict == nil || conflict.PID <= 0 {
		return errors.New("no known process is holding the port; press Start again to check")
	}
	if pid != conflict.PID {
		return fmt.Errorf("refusing to kill pid %d: the port is held by pid %d", pid, conflict.PID)
	}
	if pid == os.Getpid() {
		return errors.New("that is this launcher's own pid")
	}
	// A pid that cannot be opened or killed is usually one that has already
	// gone, which is the outcome the operator wanted anyway; say that rather
	// than showing them a Windows API error.
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("pid %d could not be opened (%v); it may have exited already — press Start to check", pid, err)
	}
	if err := process.Kill(); err != nil {
		return fmt.Errorf("could not kill pid %d (%v); it may have exited already, or it may belong to another user — press Start to check", pid, err)
	}
	s.logs.Add("launcher", fmt.Sprintf("killed pid %d at your request; it held %s", pid, conflict.Address))
	s.mu.Lock()
	s.conflict = nil
	s.mu.Unlock()
	return nil
}
