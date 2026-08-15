package project

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	projectdomain "nodevas/internal/project"
	"nodevas/internal/realtime"
	"nodevas/internal/store"
)

// writeStatusProject creates one project holding a single custom lifecycle
// state.
func writeStatusProject(t *testing.T, workspace, name, statusID, label string) {
	t.Helper()
	root := filepath.Join(workspace, name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	graph := "version: 1\nnodes:\n  - id: n1\n    title: One\nui:\n  custom_statuses:\n" +
		"    - id: " + statusID + "\n      label: " + label +
		"\n      color: \"#888888\"\n      shape: circle\n"
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), []byte(graph), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sharedLabels(t *testing.T, workspace string) []string {
	t.Helper()
	statuses := store.LoadWorkspaceStatuses(workspace)
	out := make([]string, 0, len(statuses))
	for _, definition := range statuses {
		out = append(out, definition.Label)
	}
	sort.Strings(out)
	return out
}

func TestHoistStatusesRerunsWhenProjectsChange(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, workspace string)
		want   []string
	}{
		{
			name:   "same projects, no second read",
			change: func(t *testing.T, workspace string) {},
			want:   []string{"draft"},
		},
		{
			// The project list is the signal, so a state added to a project
			// that was already hoisted waits for the project to be written
			// through the editor rather than being picked up here.
			name: "known project gains a state",
			change: func(t *testing.T, workspace string) {
				writeStatusProject(t, workspace, "a", "custom-status-late", "late")
			},
			want: []string{"draft"},
		},
		{
			name: "project created after the first hoist",
			change: func(t *testing.T, workspace string) {
				writeStatusProject(t, workspace, "b", "custom-status-review", "review")
			},
			want: []string{"draft", "review"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			workspace := t.TempDir()
			writeStatusProject(t, workspace, "a", "custom-status-draft", "draft")
			pm, err := projectdomain.NewManagerAt(workspace, realtime.NewHub(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(pm.Close)
			api := New(pm)

			api.hoistStatuses(pm.Workspace())
			if got := sharedLabels(t, pm.Workspace()); len(got) != 1 || got[0] != "draft" {
				t.Fatalf("first hoist = %v, want [draft]", got)
			}
			testCase.change(t, pm.Workspace())

			api.hoistStatuses(pm.Workspace())
			got := sharedLabels(t, pm.Workspace())
			if len(got) != len(testCase.want) {
				t.Fatalf("second hoist = %v, want %v", got, testCase.want)
			}
			for i, want := range testCase.want {
				if got[i] != want {
					t.Errorf("second hoist[%d] = %q, want %q", i, got[i], want)
				}
			}
		})
	}
}
