package runner

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"nodevas/internal/runnerwire"
)

// The built-in runner (single-user edition; runnerwire/edition.go). On a
// single-user hub the runner is part of the app: serve starts it, the person
// who clicks in Nodevas is the person at this machine, and the runs they
// start begin right away, with no question at a terminal. A runner acts as
// the built-in runner only when all of these hold; otherwise it behaves
// exactly as a classic runner:
//
//  1. serve started it on this machine, as this OS user, for a single-user
//     hub: ServeManaged and Edition, from the environment serve set, which
//     the CLI reads once and removes, so an agent's own `nodevas runner`
//     cannot inherit them;
//  2. its hub is plain http on loopback;
//  3. the hub's welcome says so (Edition single, BuiltIn), which a hub says
//     only for the runner it minted the token for at this startup, on a hub
//     without accounts, over a loopback connection.
//
// The runner decides: 1 and 2 without 3 stays classic, and 3 alone, the
// hub's word, never relaxes anything.

// builtInClaim reports whether this runner's own side of the built-in test
// holds (1 and 2), and when it does not, why.
func (c *Client) builtInClaim() (bool, string) {
	switch {
	case !c.ServeManaged:
		return false, "nodevas serve did not start it"
	case c.Edition != runnerwire.EditionSingle:
		return false, "serve did not start it for a single-user hub"
	}
	u, err := url.Parse(strings.TrimSpace(c.cfg().Hub))
	if err != nil || u.Scheme != "http" || !isLoopbackHost(u.Hostname()) {
		return false, "its hub is not plain http on this machine"
	}
	return true, ""
}

// decideBuiltIn settles, on a welcome, whether this connection's runner is
// the built-in one, and says so when that changed.
func (c *Client) decideBuiltIn(w *runnerwire.Welcome) bool {
	claim, why := c.builtInClaim()
	hubSays := w != nil && w.Edition == runnerwire.EditionSingle && w.BuiltIn
	builtIn := claim && hubSays
	state := int32(1)
	if builtIn {
		state = 2
	}
	if c.builtInState.Swap(state) != state {
		switch {
		case builtIn:
			c.logf("acting as this computer's built-in runner: runs you start in Nodevas begin right away")
		case claim:
			edition := "no edition"
			if w != nil && w.Edition != "" {
				edition = "edition " + w.Edition
			}
			c.logf("not acting as this computer's built-in runner: the hub did not confirm (%s, builtIn %t)", edition, hubSays)
		case hubSays:
			c.logf("the hub calls this runner its built-in one, but %s; acting as a classic runner", why)
		}
	}
	c.builtIn.Store(builtIn)
	return builtIn
}

// BuiltIn reports whether this runner acts as the built-in runner of a
// single-user hub on its current connection (before a welcome: whether the
// last one said so and its own side still holds; false before the first).
func (c *Client) BuiltIn() bool { return c.builtIn.Load() }

// BuiltInDecision decides an offer to the built-in runner: start says it
// starts right away; otherwise reasons says why the owner is asked in the
// app first. Everything starts, a run someone started by hand included,
// except an automatic run (Autopilot, a review round, a continuation, or an
// origin the hub did not say) of an agent that may run any command here and
// is not on shellWithoutAsking, and every run while askEveryRun is on.
//
// Why the one guard: agents write board nodes, and Autopilot turns board
// nodes into prompts, so a prompt-injected read-only agent could plant a
// task that an automatically started full-access agent then carries out.
func BuiltInDecision(cfg *Config, offer Offer) (start bool, reasons []runnerwire.AskReason) {
	job := offer.Job
	if cfg.AskEveryRun {
		reasons = append(reasons, runnerwire.AskReason{Code: runnerwire.AskEveryRun})
	}
	if offer.Shell != "" && job.Origin != runnerwire.OriginPerson && !slices.Contains(cfg.ShellWithoutAsking, job.Agent) {
		reasons = append(reasons, runnerwire.AskReason{Code: runnerwire.AskShellAgent, Agent: job.Agent,
			Setting: shellSetting(offer.Shell)})
	}
	return len(reasons) == 0, reasons
}

// shellSetting is Config.ShellAccess's "codexSandbox danger-full-access" as
// AskReason.Setting spells it, "codexSandbox=danger-full-access".
func shellSetting(shell string) string {
	key, value, ok := strings.Cut(shell, " ")
	if !ok {
		return truncate(shell, runnerwire.MaxAskFieldBytes)
	}
	return truncate(key+"="+value, runnerwire.MaxAskFieldBytes)
}

// startedBy says who started a run, for the line serve's console prints.
func startedBy(origin string) string {
	switch origin {
	case runnerwire.OriginPerson:
		return "you started it in Nodevas"
	case runnerwire.OriginAutopilot:
		return "Autopilot started it"
	case runnerwire.OriginPipeline:
		return "a review round started it"
	case runnerwire.OriginHandoff:
		return "it continues after a usage limit"
	}
	return "started from Nodevas"
}

// askText lists an ask's reasons for the log.
func askText(reasons []runnerwire.AskReason) string {
	var parts []string
	for _, reason := range reasons {
		switch reason.Code {
		case runnerwire.AskEveryRun:
			parts = append(parts, "you asked to confirm every run")
		case runnerwire.AskShellAgent:
			parts = append(parts, fmt.Sprintf("%s may run any command here (%s) and this run started automatically",
				reason.Agent, reason.Setting))
		default:
			text := reason.Code
			if reason.Missing != "" {
				text += " (" + reason.Missing + ")"
			}
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return "auto-accept is off"
	}
	return strings.Join(parts, "; ")
}
