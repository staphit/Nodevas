package project

import (
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"nodevas/demo"
	"nodevas/internal/engine"
	"nodevas/internal/realtime"
	"nodevas/internal/store"
)

// pinExampleToday makes "today" for an example install the given date.
func pinExampleToday(t *testing.T, today string) {
	t.Helper()
	day, err := time.ParseInLocation(time.DateOnly, today, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	previous := exampleNow
	exampleNow = func() time.Time { return day.Add(9 * time.Hour) }
	t.Cleanup(func() { exampleNow = previous })
}

func exampleManager(t *testing.T) *ProjectManager {
	t.Helper()
	pm, err := NewManagerAt(t.TempDir(), realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pm.Close)
	return pm
}

func readInstalled(t *testing.T, pm *ProjectManager, name, rel string) string {
	t.Helper()
	dir, err := pm.Resolve(name)
	if err != nil {
		t.Fatal(err)
	}
	data, err := store.ReadProjectFile(dir, filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// An example's plan is written for the day it was made. Copied two months
// later, every step was already overdue the moment it opened: the Problems
// panel listed four or five at once. The copy moves every date by the same
// number of days, so the earliest planned day is today and the run that the
// example shows happened just before it.
func TestInstallExampleMovesItsDatesToToday(t *testing.T) {
	pinExampleToday(t, "2026-10-28")
	examples := fstest.MapFS{
		"plan/graph.yaml": {Data: []byte("version: 1\ntype: workflow\nnodes:\n" +
			"  - id: n1\n    title: One\n    deadline: \"2026-08-14\"\n" +
			"  - id: n2\n    title: Two\n    deadline: 2026-08-20T17:30\n" +
			"ui:\n  plans:\n    n1:\n      - date: \"2026-08-13\"\n        status: started\n" +
			"    n2:\n      - date: \"2026-08-16\"\n        status: started\n")},
		"plan/run/state.json":    {Data: []byte(`{"startedAt":"2026-08-11T18:42:39Z","nodes":{"n1":{"status":"done","at":"2026-08-11T18:42:39Z"}}}`)},
		"plan/run/journal.jsonl": {Data: []byte(`{"id":"ev-1786473759949184100-0","t":"2026-08-11T18:42:39Z","event":"status"}` + "\n")},
		"plan/nodes/n1.md":       {Data: []byte("Written on 2026-08-11.\n")},
	}
	pm := exampleManager(t)
	name, err := pm.InstallExample(examples, "plan")
	if err != nil {
		t.Fatal(err)
	}

	// 2026-08-13 is the earliest planned day, and it becomes 2026-10-28:
	// 76 days later.
	graph := readInstalled(t, pm, name, "graph.yaml")
	for _, want := range []string{
		`deadline: "2026-10-29"`, "deadline: 2026-11-04T17:30",
		`date: "2026-10-28"`, `date: "2026-10-31"`,
	} {
		if !strings.Contains(graph, want) {
			t.Errorf("graph.yaml lacks %s:\n%s", want, graph)
		}
	}
	if state := readInstalled(t, pm, name, "run/state.json"); !strings.Contains(state, `"startedAt":"2026-10-26T18:42:39Z"`) ||
		!strings.Contains(state, `"at":"2026-10-26T18:42:39Z"`) {
		t.Errorf("run/state.json was not moved with the plan: %s", state)
	}
	journal := readInstalled(t, pm, name, "run/journal.jsonl")
	if !strings.Contains(journal, `"t":"2026-10-26T18:42:39Z"`) || !strings.Contains(journal, "ev-1786473759949184100-0") {
		t.Errorf("run/journal.jsonl was not moved with the plan, or its ids changed: %s", journal)
	}
	if doc := readInstalled(t, pm, name, "nodes/n1.md"); doc != "Written on 2026-08-11.\n" {
		t.Errorf("a node document's text changed: %q", doc)
	}
}

// Every bundled example, in every language, opens with nothing planned or due
// before today.
func TestBundledExamplesOpenWithNothingOverdue(t *testing.T) {
	const today = "2026-10-28"
	pinExampleToday(t, today)
	for _, language := range []string{"en", "zh-TW"} {
		for _, example := range demo.Names() {
			t.Run(language+"/"+example, func(t *testing.T) {
				pm := exampleManager(t)
				name, err := pm.InstallExample(demo.ExamplesFor(language), example)
				if err != nil {
					t.Fatal(err)
				}
				g, err := engine.ParseGraph([]byte(readInstalled(t, pm, name, "graph.yaml")))
				if err != nil {
					t.Fatal(err)
				}
				for _, node := range g.Nodes {
					if node.Deadline != "" && node.Deadline[:len(today)] < today {
						t.Errorf("%s is due %s, before today", node.ID, node.Deadline)
					}
				}
				if g.UI == nil {
					return
				}
				for id, milestones := range g.UI.Plans {
					for _, milestone := range milestones {
						if milestone.Date < today {
							t.Errorf("%s has a milestone on %s, before today", id, milestone.Date)
						}
					}
				}
			})
		}
	}
}
