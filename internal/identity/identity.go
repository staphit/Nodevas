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
}

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
