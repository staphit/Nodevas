package runner

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"nodevas/internal/runnerwire"
)

// The connection never waits for an agent CLI. A hub closes a connection
// that has not said hello within seconds and drops a runner that stops
// pinging, while one CLI asked for its models can take a quarter of a
// minute to answer, or never answer. So the looking happens here, on its
// own goroutine: once when the runner starts, again every redetect
// interval, and when the owner asks from the app. Hellos only read what the
// last look found.

// defaultCheckAgain is how long a "check again" from the app waits for its
// look before answering with what is known: well inside the time the hub
// waits for that answer, with room for the hello that goes first.
const defaultCheckAgain = (runnerwire.SettingsRedetectTimeoutSeconds - 15) * time.Second

// defaultSignInRecheck is how often an agent the last look found signed out
// is asked whether it is signed in again. The hub refuses a signed-out
// agent, and the person who just signed in is told the app notices within a
// minute: waiting for the next full look (defaultRedetect) would leave them
// refused for minutes. Asking costs one `claude auth status`, which reads
// the stored login and asks no model.
const defaultSignInRecheck = 20 * time.Second

// detector keeps what the runner knows about the agents on its machine, one
// look at a time.
type detector struct {
	c     *Client
	probe *prober
	// first closes once the first look is over.
	first chan struct{}
	// wake starts a look for whoever is waiting (again).
	wake chan struct{}

	mu sync.Mutex
	// agents and local are what the last look found: the agents, and the
	// copilot model on this machine (localLLM). A look replaces them whole
	// and never changes them in place: hellos and the offer check share
	// them.
	agents []runnerwire.Agent
	local  *runnerwire.LLMInfo
	// waiters are closed when the next look to start is over.
	waiters []chan struct{}
}

func newDetector(c *Client) *detector {
	probe := newProber(c.Commands)
	if c.versionTimeout > 0 {
		probe.versionTimeout = c.versionTimeout
	}
	if c.catalogTimeout > 0 {
		probe.catalogTimeout = c.catalogTimeout
	}
	return &detector{c: c, probe: probe, first: make(chan struct{}), wake: make(chan struct{}, 1)}
}

// run looks until ctx ends.
func (d *detector) run(ctx context.Context) {
	d.look(ctx, true)
	close(d.first)
	tick := time.NewTicker(d.c.redetectInterval())
	defer tick.Stop()
	recheck := time.NewTicker(d.c.signInRecheckInterval())
	defer recheck.Stop()
	for {
		asked := false
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-d.wake:
			asked = true
		case <-recheck.C:
			if !d.signedBackIn(ctx) {
				continue
			}
		}
		d.mu.Lock()
		waiters := d.waiters
		d.waiters = nil
		d.mu.Unlock()
		if asked && len(waiters) == 0 {
			// The look before this one already took whoever asked.
			continue
		}
		d.look(ctx, false)
		for _, done := range waiters {
			close(done)
		}
	}
}

// look is one look at this machine. What it found replaces what the runner
// knew, and when that differs the connection is asked for a new hello. The
// log gets one line about it: always for the first look, later only when
// the look was slow, a command failed, or something changed.
func (d *detector) look(ctx context.Context, first bool) {
	start := time.Now()
	cfg := d.c.cfg()
	var local *runnerwire.LLMInfo
	var wg sync.WaitGroup
	wg.Go(func() { local = localLLM(ctx, cfg) })
	agents, said := d.probe.look(ctx, cfg)
	wg.Wait()
	if ctx.Err() != nil {
		// The runner is stopping: its commands were cut short, and what
		// they seemed to say is not what is on this machine.
		return
	}
	took := time.Since(start)
	d.mu.Lock()
	changed := !first && !(reflect.DeepEqual(agents, d.agents) && reflect.DeepEqual(local, d.local))
	d.agents, d.local = agents, local
	d.mu.Unlock()
	if first || changed || len(said.problems) > 0 || took > slowLook {
		line := fmt.Sprintf("checked agents in %s", roughly(took))
		if changed {
			line += "; what was found changed, telling the hub"
		}
		if details := append(said.problems, said.notes...); len(details) > 0 {
			line += " (" + strings.Join(details, "; ") + ")"
		}
		d.c.logf("%s", line)
	}
	if changed {
		d.c.changedInfo()
	}
}

// signedBackIn reports whether an agent the last look found signed out now
// needs a full look: it says it is signed in, or its CLI is gone. Only
// Claude Code is ever found signed out (prober.version); with none signed
// out nothing is asked.
func (d *detector) signedBackIn(ctx context.Context) bool {
	agents, _ := d.found()
	for _, agent := range agents {
		if agent.Kind != "claude" || !agent.SignedOut {
			continue
		}
		bin, err := agentBinary(d.probe.cmds, d.c.cfg(), agent.Kind)
		return err != nil || !d.probe.claudeSignedOut(ctx, bin)
	}
	return false
}

// found is what the last look found.
func (d *detector) found() ([]runnerwire.Agent, *runnerwire.LLMInfo) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.agents, d.local
}

// again asks for a new look and waits until it is over, at most wait. The
// look starts after the asking: one already under way may have passed the
// agent the owner just installed, so it does not count.
func (d *detector) again(ctx context.Context, wait time.Duration) {
	done := make(chan struct{})
	d.mu.Lock()
	d.waiters = append(d.waiters, done)
	d.mu.Unlock()
	select {
	case d.wake <- struct{}{}:
	default:
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	case <-ctx.Done():
	}
}

// soon asks for a new look without waiting for it: something a run saw
// (an agent that turned out signed out) may have changed what is here.
func (d *detector) soon() {
	d.mu.Lock()
	// A look with nobody waiting on it would be skipped as already taken.
	d.waiters = append(d.waiters, make(chan struct{}))
	d.mu.Unlock()
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// lookSoon is detector.soon, for a client that has one (tests make jobs
// without).
func (c *Client) lookSoon() {
	if c.detect != nil {
		c.detect.soon()
	}
}

// info is the hello as of now: what the last look found, with the settings
// in effect (refreshInfo): the limit a change from the hub (settings.go) may
// have moved off the config's, and the built-in runner's own
// (localsettings.go). It runs no agent CLI, so no hello waits for one.
func (c *Client) info() runnerwire.Info {
	agents, local := c.detect.found()
	return c.refreshInfo(helloInfo(c.cfg(), agents, local))
}
