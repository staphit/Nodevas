// The role guard: an agent acting in a role (identity.Actor.HasRole) may call
// only the routes its role's capabilities cover. Default deny. The table is
// derived from the runner proxy's route list (spec 2.3).
//
// RED PHASE STUB: declarations only, no behavior.

package server

// roleRouteCapability is the capability (engine.Capability, or "ops" for
// POST /api/graph/ops, whose ops the store checks one by one) a role needs to
// call method on target. ok is false for a route no role may call.
func roleRouteCapability(method, target string) (capability string, ok bool) {
	return "", false
}
