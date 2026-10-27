// Package identity carries who a request acts as.
//
// It is its own package so the layers that record an actor (the audit trail in
// the store) and the layer that decides one (authentication) can both name the
// type without depending on each other.
package identity

// Role controls access to server-wide settings. Project content remains
// collaborative, while host, credential and workspace administration is
// reserved for administrators.
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

// AgentClass says what kind of automation, if any, a request acts as. It is
// orthogonal to Role: a role says what an account may reach, the class says
// whether a person or an agent is behind the request.
type AgentClass string

const (
	AgentHuman        AgentClass = "" // browser / person
	AgentOrchestrator AgentClass = "orchestrator"
	AgentWorker       AgentClass = "worker"
)

// Actor is who a request acts as. Every write records one, so history can say
// who changed what once more than one person can reach the server.
type Actor struct {
	ID    string     `json:"id"`
	Name  string     `json:"name"`
	Role  Role       `json:"role,omitempty"`
	Agent AgentClass `json:"agent,omitempty"`
	// AgentRole is the agent role (engine roles: planner, qa, tester,
	// executor, reviewer, researcher or custom-role-*) the request acts in;
	// "" is legacy, the behavior before roles. A human ignores it.
	AgentRole string `json:"agentRole,omitempty"`
	// RunID and RunNodeID name the hub-dispatched run a runner-proxied
	// request comes from, and that run's node; "" otherwise.
	RunID     string `json:"runId,omitempty"`
	RunNodeID string `json:"runNodeId,omitempty"`
}

// HasRole reports whether the actor is an agent acting in a role.
// RED PHASE STUB: always false.
func (a Actor) HasRole() bool { return false }

// LowerClass is the lower-ranked of two agent classes (worker below
// orchestrator); a role never raises what the request declared. Human ("")
// ranks highest, so LowerClass("", c) is c.
// RED PHASE STUB: returns a unchanged.
func LowerClass(a, b AgentClass) AgentClass { return a }

// System is the hub acting on its own behalf (locking acceptance at Executor
// dispatch). Only server code constructs it; no request can.
var System = Actor{ID: "hub", Name: "hub"}

// IsAgent reports whether the actor is automation rather than a person.
func (a Actor) IsAgent() bool { return a.Agent != "" }

// IsAdmin reports whether the actor may change server-wide settings. The
// loopback-only actor is an administrator because the OS account is the trust
// boundary in desktop mode.
func (a Actor) IsAdmin() bool {
	return a.Role == RoleAdmin || a.ID == "local"
}

// Local is the single actor of a loopback-only server: the OS already decided
// who may connect, so every request is the local user.
var Local = Actor{ID: "local", Name: "local", Role: RoleAdmin}
