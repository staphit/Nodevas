package runners

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The hub must never start a process: it is reachable from the internet, and
// a run is code execution. Only the runner on its owner's machine executes.
// This holds the hub-side runner packages to that, so the rule cannot erode
// one convenient import at a time.
func TestHubRunnerPackagesDoNotExec(t *testing.T) {
	forbidden := map[string]bool{"os/exec": true, "syscall": true, "golang.org/x/sys/execabs": true}
	for _, dir := range []string{".", "../httpapi/runners", "../runnerwire", "../runs", "../httpapi/runs"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, spec := range file.Imports {
				imported, _ := strconv.Unquote(spec.Path.Value)
				if forbidden[imported] {
					t.Errorf("%s imports %s: the hub must not start processes", path, imported)
				}
			}
		}
	}
}
