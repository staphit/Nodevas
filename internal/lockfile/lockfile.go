// Package lockfile puts an operating-system advisory lock on a file so that
// two *processes* cannot own the same resource at once.
//
// Every other lock in this codebase is a sync.Mutex, which only serialises
// goroutines inside one process. That is not enough here: the desktop app
// spawns the server binary, and the same binary is also a CLI, so two
// processes can legitimately be pointed at one workspace at the same time.
// Files guarded by the sha256 Rev optimistic-lock survive that (the second
// writer gets a 409); files written read-modify-write with no revision check
// silently lose an update.
//
// The lock is held on an open file descriptor, never on the *contents* of the
// file. That is the whole point: the kernel drops the lock when the owning
// process exits, including a SIGKILL or a Windows TerminateProcess, so a
// crashed or force-restarted backend never leaves a workspace wedged. A
// "does a pid file exist" check has the opposite behaviour and is why one is
// deliberately not used here.
//
// The owning pid and a timestamp are written into the file, but only so a
// human (or an error message) can see who holds it. Nothing about correctness
// reads them back.
package lockfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrLocked reports that another descriptor -- in this process or another one
// -- already holds the lock. It is deliberately distinct from an I/O failure:
// "someone else is using the workspace" is a normal, actionable condition,
// whereas a real error opening the file is not.
var ErrLocked = errors.New("lock is held by another process")

// Owner is the diagnostic record written into the lock file. It exists to make
// error messages nameable, not to decide anything.
type Owner struct {
	PID     int    `json:"pid"`
	Host    string `json:"host,omitempty"`
	Program string `json:"program,omitempty"`
	Since   string `json:"since,omitempty"`
}

// Lock is a held advisory lock. Release, or let the process die.
type Lock struct {
	file     *os.File
	path     string
	released bool
}

// Path is the lock file this lock is held on.
func (l *Lock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// Acquire takes the lock at path without waiting, creating the file and its
// parent directory if needed. It returns ErrLocked (wrapped) when someone else
// holds it.
//
// The returned Lock owns an open descriptor and must be released, but an
// unreleased lock is not a leak that can outlive the process.
func Acquire(path string) (*Lock, error) {
	if path == "" {
		return nil, errors.New("lockfile: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("lockfile: create %s: %w", filepath.Dir(path), err)
	}
	file, err := openLockFile(path)
	if err != nil {
		return nil, fmt.Errorf("lockfile: open %s: %w", path, err)
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		if errors.Is(err, ErrLocked) {
			return nil, fmt.Errorf("lockfile: %s: %w", path, ErrLocked)
		}
		return nil, fmt.Errorf("lockfile: lock %s: %w", path, err)
	}
	lock := &Lock{file: file, path: path}
	lock.writeOwner()
	return lock, nil
}

// AcquireWait retries Acquire until timeout elapses. Use it for short
// read-modify-write critical sections where blocking briefly is kinder than
// failing; use Acquire for anything a process holds for its whole lifetime.
func AcquireWait(path string, timeout time.Duration) (*Lock, error) {
	deadline := time.Now().Add(timeout)
	delay := 5 * time.Millisecond
	for {
		lock, err := Acquire(path)
		if err == nil || !errors.Is(err, ErrLocked) {
			return lock, err
		}
		if !time.Now().Before(deadline) {
			return nil, err
		}
		time.Sleep(delay)
		if delay < 100*time.Millisecond {
			delay *= 2
		}
	}
}

// Release drops the lock and closes the descriptor. The file itself is left on
// disk on purpose: deleting it would race with another process that has
// already opened the same path and is about to lock it, and a stale lock
// *file* blocks nothing -- only a live descriptor does.
func (l *Lock) Release() error {
	if l == nil || l.released {
		return nil
	}
	l.released = true
	unlockErr := unlockFile(l.file)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return fmt.Errorf("lockfile: unlock %s: %w", l.path, unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("lockfile: close %s: %w", l.path, closeErr)
	}
	return nil
}

// writeOwner stamps the holder into the file for diagnostics. Failures are
// ignored: the lock is already held at this point, and nothing may depend on
// these bytes.
func (l *Lock) writeOwner() {
	host, _ := os.Hostname()
	owner := Owner{
		PID:     os.Getpid(),
		Host:    host,
		Program: filepath.Base(os.Args[0]),
		Since:   time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.Marshal(owner)
	if err != nil {
		return
	}
	data = append(data, '\n')
	if err := l.file.Truncate(0); err != nil {
		return
	}
	if _, err := l.file.WriteAt(data, 0); err != nil {
		return
	}
	_ = l.file.Sync()
}

// ReadOwner reports who last stamped the lock file, best effort. A missing,
// empty or unreadable file is not an error: the caller only ever uses this to
// make a message friendlier.
func ReadOwner(path string) (Owner, bool) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return Owner{}, false
	}
	var owner Owner
	if err := json.Unmarshal(data, &owner); err != nil || owner.PID == 0 {
		return Owner{}, false
	}
	return owner, true
}

// Describe renders the holder of path as a short parenthetical for an error
// message, or "" when nothing readable is there.
func Describe(path string) string {
	owner, ok := ReadOwner(path)
	if !ok {
		return ""
	}
	text := fmt.Sprintf("pid %d", owner.PID)
	if owner.Host != "" {
		text += " on " + owner.Host
	}
	if owner.Since != "" {
		text += ", since " + owner.Since
	}
	return text
}
