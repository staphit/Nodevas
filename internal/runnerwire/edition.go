package runnerwire

// Editions: who the hub serves, decided at runtime, never stored.
//
// Single-user: `nodevas serve` on loopback with no accounts (or the desktop
// app in local mode). The runner is part of the app: serve starts it, and the
// person who clicks is the person at the machine, so runs they start begin
// right away. Team: a hub with accounts; each member's runner is a separate
// install on their own PC, and approvals stay on that PC.
const (
	EditionSingle = "single"
	EditionTeam   = "team"
)
