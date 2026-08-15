package fsbrowse

import (
	"os/exec"
	"runtime"
)

type fsDirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func OpenDirectory(path string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("explorer.exe", path)
	case "darwin":
		command = exec.Command("open", path)
	default:
		command = exec.Command("xdg-open", path)
	}
	return command.Start()
}
