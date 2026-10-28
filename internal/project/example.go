package project

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
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
	shift := exampleDateShift(examples, example)
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
		if shift != 0 && datedExampleFile(rel) {
			data = shiftDates(data, shift)
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

// An example's plan is written for the day it was made, and copied as it is
// it opens with every step overdue. So a copy moves every date in its plan
// (graph.yaml) and its run (run/*.json, run/*.jsonl) by the same number of
// days: the earliest date in the plan becomes today, and the run the example
// shows lands just before it. Node documents are prose and keep their text.

// exampleDate matches one YYYY-MM-DD, alone or at the start of a timestamp.
var exampleDate = regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}`)

// exampleDateShift is how many days the example's dates move: from the
// earliest date its graph.yaml holds to today. 0 when it holds none.
func exampleDateShift(examples fs.FS, example string) int {
	data, err := fs.ReadFile(examples, path.Join(example, "graph.yaml"))
	if err != nil {
		return 0
	}
	var earliest time.Time
	eachDate(data, func(day time.Time) {
		if earliest.IsZero() || day.Before(earliest) {
			earliest = day
		}
	})
	if earliest.IsZero() {
		return 0
	}
	year, month, date := exampleNow().Date()
	today := time.Date(year, month, date, 0, 0, 0, 0, time.UTC)
	return int(today.Sub(earliest).Hours() / 24)
}

// datedExampleFile reports whether rel (relative to the example) holds
// dates that move with the plan.
func datedExampleFile(rel string) bool {
	rel = filepath.ToSlash(rel)
	if rel == "graph.yaml" {
		return true
	}
	return path.Dir(rel) == "run" && (path.Ext(rel) == ".json" || path.Ext(rel) == ".jsonl")
}

// shiftDates moves every date in data by days.
func shiftDates(data []byte, days int) []byte {
	var out []byte
	last := 0
	for _, match := range exampleDate.FindAllIndex(data, -1) {
		day, ok := parseExampleDate(data, match)
		if !ok {
			continue
		}
		out = append(out, data[last:match[0]]...)
		out = append(out, day.AddDate(0, 0, days).Format(time.DateOnly)...)
		last = match[1]
	}
	if out == nil {
		return data
	}
	return append(out, data[last:]...)
}

// eachDate calls visit with every date in data.
func eachDate(data []byte, visit func(time.Time)) {
	for _, match := range exampleDate.FindAllIndex(data, -1) {
		if day, ok := parseExampleDate(data, match); ok {
			visit(day)
		}
	}
}

// parseExampleDate reads the date at match, which must not be part of a
// longer run of digits.
func parseExampleDate(data []byte, match []int) (time.Time, bool) {
	isDigit := func(b byte) bool { return b >= '0' && b <= '9' }
	if match[0] > 0 && isDigit(data[match[0]-1]) || match[1] < len(data) && isDigit(data[match[1]]) {
		return time.Time{}, false
	}
	day, err := time.Parse(time.DateOnly, string(data[match[0]:match[1]]))
	return day, err == nil
}
