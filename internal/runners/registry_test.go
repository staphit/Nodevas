package runners

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"nodevas/internal/db/dbtest"
	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
)

var (
	ann   = identity.Actor{ID: "u-ann", Name: "ann", Role: identity.RoleMember}
	bob   = identity.Actor{ID: "u-bob", Name: "bob", Role: identity.RoleMember}
	admin = identity.Actor{ID: "u-root", Name: "root", Role: identity.RoleAdmin}
)

type fakeConn struct {
	mu      sync.Mutex
	reasons []string
}

func (f *fakeConn) Bye(reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reasons = append(f.reasons, reason)
}

func (f *fakeConn) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reasons...)
}

func newRegistry(t *testing.T) (*Registry, *int) {
	t.Helper()
	changes := 0
	return NewRegistry(dbtest.Open(t), func() { changes++ }), &changes
}

func TestCreateStoresOnlyTheTokenHash(t *testing.T) {
	reg, changes := newRegistry(t)
	ctx := context.Background()
	runner, token, err := reg.Create(ctx, ann, "  desk PC  ")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, runnerwire.TokenPrefix) || len(token) < 40 {
		t.Fatalf("token = %q", token)
	}
	if runner.Name != "desk PC" || runner.OwnerID != ann.ID || runner.OwnerName != "ann" {
		t.Fatalf("runner = %+v", runner)
	}
	if *changes != 1 {
		t.Fatalf("changes = %d", *changes)
	}
	var stored string
	if err := reg.db.QueryRowContext(ctx, `SELECT token_hash FROM runner_tokens WHERE runner_id = ?`, runner.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != HashToken(token) || strings.Contains(stored, token[len(runnerwire.TokenPrefix):]) {
		t.Fatalf("stored %q is not the hash of the token", stored)
	}
}

func TestCreateRejectsBadNames(t *testing.T) {
	reg, _ := newRegistry(t)
	for _, name := range []string{"", "   ", strings.Repeat("x", MaxNameRunes+1), "bad\x07name"} {
		if _, _, err := reg.Create(context.Background(), ann, name); !errors.Is(err, ErrInvalid) {
			t.Errorf("Create(%q) err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestAuthenticate(t *testing.T) {
	reg, _ := newRegistry(t)
	ctx := context.Background()
	runner, token, err := reg.Create(ctx, ann, "pc")
	if err != nil {
		t.Fatal(err)
	}
	got, err := reg.Authenticate(ctx, token)
	if err != nil || got.ID != runner.ID {
		t.Fatalf("Authenticate = %+v, %v", got, err)
	}
	for _, bad := range []string{"", "nvr_wrong", token[len(runnerwire.TokenPrefix):], token + "x"} {
		if _, err := reg.Authenticate(ctx, bad); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("Authenticate(%q) err = %v", bad, err)
		}
	}
	if err := reg.Revoke(ctx, ann, runner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Authenticate(ctx, token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked token err = %v", err)
	}
}

func TestListAndRevokeVisibility(t *testing.T) {
	reg, _ := newRegistry(t)
	ctx := context.Background()
	annRunner, _, _ := reg.Create(ctx, ann, "ann-pc")
	bobRunner, _, _ := reg.Create(ctx, bob, "bob-pc")

	mine, err := reg.List(ctx, ann)
	if err != nil || len(mine) != 1 || mine[0].ID != annRunner.ID {
		t.Fatalf("ann sees %+v, %v", mine, err)
	}
	all, err := reg.List(ctx, admin)
	if err != nil || len(all) != 2 {
		t.Fatalf("admin sees %+v, %v", all, err)
	}
	if err := reg.Revoke(ctx, ann, bobRunner.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ann revoking bob's runner err = %v", err)
	}
	if err := reg.Revoke(ctx, admin, bobRunner.ID); err != nil {
		t.Fatalf("admin revoke: %v", err)
	}
	all, _ = reg.List(ctx, admin)
	if len(all) != 1 || all[0].ID != annRunner.ID {
		t.Fatalf("after revoke admin sees %+v", all)
	}
	if err := reg.Revoke(ctx, admin, bobRunner.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second revoke err = %v", err)
	}
}

func TestSetHelloStoresAndValidates(t *testing.T) {
	reg, _ := newRegistry(t)
	ctx := context.Background()
	runner, _, _ := reg.Create(ctx, ann, "pc")
	info := runnerwire.Info{
		Version: "1.2.3", Hostname: "desk", OS: "windows", Arch: "amd64",
		Agents: []runnerwire.Agent{{Kind: "claude", Installed: true, Version: "2.0",
			Models: []runnerwire.Model{{ID: "default", Default: true}, {ID: "opus"}}}},
		Repos: []runnerwire.Repo{{ID: "r1", Name: "nodevas", Path: `H:\visual-editor`}},
	}
	if err := reg.SetHello(ctx, runner.ID, info); err != nil {
		t.Fatal(err)
	}
	got, err := reg.Get(ctx, runner.ID)
	if err != nil || got.Info == nil || got.Info.Hostname != "desk" || got.LastSeenAt == "" ||
		len(got.Info.Agents) != 1 || len(got.Info.Agents[0].Models) != 2 {
		t.Fatalf("Get = %+v, %v", got, err)
	}

	tooMany := info
	tooMany.Repos = make([]runnerwire.Repo, runnerwire.MaxRepos+1)
	for i := range tooMany.Repos {
		tooMany.Repos[i] = runnerwire.Repo{ID: "r"}
	}
	long := info
	long.Hostname = strings.Repeat("h", 300)
	noModelID := info
	noModelID.Agents = []runnerwire.Agent{{Kind: "claude", Models: []runnerwire.Model{{ID: " "}}}}
	for name, bad := range map[string]runnerwire.Info{"repos": tooMany, "hostname": long, "model id": noModelID} {
		if err := reg.SetHello(ctx, runner.ID, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if err := reg.SetHello(ctx, "nope", info); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown runner err = %v", err)
	}
}

func TestAttachReplacesAndRevokeKicks(t *testing.T) {
	reg, _ := newRegistry(t)
	ctx := context.Background()
	runner, _, _ := reg.Create(ctx, ann, "pc")

	first := &fakeConn{}
	detachFirst := reg.Attach(runner.ID, first)
	if !reg.Online(runner.ID) {
		t.Fatal("not online after attach")
	}
	second := &fakeConn{}
	detachSecond := reg.Attach(runner.ID, second)
	if got := first.got(); len(got) != 1 || got[0] != runnerwire.ByeReplaced {
		t.Fatalf("first got %v", got)
	}
	// The replaced connection going away late must not take its successor
	// offline.
	detachFirst()
	if !reg.Online(runner.ID) {
		t.Fatal("successor detached by the replaced connection")
	}
	list, _ := reg.List(ctx, ann)
	if len(list) != 1 || !list[0].Online {
		t.Fatalf("list = %+v", list)
	}

	if err := reg.Revoke(ctx, ann, runner.ID); err != nil {
		t.Fatal(err)
	}
	if got := second.got(); len(got) != 1 || got[0] != runnerwire.ByeRevoked {
		t.Fatalf("second got %v", got)
	}
	if reg.Online(runner.ID) {
		t.Fatal("still online after revoke")
	}
	detachSecond()
}
