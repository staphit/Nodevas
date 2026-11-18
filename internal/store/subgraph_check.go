// The dry run of create_subgraph: what a commit would do, written nowhere.

package store

import "nodevas/internal/identity"

// CheckSubgraph is CreateSubgraph's dry run: every check CreateSubgraph makes
// (they share prepareSubgraphLocked), and nothing written: no file, no
// history, no change event, no batch record. The ids in the result are the
// ones a commit made now would get, not a reservation; Rev is the board's
// current revision and Issues the validation of the board the commit would
// leave. BatchID and Undoable stay empty.
//
// It holds the store's lock only for reading, so a person's debounced
// re-checks never queue behind each other.
func (s *Store) CheckSubgraph(actor identity.Actor, spec SubgraphSpec) (SubgraphResult, error) {
	if err := checkSubgraphShape(spec); err != nil {
		return SubgraphResult{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Everything prepare reads (the graph through currentGraphLocked, the
	// trash listing, a stat per new node) is safe under the read lock, and
	// what it changes is its own clone of the graph.
	plan, err := s.prepareSubgraphLocked(actor, spec)
	if err != nil {
		return SubgraphResult{}, err
	}
	return plan.result, nil
}
