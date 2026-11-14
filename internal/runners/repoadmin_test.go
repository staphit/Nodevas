package runners

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

func repoAdminInfo(caps ...string) runnerwire.Info {
	return runnerwire.Info{Version: "v", MaxConcurrent: 1,
		Capabilities: append([]string{runnerwire.CapHeadless}, caps...)}
}

func inspectReq(accountID string) runnerwire.RepoAdminRequest {
	return runnerwire.RepoAdminRequest{AccountID: accountID, Op: runnerwire.RepoAdminInspect, Path: `C:\code\app`}
}

// Single-user: the repo admin relay reaches only the asker's own runner, and
// only while it is the hub's built-in runner (welcomed so) taking repo
// admin; it takes the answer only from that runner, clamped, and gives up
// after its timeout.
func TestRepoAdminRelayRefusesAndRelays(t *testing.T) {
	reg, _ := newRegistry(t)
	relay := NewRepoAdminRelay(reg)
	relay.Timeout = 2 * time.Second
	ctx := context.Background()

	conn := newLLMConn()
	mine := online(t, reg, "ann", repoAdminInfo(runnerwire.CapRepoAdmin, runnerwire.CapAppAccept), conn)
	reg.SetWelcomed(mine.ID, true)
	theirs := online(t, reg, "bob", repoAdminInfo(runnerwire.CapRepoAdmin), newLLMConn())
	reg.SetWelcomed(theirs.ID, true)
	// A classic runner: the capability without the hub's welcome, or the
	// welcome without the capability (an older runner).
	classic := online(t, reg, "ann", repoAdminInfo(runnerwire.CapRepoAdmin), newLLMConn())
	old := online(t, reg, "ann", repoAdminInfo(), newLLMConn())
	reg.SetWelcomed(old.ID, true)

	if _, err := relay.Request(ctx, theirs.ID, inspectReq(ann.ID)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob's runner: %v, want ErrNotFound", err)
	}
	if _, err := relay.Request(ctx, mine.ID, inspectReq(admin.ID)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an administrator on ann's runner: %v, want ErrNotFound", err)
	}
	if _, err := relay.Request(ctx, mine.ID, inspectReq("")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nobody: %v, want ErrNotFound", err)
	}
	for name, runner := range map[string]runnerwire.Runner{"classic": classic, "old": old} {
		if _, err := relay.Request(ctx, runner.ID, inspectReq(ann.ID)); !errors.Is(err, ErrRepoAdminOff) {
			t.Fatalf("%s runner: %v, want ErrRepoAdminOff", name, err)
		}
	}
	offline, _, err := reg.Create(ctx, ann, "ann laptop")
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.SetHello(ctx, offline.ID, repoAdminInfo(runnerwire.CapRepoAdmin)); err != nil {
		t.Fatal(err)
	}
	reg.SetWelcomed(offline.ID, true)
	if _, err := relay.Request(ctx, offline.ID, inspectReq(ann.ID)); !errors.Is(err, ErrRunnerOffline) {
		t.Fatalf("offline runner: %v, want ErrRunnerOffline", err)
	}

	type result struct {
		done runnerwire.RepoAdminDone
		err  error
	}
	results := make(chan result, 1)
	go func() {
		done, err := relay.Request(ctx, mine.ID, inspectReq(ann.ID))
		results <- result{done, err}
	}()
	sent := conn.next(t)
	if sent.Type != runnerwire.TypeRepoAdmin || sent.RepoAdmin == nil || sent.RepoAdmin.ID == "" ||
		sent.RepoAdmin.AccountID != ann.ID || sent.RepoAdmin.Op != runnerwire.RepoAdminInspect || sent.RepoAdmin.Path != `C:\code\app` {
		t.Fatalf("sent = %+v", sent)
	}
	ignored := make([]string, 100)
	for i := range ignored {
		ignored[i] = "node_modules/"
	}
	answer := runnerwire.Envelope{Type: runnerwire.TypeRepoAdminDone, RepoAdminDone: &runnerwire.RepoAdminDone{
		ID: sent.RepoAdmin.ID, Error: strings.Repeat("e", 5000),
		Inspect: &runnerwire.RepoInspect{Path: `C:\code\app`, State: runnerwire.RepoStateNotRepo,
			Init: &runnerwire.InitPlan{Files: 312, Ignored: ignored, WritesGitignore: true, Hash: "h1"}},
	}}
	// The same answer from another runner is dropped, as is one for an id
	// nobody asked with, and one with nothing in it.
	_ = relay.Deliver(theirs.ID, answer)
	_ = relay.Deliver(mine.ID, runnerwire.Envelope{Type: runnerwire.TypeRepoAdminDone,
		RepoAdminDone: &runnerwire.RepoAdminDone{ID: "stray"}})
	_ = relay.Deliver(mine.ID, runnerwire.Envelope{Type: runnerwire.TypeRepoAdminDone})
	if err := relay.Deliver(mine.ID, answer); err != nil {
		t.Fatal(err)
	}
	got := <-results
	if got.err != nil || got.done.ID != sent.RepoAdmin.ID || got.done.Inspect == nil || got.done.Inspect.Init == nil ||
		got.done.Inspect.Init.Files != 312 || got.done.Inspect.Init.Hash != "h1" {
		t.Fatalf("request = %+v, %v", got.done, got.err)
	}
	if len(got.done.Error) > runnerwire.MaxRepoAdminError || len(got.done.Inspect.Init.Ignored) != maxInitIgnored {
		t.Fatalf("not clamped: error %d bytes, %d ignored", len(got.done.Error), len(got.done.Inspect.Init.Ignored))
	}

	relay.Timeout = 50 * time.Millisecond
	if _, err := relay.Request(ctx, mine.ID, inspectReq(ann.ID)); !errors.Is(err, ErrRepoTimeout) {
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

// Making a folder a git project saves every file of it: the relay waits
// longer for an init_add than for any other op.
func TestRepoAdminRelayWaitsLongerForAnInit(t *testing.T) {
	reg, _ := newRegistry(t)
	relay := NewRepoAdminRelay(reg)
	ctx := context.Background()
	conn := newLLMConn()
	mine := online(t, reg, "ann", repoAdminInfo(runnerwire.CapRepoAdmin, runnerwire.CapAppAccept), conn)
	reg.SetWelcomed(mine.ID, true)
	defer func(op, init time.Duration) {
		DefaultRepoAdminTimeout, DefaultRepoAdminInitTimeout = op, init
	}(DefaultRepoAdminTimeout, DefaultRepoAdminInitTimeout)
	DefaultRepoAdminTimeout, DefaultRepoAdminInitTimeout = 50*time.Millisecond, 10*time.Second

	if _, err := relay.Request(ctx, mine.ID, inspectReq(ann.ID)); !errors.Is(err, ErrRepoTimeout) {
		t.Fatalf("inspect: %v, want ErrRepoTimeout", err)
	}
	conn.next(t)
	results := make(chan error, 1)
	go func() {
		_, err := relay.Request(ctx, mine.ID, runnerwire.RepoAdminRequest{AccountID: ann.ID, Op: runnerwire.RepoAdminInitAdd,
			Path: `C:\code\app`, InitPlan: "h1"})
		results <- err
	}()
	sent := conn.next(t)
	time.Sleep(200 * time.Millisecond) // past the other ops' wait
	if err := relay.Deliver(mine.ID, runnerwire.Envelope{Type: runnerwire.TypeRepoAdminDone,
		RepoAdminDone: &runnerwire.RepoAdminDone{ID: sent.RepoAdmin.ID, Repo: &runnerwire.Repo{ID: "r1"}}}); err != nil {
		t.Fatal(err)
	}
	if err := <-results; err != nil {
		t.Fatalf("init_add answered after the other ops' wait: %v", err)
	}
}

// The built-in runner is the one SetBuiltIn named, and Runner.BuiltIn says
// what its last welcome said; nothing else turns it on.
func TestRegistryBuiltIn(t *testing.T) {
	reg, _ := newRegistry(t)
	ctx := context.Background()
	runner, _, err := reg.Create(ctx, ann, "this machine")
	if err != nil {
		t.Fatal(err)
	}
	if reg.IsBuiltInID(runner.ID) || reg.IsBuiltInID("") {
		t.Fatal("a runner is built in before any was named")
	}
	reg.SetBuiltIn(runner.ID)
	if !reg.IsBuiltInID(runner.ID) || reg.IsBuiltInID("other") {
		t.Fatal("IsBuiltInID does not follow SetBuiltIn")
	}
	if got, err := reg.Get(ctx, runner.ID); err != nil || got.BuiltIn {
		t.Fatalf("before any welcome: %+v, %v", got, err)
	}
	reg.SetWelcomed(runner.ID, true)
	if got, err := reg.Get(ctx, runner.ID); err != nil || !got.BuiltIn {
		t.Fatalf("welcomed built in: %+v, %v", got, err)
	}
	list, err := reg.List(ctx, ann)
	if err != nil || len(list) != 1 || !list[0].BuiltIn {
		t.Fatalf("list = %+v, %v", list, err)
	}
	reg.SetWelcomed(runner.ID, false)
	if got, err := reg.Get(ctx, runner.ID); err != nil || got.BuiltIn {
		t.Fatalf("welcomed classic: %+v, %v", got, err)
	}
}

// What the relay takes for the built-in runner's own settings, and when.
func TestSettingsRelayLocalOnly(t *testing.T) {
	reg, _ := newRegistry(t)
	relay := NewSettingsRelay(reg)
	relay.Timeout = 2 * time.Second
	ctx := context.Background()
	yes := true
	local := runnerwire.SettingsRequest{AccountID: ann.ID, AskEveryRun: &yes}
	if !LocalOnly(local) || LocalOnly(settingsReq(ann.ID, 2)) {
		t.Fatal("LocalOnly misreads a request")
	}
	conn := newLLMConn()
	builtIn := online(t, reg, "ann", settingsInfo(runnerwire.CapRemoteSettings), conn)
	reg.SetWelcomed(builtIn.ID, true)
	classic := online(t, reg, "ann", settingsInfo(runnerwire.CapRemoteSettings), newLLMConn())

	if _, err := relay.Request(ctx, classic.ID, local); !errors.Is(err, ErrSettingsLocalOnly) {
		t.Fatalf("classic runner: %v, want ErrSettingsLocalOnly", err)
	}
	if _, err := relay.Request(ctx, builtIn.ID, runnerwire.SettingsRequest{AccountID: ann.ID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nothing asked: %v, want ErrInvalid", err)
	}
	errs := make(chan error, 1)
	go func() {
		_, err := relay.Request(ctx, builtIn.ID, local)
		errs <- err
	}()
	sent := conn.next(t)
	if sent.Settings == nil || sent.Settings.AskEveryRun == nil || !*sent.Settings.AskEveryRun || sent.Settings.MaxConcurrent != nil {
		t.Fatalf("sent = %+v", sent.Settings)
	}
	_ = relay.Deliver(builtIn.ID, runnerwire.Envelope{Type: runnerwire.TypeSettingsDone,
		SettingsDone: &runnerwire.SettingsDone{ID: sent.Settings.ID, MaxConcurrent: 1, Persisted: true}})
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
}
