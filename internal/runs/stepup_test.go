package runs

import (
	"context"
	"sync"
	"testing"
	"time"

	"nodevas/internal/identity"
	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
)

// offerConn records the offers a runner is sent.
type offerConn struct {
	mu     sync.Mutex
	offers []runnerwire.Job
}

func (c *offerConn) Bye(string) {}

func (c *offerConn) Send(env runnerwire.Envelope) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if env.Type == runnerwire.TypeOffer && env.Job != nil {
		c.offers = append(c.offers, *env.Job)
	}
	return nil
}

func (c *offerConn) last(t *testing.T, runID string) runnerwire.Job {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.offers) - 1; i >= 0; i-- {
		if c.offers[i].RunID == runID {
			return c.offers[i]
		}
	}
	t.Fatalf("no offer for %s in %+v", runID, c.offers)
	return runnerwire.Job{}
}

// stepUpFixture is a dispatcher with one connected headless runner and a
// clock the test moves.
type stepUpFixture struct {
	s      *Store
	d      *Dispatcher
	conn   *offerConn
	runner runnerwire.Runner
	clock  time.Time
}

func newStepUpFixture(t *testing.T) *stepUpFixture {
	t.Helper()
	ctx := context.Background()
	s := newStore(t)
	reg := runners.NewRegistry(s.db, func() {})
	runner, _, err := reg.Create(ctx, identity.Actor{ID: "u-ann", Name: "ann"}, "desk")
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.SetHello(ctx, runner.ID, runnerwire.Info{
		Capabilities: []string{runnerwire.CapHeadless}, MaxConcurrent: 4,
	}); err != nil {
		t.Fatal(err)
	}
	f := &stepUpFixture{s: s, conn: &offerConn{}, runner: runner, clock: time.Now()}
	reg.Attach(runner.ID, f.conn)
	f.d = NewDispatcher(s, reg, nil, nil)
	f.d.now = func() time.Time { return f.clock }
	f.d.SetStepUp(true, 10*time.Minute)
	return f
}

func (f *stepUpFixture) submit(t *testing.T, steppedUpAt string) Record {
	t.Helper()
	r, err := f.s.Create(context.Background(), New{
		Project: ".", NodeID: "n1", NodeTitle: "Title", RunnerID: f.runner.ID, RunnerName: "desk",
		OwnerID: "u-ann", Agent: "claude", Model: "default", RepoID: "repo", RepoName: "repo",
		JobPrompt: "# Title", CreatedBy: "u-ann", CreatedByName: "ann", SteppedUpAt: steppedUpAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.SteppedUpAt != steppedUpAt {
		t.Fatalf("stored stepped_up_at = %q, want %q", r.SteppedUpAt, steppedUpAt)
	}
	return f.d.Submit(context.Background(), r)
}

func TestOffersVouchOnlyForAStepUpWithinTheWindow(t *testing.T) {
	f := newStepUpFixture(t)
	stamp := f.clock.UTC().Format(time.RFC3339Nano)

	stepped := f.submit(t, stamp)
	if stepped.Status != runnerwire.RunOffered || !f.conn.last(t, stepped.ID).SteppedUp {
		t.Fatalf("a stepped-up dispatch was offered without the vouch: %+v", stepped)
	}
	plain := f.submit(t, "")
	if f.conn.last(t, plain.ID).SteppedUp {
		t.Fatal("a dispatch without a step-up was offered as stepped up")
	}

	// A step-up that ran out before the run was made vouches for nothing.
	stale := f.clock.Add(-11 * time.Minute).UTC().Format(time.RFC3339Nano)
	if late := f.submit(t, stale); f.conn.last(t, late.ID).SteppedUp {
		t.Fatal("an expired step-up vouched")
	}

	// The runner comes back without the offered run: it is offered again,
	// and past the window it no longer vouches.
	f.clock = f.clock.Add(10*time.Minute + time.Second)
	f.d.Attached(context.Background(), f.runner.ID, nil)
	if f.conn.last(t, stepped.ID).SteppedUp {
		t.Fatal("a re-offer after the window still vouched")
	}
}

func TestLoopbackOffersAlwaysVouch(t *testing.T) {
	f := newStepUpFixture(t)
	f.d.SetStepUp(false, 0)
	r := f.submit(t, "")
	if !f.conn.last(t, r.ID).SteppedUp {
		t.Fatal("a loopback offer did not vouch")
	}
	if f.d.stepUpWindow != 10*time.Minute {
		t.Fatalf("SetStepUp(false, 0) changed the window to %v", f.d.stepUpWindow)
	}
}

func TestNewDispatcherFailsClosed(t *testing.T) {
	d := NewDispatcher(newStore(t), nil, nil, nil)
	if d.SteppedUp(Record{}) {
		t.Fatal("a fresh dispatcher vouched for a run without a step-up")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if !d.SteppedUp(Record{SteppedUpAt: now}) {
		t.Fatal("a fresh dispatcher refused a current step-up")
	}
	if d.SteppedUp(Record{SteppedUpAt: "not a time"}) {
		t.Fatal("an unreadable step-up vouched")
	}
}
