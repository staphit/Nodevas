package runs

import (
	"context"
	"errors"
	"slices"
	"time"

	"nodevas/internal/locale"
	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
)

// Offer asks (runnerwire/offerask.go): a runner says why it has not started
// an offered run yet, and where the answer is given. The hub keeps the ask
// in memory beside the run and shows it on the run; on a single-user hub it
// relays the owner's answer from the app to the built-in runner that asked.
// It never answers for anyone: the runner takes an answer from its owner
// only, and a runner that is not the built-in one takes none at all, so on a
// team hub the approval stays on the member's own PC.
//
// The asks are memory only: one is dropped when its run moves on (accepted,
// declined, cancelled, back in the queue) or its runner goes away, and the
// runner says it again after a reconnect.

// pendingAsk is one runner's ask about one of its offered runs.
type pendingAsk struct {
	runnerID string
	ask      runnerwire.OfferAsk
}

var (
	// ErrNotWaiting: the run is not waiting for an answer in the app.
	ErrNotWaiting = locale.Errorf("this run is not waiting for an answer", "這次執行沒有在等你回答")
	// ErrApproveOnRunner: the run is approved on its runner's own computer,
	// never from the hub.
	ErrApproveOnRunner = locale.Errorf("this run is approved on its runner's computer, not here",
		"這次執行要在 runner 所在的電腦上核准，不能在這裡核准")
)

// CleanDeclineCode is a runner's decline code as the hub records it: one of
// runnerwire's Decline* codes, "" for none (an older runner), and
// DeclineOther for any code this hub does not know.
func CleanDeclineCode(code string) string {
	switch code {
	case "", runnerwire.DeclineInApp, runnerwire.DeclineOnRunner, runnerwire.DeclineTimedOut,
		runnerwire.DeclineNoOneThere, runnerwire.DeclineRepoNotAdded, runnerwire.DeclineAgentMissing,
		runnerwire.DeclineInteractiveOff, runnerwire.DeclineGlobalOff, runnerwire.DeclineBusy,
		runnerwire.DeclineOther, runnerwire.DeclineAgentSignedOut:
		return code
	}
	return runnerwire.DeclineOther
}

// cleanAsk clamps an ask to the limits, or reports it unusable: it must say
// where it is answered.
func cleanAsk(ask runnerwire.OfferAsk) (runnerwire.OfferAsk, bool) {
	if ask.Where != runnerwire.AskInApp && ask.Where != runnerwire.AskOnRunner {
		return runnerwire.OfferAsk{}, false
	}
	out := runnerwire.OfferAsk{
		RunID: ask.RunID, Where: ask.Where,
		Machine: Truncate(ask.Machine, runnerwire.MaxAskFieldBytes),
		Reasons: make([]runnerwire.AskReason, 0, min(len(ask.Reasons), runnerwire.MaxAskReasons)),
	}
	for _, reason := range ask.Reasons {
		if len(out.Reasons) == runnerwire.MaxAskReasons {
			break
		}
		if reason.Code == "" {
			continue
		}
		out.Reasons = append(out.Reasons, runnerwire.AskReason{
			Code:    Truncate(reason.Code, runnerwire.MaxAskFieldBytes),
			Agent:   Truncate(reason.Agent, runnerwire.MaxAskFieldBytes),
			Setting: Truncate(reason.Setting, runnerwire.MaxAskFieldBytes),
			Missing: Truncate(reason.Missing, runnerwire.MaxAskFieldBytes),
		})
	}
	// The deadline is the runner's to keep; a malformed one is not shown.
	if at, err := time.Parse(time.RFC3339, ask.ExpiresAt); err == nil {
		out.ExpiresAt = at.UTC().Format(time.RFC3339)
	}
	return out, true
}

// OfferAsk records what runnerID asks about one of its offered runs, and
// tells whoever watches the run. The first ask about a run is also worth a
// notification: somebody has to answer it. An ask about a run that is not
// the runner's is ErrNotFound; about one that is no longer offered,
// ErrTransition; one that does not say where it is answered, ErrInvalid.
func (d *Dispatcher) OfferAsk(ctx context.Context, runnerID string, ask runnerwire.OfferAsk) error {
	r, err := d.store.Get(ctx, ask.RunID)
	if err != nil {
		return err
	}
	if r.RunnerID != runnerID {
		return ErrNotFound
	}
	if r.Status != runnerwire.RunOffered {
		return ErrTransition
	}
	clean, ok := cleanAsk(ask)
	if !ok {
		return ErrInvalid
	}
	// Only the built-in runner is answered in the app: any other runner's
	// ask is answered on its own PC, whatever it says, so the app offers no
	// buttons for it.
	if clean.Where == runnerwire.AskInApp && !d.answersInApp(ctx, runnerID) {
		clean.Where = runnerwire.AskOnRunner
	}
	d.asksMu.Lock()
	if d.asks == nil {
		d.asks = map[string]pendingAsk{}
	}
	if d.asked == nil {
		d.asked = map[string]bool{}
	}
	d.asks[r.ID] = pendingAsk{runnerID: runnerID, ask: clean}
	first := !d.asked[r.ID]
	d.asked[r.ID] = true
	d.asksMu.Unlock()
	// A cancel or an accept may have moved the run on meanwhile; its
	// notifyChanged dropped the ask before it was stored.
	if latest, err := d.store.Get(ctx, r.ID); err == nil && latest.Status != runnerwire.RunOffered {
		d.forgetAsk(r.ID)
		return ErrTransition
	}
	d.announceChanged(r)
	if first {
		d.notifyRun(r)
	}
	return nil
}

// answersInApp reports whether runnerID takes answers from the app: this
// hub's built-in runner, saying it takes them (runnerwire.CapAppAccept).
func (d *Dispatcher) answersInApp(ctx context.Context, runnerID string) bool {
	if d.reg == nil {
		return false
	}
	runner, err := d.reg.Get(ctx, runnerID)
	return err == nil && runner.BuiltIn && runner.Info != nil && slices.Contains(runner.Info.Capabilities, runnerwire.CapAppAccept)
}

// Ask is the runner's pending ask about run id, if it has one.
func (d *Dispatcher) Ask(id string) (runnerwire.OfferAsk, bool) {
	d.asksMu.Lock()
	defer d.asksMu.Unlock()
	entry, ok := d.asks[id]
	if !ok {
		return runnerwire.OfferAsk{}, false
	}
	ask := entry.ask
	ask.Reasons = slices.Clone(ask.Reasons)
	return ask, true
}

// WithAsk is run as the API shows it: with its runner's pending ask while it
// is offered, and none otherwise.
func (d *Dispatcher) WithAsk(run runnerwire.Run) runnerwire.Run {
	run.Ask = nil
	if run.Status != runnerwire.RunOffered {
		return run
	}
	if ask, ok := d.Ask(run.ID); ok {
		run.Ask = &ask
	}
	return run
}

// claimAsk takes the ask about run id out of the map, for one answer: a
// second answer finds none (ErrNotWaiting) while the first is on its way,
// and the run shows no question meanwhile. Whether it was notified is kept.
func (d *Dispatcher) claimAsk(id string) (pendingAsk, bool) {
	d.asksMu.Lock()
	defer d.asksMu.Unlock()
	entry, ok := d.asks[id]
	if ok {
		delete(d.asks, id)
	}
	return entry, ok
}

// restoreAsk puts back an ask claimAsk took whose answer never left, unless
// the runner has asked again meanwhile.
func (d *Dispatcher) restoreAsk(id string, entry pendingAsk) {
	d.asksMu.Lock()
	defer d.asksMu.Unlock()
	if _, ok := d.asks[id]; !ok {
		d.asks[id] = entry
	}
}

// forgetAsk drops the ask about run id, and whether it was notified: the
// run has moved on.
func (d *Dispatcher) forgetAsk(id string) {
	d.asksMu.Lock()
	delete(d.asks, id)
	delete(d.asked, id)
	d.asksMu.Unlock()
}

// forgetRunnerAsks drops every ask runnerID made and returns their runs.
// Whether each was notified is kept: the runner says it again on its
// reconnect, which is not news.
func (d *Dispatcher) forgetRunnerAsks(runnerID string) []string {
	d.asksMu.Lock()
	defer d.asksMu.Unlock()
	var ids []string
	for id, entry := range d.asks {
		if entry.runnerID == runnerID {
			ids = append(ids, id)
			delete(d.asks, id)
		}
	}
	return ids
}

// dropRunnerAsks forgets runnerID's asks and tells whoever watched those
// runs, so nobody is left answering an ask the runner no longer holds.
func (d *Dispatcher) dropRunnerAsks(ctx context.Context, runnerID string) {
	for _, id := range d.forgetRunnerAsks(runnerID) {
		if r, err := d.store.Get(ctx, id); err == nil {
			d.announceChanged(r)
		}
	}
}

// Detached is told when runnerID's socket closes. Unless a newer connection
// has taken its place, the runner's asks go with it: nothing can answer
// them until it is back, and it says them again then.
func (d *Dispatcher) Detached(ctx context.Context, runnerID string) {
	if d.reg == nil || d.reg.Online(runnerID) {
		return
	}
	d.dropRunnerAsks(ctx, runnerID)
}

// OfferAnswer is a person's answer, from the app, to a runner's ask.
type OfferAnswer struct {
	// AccountID is who answers; it must be the run's runner's owner.
	// AccountName is for the audit trail.
	AccountID, AccountName string
	Start                  bool
	// AlwaysAllowShell: don't ask again for this agent's automatic runs.
	AlwaysAllowShell bool
}

// AnswerOffer relays the owner's answer about r to the built-in runner that
// asked in the app. The runner checks it again and answers with an accept or
// a decline; until then the run stays offered. Refusals: ErrNotFound (not
// the owner's), ErrNotWaiting (no ask, one answered already, or the run
// moved on), ErrApproveOnRunner (the ask is answered on the runner's own PC,
// or the runner is not this hub's built-in runner) and
// runners.ErrRunnerOffline.
func (d *Dispatcher) AnswerOffer(ctx context.Context, r Record, answer OfferAnswer) error {
	if answer.AccountID == "" || answer.AccountID != r.OwnerID {
		return ErrNotFound
	}
	current, err := d.store.Get(ctx, r.ID)
	if err != nil {
		return err
	}
	ask, waiting := d.Ask(r.ID)
	if current.Status != runnerwire.RunOffered || !waiting {
		return ErrNotWaiting
	}
	if ask.Where != runnerwire.AskInApp {
		return ErrApproveOnRunner
	}
	runner, err := d.reg.Get(ctx, current.RunnerID)
	if err != nil {
		return err
	}
	// Only the runner this hub started for itself, and only one that takes
	// answers: anywhere else the approval is on the runner's own PC.
	if !runner.BuiltIn || runner.Info == nil || !slices.Contains(runner.Info.Capabilities, runnerwire.CapAppAccept) {
		return ErrApproveOnRunner
	}
	if !d.reg.Online(current.RunnerID) {
		return runners.ErrRunnerOffline
	}
	// One answer per ask: the first takes it, and a second one (a double
	// click, another tab) finds nothing to answer.
	entry, waiting := d.claimAsk(r.ID)
	if !waiting {
		return ErrNotWaiting
	}
	err = d.reg.Send(current.RunnerID, runnerwire.Envelope{Type: runnerwire.TypeOfferAnswer, OfferAnswer: &runnerwire.OfferAnswer{
		RunID: current.ID, AccountID: answer.AccountID, Start: answer.Start, AlwaysAllowShell: answer.AlwaysAllowShell,
	}})
	if err != nil {
		d.restoreAsk(r.ID, entry)
		if errors.Is(err, runners.ErrOffline) {
			return runners.ErrRunnerOffline
		}
		return err
	}
	// The question is gone from the run until the runner's accept or
	// decline moves it on (or, if that answer never reached it, until it
	// asks again after a reconnect).
	d.announceChanged(current)
	// Who let what start on this machine, and whether they waved the guard
	// for that agent from now on.
	d.record(AuditEntry{Project: current.Project, ActorID: answer.AccountID, ActorName: answer.AccountName,
		Action: ActionOfferAnswer, Target: current.ID, Detail: map[string]any{
			"runner": current.RunnerID, "agent": current.Agent, "origin": current.Origin,
			"start": answer.Start, "alwaysAllowShell": answer.AlwaysAllowShell,
		}})
	return nil
}

// ActionOfferAnswer is the audit row of an answer relayed from the app.
const ActionOfferAnswer = "run.offer.answer"
