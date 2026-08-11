package main

import (
	"fmt"
	"net"
	"strings"
	"testing"
)

// A port already held by something else must be reported before the child is
// spawned, because letting the child fail puts the explanation on a stderr
// stream nobody is reading. This is the first of the two failures the launcher
// exists for.
func TestCheckPortFreeReportsAPortThatIsAlreadyHeld(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	conflict := checkPortFree("127.0.0.1", port)
	if conflict == nil {
		t.Fatalf("checkPortFree(127.0.0.1, %d) = nil, want a conflict while the port is held", port)
	}
	if !strings.Contains(conflict.Error(), fmt.Sprint(port)) {
		t.Errorf("conflict message %q does not name the port %d", conflict.Error(), port)
	}
}

// The same check must not invent a conflict for a free port: a launcher that
// refuses to start when nothing is wrong is worse than no check at all.
func TestCheckPortFreeAcceptsAPortNothingIsUsing(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	// Release it, so the port is known-free rather than merely likely to be.
	listener.Close()

	if conflict := checkPortFree("127.0.0.1", port); conflict != nil {
		t.Fatalf("checkPortFree(127.0.0.1, %d) = %v, want nil for a released port", port, conflict)
	}
}

// The Windows holder lookup parses netstat output, which cannot be produced on
// demand in a test. Parsing is therefore separated from the exec call and
// checked against a captured sample, including the rows for other ports that
// must not match.
func TestParseNetstatListenerFindsThePidForTheRequestedPortOnly(t *testing.T) {
	sample := strings.Join([]string{
		"Active Connections",
		"",
		"  Proto  Local Address          Foreign Address        State           PID",
		"  TCP    0.0.0.0:135            0.0.0.0:0              LISTENING       1044",
		"  TCP    127.0.0.1:5666         0.0.0.0:0              LISTENING       23188",
		"  TCP    127.0.0.1:56660        0.0.0.0:0              LISTENING       999",
		"  TCP    127.0.0.1:5666         127.0.0.1:51022        ESTABLISHED     23188",
	}, "\r\n")

	if pid := parseNetstatListener(sample, 5666); pid != 23188 {
		t.Errorf("parseNetstatListener(..., 5666) = %d, want 23188", pid)
	}
	// 56660 ends with the digits of 5666; a suffix match on the port number
	// alone rather than on ":5666" would report the wrong process, which is
	// the one thing worse than reporting none.
	if pid := parseNetstatListener(sample, 56660); pid != 999 {
		t.Errorf("parseNetstatListener(..., 56660) = %d, want 999", pid)
	}
	if pid := parseNetstatListener(sample, 5667); pid != 0 {
		t.Errorf("parseNetstatListener(..., 5667) = %d, want 0 for a port nothing is listening on", pid)
	}
}

// The Unix lookup reads lsof, whose first data row carries the command and the
// pid. The header row must be skipped.
func TestParseLsofListenerReadsTheCommandAndPidFromTheFirstDataRow(t *testing.T) {
	sample := "COMMAND   PID  USER   FD   TYPE DEVICE SIZE/OFF NODE NAME\n" +
		"nodevas 41233 patrick    7u  IPv4 0x1234      0t0  TCP 127.0.0.1:5666 (LISTEN)\n"

	pid, name := parseLsofListener(sample)
	if pid != 41233 || name != "nodevas" {
		t.Fatalf("parseLsofListener() = (%d, %q), want (41233, \"nodevas\")", pid, name)
	}
}

// The ring keeps recent output for the page and must drop the oldest lines
// rather than grow: a dev server left running overnight would otherwise fill
// memory in a process nobody is watching.
func TestLogRingDropsTheOldestLinesOnceItIsFull(t *testing.T) {
	ring := newLogRing(3)
	for i := 1; i <= 5; i++ {
		ring.Add("stdout", fmt.Sprintf("line %d", i))
	}

	if got := ring.Len(); got != 3 {
		t.Fatalf("ring length = %d, want the capacity 3", got)
	}
	lines := ring.Lines()
	want := []string{"line 3", "line 4", "line 5"}
	for index, text := range want {
		if lines[index].Text != text {
			t.Errorf("line %d = %q, want %q", index, lines[index].Text, text)
		}
	}
	// Sequence numbers keep counting past the drop, so nothing reading the
	// pane mistakes a dropped line for a repeated one.
	if lines[0].Seq != 3 || lines[2].Seq != 5 {
		t.Errorf("sequence numbers = %d..%d, want 3..5", lines[0].Seq, lines[2].Seq)
	}
}

// Two clicks arriving together must not both spawn a server. The state is
// claimed under the lock before any of the slow preflight work, so a start
// against a claimed supervisor is refused with an explanation rather than
// producing a second child that fights the first for the port and the
// workspace.
func TestStartIsRefusedWhileTheServerIsAlreadyRunning(t *testing.T) {
	sup := newSupervisor(supervisorConfig{
		Binary: "nodevas", Workspace: t.TempDir(), ServerHost: "127.0.0.1", ServerPort: 5666,
	})
	sup.state = stateRunning

	err := sup.Start()
	if err == nil {
		t.Fatal("Start() = nil while running, want a refusal")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("Start() error = %q, want it to say the server is already running", err)
	}
	if sup.state != stateRunning {
		t.Errorf("state after a refused start = %q, want it left at running", sup.state)
	}
}

// A start while a stop is still in flight is refused for the same reason: the
// old process has not released the port or the workspace yet.
func TestStartIsRefusedWhileAStopIsStillInFlight(t *testing.T) {
	sup := newSupervisor(supervisorConfig{
		Binary: "nodevas", Workspace: t.TempDir(), ServerHost: "127.0.0.1", ServerPort: 5666,
	})
	sup.state = stateStopping

	if err := sup.Start(); err == nil {
		t.Fatal("Start() = nil while stopping, want a refusal")
	}
}

// Stopping something that is not running is not an error: Restart calls Stop
// unconditionally, and a spurious failure there would leave the operator
// unable to start a server that had already exited on its own.
func TestStopIsSilentWhenNothingIsRunning(t *testing.T) {
	sup := newSupervisor(supervisorConfig{
		Binary: "nodevas", Workspace: t.TempDir(), ServerHost: "127.0.0.1", ServerPort: 5666,
	})

	if err := sup.Stop(); err != nil {
		t.Fatalf("Stop() on a stopped supervisor = %v, want nil", err)
	}
}

// ReleasePort is the only path that kills anything the launcher did not spawn.
// It must accept exactly the pid the last failed start reported, so that a
// stale page, a typo, or a forged request cannot turn it into a general kill.
func TestReleasePortOnlyAcceptsThePidTheLaunchReported(t *testing.T) {
	sup := newSupervisor(supervisorConfig{
		Binary: "nodevas", Workspace: t.TempDir(), ServerHost: "127.0.0.1", ServerPort: 5666,
	})

	if err := sup.ReleasePort(1234); err == nil {
		t.Fatal("ReleasePort() = nil with no known conflict, want a refusal")
	}
	sup.conflict = &portConflict{Address: "127.0.0.1:5666", PID: 4321, Process: "nodevas"}
	err := sup.ReleasePort(1234)
	if err == nil {
		t.Fatal("ReleasePort(1234) = nil while pid 4321 holds the port, want a refusal")
	}
	if !strings.Contains(err.Error(), "4321") {
		t.Errorf("refusal %q does not name the pid that actually holds the port", err)
	}
}

// The workspace the child opens is not necessarily the one --project names:
// internal/project reopens the last registered workspace root from the user
// config directory. The panel therefore reads the answer back out of the
// child's own startup line rather than claiming the requested path.
func TestInspectReadsTheWorkspaceTheChildActuallyOpened(t *testing.T) {
	sup := newSupervisor(supervisorConfig{Workspace: `C:\requested-workspace`, ServerHost: "127.0.0.1", ServerPort: 5666})

	sup.inspect(`2026/08/04 17:34:22 nodevas serving C:\workspace on http://127.0.0.1:5666`)

	if sup.serving != `C:\workspace` {
		t.Fatalf("serving = %q, want the path the child reported", sup.serving)
	}
}

// The second failure this launcher exists for: a workspace held by another
// instance. cmd/nodevas already prints a human explanation for it, and the
// panel must show that sentence rather than the "exit status 1" it would
// otherwise be reduced to.
func TestInspectPromotesTheWorkspaceBusyExplanationOutOfTheLog(t *testing.T) {
	sup := newSupervisor(supervisorConfig{Workspace: `C:\other-workspace`, ServerHost: "127.0.0.1", ServerPort: 5666})

	sup.inspect(`workspace "C:\workspace" is already open in another Nodevas instance (pid 21588 on host, since 2026-08-04T08:28:00Z).`)

	if sup.problem == "" {
		t.Fatal("problem = \"\", want the child's own explanation of the busy workspace")
	}
	if !strings.Contains(sup.problem, "pid 21588") {
		t.Errorf("problem = %q, want it to keep the holder the child named", sup.problem)
	}
	if !strings.Contains(sup.problem, "Close the other Nodevas window") {
		t.Errorf("problem = %q, want it to say what to do about it", sup.problem)
	}
}

// The control panel has no authentication and its endpoints start and stop
// processes, so binding anything the network can reach would be a remote
// shell. Every non-loopback value must be refused rather than warned about.
func TestRequireLoopbackRefusesAnythingTheNetworkCanReach(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "127.0.0.53", "::1", "localhost", "[::1]"} {
		if err := requireLoopback(host); err != nil {
			t.Errorf("requireLoopback(%q) = %v, want nil for a loopback address", host, err)
		}
	}
	for _, host := range []string{"", "0.0.0.0", "::", "192.168.1.20", "example.com"} {
		if err := requireLoopback(host); err == nil {
			t.Errorf("requireLoopback(%q) = nil, want a refusal: the panel must not be reachable from the network", host)
		}
	}
}

// The guard on the POST endpoints turns on the same loopback question for the
// Origin header, since a page on another origin driving Start and Stop is the
// cross-site version of the problem above.
func TestIsLocalOriginAcceptsOnlyLoopbackOrigins(t *testing.T) {
	for _, origin := range []string{"http://127.0.0.1:5667", "http://localhost:5667", "http://[::1]:5667"} {
		if !isLocalOrigin(origin) {
			t.Errorf("isLocalOrigin(%q) = false, want true", origin)
		}
	}
	for _, origin := range []string{"https://example.com", "http://192.168.1.20:5667", "null"} {
		if isLocalOrigin(origin) {
			t.Errorf("isLocalOrigin(%q) = true, want false", origin)
		}
	}
}
