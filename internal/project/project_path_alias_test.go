package project

import (
	"os"
	"path/filepath"
	"testing"
)

// Windows drops a trailing dot or space from a path segment, so "other."
// opens the folder "other" under a name, and a membership key, of its own.
// No folder on Windows can carry such a name, so it is refused everywhere.
func TestProjectPathRefusesTrailingDotOrSpace(t *testing.T) {
	for _, name := range []string{"other.", "a/other.", "other.../x", "other ", "a/b /c", "x. "} {
		if ValidProjectPath(name) {
			t.Errorf("ValidProjectPath(%q) = true", name)
		}
	}
	for _, name := range []string{"other", "v1.2", "a.b/c d", "my project", "a/b-c_d"} {
		if !ValidProjectPath(name) {
			t.Errorf("ValidProjectPath(%q) = false", name)
		}
	}
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	manager := &ProjectManager{workspace: workspace}
	for _, name := range []string{"other.", "other "} {
		if dir, err := manager.Resolve(name); err == nil {
			t.Errorf("Resolve(%q) = %q, want an error", name, dir)
		}
	}
}

// On a filesystem that ignores case (Windows, macOS, a case-insensitive
// dataset), "MAIN" opens the folder "main". Resolve names the folder as it
// is spelled on disk, so both spellings are one project: one Store, one
// membership key. A name spelled as on disk resolves as before.
func TestResolveSpellsTheFolderAsOnDisk(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "group", "main"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(workspace, "GROUP", "MAIN")); err != nil {
		t.Skip("this filesystem tells the spellings apart: they are different folders")
	}
	manager := &ProjectManager{workspace: workspace}
	want := filepath.Join(workspace, "group", "main")
	for _, name := range []string{"group/main", "GROUP/MAIN", "Group/main", "group/Main"} {
		dir, err := manager.Resolve(name)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", name, err)
		}
		if dir != want {
			t.Errorf("Resolve(%q) = %q, want %q", name, dir, want)
		}
	}
	// A folder that does not exist yet keeps the spelling asked for.
	if dir, err := manager.Resolve("group/New"); err != nil || dir != filepath.Join(workspace, "group", "New") {
		t.Errorf("Resolve(group/New) = %q, %v", dir, err)
	}
}
