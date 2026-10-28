// Group commit for graph ops.
//
// A graph op is a few bytes of change to graph.yaml, but committing it means
// reading the file, marshalling the whole graph, writing a temporary copy,
// fsyncing it and renaming it into place, all under the store's write lock.
// One at a time, that I/O is the ceiling on writes per second however many
// people are dragging cards, and every write queues behind every other.
//
// So the calls that pile up while the lock is held are committed together.
// The first caller to arrive leads: it takes the lock, drains the queue and
// applies every queued call in arrival order, each to its own copy of the
// graph as the calls before it left it. A call whose ops fail or leave an
// invalid graph is refused alone; the rest are marshalled and written once.
// The leader then hands the lead to the oldest call that queued meanwhile, so
// no caller serves the queue for longer than one batch.

package store

import (
	"errors"
	"fmt"
	"sort"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

// maxGraphOpsBatch bounds the calls one commit takes, so one batch's
// validation work stays bounded; the rest wait for the next commit.
const maxGraphOpsBatch = 64

// errGraphOpsAbandoned answers the calls of a batch whose leader panicked.
var errGraphOpsAbandoned = errors.New("graph change was not applied: the commit it was part of failed")

// graphOpsCall is one ApplyGraphOps call, from queueing to its answer.
type graphOpsCall struct {
	actor identity.Actor
	ops   []GraphOp

	// lead is closed to make this call the one that commits the next batch;
	// done is closed once the call has its answer.
	lead chan struct{}
	done chan struct{}

	graph   *engine.Graph
	rev     string
	synced  []string
	err     error
	touched map[string]bool
}

// queueGraphOps queues one call and returns once it has been committed or
// refused, committing a batch itself when it is its turn to lead.
func (s *Store) queueGraphOps(actor identity.Actor, ops []GraphOp) *graphOpsCall {
	call := &graphOpsCall{actor: actor, ops: ops, lead: make(chan struct{}), done: make(chan struct{})}
	s.opsMu.Lock()
	s.opsQueue = append(s.opsQueue, call)
	if !s.opsLeading {
		s.opsLeading = true
		close(call.lead)
	}
	s.opsMu.Unlock()
	select {
	case <-call.done:
	case <-call.lead:
		s.commitGraphOpsBatch()
		<-call.done
	}
	return call
}

// commitGraphOpsBatch is the leader's turn: one batch, then the lead passes
// on. Whatever happens in between, including a panic, every call taken into
// the batch gets an answer and the queue gets a leader.
func (s *Store) commitGraphOpsBatch() {
	var batch []*graphOpsCall
	defer func() {
		s.opsMu.Lock()
		if len(s.opsQueue) > 0 {
			close(s.opsQueue[0].lead)
		} else {
			s.opsLeading = false
		}
		s.opsMu.Unlock()
		for _, call := range batch {
			if call.err == nil && call.graph == nil {
				call.err = errGraphOpsAbandoned
			}
			close(call.done)
		}
	}()

	s.mu.Lock()
	defer s.mu.Unlock()
	// Drained only once the lock is ours: everything that queued while the
	// previous write held it goes into this one.
	s.opsMu.Lock()
	n := min(len(s.opsQueue), maxGraphOpsBatch)
	batch = s.opsQueue[:n:n]
	s.opsQueue = append([]*graphOpsCall(nil), s.opsQueue[n:]...)
	s.opsMu.Unlock()

	s.applyGraphOpsBatchLocked(batch)
}

// applyGraphOpsBatchLocked applies batch in order and writes the result once.
// The caller holds s.mu.
func (s *Store) applyGraphOpsBatchLocked(batch []*graphOpsCall) {
	fail := func(calls []*graphOpsCall, err error) {
		for _, call := range calls {
			call.err = err
		}
	}
	graph, _, err := s.loadGraphLocked()
	if err != nil {
		fail(batch, err)
		return
	}
	accepted := make([]*graphOpsCall, 0, len(batch))
	for _, call := range batch {
		call.touched = map[string]bool{}
		// A lone call works on the private copy directly: if it fails,
		// nothing is written and the copy is dropped. In a batch each call
		// gets a copy of the graph as the calls before it left it, so a
		// refused call takes none of its changes with it.
		candidate := graph
		if len(batch) > 1 {
			candidate = graph.Clone()
		}
		if err := s.applyGraphOpsLocked(call.actor, candidate, call.ops, call.touched); err != nil {
			call.err = err
			continue
		}
		graph = candidate
		accepted = append(accepted, call)
	}
	if len(accepted) == 0 {
		return
	}

	data, err := s.marshalGraph(graph)
	if err != nil {
		fail(accepted, err)
		return
	}
	// Node metadata lives in two places: graph.yaml and the document's
	// frontmatter. Writing only one of them is how they drift apart.
	touched := map[string]bool{}
	for _, call := range accepted {
		for id := range call.touched {
			touched[id] = true
		}
	}
	ids := make([]string, 0, len(touched))
	for id := range touched {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	updates := make([]fileUpdate, 0, len(ids)+1)
	synced := map[string]bool{}
	for _, id := range ids {
		node := graph.NodeByID(id)
		if node == nil {
			continue
		}
		update, err := s.prepareNodeFileUpdate(node)
		if err != nil {
			fail(accepted, fmt.Errorf("sync node %s: %w", id, err))
			return
		}
		if update != nil {
			updates = append(updates, *update)
			synced[id] = true
		}
	}
	updates = append(updates, fileUpdate{path: s.GraphPath(), data: data})
	actors := make([]identity.Actor, 0, len(accepted))
	for _, call := range accepted {
		actors = append(actors, call.actor)
	}
	if err := s.applyUpdatesForLocked(actors, updates); err != nil {
		fail(accepted, err)
		return
	}
	rev := Rev(data)
	for _, call := range accepted {
		call.graph, call.rev = graph, rev
		for id := range call.touched {
			if synced[id] {
				call.synced = append(call.synced, id)
			}
		}
		sort.Strings(call.synced)
	}
}
