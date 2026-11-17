package runners

import (
	"context"
	"errors"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

func settingsInfo(caps ...string) runnerwire.Info {
	return runnerwire.Info{Version: "v", MaxConcurrent: 1, MaxConcurrentCeiling: 8,
		Capabilities: append([]string{runnerwire.CapHeadless}, caps...)}
}

func settingsReq(accountID string, n int) runnerwire.SettingsRequest {
	return runnerwire.SettingsRequest{AccountID: accountID, MaxConcurrent: &n}
}

// Phase 2: the settings relay reaches only the asker's own online runner
// that takes remote settings; it takes the answer only from that runner and
// gives up after its timeout.
func TestSettingsRelayRefusesAndRelays(t *testing.T) {
	reg, _ := newRegistry(t)
	relay := NewSettingsRelay(reg)
	relay.Timeout = 2 * time.Second
	ctx := context.Background()

	conn := newLLMConn()
	mine := online(t, reg, "ann", settingsInfo(runnerwire.CapGlobalRuns, runnerwire.CapRemoteSettings), conn)
	theirs := online(t, reg, "bob", settingsInfo(runnerwire.CapGlobalRuns, runnerwire.CapRemoteSettings), newLLMConn())
	off := online(t, reg, "ann", settingsInfo(runnerwire.CapRunWorkspace, runnerwire.CapGlobalRuns), newLLMConn())
	// Phase 1 knows run workspaces, not remote settings: outdated.
	old := online(t, reg, "ann", settingsInfo(runnerwire.CapRunWorkspace), newLLMConn())
	noCeiling := settingsInfo(runnerwire.CapGlobalRuns, runnerwire.CapRemoteSettings)
	noCeiling.MaxConcurrentCeiling = 0
	zero := online(t, reg, "ann", noCeiling, newLLMConn())

	if _, err := relay.Request(ctx, theirs.ID, settingsReq(ann.ID, 2)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob's runner: %v, want ErrNotFound", err)
	}
	if _, err := relay.Request(ctx, mine.ID, settingsReq(admin.ID, 2)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an administrator on ann's runner: %v, want ErrNotFound", err)
	}
	if _, err := relay.Request(ctx, "nope", settingsReq(ann.ID, 2)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown runner: %v, want ErrNotFound", err)
	}
	if _, err := relay.Request(ctx, off.ID, settingsReq(ann.ID, 2)); !errors.Is(err, ErrSettingsOff) {
		t.Fatalf("remoteSettings off: %v, want ErrSettingsOff", err)
	}
	if _, err := relay.Request(ctx, zero.ID, settingsReq(ann.ID, 2)); !errors.Is(err, ErrSettingsOff) {
		t.Fatalf("no ceiling: %v, want ErrSettingsOff", err)
	}
	if _, err := relay.Request(ctx, old.ID, settingsReq(ann.ID, 2)); !errors.Is(err, ErrSettingsOutdated) {
		t.Fatalf("runner from before global agents: %v, want ErrSettingsOutdated", err)
	}
	offline, _, err := reg.Create(ctx, ann, "ann laptop")
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.SetHello(ctx, offline.ID, settingsInfo(runnerwire.CapGlobalRuns, runnerwire.CapRemoteSettings)); err != nil {
		t.Fatal(err)
	}
	if _, err := relay.Request(ctx, offline.ID, settingsReq(ann.ID, 2)); !errors.Is(err, ErrRunnerOffline) {
		t.Fatalf("offline runner: %v, want ErrRunnerOffline", err)
	}

	type result struct {
		done runnerwire.SettingsDone
		err  error
	}
	results := make(chan result, 1)
	go func() {
		done, err := relay.Request(ctx, mine.ID, settingsReq(ann.ID, 3))
		results <- result{done, err}
	}()
	sent := conn.next(t)
	if sent.Type != runnerwire.TypeSettingsRequest || sent.Settings == nil || sent.Settings.ID == "" ||
		sent.Settings.AccountID != ann.ID || sent.Settings.MaxConcurrent == nil || *sent.Settings.MaxConcurrent != 3 {
		t.Fatalf("sent = %+v", sent)
	}
	answer := runnerwire.Envelope{Type: runnerwire.TypeSettingsDone,
		SettingsDone: &runnerwire.SettingsDone{ID: sent.Settings.ID, MaxConcurrent: 3, Persisted: true}}
	// The same answer from another runner is dropped, as is one for an id
	// nobody asked with.
	_ = relay.Deliver(theirs.ID, answer)
	_ = relay.Deliver(mine.ID, runnerwire.Envelope{Type: runnerwire.TypeSettingsDone,
		SettingsDone: &runnerwire.SettingsDone{ID: "stray", MaxConcurrent: 9}})
	_ = relay.Deliver(mine.ID, runnerwire.Envelope{Type: runnerwire.TypeSettingsDone})
	if err := relay.Deliver(mine.ID, answer); err != nil {
		t.Fatal(err)
	}
	got := <-results
	if got.err != nil || got.done.MaxConcurrent != 3 || !got.done.Persisted || got.done.ID != sent.Settings.ID {
		t.Fatalf("request = %+v, %v", got.done, got.err)
	}
	// An answer after its request is done is dropped, not delivered twice.
	if err := relay.Deliver(mine.ID, answer); err != nil {
		t.Fatal(err)
	}

	relay.Timeout = 50 * time.Millisecond
	if _, err := relay.Request(ctx, mine.ID, settingsReq(ann.ID, 2)); !errors.Is(err, ErrRepoTimeout) {
		t.Fatalf("unanswered: %v, want ErrRepoTimeout", err)
	}
	conn.next(t) // the request that timed out
	relay.mu.Lock()
	left := len(relay.calls)
	relay.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d calls left waiting", left)
	}
}

// Looking for the agent CLIs again asks each of them, which takes seconds
// apiece: the relay waits longer for a redetect than for any other change.
func TestSettingsRelayWaitsLongerForARedetect(t *testing.T) {
	reg, _ := newRegistry(t)
	relay := NewSettingsRelay(reg)
	ctx := context.Background()
	conn := newLLMConn()
	mine := online(t, reg, "ann", settingsInfo(runnerwire.CapRemoteSettings, runnerwire.CapAppAccept), conn)
	reg.SetWelcomed(mine.ID, true)
	defer func(change, redetect time.Duration) {
		DefaultSettingsTimeout, DefaultSettingsRedetectTimeout = change, redetect
	}(DefaultSettingsTimeout, DefaultSettingsRedetectTimeout)
	DefaultSettingsTimeout, DefaultSettingsRedetectTimeout = 50*time.Millisecond, 10*time.Second

	if _, err := relay.Request(ctx, mine.ID, settingsReq(ann.ID, 2)); !errors.Is(err, ErrRepoTimeout) {
		t.Fatalf("maxConcurrent: %v, want ErrRepoTimeout", err)
	}
	conn.next(t)
	results := make(chan error, 1)
	go func() {
		_, err := relay.Request(ctx, mine.ID, runnerwire.SettingsRequest{AccountID: ann.ID, Redetect: true})
		results <- err
	}()
	sent := conn.next(t)
	time.Sleep(200 * time.Millisecond) // past the other changes' wait
	if err := relay.Deliver(mine.ID, runnerwire.Envelope{Type: runnerwire.TypeSettingsDone,
		SettingsDone: &runnerwire.SettingsDone{ID: sent.Settings.ID, MaxConcurrent: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := <-results; err != nil {
		t.Fatalf("redetect answered after the other changes' wait: %v", err)
	}
}

// A runner that goes away mid-request ends the wait at once, not at the
// timeout.
func TestSettingsRelayRunnerGone(t *testing.T) {
	reg, _ := newRegistry(t)
	relay := NewSettingsRelay(reg)
	relay.Timeout = 30 * time.Second
	ctx := context.Background()
	conn := newLLMConn()
	runner, _, err := reg.Create(ctx, ann, "ann pc")
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.SetHello(ctx, runner.ID, settingsInfo(runnerwire.CapGlobalRuns, runnerwire.CapRemoteSettings)); err != nil {
		t.Fatal(err)
	}
	detach := reg.Attach(runner.ID, conn)
	errs := make(chan error, 1)
	go func() {
		_, err := relay.Request(ctx, runner.ID, settingsReq(ann.ID, 2))
		errs <- err
	}()
	conn.next(t)
	detach()
	select {
	case err := <-errs:
		if !errors.Is(err, ErrRepoRunnerGone) {
			t.Fatalf("runner gone: %v, want ErrRepoRunnerGone", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the request outlived its runner")
	}
}

// An agent path and the permission relay are the built-in runner's own
// settings: a classic runner is never even sent them.
func TestSettingsRelayAgentPathAndRelayAreLocal(t *testing.T) {
	on := false
	for name, req := range map[string]runnerwire.SettingsRequest{
		"agentPath":       {AccountID: ann.ID, AgentPath: map[string]string{"claude": "/opt/claude"}},
		"agentPath clear": {AccountID: ann.ID, AgentPath: map[string]string{"claude": ""}},
		"agentPath empty": {AccountID: ann.ID, AgentPath: map[string]string{}},
		"permissionRelay": {AccountID: ann.ID, PermissionRelay: &on},
	} {
		if !LocalOnly(req) {
			t.Errorf("%s: not local", name)
		}
	}
	if LocalOnly(settingsReq(ann.ID, 2)) {
		t.Error("maxConcurrent alone is local")
	}

	reg, _ := newRegistry(t)
	relay := NewSettingsRelay(reg)
	relay.Timeout = 2 * time.Second
	ctx := context.Background()
	conn := newLLMConn()
	classic := online(t, reg, "ann", settingsInfo(runnerwire.CapGlobalRuns, runnerwire.CapRemoteSettings), conn)
	for name, req := range map[string]runnerwire.SettingsRequest{
		"agentPath":       {AccountID: ann.ID, AgentPath: map[string]string{"claude": "/opt/claude"}},
		"permissionRelay": {AccountID: ann.ID, PermissionRelay: &on},
	} {
		if _, err := relay.Request(ctx, classic.ID, req); !errors.Is(err, ErrSettingsLocalOnly) {
			t.Errorf("%s on a classic runner: %v, want ErrSettingsLocalOnly", name, err)
		}
	}
	if len(conn.got) != 0 {
		t.Fatalf("a classic runner was sent %d requests", len(conn.got))
	}
}

func TestSettable(t *testing.T) {
	ceiling := settingsInfo(runnerwire.CapGlobalRuns, runnerwire.CapRemoteSettings)
	noCeiling := ceiling
	noCeiling.MaxConcurrentCeiling = 0
	off := settingsInfo(runnerwire.CapGlobalRuns)
	phase1 := settingsInfo(runnerwire.CapRunWorkspace)
	old := settingsInfo()
	for _, c := range []struct {
		online bool
		info   *runnerwire.Info
		want   string
	}{
		{true, &ceiling, ""},
		{false, &ceiling, runnerwire.RunnerBlockedOffline},
		{true, &noCeiling, runnerwire.RunnerBlockedSettingsOff},
		{true, &off, runnerwire.RunnerBlockedSettingsOff},
		{true, &phase1, runnerwire.RunnerBlockedOutdated},
		{true, &old, runnerwire.RunnerBlockedOutdated},
		{true, nil, runnerwire.RunnerBlockedOutdated},
	} {
		if got := Settable(c.online, c.info); got != c.want {
			t.Errorf("Settable(%v, %+v) = %q, want %q", c.online, c.info, got, c.want)
		}
	}
}
