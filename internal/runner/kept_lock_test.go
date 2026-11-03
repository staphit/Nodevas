package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// keptWriterEnv makes TestKeptWriterProcess record one kept worktree, as a
// second runner (or `nodevas runner prune-kept`) would from its own process.
const keptWriterEnv = "NODEVAS_KEPT_WRITER_TEST"

// Two processes recording kept worktrees at once must not lose one: a lost
// record leaves its worktree unlisted, so no one is told it is there. Writer
// A reads the record and holds still; writer B then records its worktree;
// only after B has written (or is waiting for A) does A write. A's write
// must not drop B's entry.
func TestKeptRecordKeepsBothOfTwoProcessesWritingAtOnce(t *testing.T) {
	dataDir := t.TempDir()
	sig := t.TempDir()
	for _, id := range []string{"run-a", "run-b"} {
		if err := os.MkdirAll(filepath.Join(dataDir, "worktrees", id), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writer := func(runID string, pause bool) (*exec.Cmd, <-chan error) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestKeptWriterProcess$")
		cmd.Env = append(os.Environ(), keptWriterEnv+"=1", "KEPT_DATA="+dataDir, "KEPT_SIG="+sig, "KEPT_RUN="+runID)
		if pause {
			cmd.Env = append(cmd.Env, "KEPT_PAUSE=1")
		}
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			err := cmd.Wait()
			if err != nil {
				err = fmt.Errorf("%w\n%s", err, out.String())
			}
			done <- err
		}()
		t.Cleanup(func() {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				<-done
			}
		})
		return cmd, done
	}
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(sig, name))
		return err == nil
	}
	deadline := time.Now().Add(2 * time.Minute)
	until := func(what string, cond func() bool) {
		t.Helper()
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("gave up waiting for %s", what)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	_, doneA := writer("run-a", true)
	until("writer A to read the record", func() bool { return exists("loaded-run-a") })

	_, doneB := writer("run-b", false)
	var errB error
	finishedB := false
	until("writer B to write or to wait for A", func() bool {
		select {
		case errB = <-doneB:
			finishedB = true
			return true
		default:
			return exists("waiting-run-b")
		}
	})
	if err := os.WriteFile(filepath.Join(sig, "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-doneA; err != nil {
		t.Fatalf("writer A: %v", err)
	}
	if !finishedB {
		errB = <-doneB
	}
	if errB != nil {
		t.Fatalf("writer B: %v", errB)
	}

	kept, err := KeptWorktrees(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, k := range kept {
		listed = append(listed, k.RunID)
	}
	if slices.Sort(listed); !slices.Equal(listed, []string{"run-a", "run-b"}) {
		t.Fatalf("kept worktrees listed: %v, want run-a and run-b", listed)
	}
}

// TestKeptWriterProcess is the writer process of the test above.
func TestKeptWriterProcess(t *testing.T) {
	if os.Getenv(keptWriterEnv) == "" {
		t.Skip("helper process")
	}
	dataDir, sig, runID := os.Getenv("KEPT_DATA"), os.Getenv("KEPT_SIG"), os.Getenv("KEPT_RUN")
	touch := func(name string) {
		if err := os.WriteFile(filepath.Join(sig, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	keptLockWaiting = func() { touch("waiting-" + runID) }
	keptAfterLoad = func() {
		touch("loaded-" + runID)
		if os.Getenv("KEPT_PAUSE") == "" {
			return
		}
		// Hold still until the test says so; the test kills this process if
		// it gives up.
		for {
			if _, err := os.Stat(filepath.Join(sig, "release")); err == nil {
				return
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	wt := Worktree{Path: filepath.Join(dataDir, "worktrees", runID), Branch: "agent/n-1-" + runID}
	if err := RecordKept(context.Background(), dataDir, filepath.Join(dataDir, "repo"), wt, []string{".codex/config.toml"}); err != nil {
		t.Fatal(err)
	}
}
