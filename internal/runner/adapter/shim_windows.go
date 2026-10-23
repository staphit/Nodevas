//go:build windows

package adapter

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// wrapBatch makes cmd run a .cmd/.bat program through an explicit cmd.exe
// command line (see shim.go). Any other program is left as it is.
func wrapBatch(cmd *exec.Cmd) error {
	if !isBatch(cmd.Path) {
		return nil
	}
	args := cmd.Args[1:]
	if err := checkBatchArgs(append([]string{cmd.Path}, args...)); err != nil {
		return err
	}
	var line strings.Builder
	line.WriteString(syscall.EscapeArg(cmd.Path))
	for _, arg := range args {
		line.WriteByte(' ')
		line.WriteString(syscall.EscapeArg(arg))
	}
	comspec := os.Getenv("ComSpec")
	if comspec == "" || !filepath.IsAbs(comspec) {
		comspec = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	cmd.Path = comspec
	cmd.Args = []string{comspec, "/d", "/v:off", "/s", "/c", line.String()}
	cmd.Err = nil
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CmdLine = syscall.EscapeArg(comspec) + ` /d /v:off /s /c "` + line.String() + `"`
	return nil
}
