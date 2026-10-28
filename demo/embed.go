// Package demo embeds the example projects in this directory into the
// nodevas binary, so the start screen can offer them on a fresh workspace
// without the repository being anywhere near the machine it runs on.
//
// Each example is an ordinary project directory: graph.yaml, the node
// documents under nodes/, and run/ with the progress the example shows.
package demo

import (
	"embed"
	"io/fs"
	"sort"
)

//go:embed ai-harness-engineering daily-work-schedule novel-writing
var files embed.FS

// Examples returns the examples as one filesystem whose top-level
// directories are the example names.
func Examples() fs.FS {
	return files
}

// Names lists the embedded examples in a stable order.
func Names() []string {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

// ExamplesFor returns the examples in language lang (stub: the English
// originals for every language until the localized copies are wired).
func ExamplesFor(lang string) fs.FS {
	return files
}
