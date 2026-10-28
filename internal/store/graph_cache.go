// The parsed-graph cache.
//
// Parsing and validating graph.yaml is most of what a read costs (about 10 ms
// at 2k nodes), and every client re-reads the graph after every change. So the
// store keeps the last graph it parsed and hands that same value to every
// reader until the file's bytes change.
//
// Freshness does not depend on anyone remembering to invalidate: each read
// still reads graph.yaml and compares its SHA-256 with the cached one, so an
// edit from outside the server (git checkout, an agent writing the file, a
// text editor) is picked up by the next read even with the size and timestamp
// unchanged. Store writes and the file watcher also drop the entry, which only
// frees memory sooner.
//
// The cached graph is shared, so it is read-only. LoadGraph's callers must not
// change what they get; one that wants to edit a graph clones it
// (engine.Graph.Clone). Store write paths go through loadGraphLocked, which
// hands out a private clone.
//
// Write paths clone a second, pristine copy that is never handed to anyone.
// A caller that breaks the read-only rule can then only confuse readers until
// the next change; it cannot reach what a write checks permissions against or
// writes back to disk.

package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"nodevas/internal/engine"
)

type cachedGraph struct {
	sum    [sha256.Size]byte
	shared []engine.StatusDefinition
	// graph is what readers get; pristine is only ever cloned.
	graph    *engine.Graph
	pristine *engine.Graph
	rev      string
}

// InvalidateGraphCache drops the cached graph. The file watcher calls it when
// graph.yaml changes on disk.
func (s *Store) InvalidateGraphCache() {
	s.graphCacheMu.Lock()
	s.graphCache = nil
	s.graphCacheMu.Unlock()
}

func (s *Store) isGraphPath(path string) bool {
	return filepath.Clean(path) == filepath.Clean(s.GraphPath())
}

func (s *Store) cachedGraphFor(sum [sha256.Size]byte, shared []engine.StatusDefinition) *cachedGraph {
	s.graphCacheMu.Lock()
	defer s.graphCacheMu.Unlock()
	cached := s.graphCache
	if cached == nil || cached.sum != sum || !slices.Equal(cached.shared, shared) {
		return nil
	}
	return cached
}

// graphViewLocked returns the current graph as a shared, read-only value.
// The caller holds s.mu, for reading or writing.
func (s *Store) graphViewLocked() (*engine.Graph, string, error) {
	entry, err := s.currentGraphLocked()
	if err != nil {
		return nil, "", err
	}
	return entry.graph, entry.rev, nil
}

// loadGraphLocked returns a private copy of the graph that the caller, a write
// path holding s.mu, may change and save. Read-only paths use graphViewLocked
// and skip the copy.
func (s *Store) loadGraphLocked() (*engine.Graph, string, error) {
	entry, err := s.currentGraphLocked()
	if err != nil {
		return nil, "", err
	}
	return entry.pristine.Clone(), entry.rev, nil
}

// currentGraphLocked reads graph.yaml and returns the cache entry for its
// bytes, parsing them on a miss.
//
// Readers holding s.mu only for reading may run this concurrently;
// graphFillMu makes the ones that miss wait for one parse instead of each
// parsing the same bytes.
func (s *Store) currentGraphLocked() (*cachedGraph, error) {
	data, err := s.ReadFile(s.GraphPath())
	if err != nil {
		if os.IsNotExist(err) {
			empty := &engine.Graph{Version: 1}
			return &cachedGraph{graph: empty, pristine: empty.Clone()}, nil
		}
		return nil, err
	}
	sum := sha256.Sum256(data)
	// The workspace vocabulary is part of the graph everyone downstream reads,
	// but never part of the file (workspacedefs.go), so it is part of the key.
	shared := s.sharedStatusDefinitions()
	if cached := s.cachedGraphFor(sum, shared); cached != nil {
		return cached, nil
	}

	s.graphFillMu.Lock()
	defer s.graphFillMu.Unlock()
	if cached := s.cachedGraphFor(sum, shared); cached != nil {
		return cached, nil
	}
	g, err := engine.ParseGraph(data)
	if err != nil {
		return nil, err
	}
	if err := ValidateGraphForStorage(g); err != nil {
		return nil, fmt.Errorf("graph.yaml: %w", err)
	}
	mergeSharedStatuses(g, shared)
	entry := &cachedGraph{
		sum: sum, shared: slices.Clone(shared),
		graph: g, pristine: g.Clone(),
		rev: hex.EncodeToString(sum[:])[:12], // == Rev(data)
	}
	s.graphCacheMu.Lock()
	s.graphCache = entry
	s.graphCacheMu.Unlock()
	return entry, nil
}
