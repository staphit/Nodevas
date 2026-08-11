// Reading and writing graph.yaml, and the per-node markdown that mirrors it.

package store

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"

	"nodevas/internal/engine"
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
func (s *Store) SaveGraph(g *engine.Graph, baseRev string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	data, err := s.marshalGraph(g)
	if err != nil {
		return "", err
	}
	updates := make([]fileUpdate, 0, len(g.Nodes)+1)
	for _, n := range g.Nodes {
		if reflect.DeepEqual(currentGraph.NodeByID(n.ID), n) {
			continue
		}
		update, err := s.prepareNodeFileUpdate(n)
		if err != nil {
			return "", fmt.Errorf("sync node %s: %w", n.ID, err)
		}
		if update != nil {
			updates = append(updates, *update)
		}
	}
	updates = append(updates, fileUpdate{
		path:          s.GraphPath(),
		data:          data,
		checkRevision: true,
		expectedRev:   baseRev,
	})
	if err := s.applyUpdatesLocked(updates); err != nil {
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
func (s *Store) SaveNodeContent(id, content, baseRev string) (string, string, error) {
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
	nf, err := engine.ParseNodeFile([]byte(content))
	if err != nil {
		return "", "", fmt.Errorf("node file: %w", err)
	}
	engine.SyncFrontmatter(nf, n)
	out, err := engine.ComposeNodeFile(nf)
	if err != nil {
		return "", "", err
	}
	if err := s.applyUpdatesLocked([]fileUpdate{{
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
