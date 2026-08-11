package main

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// portConflict says a port cannot be bound and, when the operating system was
// willing to tell us, who is holding it.
//
// This check runs before the child is spawned. Letting the child fail instead
// produces one line on a stderr stream nobody is reading, which is the failure
// this launcher exists to make visible.
type portConflict struct {
	Address string `json:"address"`
	Reason  string `json:"reason"`
	PID     int    `json:"pid,omitempty"`
	Process string `json:"process,omitempty"`
}

func (c *portConflict) Error() string {
	if c == nil {
		return ""
	}
	if c.PID > 0 {
		name := c.Process
		if name == "" {
			name = "an unidentified program"
		}
		return fmt.Sprintf("%s is already in use by %s (pid %d)", c.Address, name, c.PID)
	}
	return fmt.Sprintf("%s is already in use (%s); the holding process could not be identified", c.Address, c.Reason)
}

// checkPortFree tries to bind the address the child would bind. A successful
// bind is released immediately, which leaves a small window in which something
// else could take the port; the child's own failure is still reported if that
// happens, and closing the window would mean handing the listener to the child,
// which the server's flag contract does not offer.
func checkPortFree(host string, port int) *portConflict {
	address := net.JoinHostPort(host, strconv.Itoa(port))
	listener, err := net.Listen("tcp", address)
	if err == nil {
		_ = listener.Close()
		return nil
	}
	conflict := &portConflict{Address: address, Reason: err.Error()}
	if pid, name := findPortHolder(port); pid > 0 {
		conflict.PID = pid
		conflict.Process = name
	}
	return conflict
}

// findPortHolder asks the operating system which process is listening on a
// port. It shells out because the standard library has no portable answer, and
// it is best effort: an unknown holder is still a useful message, so every
// failure here returns zero rather than an error.
func findPortHolder(port int) (int, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if runtime.GOOS == "windows" {
		out, err := exec.CommandContext(ctx, "netstat", "-ano", "-p", "tcp").Output()
		if err != nil {
			return 0, ""
		}
		pid := parseNetstatListener(string(out), port)
		if pid == 0 {
			return 0, ""
		}
		return pid, windowsProcessName(ctx, pid)
	}
	out, err := exec.CommandContext(ctx, "lsof", "-nP", "-sTCP:LISTEN",
		"-iTCP:"+strconv.Itoa(port)).Output()
	if err != nil {
		return 0, ""
	}
	return parseLsofListener(string(out))
}

// netstatLine matches "TCP 127.0.0.1:5666 0.0.0.0:0 LISTENING 1234" on both
// the English and the localised Windows netstat, whose only stable columns are
// the addresses and the trailing pid.
var netstatLine = regexp.MustCompile(`^\s*TCP\s+(\S+)\s+\S+\s+\S+\s+(\d+)\s*$`)

// parseNetstatListener finds the pid listening on a port in `netstat -ano`
// output. Split out from the exec call so it can be tested without a machine
// that happens to have something listening.
func parseNetstatListener(output string, port int) int {
	suffix := ":" + strconv.Itoa(port)
	for _, line := range strings.Split(output, "\n") {
		match := netstatLine.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if match == nil {
			continue
		}
		if !strings.HasSuffix(match[1], suffix) {
			continue
		}
		pid, err := strconv.Atoi(match[2])
		if err != nil || pid <= 0 {
			continue
		}
		return pid
	}
	return 0
}

// parseLsofListener reads the first data row of `lsof -nP -iTCP:port`, whose
// first two columns are the command name and the pid.
func parseLsofListener(output string) (int, string) {
	for index, line := range strings.Split(output, "\n") {
		if index == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[1])
		if err != nil || pid <= 0 {
			continue
		}
		return pid, fields[0]
	}
	return 0, ""
}

func windowsProcessName(ctx context.Context, pid int) string {
	out, err := exec.CommandContext(ctx, "tasklist", "/FI",
		"PID eq "+strconv.Itoa(pid), "/NH", "/FO", "CSV").Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(out))
	if !strings.HasPrefix(line, `"`) {
		return ""
	}
	fields := strings.SplitN(strings.TrimPrefix(line, `"`), `"`, 2)
	return fields[0]
}
