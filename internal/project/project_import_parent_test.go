package project

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// wantImportRefused fails unless the install came back as the given HTTP
// status. An import that is merely "an error" is not enough: the whole point of
// InstallError is that a bad request from a client never reads as a server
// fault.
func wantImportRefused(t *testing.T, status int, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("install succeeded, want %d", status)
	}
	var install *InstallError
	if !errors.As(err, &install) {
		t.Fatalf("install failed with %v, want an InstallError", err)
	}
	if install.Status() != status {
		t.Fatalf("install failed with status %d (%v), want %d", install.Status(), err, status)
	}
}

func TestInstallArchiveImportsIntoAnExistingParent(t *testing.T) {
	pm := importTestManager(t)
	writeTestProject(t, filepath.Join(pm.Workspace(), "Notes"), "Notes")
	data := buildTestProjectArchive(t, "Stellaris")

	active, destination, err := pm.InstallArchiveMode(
		data, "", "Stellaris.veproj", ImportModeFolder, "Notes",
	)
	if err != nil {
		t.Fatalf("install under a parent: %v", err)
	}
	if active != "Notes/Stellaris" {
		t.Fatalf("activated %q, want Notes/Stellaris", active)
	}
	if destination != filepath.Join(pm.Workspace(), "Notes", "Stellaris") {
		t.Fatalf("installed at %q", destination)
	}
	if _, err := os.Stat(filepath.Join(destination, "graph.yaml")); err != nil {
		t.Fatalf("imported project: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pm.Workspace(), "Stellaris")); !os.IsNotExist(err) {
		t.Fatalf("the import also landed at the workspace root: %v", err)
	}
}

// A bundle keeps its wrapper under the parent, and the activation target inside
// the bundle is still reached through it.
func TestInstallArchiveImportsABundleIntoAnExistingParent(t *testing.T) {
	pm := importTestManager(t)
	writeTestProject(t, filepath.Join(pm.Workspace(), "Notes"), "Notes")
	data := buildTestBundle(t, "workspace", []string{"Stellaris"}, nil)

	active, _, err := pm.InstallArchiveMode(
		data, "", "workspace.veproj", ImportModeFolder, "Notes",
	)
	if err != nil {
		t.Fatalf("install bundle under a parent: %v", err)
	}
	if active != "Notes/workspace/Stellaris" {
		t.Fatalf("activated %q, want Notes/workspace/Stellaris", active)
	}
	if _, err := os.Stat(
		filepath.Join(pm.Workspace(), "Notes", "workspace", "Stellaris", "graph.yaml"),
	); err != nil {
		t.Fatalf("wrapped project: %v", err)
	}
}

// The parent has to be somewhere that already exists. Creating it on demand
// would let a typo in one upload leave a folder behind in the workspace.
func TestInstallArchiveRefusesAParentThatDoesNotExist(t *testing.T) {
	pm := importTestManager(t)
	data := buildTestProjectArchive(t, "Stellaris")

	_, _, err := pm.InstallArchiveMode(
		data, "", "Stellaris.veproj", ImportModeFolder, "Notes",
	)
	wantImportRefused(t, http.StatusBadRequest, err)
	if _, err := os.Stat(filepath.Join(pm.Workspace(), "Notes")); !os.IsNotExist(err) {
		t.Fatalf("the refused import created its parent: %v", err)
	}
}

// The parent is the one part of an import the client names outright, so every
// shape that could point outside the workspace has to come back refused rather
// than resolved.
func TestInstallArchiveRefusesAParentOutsideTheWorkspace(t *testing.T) {
	escapes := map[string]string{
		"parent directory": "../elsewhere",
		"deep traversal":   "Notes/../../elsewhere",
		"absolute unix":    "/etc",
		"absolute windows": `C:\Windows`,
		"backslash":        `..\elsewhere`,
		"empty segment":    "Notes//Ideas",
	}
	for label, parent := range escapes {
		t.Run(label, func(t *testing.T) {
			pm := importTestManager(t)
			writeTestProject(t, filepath.Join(pm.Workspace(), "Notes"), "Notes")
			data := buildTestProjectArchive(t, "Stellaris")

			_, _, err := pm.InstallArchiveMode(
				data, "", "Stellaris.veproj", ImportModeFolder, parent,
			)
			wantImportRefused(t, http.StatusBadRequest, err)
			outside := filepath.Join(filepath.Dir(pm.Workspace()), "elsewhere")
			if _, err := os.Stat(outside); !os.IsNotExist(err) {
				t.Fatalf("the refused import wrote outside the workspace: %v", err)
			}
		})
	}
}

// Nesting must not cost an import the protection top-level imports have: a name
// already taken inside the parent is worked around, never written over.
func TestInstallArchiveRenamesASiblingCollisionUnderAParent(t *testing.T) {
	pm := importTestManager(t)
	existing := filepath.Join(pm.Workspace(), "Notes", "Stellaris")
	writeTestProject(t, existing, "Original")
	data := buildTestProjectArchive(t, "Stellaris")

	active, destination, err := pm.InstallArchiveMode(
		data, "", "Stellaris.veproj", ImportModeFolder, "Notes",
	)
	if err != nil {
		t.Fatalf("install under a parent: %v", err)
	}
	if active != "Notes/Stellaris-2" {
		t.Fatalf("activated %q, want Notes/Stellaris-2", active)
	}
	if destination != filepath.Join(pm.Workspace(), "Notes", "Stellaris-2") {
		t.Fatalf("installed at %q", destination)
	}
	graph, err := os.ReadFile(filepath.Join(existing, "graph.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(graph) != string(testProjectGraph("Original")) {
		t.Fatalf("the existing sibling was overwritten:\n%s", graph)
	}
}

// Every caller that predates the parent field sends it empty, and those callers
// have to keep landing exactly where they always did.
func TestInstallArchiveWithoutAParentStillImportsAtTheWorkspaceRoot(t *testing.T) {
	data := buildTestProjectArchive(t, "Stellaris")

	for label, parent := range map[string]string{
		"empty":    "",
		"blank":    "   ",
		"the root": ".",
	} {
		t.Run(label, func(t *testing.T) {
			pm := importTestManager(t)
			active, destination, err := pm.InstallArchiveMode(
				data, "", "Stellaris.veproj", ImportModeFolder, parent,
			)
			if err != nil {
				t.Fatalf("install without a parent: %v", err)
			}
			if active != "Stellaris" {
				t.Fatalf("activated %q, want Stellaris", active)
			}
			if destination != filepath.Join(pm.Workspace(), "Stellaris") {
				t.Fatalf("installed at %q", destination)
			}
		})
	}
}

// Root mode builds no wrapper to put anywhere, so the two requests contradict
// each other and the caller is told so rather than having the parent dropped.
func TestInstallArchiveRefusesAParentInRootMode(t *testing.T) {
	pm := importTestManager(t)
	writeTestProject(t, filepath.Join(pm.Workspace(), "Notes"), "Notes")
	data := buildTestBundle(t, "workspace", []string{"Stellaris"}, nil)

	_, _, err := pm.InstallArchiveMode(data, "", "workspace.veproj", ImportModeRoot, "Notes")
	wantImportRefused(t, http.StatusBadRequest, err)
	if _, err := os.Stat(filepath.Join(pm.Workspace(), "Stellaris")); !os.IsNotExist(err) {
		t.Fatalf("the refused import unpacked anyway: %v", err)
	}
}
