package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseNodeLinksResolvesTargets(t *testing.T) {
	links := ParseNodeLinks(
		"見 [[node-0006]] 與 [[Story/node-0012|主線]]，還有 [[a/b/node-1]]",
		"Current",
	)
	if len(links) != 3 {
		t.Fatalf("links = %+v, want 3", links)
	}
	// A bare target belongs to the document's own project.
	if links[0].ToProject != "Current" || links[0].ToNode != "node-0006" {
		t.Errorf("link 0 = %+v", links[0])
	}
	if links[1].ToProject != "Story" || links[1].ToNode != "node-0012" ||
		links[1].Label != "主線" {
		t.Errorf("link 1 = %+v", links[1])
	}
	// Nested project paths split at the last slash.
	if links[2].ToProject != "a/b" || links[2].ToNode != "node-1" {
		t.Errorf("link 2 = %+v", links[2])
	}
}

func TestParseNodeLinksSkipsEmptyAndMultiline(t *testing.T) {
	if links := ParseNodeLinks("[[]] [[Story/\nnode-1]]", "P"); len(links) != 0 {
		t.Errorf("links = %+v, want none", links)
	}
}

func TestNodeTitlesKeepsGraphOrder(t *testing.T) {
	dir := t.TempDir()
	graph := []byte("version: 1\nnodes:\n  - id: b\n    title: Beta\n  - id: a\n    title: Alpha\n")
	if err := os.WriteFile(filepath.Join(dir, "graph.yaml"), graph, 0o644); err != nil {
		t.Fatal(err)
	}
	got := NodeTitles(ProjectInfo{Name: "p", Path: dir})
	want := [][2]string{{"b", "Beta"}, {"a", "Alpha"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("NodeTitles = %v, want %v", got, want)
	}
}
