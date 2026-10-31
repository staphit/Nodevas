// Command slowgit is git with one slow command: the first command (--version
// aside) to create the file NODEVAS_TEST_GIT_HOLD waits five seconds before it
// runs. Every command runs the real git (NODEVAS_TEST_REAL_GIT) with the same
// arguments, standard streams and exit code. Once that file exists, a git is
// in flight in the hub's project for the next few seconds.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

func main() {
	real, hold := os.Getenv("NODEVAS_TEST_REAL_GIT"), os.Getenv("NODEVAS_TEST_GIT_HOLD")
	if real == "" || hold == "" {
		fmt.Fprintln(os.Stderr, "slowgit: NODEVAS_TEST_REAL_GIT and NODEVAS_TEST_GIT_HOLD must be set")
		os.Exit(2)
	}
	if len(os.Args) < 2 || os.Args[1] != "--version" {
		if file, err := os.OpenFile(hold, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644); err == nil {
			_ = file.Close()
			time.Sleep(5 * time.Second)
		}
	}
	cmd := exec.Command(real, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		os.Exit(exit.ExitCode())
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "slowgit:", err)
		os.Exit(2)
	}
}
