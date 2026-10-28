package project

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"time"

	"nodevas/internal/store"
)

// ErrUnknownExample is returned for an example name the binary does not carry.
var ErrUnknownExample = errors.New("unknown example")

// exampleNow is the clock an example's dates are moved to. A var so tests can
// pin today.
var exampleNow = time.Now

// maxExampleNameTries bounds the "-2", "-3", … search for a free name, so a
// workspace full of copies fails with a message instead of looping.
const maxExampleNameTries = 100

// InstallExample copies one example project out of examples (a filesystem
// whose top-level directories are example names, see nodevas/demo) into the
// workspace and makes it the active project.
//
// The copy lands at the workspace root under the example's own name, or the
// first free "name-2", "name-3"… when that is taken: opening an example a
// second time gives a fresh copy rather than refusing or overwriting the
// first. It returns the project name it created.
func (pm *ProjectManager) InstallExample(examples fs.FS, example string) (string, error) {
	if examples == nil || example == "" || example == "." || !fs.ValidPath(example) || path.Dir(example) != "." {
		return "", fmt.Errorf("%w %q", ErrUnknownExample, example)
	}
	if info, err := fs.Stat(examples, path.Join(example, "graph.yaml")); err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w %q", ErrUnknownExample, example)
	}

	defer pm.LockImport()()

	name, dest, err := pm.freeExampleName(example)
	if err != nil {
		return "", err
	}
	// A half-written copy leaves a directory nothing can open, so a failure
	// takes it away again, with any Store cached for it.
	cleanup := func() {
		pm.EvictStores(dest)
		_ = store.RemoveAllProjectPath(pm.Workspace(), dest)
	}
	copied := 0
	err = fs.WalkDir(examples, example, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(filepath.FromSlash(example), filepath.FromSlash(name))
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dest, rel)
		if entry.IsDir() {
			return store.MkdirAllProjectPath(pm.Workspace(), target, 0o755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("example %q contains non-regular file %q", example, name)
		}
		copied++
		if copied > MaxImportPathFiles {
			return fmt.Errorf("example %q has more than %d files", example, MaxImportPathFiles)
		}
		data, readErr := fs.ReadFile(examples, name)
		if readErr != nil {
			return readErr
		}
		return store.WriteProjectFileAtomicMode(pm.Workspace(), target, data, 0o644)
	})
	if err != nil {
		cleanup()
		return "", fmt.Errorf("copy example %q: %w", example, err)
	}
	if err := pm.OpenForCaller(name); err != nil {
		cleanup()
		return "", fmt.Errorf("open example %q: %w", example, err)
	}
	return name, nil
}

// freeExampleName is the first of example, example-2, example-3… with nothing
// at its path in the workspace yet, and that path.
func (pm *ProjectManager) freeExampleName(example string) (string, string, error) {
	for try := 1; try <= maxExampleNameTries; try++ {
		name := example
		if try > 1 {
			name = example + "-" + strconv.Itoa(try)
		}
		dest, err := pm.Resolve(name)
		if err != nil {
			return "", "", err
		}
		if _, err := store.StatProjectPath(pm.Workspace(), dest); errors.Is(err, os.ErrNotExist) {
			return name, dest, nil
		} else if err != nil {
			return "", "", err
		}
	}
	return "", "", fmt.Errorf("no free project name for example %q", example)
}
