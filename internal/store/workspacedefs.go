// Workspace-wide workflow definitions.
//
// Custom lifecycle states are a vocabulary, not project data: retyping "審稿"
// in every project is busywork and leaves each project with a slightly
// different palette. They live once per workspace, in
// <workspace>/.vised/workflow.json, and every project in that workspace reads
// them.
//
// A project's graph.yaml keeps only its own definitions. The shared ones are
// merged into the graph as it is loaded and stripped again as it is written,
// so sharing never rewrites a project file and removing a shared definition
// does not leave copies behind.

package store

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"

	"nodevas/internal/engine"
)

// WorkflowFileName is the shared definition file inside the workspace's
// .vised directory.
const WorkflowFileName = "workflow.json"

// MaxWorkspaceStatuses bounds the shared vocabulary; it is a palette, not a
// database.
const MaxWorkspaceStatuses = 200

type workflowFile struct {
	CustomStatuses []engine.StatusDefinition `json:"customStatuses"`
}

func WorkspaceWorkflowPath(workspace string) string {
	return filepath.Join(workspace, DataDir, WorkflowFileName)
}

// LoadWorkspaceStatuses reads the shared lifecycle states. A missing or
// unreadable file is an empty vocabulary, never an error the editor has to
// show: the projects still open, they just have no shared states.
func LoadWorkspaceStatuses(workspace string) []engine.StatusDefinition {
	if workspace == "" {
		return nil
	}
	data, err := ReadProjectFile(workspace, WorkspaceWorkflowPath(workspace))
	if err != nil {
		return nil
	}
	var file workflowFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil
	}
	if len(file.CustomStatuses) > MaxWorkspaceStatuses {
		file.CustomStatuses = file.CustomStatuses[:MaxWorkspaceStatuses]
	}
	return file.CustomStatuses
}

var workspaceWorkflowMu sync.Mutex

// SaveWorkspaceStatuses replaces the shared lifecycle states.
func SaveWorkspaceStatuses(workspace string, statuses []engine.StatusDefinition) error {
	workspaceWorkflowMu.Lock()
	defer workspaceWorkflowMu.Unlock()
	path := WorkspaceWorkflowPath(workspace)
	data, err := json.MarshalIndent(workflowFile{CustomStatuses: statuses}, "", "  ")
	if err != nil {
		return err
	}
	return WriteProjectFileAtomic(workspace, path, append(data, '\n'))
}

// mergeSharedStatuses adds the workspace vocabulary to an in-memory graph.
// A project that defines the same id keeps its own definition, so a local
// override is never overwritten by the shared one.
func mergeSharedStatuses(g *engine.Graph, shared []engine.StatusDefinition) {
	if g == nil || len(shared) == 0 {
		return
	}
	if g.UI == nil {
		g.UI = &engine.UIState{}
	}
	own := make(map[string]bool, len(g.UI.CustomStatuses))
	for _, definition := range g.UI.CustomStatuses {
		own[definition.ID] = true
	}
	for _, definition := range shared {
		if !own[definition.ID] {
			g.UI.CustomStatuses = append(g.UI.CustomStatuses, definition)
		}
	}
}

// withoutSharedStatuses returns the graph as it should be written to disk:
// shared definitions removed, everything else untouched. The graph itself is
// not modified, because the caller keeps using the merged copy.
func withoutSharedStatuses(
	g *engine.Graph,
	shared []engine.StatusDefinition,
) *engine.Graph {
	if g == nil || g.UI == nil || len(shared) == 0 || len(g.UI.CustomStatuses) == 0 {
		return g
	}
	sharedIDs := make(map[string]bool, len(shared))
	for _, definition := range shared {
		sharedIDs[definition.ID] = true
	}
	kept := make([]engine.StatusDefinition, 0, len(g.UI.CustomStatuses))
	for _, definition := range g.UI.CustomStatuses {
		if !sharedIDs[definition.ID] {
			kept = append(kept, definition)
		}
	}
	if len(kept) == len(g.UI.CustomStatuses) {
		return g
	}
	// Copy only what changes: the graph and its UI block, never the maps and
	// slices inside, which the caller is still reading.
	graphCopy := *g
	uiCopy := *g.UI
	uiCopy.CustomStatuses = kept
	graphCopy.UI = &uiCopy
	return &graphCopy
}

// SetSharedStatuses installs the workspace vocabulary provider. It is called
// once per Store, by whoever knows which workspace the project belongs to.
func (s *Store) SetSharedStatuses(provider func() []engine.StatusDefinition) {
	s.sharedMu.Lock()
	defer s.sharedMu.Unlock()
	s.sharedStatuses = provider
}

func (s *Store) sharedStatusDefinitions() []engine.StatusDefinition {
	s.sharedMu.Lock()
	provider := s.sharedStatuses
	s.sharedMu.Unlock()
	if provider == nil {
		return nil
	}
	return provider()
}

// marshalGraph encodes a graph for graph.yaml, leaving the workspace's shared
// definitions out of the file.
func (s *Store) marshalGraph(g *engine.Graph) ([]byte, error) {
	return engine.MarshalGraph(withoutSharedStatuses(g, s.sharedStatusDefinitions()))
}

// HoistProjectStatuses lifts each project's own lifecycle states into the
// workspace vocabulary, so a workspace opened for the first time after this
// became shared already has the states people defined project by project.
//
// It only ever adds. A definition whose id or label already exists in the
// shared list is left where it is, because two projects may have given the
// same id different meanings; that project keeps its local override.
func HoistProjectStatuses(workspace string, graphPaths []string) error {
	if workspace == "" || len(graphPaths) == 0 {
		return nil
	}
	shared := LoadWorkspaceStatuses(workspace)
	ids := make(map[string]bool, len(shared))
	labels := make(map[string]bool, len(shared))
	for _, definition := range shared {
		ids[definition.ID] = true
		labels[strings.ToLower(strings.TrimSpace(definition.Label))] = true
	}
	changed := false
	for _, path := range graphPaths {
		data, err := ReadProjectFile(workspace, path)
		if err != nil {
			continue
		}
		g, err := engine.ParseGraph(data)
		if err != nil || g.UI == nil {
			continue
		}
		for _, definition := range g.UI.CustomStatuses {
			label := strings.ToLower(strings.TrimSpace(definition.Label))
			if ids[definition.ID] || labels[label] || label == "" {
				continue
			}
			if len(shared) >= MaxWorkspaceStatuses {
				break
			}
			shared = append(shared, definition)
			ids[definition.ID] = true
			labels[label] = true
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return SaveWorkspaceStatuses(workspace, shared)
}
