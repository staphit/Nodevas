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
	"slices"
	"sort"
)

//go:embed ai-harness-engineering daily-work-schedule novel-writing zh-TW
var files embed.FS

// localized holds each translated set of examples, under its language tag:
// the same example names, the same ids, the text translated.
var localized = []string{"zh-TW"}

// Examples returns the English examples as one filesystem whose top-level
// directories are the example names.
func Examples() fs.FS {
	return files
}

// ExamplesFor returns the examples in language lang: its translated copies
// when there are any, else the English originals.
func ExamplesFor(lang string) fs.FS {
	if slices.Contains(localized, lang) {
		if sub, err := fs.Sub(files, lang); err == nil {
			return sub
		}
	}
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
		if entry.IsDir() && !slices.Contains(localized, entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

