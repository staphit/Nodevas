package copilot

import (
	"nodevas/internal/copilot/toolbridge"
)

// Mode is how much the copilot may do without asking, modelled on Claude
// Code's permission modes.
type Mode string

const (
	// ModePlan runs reads only. Anything else is refused and the model is
	// told to propose the change instead.
	ModePlan Mode = "plan"
	// ModeDefault runs reads and asks before every write.
	ModeDefault Mode = "default"
	// ModeAcceptEdits runs reads and ordinary writes, and asks before the
	// risky ones (a page replaced wholesale).
	ModeAcceptEdits Mode = "acceptEdits"
	// ModeBypass runs everything. Only offered when the settings allow it.
	ModeBypass Mode = "bypassPermissions"
)

// AllModes is the cycle order the panel presents.
var AllModes = []Mode{ModeDefault, ModeAcceptEdits, ModePlan, ModeBypass}

// Valid reports whether m is one of the four modes.
func (m Mode) Valid() bool {
	switch m {
	case ModePlan, ModeDefault, ModeAcceptEdits, ModeBypass:
		return true
	}
	return false
}

// Decision is the person's answer to a permission request.
type Decision string

const (
	DecisionAllow Decision = "allow"
	// DecisionAllowSession allows this tool for the rest of the conversation.
	DecisionAllowSession Decision = "allow_session"
	DecisionDeny         Decision = "deny"
)

// Valid reports whether d is one the browser may send.
func (d Decision) Valid() bool {
	switch d {
	case DecisionAllow, DecisionAllowSession, DecisionDeny:
		return true
	}
	return false
}

// effectiveMode is the mode a turn actually runs in. Bypass the server does
// not allow degrades to acceptEdits, the strongest mode that still asks before
// a wholesale replacement.
func effectiveMode(requested Mode, allowBypass bool) Mode {
	if requested == ModeBypass && !allowBypass {
		return ModeAcceptEdits
	}
	if !requested.Valid() {
		return ModeDefault
	}
	return requested
}

// verdict is what the permission gate decides for one call before anyone is
// asked.
type verdict int

const (
	verdictRun verdict = iota
	verdictAsk
	verdictRefuse
)

// gate applies the mode table in COPILOT.md. Unknown kinds are treated as
// risky: a tool nobody classified is one nobody has vouched for.
func gate(mode Mode, kind toolbridge.Kind) verdict {
	if kind == toolbridge.KindRead {
		return verdictRun
	}
	switch mode {
	case ModePlan:
		return verdictRefuse
	case ModeBypass:
		return verdictRun
	case ModeAcceptEdits:
		if kind == toolbridge.KindWrite {
			return verdictRun
		}
		return verdictAsk
	default:
		return verdictAsk
	}
}

// availableModes is what the panel may offer.
func availableModes(allowBypass bool) []Mode {
	modes := make([]Mode, 0, len(AllModes))
	for _, mode := range AllModes {
		if mode == ModeBypass && !allowBypass {
			continue
		}
		modes = append(modes, mode)
	}
	return modes
}
