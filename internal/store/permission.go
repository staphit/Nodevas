// Per-node write permission: which class of actor may modify a node.
//
// The check lives in the store rather than in the HTTP handlers because every
// write path already resolves the node under the store's lock; a handler-side
// check would be one more thing each new route has to remember.

package store

import (
	"fmt"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

// ErrNodeWriteDenied is returned when an agent tries to modify a node whose
// write access outranks it. Humans are never denied.
type ErrNodeWriteDenied struct {
	NodeID string
	Access string
	Class  identity.AgentClass
}

func (e *ErrNodeWriteDenied) Error() string {
	if e.Access == engine.WriteAccessHumanOnly {
		return fmt.Sprintf("node %q is human-only: agents may not modify it", e.NodeID)
	}
	return fmt.Sprintf("node %q requires %s access; this agent is a %s",
		e.NodeID, e.Access, e.Class)
}

// writeAccessRank orders a node's requirement. Unknown values rank as
// unrestricted, but validation refuses them before they can be stored.
func writeAccessRank(access string) int {
	switch access {
	case engine.WriteAccessWorker:
		return 1
	case engine.WriteAccessOrchestrator:
		return 2
	case engine.WriteAccessHumanOnly:
		return 3
	default:
		return 0
	}
}

// agentRank orders an actor's class: human > orchestrator > worker.
func agentRank(class identity.AgentClass) int {
	switch class {
	case identity.AgentWorker:
		return 1
	case identity.AgentOrchestrator:
		return 2
	default:
		return 3 // human
	}
}

// checkNodeWrite is the single write-permission decision: an actor may modify
// a node when its rank meets the node's requirement. A nil node passes, so
// call sites that gate "the node if it still exists" stay one line.
func checkNodeWrite(actor identity.Actor, n *engine.Node) error {
	if n == nil || !actor.IsAgent() {
		return nil
	}
	if agentRank(actor.Agent) >= writeAccessRank(n.WriteAccess) {
		return nil
	}
	return &ErrNodeWriteDenied{NodeID: n.ID, Access: n.WriteAccess, Class: actor.Agent}
}

// normalizeWriteAccess maps the spelled-out "all" a client may send onto the
// stored zero value.
func normalizeWriteAccess(v string) string {
	if v == "all" {
		return engine.WriteAccessAll
	}
	return v
}
