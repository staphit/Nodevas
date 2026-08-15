// Bulk creation of a project's initial content, for importing a folder of
// documents that has no graph.yaml of its own.

package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"nodevas/internal/engine"
)

// ImportDocument is one source file on its way to becoming a node.
type ImportDocument struct {
	Title string
	Body  string
}

// ImportDocuments installs docs as the whole content of a project that does
// not exist yet: every document becomes nodes/node-NNNN.md, and graph.yaml is
// written last so a reader never sees a graph naming a node file that is not
// on disk. The batch is one critical section, which is what keeps a save
// running beside an import from being overwritten by it — the reason this
// lives here rather than in the handler that calls it.
//
// Every file is created with an empty base revision, so an import can never
// land on top of content someone else owns.
func (s *Store) ImportDocuments(g *engine.Graph, docs []ImportDocument) (int, error) {
	if g == nil {
		return 0, errors.New("graph is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.statPath(s.GraphPath()); err == nil {
		return 0, fmt.Errorf("%s already exists", s.GraphPath())
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	// An import of nothing still has to leave a usable project behind, so the
	// node directory is made even when no document lands in it.
	if err := MkdirAllProjectPath(s.root, filepath.Join(s.root, "nodes"), 0o755); err != nil {
		return 0, err
	}

	updates := make([]fileUpdate, 0, len(docs)+1)
	for index, doc := range docs {
		id := fmt.Sprintf("node-%04d", index+1)
		nodeFile, err := engine.ParseNodeFile([]byte(doc.Body))
		if err != nil {
			return 0, fmt.Errorf("%s: %w", doc.Title, err)
		}
		node := &engine.Node{ID: id, Title: doc.Title}
		hydrateNodeFromMeta(node, nodeFile.Meta)
		if node.Kind == "" {
			node.Kind = "task"
		}
		engine.SyncFrontmatter(nodeFile, node)
		out, err := engine.ComposeNodeFile(nodeFile)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", doc.Title, err)
		}
		g.Nodes = append(g.Nodes, node)
		updates = append(updates, fileUpdate{
			path:          s.NodePath(id),
			data:          out,
			checkRevision: true,
		})
	}
	if err := ValidateGraphForStorage(g); err != nil {
		return 0, err
	}
	data, err := s.marshalGraph(g)
	if err != nil {
		return 0, err
	}
	updates = append(updates, fileUpdate{
		path:          s.GraphPath(),
		data:          data,
		checkRevision: true,
	})
	if err := s.applyUpdatesLocked(updates); err != nil {
		return 0, err
	}
	return len(docs), nil
}
