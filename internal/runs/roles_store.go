package runs

// Per-owner role settings: max concurrent runs and daily budget per role
// (table run_role_settings). Agent, model and fallbacks per role are routing
// rules with match "role".
//
// RED PHASE STUB: declarations only, no behavior.

import (
	"context"
	"errors"

	"nodevas/internal/runnerwire"
)

var errRolesNotImplemented = errors.New("agent roles: not implemented")

// RoleSettings returns an owner's role settings.
func (s *Store) RoleSettings(ctx context.Context, ownerID string) ([]runnerwire.RoleSettings, error) {
	return nil, errRolesNotImplemented
}

// SetRoleSettings replaces an owner's role settings and returns what was
// stored.
func (s *Store) SetRoleSettings(ctx context.Context, ownerID string, settings []runnerwire.RoleSettings) ([]runnerwire.RoleSettings, error) {
	return nil, errRolesNotImplemented
}

// RoleBudgetStatus is an owner's budget use today for runs in one role.
func (s *Store) RoleBudgetStatus(ctx context.Context, ownerID, role string) (runnerwire.BudgetStatus, error) {
	return runnerwire.BudgetStatus{}, errRolesNotImplemented
}

// SetOnApproved sets the hook a staged pipeline calls when it is approved:
// the server marks the node done (by "pipeline") with the evidence summary.
func (d *Dispatcher) SetOnApproved(fn func(project, nodeID, evidence string)) {}

// SetEvidence sets the hook that records a pipeline evidence event on a
// node's journal (store.AppendEvidence).
func (d *Dispatcher) SetEvidence(fn func(project, nodeID, kind, summary string)) {}
