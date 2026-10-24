package members

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"nodevas/internal/db"
	"nodevas/internal/identity"
)

func testStore(t *testing.T) (*Store, *db.DB) {
	t.Helper()
	database, err := db.OpenAt(filepath.Join(t.TempDir(), db.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store := NewStore(database)
	if err := store.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store, database
}

var (
	admin = identity.Actor{ID: "u-admin", Name: "ann", Role: identity.RoleAdmin}
	bea   = identity.Actor{ID: "u-bea", Name: "bea", Role: identity.RoleMember}
	cal   = identity.Actor{ID: "u-cal", Name: "cal", Role: identity.RoleMember}
	dan   = identity.Actor{ID: "u-dan", Name: "dan", Role: identity.RoleMember}
)

const proj = "/srv/ws/other"

func TestOpenProjectByDefault(t *testing.T) {
	store, _ := testStore(t)
	access := store.Access(dan, proj)
	if access.Restricted || !access.CanRead() || !access.CanWrite() || access.EffectiveRole() != RoleEditor {
		t.Fatalf("open project access = %+v", access)
	}
	if !store.CanManage(dan, proj) {
		t.Fatal("anyone may restrict an open project")
	}
	agent := dan
	agent.Agent = identity.AgentWorker
	if store.CanManage(agent, proj) {
		t.Fatal("an agent may never manage members")
	}
}

func TestRolesAndOwnership(t *testing.T) {
	store, database := testStore(t)
	ctx := context.Background()
	changes := 0
	store.OnChange(func(string) { changes++ })

	if err := store.Set(ctx, bea, proj, cal.ID, RoleViewer); err != nil {
		t.Fatal(err)
	}
	if changes == 0 {
		t.Fatal("no change notification")
	}
	if got := store.Access(bea, proj); got.Role != RoleOwner {
		t.Fatalf("the person who restricted the project is %q, want owner", got.Role)
	}
	if got := store.Access(cal, proj); !got.CanRead() || got.CanWrite() {
		t.Fatalf("viewer access = %+v", got)
	}
	if got := store.Access(dan, proj); got.CanRead() {
		t.Fatalf("non-member access = %+v", got)
	}
	if got := store.Access(admin, proj); !got.CanRead() || !got.CanWrite() || !store.CanManage(admin, proj) {
		t.Fatalf("admin access = %+v", got)
	}
	if err := store.Set(ctx, cal, proj, dan.ID, RoleViewer); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a viewer adding a member: %v", err)
	}
	if err := store.Set(ctx, bea, proj, bea.ID, RoleEditor); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("the last owner stepping down: %v", err)
	}
	if err := store.Remove(ctx, bea, proj, bea.ID); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("the last owner leaving: %v", err)
	}
	if err := store.Set(ctx, bea, proj, cal.ID, "boss"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an unknown role: %v", err)
	}

	// What the store holds survives a restart.
	reloaded := NewStore(database)
	if err := reloaded.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Access(cal, proj); got.Role != RoleViewer || !got.Restricted {
		t.Fatalf("after reload cal = %+v", got)
	}

	list, err := store.List(ctx, proj)
	if err != nil || len(list) != 2 || list[0].UserID != bea.ID {
		t.Fatalf("list = %+v, %v", list, err)
	}

	if err := store.Remove(ctx, bea, proj, cal.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Remove(ctx, bea, proj, bea.ID); err != nil {
		t.Fatalf("the only member leaving reopens the project: %v", err)
	}
	if store.Restricted(proj) {
		t.Fatal("still restricted with no members")
	}
}

func TestMovedCopiedRemoved(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	if err := store.Set(ctx, bea, proj, cal.ID, RoleViewer); err != nil {
		t.Fatal(err)
	}
	child := proj + "/child"
	if err := store.Set(ctx, bea, child, cal.ID, RoleEditor); err != nil {
		t.Fatal(err)
	}
	if err := store.Moved(ctx, proj, "/srv/ws/renamed"); err != nil {
		t.Fatal(err)
	}
	if store.Restricted(proj) || store.Restricted(child) {
		t.Fatal("the old place is still restricted")
	}
	if got := store.Access(dan, "/srv/ws/renamed/child"); got.CanRead() {
		t.Fatal("a moved child project opened up")
	}
	if err := store.Copied(ctx, "/srv/ws/renamed", "/srv/ws/copy"); err != nil {
		t.Fatal(err)
	}
	if got := store.Access(dan, "/srv/ws/copy"); got.CanRead() {
		t.Fatal("a copy of a restricted project is open")
	}
	if got := store.Access(cal, "/srv/ws/copy/child"); got.Role != RoleEditor {
		t.Fatalf("copied child role = %q", got.Role)
	}
	if err := store.Removed(ctx, "/srv/ws/copy"); err != nil {
		t.Fatal(err)
	}
	if store.Restricted("/srv/ws/copy") || store.Restricted("/srv/ws/copy/child") {
		t.Fatal("a deleted project kept its members")
	}
	// A sibling sharing a name prefix is not under the moved directory.
	if err := store.Set(ctx, bea, "/srv/ws/renamed-2", cal.ID, RoleViewer); err != nil {
		t.Fatal(err)
	}
	if err := store.Removed(ctx, "/srv/ws/renamed"); err != nil {
		t.Fatal(err)
	}
	if !store.Restricted("/srv/ws/renamed-2") {
		t.Fatal("removing a project took a sibling's members with it")
	}
}

// A store that could not load fails closed.
func TestUnavailableFailsClosed(t *testing.T) {
	store := Unavailable()
	if got := store.Access(dan, proj); got.CanRead() {
		t.Fatalf("unavailable store let a member in: %+v", got)
	}
	if got := store.Access(admin, proj); !got.CanRead() {
		t.Fatal("unavailable store locked out an administrator")
	}
	if store.CanManage(admin, proj) {
		t.Fatal("unavailable store accepts changes")
	}
}
