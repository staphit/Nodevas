package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/engine"
)

func TestWorkspaceStatusesMergeAndStrip(t *testing.T) {
	workspace := t.TempDir()
	projectDir := filepath.Join(workspace, "p1")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shared := []engine.StatusDefinition{
		{ID: "custom-status-1", Label: "審稿", Color: "#888", Shape: "circle"},
	}
	if err := SaveWorkspaceStatuses(workspace, shared); err != nil {
		t.Fatal(err)
	}

	st := NewStore(projectDir)
	st.SetSharedStatuses(func() []engine.StatusDefinition {
		return LoadWorkspaceStatuses(workspace)
	})
	graph := &engine.Graph{Version: 1, Nodes: []*engine.Node{{ID: "a", Title: "A"}}}
	if _, err := st.SaveGraph(graph, ""); err != nil {
		t.Fatal(err)
	}

	loaded, rev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.UI == nil || len(loaded.UI.CustomStatuses) != 1 ||
		loaded.UI.CustomStatuses[0].ID != "custom-status-1" {
		t.Fatalf("shared status not merged: %+v", loaded.UI)
	}

	// Saving the merged graph must not copy the shared state into the file.
	if _, err := st.SaveGraph(loaded, rev); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(st.GraphPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "custom-status-1") {
		t.Fatalf("shared status leaked into graph.yaml:\n%s", data)
	}
}

func TestSetStatusAcceptsWorkspaceStatus(t *testing.T) {
	workspace := t.TempDir()
	projectDir := filepath.Join(workspace, "p1")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SaveWorkspaceStatuses(workspace, []engine.StatusDefinition{
		{ID: "custom-status-7", Label: "審稿", Color: "#888", Shape: "circle"},
	}); err != nil {
		t.Fatal(err)
	}
	st := NewStore(projectDir)
	st.SetSharedStatuses(func() []engine.StatusDefinition {
		return LoadWorkspaceStatuses(workspace)
	})
	if _, err := st.SaveGraph(&engine.Graph{
		Version: 1, Nodes: []*engine.Node{{ID: "a", Title: "A"}},
	}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetStatus("a", engine.Status("custom-status-7"), "tester", ""); err != nil {
		t.Fatalf("workspace status rejected: %v", err)
	}
}

func TestHoistProjectStatusesLiftsExistingDefinitions(t *testing.T) {
	workspace := t.TempDir()
	write := func(name string, statuses []engine.StatusDefinition) string {
		dir := filepath.Join(workspace, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		data, err := engine.MarshalGraph(&engine.Graph{
			Version: 1,
			Nodes:   []*engine.Node{{ID: "a", Title: "A"}},
			UI:      &engine.UIState{CustomStatuses: statuses},
		})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "graph.yaml")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	story := write("story", []engine.StatusDefinition{
		{ID: "custom-status-1", Label: "概念節點", Color: "#8b7cf6", Shape: "circle"},
	})
	// Same id, different meaning: the first one wins, the other stays local.
	other := write("other", []engine.StatusDefinition{
		{ID: "custom-status-1", Label: "待審", Color: "#111", Shape: "square"},
		{ID: "custom-status-2", Label: "封存", Color: "#222", Shape: "dash"},
	})

	if err := HoistProjectStatuses(workspace, []string{story, other}); err != nil {
		t.Fatal(err)
	}
	shared := LoadWorkspaceStatuses(workspace)
	if len(shared) != 2 {
		t.Fatalf("shared = %+v, want 概念節點 and 封存", shared)
	}
	if shared[0].Label != "概念節點" || shared[1].Label != "封存" {
		t.Errorf("shared = %+v", shared)
	}

	// Running it again adds nothing.
	if err := HoistProjectStatuses(workspace, []string{story, other}); err != nil {
		t.Fatal(err)
	}
	if again := LoadWorkspaceStatuses(workspace); len(again) != 2 {
		t.Errorf("second run changed the list: %+v", again)
	}
}

func TestHoistProjectStatusesRejectsNestedProjectSymlink(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	data, err := engine.MarshalGraph(&engine.Graph{
		Version: 1,
		UI: &engine.UIState{CustomStatuses: []engine.StatusDefinition{
			{ID: "custom-status-outside", Label: "outside"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "graph.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	group := filepath.Join(workspace, "group")
	if err := os.MkdirAll(group, 0o755); err != nil {
		t.Fatal(err)
	}
	linkedProject := filepath.Join(group, "linked-project")
	if err := os.Symlink(outside, linkedProject); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if err := HoistProjectStatuses(workspace, []string{filepath.Join(linkedProject, "graph.yaml")}); err != nil {
		t.Fatal(err)
	}
	if statuses := LoadWorkspaceStatuses(workspace); len(statuses) != 0 {
		t.Fatalf("outside graph was read through nested project symlink: %+v", statuses)
	}
}
