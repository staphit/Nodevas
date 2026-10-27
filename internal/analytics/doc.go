// Package analytics turns the hub's run history into insights, scorecards,
// tiers and auto-routing picks. It is pure SQL aggregates and deterministic
// rules: it never calls a model, never spends a token, never starts a
// process. noexec_test.go holds it to that (no os/exec, nothing under
// internal/copilot, not internal/runners).
package analytics
