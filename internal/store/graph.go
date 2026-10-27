// Reading and writing graph.yaml, and the per-node markdown that mirrors it.

package store

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

// ---------- graph ----------

func (s *Store) LoadGraph() (*engine.Graph, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadGraphLocked()
}

func (s *Store) loadGraphLocked() (*engine.Graph, string, error) {
	data, err := s.ReadFile(s.GraphPath())
	if err != nil {
		if os.IsNotExist(err) {
			return &engine.Graph{Version: 1}, "", nil
		}
		return nil, "", err
	}
	g, err := engine.ParseGraph(data)
	if err != nil {
		return nil, "", err
	}
	if err := ValidateGraphForStorage(g); err != nil {
		return nil, "", fmt.Errorf("graph.yaml: %w", err)
	}
	// The workspace vocabulary is part of the graph everyone downstream reads,
	// but never part of the file: see workspacedefs.go.
	mergeSharedStatuses(g, s.sharedStatusDefinitions())
	return g, Rev(data), nil
}

// SaveGraph writes graph.yaml (optimistic-locked) and
// refreshes every node file's frontmatter snapshot.
func (s *Store) SaveGraph(actor identity.Actor, g *engine.Graph, baseRev string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	engine.NormalizeLogicGates(g)
	if err := ValidateGraphForStorage(g); err != nil {
		return "", err
	}
	if err := s.checkRevision(s.GraphPath(), baseRev); err != nil {
		return "", err
	}
	currentGraph, _, err := s.loadGraphLocked()
	if err != nil {
		return "", err
	}
	for _, node := range currentGraph.Nodes {
		if node != nil && g.NodeByID(node.ID) == nil {
			return "", fmt.Errorf("cannot remove node %q through graph save; use the node delete endpoint", node.ID)
		}
	}
	// Node types decide what agents may do, so an agent may neither redefine
	// them nor retype a node into or out of a type it may not complete.
	if err := checkNodeTypesEdit(actor, currentGraph.NodeTypes, g.NodeTypes); err != nil {
		return "", err
	}
	if err := checkOfficeStyleEdit(actor, currentGraph.OfficeStyle, g.OfficeStyle); err != nil {
		return "", err
	}
	// Roles and stages decide what agents may do, so the same holds for them.
	if err := checkAgentRolesEdit(actor, currentGraph.AgentRoles, g.AgentRoles); err != nil {
		return "", err
	}
	if err := checkWorkflowEdit(actor, "", currentGraph.Workflow, g.Workflow); err != nil {
		return "", err
	}
	var createdByRun map[string]bool
	if actor.HasRole() && actor.RunID != "" {
		createdByRun = map[string]bool{}
		for id, entry := range s.loadCreatedByLocked().Nodes {
			if entry.Run == actor.RunID {
				createdByRun[id] = true
			}
		}
	}
	updates := make([]fileUpdate, 0, len(g.Nodes)+1)
	for _, n := range g.Nodes {
		existing := currentGraph.NodeByID(n.ID)
		if reflect.DeepEqual(existing, n) {
			continue
		}
		// A changed node is gated by the access it had before this save, so an
		// agent cannot lift a restriction by rewriting the whole file. A node
		// the save leaves untouched needs no permission, and a new node
		// (existing == nil) is anyone's to add.
		if err := checkNodeWrite(actor, existing); err != nil {
			return "", err
		}
		if existing != nil {
			if err := checkKindChange(actor, currentGraph, existing.Kind, n.Kind); err != nil {
				return "", err
			}
		}
		if err := s.checkNodeSaveRoles(actor, currentGraph, existing, n, createdByRun[n.ID]); err != nil {
			return "", err
		}
		update, err := s.prepareNodeFileUpdate(n)
		if err != nil {
			return "", fmt.Errorf("sync node %s: %w", n.ID, err)
		}
		if update != nil {
			updates = append(updates, *update)
		}
	}
	// Marshalled after the node loop: it normalizes acceptance in place.
	data, err := s.marshalGraph(g)
	if err != nil {
		return "", err
	}
	updates = append(updates, fileUpdate{
		path:          s.GraphPath(),
		data:          data,
		checkRevision: true,
		expectedRev:   baseRev,
	})
	if err := s.applyUpdatesLocked(actor, updates); err != nil {
		return "", err
	}
	return Rev(data), nil
}

func (s *Store) prepareNodeFileUpdate(n *engine.Node) (*fileUpdate, error) {
	path := s.NodePath(n.ID)
	var nf *engine.NodeFile
	prev, err := s.ReadFile(path)
	if err == nil {
		nf, err = engine.ParseNodeFile(prev)
		if err != nil {
			return nil, err
		}
	} else if errors.Is(err, os.ErrNotExist) {
		nf = &engine.NodeFile{Meta: map[string]any{}, Body: ""}
	} else {
		return nil, err
	}
	engine.SyncFrontmatter(nf, n)
	out, err := engine.ComposeNodeFile(nf)
	if err != nil {
		return nil, err
	}
	if prev != nil && bytes.Equal(prev, out) {
		return nil, nil
	}
	expectedRev := ""
	if prev != nil {
		expectedRev = Rev(prev)
	}
	return &fileUpdate{
		path:          path,
		data:          out,
		checkRevision: true,
		expectedRev:   expectedRev,
	}, nil
}

// ---------- node content ----------

func (s *Store) LoadNodeContent(id string) (string, string, error) {
	if !engine.ValidNodeID(id) {
		return "", "", fmt.Errorf("invalid node id")
	}
	data, err := s.ReadFile(s.NodePath(id))
	if err != nil {
		return "", "", err
	}
	return string(data), Rev(data), nil
}

// SaveNodeContent writes a node's markdown with exact optimistic locking.
// It returns the composed file as well as its revision. The store rewrites the
// frontmatter from the graph, so what lands on disk is rarely byte-for-byte
// what the caller sent, and the caller has to adopt it or its next save starts
// from a revision that never existed. Handing it back with the revision is what
// lets an auto-saving editor stay in step without a GET after every save.
func (s *Store) SaveNodeContent(actor identity.Actor, id, content, baseRev string) (string, string, error) {
	if !engine.ValidNodeID(id) {
		return "", "", fmt.Errorf("invalid node id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkRevision(s.NodePath(id), baseRev); err != nil {
		return "", "", err
	}
	g, _, err := s.loadGraphLocked()
	if err != nil {
		return "", "", err
	}
	n := g.NodeByID(id)
	if n == nil {
		return "", "", fmt.Errorf("node %q not found in graph", id)
	}
	if err := checkNodeWrite(actor, n); err != nil {
		return "", "", err
	}
	if err := checkRole(actor, g, engine.CapBody, n); err != nil {
		return "", "", err
	}
	nf, err := engine.ParseNodeFile([]byte(content))
	if err != nil {
		return "", "", fmt.Errorf("node file: %w", err)
	}
	engine.SyncFrontmatter(nf, n)
	out, err := engine.ComposeNodeFile(nf)
	if err != nil {
		return "", "", err
	}
	if err := s.applyUpdatesLocked(actor, []fileUpdate{{
		path:          s.NodePath(id),
		data:          out,
		checkRevision: true,
		expectedRev:   baseRev,
	}}); err != nil {
		return "", "", err
	}
	_ = s.DeleteDraft(id) // saved: draft no longer needed
	return string(out), Rev(out), nil
}

// checkNodeSaveRoles applies the role, workflow and acceptance rules to one
// node a whole-graph save adds or changes. A new node is a create; a changed
// one is a metadata edit unless only its acceptance changed. The stored
// acceptance is normalized in place (lock and attribution).
func (s *Store) checkNodeSaveRoles(actor identity.Actor, g *engine.Graph, existing, n *engine.Node, createdByRun bool) error {
	var beforeWorkflow *engine.Workflow
	var beforeAcceptance *engine.Acceptance
	if existing != nil {
		beforeWorkflow, beforeAcceptance = existing.Workflow, existing.Acceptance
	}
	if err := checkWorkflowEdit(actor, n.ID, beforeWorkflow, n.Workflow); err != nil {
		return err
	}
	if existing == nil {
		if err := checkRole(actor, g, engine.CapCreate, n); err != nil {
			return err
		}
	} else {
		rest, was := *n, *existing
		rest.Acceptance, was.Acceptance = nil, nil
		if !reflect.DeepEqual(&rest, &was) {
			if err := checkRole(actor, g, engine.CapMeta, existing); err != nil {
				return err
			}
		}
	}
	if !acceptanceEqual(beforeAcceptance, n.Acceptance) {
		normalized := normalizeAcceptance(actor, beforeAcceptance, n.Acceptance)
		if !actor.IsAgent() {
			n.Acceptance = normalized
		}
		if err := checkAcceptanceWrite(actor, g, n, beforeAcceptance, n.Acceptance, createdByRun || existing == nil); err != nil {
			return err
		}
		if actor.IsAgent() && n.Acceptance != nil {
			by := acceptanceAttribution(actor)
			if n.Acceptance.EqualItems(beforeAcceptance) && beforeAcceptance != nil {
				by = beforeAcceptance.By
			}
			n.Acceptance = &engine.Acceptance{Items: engine.AssignAcceptanceIDs(n.Acceptance.Items),
				Locked: n.Acceptance.Locked, By: by}
		}
	}
	return nil
}
