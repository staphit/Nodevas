package runs

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
)

// ReasonLost is the error a running run gets when its runner comes back
// without it: the runner crashed, restarted or its machine slept.
const ReasonLost = "runner restarted or lost the run"

// Dispatcher moves runs between the database and connected runners: it offers
// queued runs to runners with room, and applies what runners report back.
//
// It sends messages; it never executes anything. A runner may only touch its
// own runs: every runner-side call takes the runner's ID from the
// authenticated socket and checks the run belongs to it, whatever run ID the
// message names.
type Dispatcher struct {
	store *Store
	reg   *runners.Registry
	// changed is told when a run's status (or anything a run list shows)
	// changes; events when a run has new events. Either may be nil.
	changed func(project, nodeID string)
	events  func(project, runID string)
	// notify is told when a run deserves a notification (RunNotifyEvent);
	// audit records merge approvals and results. Either may be nil.
	notify func(project, runID string)
	audit  func(AuditEntry)

	// W6 step-up: whether this server has accounts to step up (a loopback
	// server does not, and vouches for everyone), how long a step-up lasts,
	// and the clock offers are judged by.
	stepUpRequired bool
	stepUpWindow   time.Duration
	now            func() time.Time

	locks sync.Map // runner ID → *sync.Mutex

	// terms relays interactive runs' terminals (terminal.go).
	termsOnce sync.Once
	terms     *Terminals

	// observers are told every run and pipeline change (W7 autopilot).
	observersMu sync.RWMutex
	observers   []func(Update)

	// usageAt is when each running run's live usage was last announced
	// (usageChanged), so a chatty agent costs one runs change per
	// UsageAnnounceInterval rather than one per event batch.
	usageMu sync.Mutex
	usageAt map[string]time.Time

	// Agent roles: onApproved marks a node done when its staged pipeline is
	// approved; evidence records red/green/guard/judge evidence on the
	// node's journal. Either may be nil.
	onApproved func(project, nodeID, evidence string)
	evidence   func(project, nodeID, kind, summary string)
}

// UsageAnnounceInterval is the least time between two runs changes a running
// run's live token usage causes. The figure itself is stored with every batch
// that carries one (Store.AppendEvents); only the announcement waits, and the
// finish announces the final figure whatever the interval. Each
// announcement makes every browser watching the project reload that node's
// runs, so it is kept to what an energy bar needs.
const UsageAnnounceInterval = 15 * time.Second

// Update is one change an observer hears about: a run (status, changes, PR,
// CI, merge, verdict) or, with Pipeline set, a pipeline's state. Observers
// run synchronously on the dispatcher's goroutine; they must not block or
// call back into the dispatcher (queue work instead).
type Update struct {
	Run      Record
	Pipeline *PipelineRecord
}

// Observe adds fn to the observers. Call it before serving.
func (d *Dispatcher) Observe(fn func(Update)) {
	d.observersMu.Lock()
	d.observers = append(d.observers, fn)
	d.observersMu.Unlock()
}

// observed tells every observer about u.
func (d *Dispatcher) observed(u Update) {
	d.observersMu.RLock()
	fns := d.observers
	d.observersMu.RUnlock()
	for _, fn := range fns {
		fn(u)
	}
}

// NewDispatcher builds a dispatcher. changed and events are the realtime
// hooks; see Dispatcher. Offers vouch for no dispatcher until SetStepUp says
// how: failing closed, a runner's auto-accept waits for a person.
func NewDispatcher(store *Store, reg *runners.Registry, changed func(project, nodeID string), events func(project, runID string)) *Dispatcher {
	return &Dispatcher{
		store: store, reg: reg, changed: changed, events: events,
		stepUpRequired: true, stepUpWindow: runnerwire.StepUpWindowSeconds * time.Second, now: time.Now,
	}
}

// SetStepUp says whether dispatchers must step up for offers to vouch for
// them (Job.SteppedUp), and for how long a step-up lasts. A server without
// accounts passes required false: the OS account is the boundary there, and
// every offer vouches. Call it before serving.
func (d *Dispatcher) SetStepUp(required bool, window time.Duration) {
	d.stepUpRequired = required
	if window > 0 {
		d.stepUpWindow = window
	}
}

// SteppedUp is what an offer of r says in Job.SteppedUp now: the dispatcher
// was stepped up when the run was made and that step-up has not run out. A
// run left queued past the window is offered without the vouch, so a runner
// does not auto-accept it hours later.
func (d *Dispatcher) SteppedUp(r Record) bool {
	if !d.stepUpRequired {
		return true
	}
	if r.SteppedUpAt == "" {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, r.SteppedUpAt)
	if err != nil {
		return false
	}
	now := time.Now
	if d.now != nil {
		now = d.now
	}
	return now().Before(at.Add(d.stepUpWindow))
}

// AuditEntry is a merge approval or result for the audit trail. Project is
// the project's name as ?project= takes it.
type AuditEntry struct {
	Project, ActorID, ActorName, Action, Target string
	Detail                                      map[string]any
}

// SetNotify sets the hook told when a run finishes (succeeded or failed), its
// CI turns red, or its merge succeeds or fails. Call it before serving.
func (d *Dispatcher) SetNotify(fn func(project, runID string)) { d.notify = fn }

// SetAudit sets the hook that records merge approvals and results. Call it
// before serving.
func (d *Dispatcher) SetAudit(fn func(AuditEntry)) { d.audit = fn }

func (d *Dispatcher) notifyRun(r Record) {
	if d.notify != nil {
		d.notify(r.Project, r.ID)
	}
}

func (d *Dispatcher) record(entry AuditEntry) {
	if d.audit != nil {
		d.audit(entry)
	}
}

// finished tells about a run that just reached an end state. Only success and
// failure are worth a notification: a cancel or decline was someone's choice.
func (d *Dispatcher) finished(r Record) {
	if r.Status == runnerwire.RunSucceeded || r.Status == runnerwire.RunFailed {
		d.notifyRun(r)
	}
}

// Store is the run store the dispatcher writes to.
func (d *Dispatcher) Store() *Store { return d.store }

func (d *Dispatcher) notifyChanged(r Record) {
	if runnerwire.RunFinal(r.Status) {
		d.forgetUsage(r.ID)
	}
	d.termStatus(r)
	if d.changed != nil {
		d.changed(r.Project, r.NodeID)
	}
	d.observed(Update{Run: r})
}

func (d *Dispatcher) notifyEvents(r Record) {
	if d.events != nil {
		d.events(r.Project, r.ID)
	}
}

// lock serialises the room check and the offers for one runner, so two
// pumps racing (a new run and a finish) cannot both fill the last slot.
func (d *Dispatcher) lock(runnerID string) func() {
	value, _ := d.locks.LoadOrStore(runnerID, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// Capacity is how many runs a runner takes at once, from its last hello.
func Capacity(info *runnerwire.Info) int {
	if info == nil || info.MaxConcurrent <= 0 {
		return 1
	}
	return info.MaxConcurrent
}

// Headless reports whether a runner's hello says it takes offers.
func Headless(info *runnerwire.Info) bool {
	return info != nil && slices.Contains(info.Capabilities, runnerwire.CapHeadless)
}

// Submit offers a newly created run at once if its runner is online and has
// room, and returns the run as it now stands. Otherwise the run stays queued
// until the runner connects or frees a slot.
func (d *Dispatcher) Submit(ctx context.Context, r Record) Record {
	d.notifyChanged(r)
	d.Pump(ctx, r.RunnerID)
	if latest, err := d.store.Get(ctx, r.ID); err == nil {
		return latest
	}
	return r
}

// Pump offers the runner's queued runs, oldest first, while it has room.
func (d *Dispatcher) Pump(ctx context.Context, runnerID string) {
	unlock := d.lock(runnerID)
	defer unlock()
	if !d.reg.Online(runnerID) {
		return
	}
	runner, err := d.reg.Get(ctx, runnerID)
	if err != nil || !Headless(runner.Info) {
		return
	}
	busy, err := d.store.Busy(ctx, runnerID)
	if err != nil {
		slog.Warn("runs: count busy", slog.String("runner", runnerID), slog.Any("error", err))
		return
	}
	room := Capacity(runner.Info) - busy
	if room <= 0 {
		return
	}
	queued, err := d.store.ForRunner(ctx, runnerID, runnerwire.RunQueued)
	if err != nil {
		slog.Warn("runs: read queue", slog.String("runner", runnerID), slog.Any("error", err))
		return
	}
	for _, r := range queued {
		if room <= 0 {
			return
		}
		if r.RetryAt != "" {
			continue // waiting for a usage limit to reset (handoff.go)
		}
		// A role's max concurrent runs (roles_store.go) holds it queued.
		if ok, err := d.store.roleHasRoom(ctx, r); err != nil || !ok {
			continue
		}
		if d.offer(ctx, r) {
			room--
		}
	}
}

// offer marks a queued run offered and sends it. If the send fails the run
// goes back to the queue for the next connection.
func (d *Dispatcher) offer(ctx context.Context, r Record) bool {
	offered, err := d.store.Transition(ctx, r.ID, runnerwire.RunOffered, Change{})
	if err != nil {
		return false
	}
	job := offered.Job()
	job.SteppedUp = d.SteppedUp(offered)
	if err := d.reg.Send(r.RunnerID, runnerwire.Envelope{Type: runnerwire.TypeOffer, Job: &job}); err != nil {
		slog.Warn("runs: send offer", slog.String("run", r.ID), slog.Any("error", err))
		if back, err := d.store.Transition(ctx, r.ID, runnerwire.RunQueued, Change{}); err == nil {
			offered = back
		}
		d.notifyChanged(offered)
		return false
	}
	d.notifyChanged(offered)
	return true
}

// Attached reconciles a runner that has just connected with what the hub
// thinks it holds, then offers it queued work. active is the run IDs the
// runner's hello says it is still executing (or still asking about).
//
// A running run it does not list is failed: whatever was running it is gone.
// An offered run it does not list goes back to the queue: nobody there saw
// it, so it is offered again. A listed run the hub already ended (cancelled
// while the runner was away) is cancelled on the runner.
func (d *Dispatcher) Attached(ctx context.Context, runnerID string, active []string) {
	listed := make(map[string]bool, len(active))
	for _, id := range active {
		listed[id] = true
	}
	held, err := d.store.ForRunner(ctx, runnerID, runnerwire.RunOffered, runnerwire.RunRunning)
	if err != nil {
		slog.Warn("runs: reconcile", slog.String("runner", runnerID), slog.Any("error", err))
	}
	for _, r := range held {
		if listed[r.ID] {
			continue
		}
		var next Record
		var err error
		if r.Status == runnerwire.RunRunning {
			next, err = d.store.Transition(ctx, r.ID, runnerwire.RunFailed, Change{Error: ReasonLost})
		} else {
			next, err = d.store.Transition(ctx, r.ID, runnerwire.RunQueued, Change{})
		}
		if err == nil {
			d.notifyChanged(next)
			d.finished(next)
			d.afterEnd(ctx, next, true)
		}
	}
	for id := range listed {
		r, err := d.store.Get(ctx, id)
		if err != nil || r.RunnerID != runnerID {
			continue
		}
		if runnerwire.RunFinal(r.Status) || r.Status == runnerwire.RunQueued {
			d.sendCancel(r, "run is "+r.Status+" on the hub")
		}
	}
	d.Pump(ctx, runnerID)
	d.sendPendingMerges(ctx, runnerID)
}

func (d *Dispatcher) sendCancel(r Record, reason string) bool {
	err := d.reg.Send(r.RunnerID, runnerwire.Envelope{Type: runnerwire.TypeCancel, RunID: r.ID, Reason: reason})
	return err == nil
}

// Accept applies a runner's accept: offered → running. An accept for a run
// that is no longer offered (it was cancelled meanwhile) is answered with a
// cancel, so the runner stops what it just started.
func (d *Dispatcher) Accept(ctx context.Context, runnerID string, accept runnerwire.Accept) error {
	r, err := d.store.Transition(ctx, accept.RunID, runnerwire.RunRunning, Change{
		Runner: runnerID, Base: accept.Base, Branch: accept.Branch,
	})
	if errors.Is(err, ErrTransition) && r.RunnerID == runnerID {
		d.sendCancel(r, "run is "+r.Status+" on the hub")
		return err
	}
	if err != nil {
		return err
	}
	d.notifyChanged(r)
	return nil
}

// Decline applies a runner's decline: offered → declined, with its reason.
func (d *Dispatcher) Decline(ctx context.Context, runnerID, runID, reason string) error {
	if reason == "" {
		reason = "declined at the runner"
	}
	r, err := d.store.Transition(ctx, runID, runnerwire.RunDeclined, Change{Runner: runnerID, Error: reason})
	if err != nil {
		return err
	}
	d.notifyChanged(r)
	d.afterEnd(ctx, r, false)
	d.Pump(ctx, runnerID)
	return nil
}

// Events stores a running run's events from its runner.
func (d *Dispatcher) Events(ctx context.Context, runnerID, runID string, events []runnerwire.RunEvent) error {
	r, err := d.store.Get(ctx, runID)
	if err != nil {
		return err
	}
	if r.RunnerID != runnerID {
		return ErrNotFound
	}
	if r.Status != runnerwire.RunRunning {
		return ErrTransition
	}
	if len(events) > runnerwire.MaxEventsPerMessage {
		events = events[:runnerwire.MaxEventsPerMessage]
	}
	if _, err := d.store.AppendEvents(ctx, runID, events); err != nil {
		return err
	}
	d.notifyEvents(r)
	if carriesUsage(events) {
		d.usageChanged(r)
	}
	return nil
}

func carriesUsage(events []runnerwire.RunEvent) bool {
	for _, event := range events {
		if event.Type == runnerwire.EventUsage && event.Usage != nil {
			return true
		}
	}
	return false
}

// usageChanged announces a running run's new live usage as a runs change, so
// run lists (which show usage) refresh without following the run's events;
// at most once per UsageAnnounceInterval per run. Only the realtime hook is
// told: nothing the observers act on has changed.
func (d *Dispatcher) usageChanged(r Record) {
	now := time.Now
	if d.now != nil {
		now = d.now
	}
	at := now()
	d.usageMu.Lock()
	last, seen := d.usageAt[r.ID]
	due := !seen || at.Sub(last) >= UsageAnnounceInterval
	if due {
		if d.usageAt == nil {
			d.usageAt = map[string]time.Time{}
		}
		d.usageAt[r.ID] = at
	}
	d.usageMu.Unlock()
	if due && d.changed != nil {
		d.changed(r.Project, r.NodeID)
	}
}

// forgetUsage drops an ended run's announcement time.
func (d *Dispatcher) forgetUsage(runID string) {
	d.usageMu.Lock()
	delete(d.usageAt, runID)
	d.usageMu.Unlock()
}

// Changes records what a run committed, from its runner. Only the first
// report counts; a resend is ignored quietly. Run lists show the counts, so a
// stored report is announced as a runs change.
func (d *Dispatcher) Changes(ctx context.Context, runnerID, runID string, changes runnerwire.RunChanges) error {
	r, stored, err := d.store.SetChanges(ctx, runID, runnerID, changes)
	if err != nil {
		return err
	}
	if stored {
		d.notifyChanged(r)
	}
	return nil
}

// PR records a run's pull request, from its runner, in any status: the
// runner reports the PR's state and CI until it is merged or closed. Only a
// report that changes what the run shows is announced.
func (d *Dispatcher) PR(ctx context.Context, runnerID, runID string, pr runnerwire.PRInfo) error {
	before, r, changed, err := d.store.setPR(ctx, runID, runnerID, pr)
	if err != nil {
		return err
	}
	if changed {
		d.notifyChanged(r)
		if r.CIStatus == runnerwire.CIFailure && before.CIStatus != runnerwire.CIFailure {
			d.notifyRun(r)
		}
		// A pipeline whose implement run ended before its PR was reported
		// moves on now.
		if before.PRURL == "" && before.PRError == "" {
			d.prReported(ctx, r)
		}
		// A staged pipeline waiting for CI on its green head moves on.
		if r.CIStatus != before.CIStatus || r.PRHeadSHA != before.PRHeadSHA {
			d.ciReported(ctx, r)
		}
	}
	return nil
}

// Finish applies a runner's finish, then offers it the next queued run.
func (d *Dispatcher) Finish(ctx context.Context, runnerID, runID string, result runnerwire.RunResult) error {
	r, err := d.store.Finish(ctx, runID, runnerID, result)
	if err != nil {
		return err
	}
	d.finished(r)
	// The follow-up (a fallback, a wait for a usage limit, a pipeline's next
	// step) is recorded before the end is announced, so an observer that
	// reads the task's runs on the announcement (the autopilot) sees the
	// task carry on rather than fail.
	d.afterEnd(ctx, r, r.Status == runnerwire.RunSucceeded || r.Status == runnerwire.RunFailed)
	d.notifyChanged(r)
	d.Pump(ctx, runnerID)
	return nil
}

// Cancel stops a run on behalf of a person. A queued or offered run ends at
// once (an offered one is also withdrawn from its runner). A running run is
// asked to stop and stays running until its runner reports the finish;
// if the runner is offline there is nobody to ask, so it ends here.
func (d *Dispatcher) Cancel(ctx context.Context, runID, reason string) (Record, error) {
	// A few tries: an accept or finish may land between the read and the
	// conditional write, and the right action depends on the new status.
	for attempt := 0; attempt < 4; attempt++ {
		r, err := d.store.Get(ctx, runID)
		if err != nil {
			return Record{}, err
		}
		switch {
		case runnerwire.RunFinal(r.Status):
			return r, nil
		case r.Status == runnerwire.RunRunning:
			if d.sendCancel(r, reason) {
				return r, nil
			}
			next, err := d.store.Transition(ctx, runID, runnerwire.RunCancelled, Change{
				Error: "cancelled while the runner was offline",
			})
			if errors.Is(err, ErrTransition) {
				continue
			}
			if err != nil {
				return Record{}, err
			}
			d.notifyChanged(next)
			d.afterEnd(ctx, next, false)
			return next, nil
		default: // queued, offered
			next, err := d.store.Transition(ctx, runID, runnerwire.RunCancelled, Change{})
			if errors.Is(err, ErrTransition) {
				continue
			}
			if err != nil {
				return Record{}, err
			}
			if r.Status == runnerwire.RunOffered {
				d.sendCancel(r, reason)
			}
			d.notifyChanged(next)
			d.afterEnd(ctx, next, false)
			d.Pump(ctx, r.RunnerID)
			return next, nil
		}
	}
	r, err := d.store.Get(ctx, runID)
	if err != nil {
		return Record{}, err
	}
	return r, ErrTransition
}
