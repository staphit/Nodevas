package analytics

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// B-40: analytics never runs a process and never reaches a model.
//
// os/exec is checked on this package's own imports: internal/db reaches it
// through internal/embeddedpg (the embedded server's own postgres), which is
// the database, not analytics. Models are checked through every dependency:
// nothing under internal/copilot, and not internal/runners (whose LLM relay
// calls models on a runner).
func TestAnalyticsNeitherExecsNorCallsModels(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := map[string]bool{"os/exec": true, "syscall": true, "golang.org/x/sys/execabs": true}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(".", name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range file.Imports {
			imported, _ := strconv.Unquote(spec.Path.Value)
			if forbidden[imported] {
				t.Errorf("%s imports %s: analytics must not start processes", name, imported)
			}
		}
	}

	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH")
	}
	out, err := exec.Command("go", "list", "-deps", "nodevas/internal/analytics").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		switch {
		case dep == "nodevas/internal/runners",
			dep == "nodevas/internal/copilot", strings.HasPrefix(dep, "nodevas/internal/copilot/"):
			t.Errorf("internal/analytics depends on %s", dep)
		}
	}
}
