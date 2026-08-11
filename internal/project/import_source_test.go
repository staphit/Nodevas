package project

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCheckImportSource(t *testing.T) {
	workspace := t.TempDir()
	parent := filepath.Dir(workspace)
	sibling := filepath.Join(parent, "sibling-"+filepath.Base(workspace))

	cases := []struct {
		name string
		src  string
		want error
	}{
		{"source is the workspace", workspace, ErrSourceContainsWorkspace},
		{"source contains the workspace", parent, ErrSourceContainsWorkspace},
		{"source is inside the workspace", filepath.Join(workspace, "專案"), ErrSourceInsideWorkspace},
		{"source is deep inside the workspace",
			filepath.Join(workspace, "群組", "專案", "nodes"), ErrSourceInsideWorkspace},
		{"sibling directory", sibling, nil},
		{"sibling with the workspace as a name prefix", workspace + "-backup", nil},
		{"trailing separator on the workspace itself",
			workspace + string(filepath.Separator), ErrSourceContainsWorkspace},
		{"unclean path back into the workspace",
			filepath.Join(workspace, "專案", "..", "其他"), ErrSourceInsideWorkspace},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckImportSource(tc.src, workspace)
			if !errors.Is(err, tc.want) {
				t.Fatalf("CheckImportSource(%q) = %v, want %v", tc.src, err, tc.want)
			}
		})
	}
}

// A relative path names the same directory as its absolute form, so the check
// has to resolve it before comparing — otherwise the refusal is bypassed by
// typing a shorter path.
func TestCheckImportSourceResolvesRelativePaths(t *testing.T) {
	workspace := t.TempDir()
	inside := filepath.Join(workspace, "專案")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatalf("create project directory: %v", err)
	}
	t.Chdir(workspace)

	if err := CheckImportSource("專案", workspace); !errors.Is(err, ErrSourceInsideWorkspace) {
		t.Fatalf("relative child = %v, want %v", err, ErrSourceInsideWorkspace)
	}
	if err := CheckImportSource(".", workspace); !errors.Is(err, ErrSourceContainsWorkspace) {
		t.Fatalf("relative self = %v, want %v", err, ErrSourceContainsWorkspace)
	}
	if err := CheckImportSource("..", workspace); !errors.Is(err, ErrSourceContainsWorkspace) {
		t.Fatalf("relative parent = %v, want %v", err, ErrSourceContainsWorkspace)
	}
}

func TestCheckImportSourceWindowsSpellings(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("drive letters and case-insensitive paths are Windows-only")
	}
	workspace := t.TempDir()
	inside := filepath.Join(workspace, "專案")

	// The same directory typed in another case is still the same directory.
	if err := CheckImportSource(strings.ToUpper(inside), workspace); !errors.Is(
		err, ErrSourceInsideWorkspace) {
		t.Fatalf("upper-cased child = %v, want %v", err, ErrSourceInsideWorkspace)
	}
	if err := CheckImportSource(inside, strings.ToUpper(workspace)); !errors.Is(
		err, ErrSourceInsideWorkspace) {
		t.Fatalf("upper-cased workspace = %v, want %v", err, ErrSourceInsideWorkspace)
	}
	if err := CheckImportSource(strings.ToLower(workspace), workspace); !errors.Is(
		err, ErrSourceContainsWorkspace) {
		t.Fatalf("lower-cased workspace = %v, want %v", err, ErrSourceContainsWorkspace)
	}

	// A different volume can never contain the workspace.
	volume := filepath.VolumeName(workspace)
	other := "Q:"
	if strings.EqualFold(volume, other) {
		other = "R:"
	}
	if err := CheckImportSource(other+string(filepath.Separator)+"匯入", workspace); err != nil {
		t.Fatalf("other drive = %v, want nil", err)
	}
}
