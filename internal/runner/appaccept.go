package runner

import (
	"context"
	"errors"
	"sync"
	"time"

	"nodevas/internal/runnerwire"
)

// AppAccepter is how the built-in runner asks for the owner's OK in the app
// (runnerwire/offerask.go), for the few offers BuiltInDecision does not start
// right away: the run waits, the hub shows "Waiting for your OK" on it, and
// the owner's answer comes back as an offer_answer. Only the built-in runner
// takes such an answer, and only from its owner (the client checks both);
// a classic runner drops every one, so a hub can never approve a run on
// someone else's machine.
type AppAccepter struct {
	// Terminal also asks at serve's terminal when the owner asked to confirm
	// every run and someone is there to answer (TerminalAccepter.Interactive):
	// the first answer wins. nil asks in the app only.
	Terminal Accepter
	// Timeout is how long an ask waits; zero means
	// runnerwire.AppAskTimeoutSeconds.
	Timeout time.Duration

	mu      sync.Mutex
	pending map[string]chan runnerwire.OfferAnswer
}

// Reasons an ask in the app ends without a start.
const (
	ReasonDeclinedInApp = "you chose not to start it in Nodevas"
	ReasonNoAnswerInApp = "nobody answered in Nodevas in time"
)

// errAnsweredInApp ends the terminal's question once the app answered.
var errAnsweredInApp = errors.New("answered in Nodevas")

func (a *AppAccepter) timeout() time.Duration {
	if a.Timeout > 0 {
		return a.Timeout
	}
	return runnerwire.AppAskTimeoutSeconds * time.Second
}

// open registers runID as waiting for the app's answer.
func (a *AppAccepter) open(runID string) <-chan runnerwire.OfferAnswer {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pending == nil {
		a.pending = map[string]chan runnerwire.OfferAnswer{}
	}
	ch := make(chan runnerwire.OfferAnswer, 1)
	a.pending[runID] = ch
	return ch
}

// closeAsk stops waiting for runID's answer.
func (a *AppAccepter) closeAsk(runID string) {
	a.mu.Lock()
	delete(a.pending, runID)
	a.mu.Unlock()
}

// Answer hands an answer from the app to the run waiting for it. It reports
// false, and changes nothing, when no run waits under that id: an unknown
// run, or one already answered (the first answer wins).
func (a *AppAccepter) Answer(ans runnerwire.OfferAnswer) bool {
	a.mu.Lock()
	ch := a.pending[ans.RunID]
	delete(a.pending, ans.RunID)
	a.mu.Unlock()
	if ch == nil {
		return false
	}
	ch <- ans
	return true
}

// appDecision is how an ask ended.
type appDecision struct {
	start bool
	// code and reason explain a no (runnerwire.Decline*); both "" when ctx
	// ended first.
	code, reason string
	// alwaysAllowShell: the owner ticked "don't ask again for this agent".
	alwaysAllowShell bool
}

// wait blocks until the app answers, the terminal does (with terminal set),
// deadline passes or ctx ends. At the terminal only a yes or a no counts: its
// own timeout, or nobody there, leaves the app asking.
func (a *AppAccepter) wait(ctx context.Context, offer Offer, answers <-chan runnerwire.OfferAnswer, deadline time.Time,
	terminal bool) appDecision {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	type termAnswer struct {
		ok     bool
		reason string
	}
	var fromTerminal chan termAnswer
	if terminal && a.Terminal != nil {
		termCtx, stop := context.WithCancelCause(ctx)
		defer stop(errAnsweredInApp)
		fromTerminal = make(chan termAnswer, 1)
		go func() {
			ok, reason := a.Terminal.Accept(termCtx, offer)
			fromTerminal <- termAnswer{ok, reason}
		}()
	}
	for {
		select {
		case ans := <-answers:
			if ans.Start {
				return appDecision{start: true, alwaysAllowShell: ans.AlwaysAllowShell}
			}
			return appDecision{code: runnerwire.DeclineInApp, reason: ReasonDeclinedInApp}
		case got := <-fromTerminal:
			fromTerminal = nil
			if got.ok {
				return appDecision{start: true}
			}
			if got.reason == ReasonDeclined {
				return appDecision{code: runnerwire.DeclineOnRunner, reason: ReasonDeclined}
			}
		case <-timer.C:
			return appDecision{code: runnerwire.DeclineTimedOut, reason: ReasonNoAnswerInApp}
		case <-ctx.Done():
			return appDecision{}
		}
	}
}

// asksAPerson is an Accepter that knows whether anyone can answer it.
type asksAPerson interface {
	// CanAsk reports whether a person can answer a question now.
	CanAsk() bool
	// AskTimeout is how long one question waits.
	AskTimeout() time.Duration
}

// CanAsk reports whether someone can type an answer: stdin is a terminal.
func (t *TerminalAccepter) CanAsk() bool { return t.Interactive }

// AskTimeout is how long one question waits.
func (t *TerminalAccepter) AskTimeout() time.Duration {
	if t.Timeout > 0 {
		return t.Timeout
	}
	return DefaultAcceptTimeout
}

// personAt reports whether a, an Accepter, puts its question to a person who
// can answer now, and how long it waits. An Accepter that does not say
// (a test's) is not one, so no offer_ask goes out for it.
func personAt(a Accepter) (bool, time.Duration) {
	if asker, ok := a.(asksAPerson); ok && asker.CanAsk() {
		return true, asker.AskTimeout()
	}
	return false, 0
}

// offerAnswer handles an offer_answer from the hub: the owner's answer in
// the app to an ask of the built-in runner. Any other runner drops it, as it
// drops one from anyone but its owner or for a run that waits for none.
func (c *Client) offerAnswer(ownerID string, ans *runnerwire.OfferAnswer) {
	switch {
	case ans == nil:
	case !c.BuiltIn():
		c.logf("run %s: an answer from the hub ignored: this runner is asked on its own machine only", ans.RunID)
	case ownerID == "" || ans.AccountID != ownerID:
		c.logf("run %s: an answer from the hub ignored: it is not from this runner's owner", ans.RunID)
	case !c.app().Answer(*ans):
		c.logf("run %s: an answer from the hub ignored: the run is not waiting for one", ans.RunID)
	}
}
