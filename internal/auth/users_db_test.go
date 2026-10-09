package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/identity"
)

func storeForTest(t *testing.T) (*UserStore, string) {
	t.Helper()
	workspace := t.TempDir()
	users, err := NewUserStore(workspace)
	if err != nil {
		t.Fatalf("NewUserStore: %v", err)
	}
	t.Cleanup(func() { _ = users.Close() })
	return users, workspace
}

// The whole point of reading the table on every check: an operator with DB
// Browser open is a supported way to change an account, and the change has to
// land on the next request rather than the next restart.
func TestAccountEditedInTheDatabaseTakesEffectAtOnce(t *testing.T) {
	users, workspace := storeForTest(t)
	if err := users.Add(context.Background(), "ann", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	if err := users.Add(context.Background(), "bob", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	actor, before, ok := users.VerifyWithRevision(context.Background(), "bob", "correct-horse-battery")
	if !ok {
		t.Fatal("bob cannot sign in")
	}

	outside, err := db.Open(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := outside.ExecContext(context.Background(),
		`UPDATE accounts SET role = 'admin' WHERE name = 'bob'`); err != nil {
		t.Fatal(err)
	}
	if err := outside.Close(); err != nil {
		t.Fatal(err)
	}

	live, after, ok := users.ActorRevision(context.Background(), actor.ID)
	if !ok {
		t.Fatal("bob disappeared")
	}
	if live.Role != identity.RoleAdmin {
		t.Fatalf("role after external edit = %q, want admin", live.Role)
	}
	if after == before {
		t.Fatal("the revision survived a role change, so the old session would too")
	}
}

// Changing or clearing the sign-in address has to end the sessions the old
// address authorised.
func TestRevisionChangesWithEveryCredentialField(t *testing.T) {
	users, _ := storeForTest(t)
	if err := users.Add(context.Background(), "ann", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	actor, first, ok := users.VerifyWithRevision(context.Background(), "ann", "correct-horse-battery")
	if !ok {
		t.Fatal("ann cannot sign in")
	}
	seen := map[string]bool{first: true}
	steps := []struct {
		name   string
		change func() error
	}{
		{"email set", func() error {
			return users.SetEmail(context.Background(), "ann", "ann@example.test")
		}},
		{"email changed", func() error {
			return users.SetEmail(context.Background(), "ann", "elsewhere@example.test")
		}},
		// The password goes before the clear: clearing the address otherwise
		// returns the row to exactly how it started, and so to the starting
		// revision — which is correct, but not what this step is testing.
		{"password", func() error { return users.SetPassword(context.Background(), "ann", "another-long-password") }},
		{"email cleared", func() error { return users.ClearEmail(context.Background(), "ann") }},
	}
	for _, step := range steps {
		if err := step.change(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		revision, ok := users.Revision(context.Background(), actor.ID)
		if !ok {
			t.Fatalf("%s: the account disappeared", step.name)
		}
		if seen[revision] {
			t.Fatalf("%s left the revision unchanged, so old sessions survive it", step.name)
		}
		seen[revision] = true
	}
}

func TestRecordsCarryNoCredentialHashes(t *testing.T) {
	users, _ := storeForTest(t)
	if err := users.Add(context.Background(), "ann", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	if err := users.SetEmail(context.Background(), "ann", "ann@example.test"); err != nil {
		t.Fatal(err)
	}
	records := users.Records(context.Background())
	if len(records) != 1 {
		t.Fatalf("records = %+v", records)
	}
	if records[0].Hash != "" {
		t.Fatalf("Records leaked a credential hash: %+v", records[0])
	}
	if records[0].Email != "ann@example.test" || records[0].Role != identity.RoleAdmin {
		t.Fatalf("records = %+v", records[0])
	}
}

// An unknown name must cost the same argon2 verification a known one does, or
// the login form becomes an account-name oracle.
func TestUnknownNameStillCostsAHashVerification(t *testing.T) {
	users, _ := storeForTest(t)
	if err := users.Add(context.Background(), "ann", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, ok := users.Verify(context.Background(), "nobody", "correct-horse-battery"); ok {
		t.Fatal("an unknown account signed in")
	}
	unknown := time.Since(start)
	// A lookup that skipped argon2 would return in microseconds; the decoy
	// costs 64 MiB and three passes.
	if unknown < 5*time.Millisecond {
		t.Fatalf("rejecting an unknown name took %v, too fast to have hashed anything", unknown)
	}
}

// The table's unique index is the real check, and it has to fail with the same
// sentence the file store used.
func TestDuplicateNamesAreRejectedCaseInsensitively(t *testing.T) {
	users, _ := storeForTest(t)
	if err := users.Add(context.Background(), "ann", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	err := users.AddWithRole(context.Background(), "ANN", "another-long-password", identity.RoleMember)
	if err == nil || !strings.Contains(err.Error(), `user "ANN" already exists`) {
		t.Fatalf("duplicate name error = %v", err)
	}
	if got := users.Count(context.Background()); got != 1 {
		t.Fatalf("Count = %d, want 1", got)
	}
}

// An accounts table with no administrator locks its operator out of account
// management, so opening one repairs it.
func TestOpeningPromotesTheFirstAccountWhenNoAdminIsLeft(t *testing.T) {
	users, _ := storeForTest(t)
	if err := users.Add(context.Background(), "ann", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	if err := users.Add(context.Background(), "bob", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	if _, err := users.database.ExecContext(context.Background(),
		`UPDATE accounts SET role = 'member'`); err != nil {
		t.Fatal(err)
	}
	if err := users.EnsureAdmin(); err != nil {
		t.Fatal(err)
	}
	records := users.Records(context.Background())
	if len(records) != 2 || records[0].Role != identity.RoleAdmin || records[1].Role != identity.RoleMember {
		t.Fatalf("roles after repair = %+v", records)
	}
}

// Removing an account must not take the record of what it did with it: the
// audit trail keeps the actor id as plain text, not a foreign key.
func TestRemoveLeavesTheAuditTrailIntact(t *testing.T) {
	users, _ := storeForTest(t)
	if err := users.Add(context.Background(), "ann", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	if err := users.Add(context.Background(), "bob", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	records := users.Records(context.Background())
	var bobID string
	for _, record := range records {
		if record.Name == "bob" {
			bobID = record.ID
		}
	}
	ctx := context.Background()
	if _, err := users.database.ExecContext(ctx,
		`INSERT INTO audit_events (at, actor_id, actor_name, action) VALUES (?, ?, 'bob', 'sign-in')`,
		db.Now(), bobID); err != nil {
		t.Fatal(err)
	}
	if err := users.Remove(context.Background(), "bob"); err != nil {
		t.Fatal(err)
	}
	var events int
	if err := users.database.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM audit_events WHERE actor_id = ?`, bobID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("audit rows for a removed account = %d, want 1", events)
	}
	if _, _, ok := users.ActorRevision(context.Background(), bobID); ok {
		t.Fatal("the removed account still resolves")
	}
}

// NewUserStoreDB is the constructor the server should use: one handle, one
// writer queue.
func TestNewUserStoreDBUsesTheCallersHandle(t *testing.T) {
	workspace := t.TempDir()
	database, err := db.Open(workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	users := NewUserStoreDB(database)
	if err := users.EnsureAdmin(); err != nil {
		t.Fatal(err)
	}
	if err := users.Add(context.Background(), "ann", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	// Close must be a no-op on a handle the store does not own.
	if err := users.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := users.Verify(context.Background(), "ann", "correct-horse-battery"); !ok {
		t.Fatal("Close on a borrowed handle shut the database")
	}
}

// The address is how an account is found, so two accounts must never share
// one — compared as a mailbox is, without regard to case — whether the clash
// comes from setting an address or from creating an account with one.
func TestSignInAddressesAreUniqueCaseInsensitively(t *testing.T) {
	users, _ := storeForTest(t)
	ctx := context.Background()
	if err := users.AddWithEmail(ctx, "ann", "correct-horse-battery", "", "Ann@Example.test"); err != nil {
		t.Fatal(err)
	}
	if err := users.Add(ctx, "bob", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}

	if err := users.SetEmail(ctx, "bob", "ann@example.TEST"); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("SetEmail with a clashing address = %v, want ErrEmailTaken", err)
	}
	if err := users.AddWithEmail(ctx, "cat", "correct-horse-battery", "", " ANN@EXAMPLE.TEST "); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("AddWithEmail with a clashing address = %v, want ErrEmailTaken", err)
	}
	if got := users.Count(ctx); got != 2 {
		t.Fatalf("Count = %d, want the clashing account not to have been created", got)
	}
	// Several accounts with no address at all are fine: the uniqueness is only
	// about addresses somebody can sign in with.
	if err := users.Add(ctx, "dan", "correct-horse-battery"); err != nil {
		t.Fatalf("a second account without an address: %v", err)
	}
	// Re-registering an account's own address, in another case, is not a clash.
	if err := users.SetEmail(ctx, "ann", "ANN@example.test"); err != nil {
		t.Fatalf("re-registering ann's own address: %v", err)
	}
}

// The lookup the sign-in uses: case-insensitive, returning the stored form,
// and never matching the accounts that have no address.
func TestAccountByEmailFindsTheRegisteredAccount(t *testing.T) {
	users, _ := storeForTest(t)
	ctx := context.Background()
	if err := users.AddWithEmail(ctx, "ann", "correct-horse-battery", "", "Ann@Example.test"); err != nil {
		t.Fatal(err)
	}
	if err := users.Add(ctx, "bob", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}

	actor, revision, stored, ok := users.AccountByEmail(ctx, "  ann@example.test")
	if !ok || actor.Name != "ann" || stored != "Ann@Example.test" || revision == "" {
		t.Fatalf("AccountByEmail = %+v %q %q %v", actor, revision, stored, ok)
	}
	for _, address := range []string{"", "nobody@example.test", "not an address", strings.Repeat("a", maxEmailBytes) + "@x.test"} {
		if _, _, _, ok := users.AccountByEmail(ctx, address); ok {
			t.Fatalf("%q resolved to an account", address)
		}
	}
}

// The migration that retired the PIN must leave no PIN hash behind, and must
// not refuse to start on a workspace where two accounts already share an
// address in different case: the oldest keeps it, the other loses it.
func TestTheEmailMigrationClearsPinsAndSettlesSharedAddresses(t *testing.T) {
	workspace := t.TempDir()
	ctx := context.Background()
	database, err := db.Open(workspace)
	if err != nil {
		t.Fatal(err)
	}
	// Wind the workspace back to how 0005 left it, then fill it the way the
	// PIN sign-in could have.
	for _, stmt := range []string{
		`DROP INDEX accounts_email_nocase`,
		`DELETE FROM schema_migrations WHERE name = '0006_email_sign_in'`,
	} {
		if _, err := database.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	// dan and eve-old were shut out with `pin-clear`, which left their address
	// behind. eve-old is older than eve, who holds the same mailbox and could
	// still sign in.
	for i, row := range []struct{ name, pin, email string }{
		{"ann", "an-old-pin-hash", "Shared@Example.test"},
		{"bob", "an-old-pin-hash", "shared@example.test"},
		{"cat", "an-old-pin-hash", "cat@example.test"},
		{"dan", "", "dan@example.test"},
		{"eve-old", "", "eve@example.test"},
		{"eve", "an-old-pin-hash", "Eve@example.test"},
	} {
		if _, err := database.ExecContext(ctx,
			`INSERT INTO accounts (id, name, role, password_hash, pin_hash, email, created_at)
			 VALUES (?, ?, ?, '', ?, ?, ?)`,
			fmt.Sprintf("id-%d", i), row.name, map[bool]string{true: "admin", false: "member"}[i == 0],
			row.pin, row.email, db.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	users, err := NewUserStore(workspace)
	if err != nil {
		t.Fatalf("reopening after the migration: %v", err)
	}
	t.Cleanup(func() { _ = users.Close() })

	var pins int
	if err := users.database.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM accounts WHERE pin_hash != ''`).Scan(&pins); err != nil {
		t.Fatal(err)
	}
	if pins != 0 {
		t.Fatalf("%d accounts still carry a PIN hash", pins)
	}
	emails := map[string]string{}
	for _, record := range users.Records(ctx) {
		emails[record.Name] = record.Email
	}
	want := map[string]string{
		"ann": "Shared@Example.test", "bob": "", "cat": "cat@example.test",
		// A revoked account stays revoked, and does not take the address from
		// the account that could actually use it.
		"dan": "", "eve-old": "", "eve": "Eve@example.test",
	}
	for name, address := range want {
		if emails[name] != address {
			t.Fatalf("after migration %s has %q, want %q (all: %v)", name, emails[name], address, emails)
		}
	}
	// And the index is in place from here on.
	if err := users.SetEmail(ctx, "bob", "SHARED@example.test"); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("after migration a clashing address = %v, want ErrEmailTaken", err)
	}
}

// A limit is required of every caller of accounts, and the one value SQLite
// reads backwards — a negative LIMIT means no limit — must not become a way to
// select the whole table by accident.
func TestAccountReadWithNoUsableLimitReturnsNothing(t *testing.T) {
	users, _ := storeForTest(t)
	if err := users.Add(context.Background(), "ann", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, limit := range []int{0, -1, -1000} {
		got, err := users.accounts(ctx, accountByName, limit, "ann")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("limit %d returned %d rows, want none", limit, len(got))
		}
	}
	if got, err := users.accounts(ctx, accountByName, 1, "ann"); err != nil || len(got) != 1 {
		t.Fatalf("a usable limit returned %d rows, %v", len(got), err)
	}
}

// The id comes off a session cookie or a stored capability, so its length is a
// client's choice. It cannot inject, but it has no business reaching the
// database at a size no id this package issues could ever have.
func TestAccountLookupRefusesAnUnusableIdentifier(t *testing.T) {
	users, _ := storeForTest(t)
	if err := users.Add(context.Background(), "ann", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", strings.Repeat("a", maxAccountIDBytes+1)} {
		if _, _, ok := users.ActorRevision(context.Background(), id); ok {
			t.Fatalf("an id of %d bytes resolved to an account", len(id))
		}
	}
}

// net/mail parses an address of any length, so without a bound of its own the
// email column takes whatever the caller hands over.
func TestSettingAnEmailRefusesAnUnusableAddress(t *testing.T) {
	users, _ := storeForTest(t)
	if err := users.Add(context.Background(), "ann", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	huge := strings.Repeat("a", maxEmailBytes) + "@example.test"
	if err := users.SetEmail(context.Background(), "ann", huge); err == nil {
		t.Fatal("an oversized address was accepted")
	}
	// Refused, not truncated: a passcode sent to half an address goes nowhere,
	// and a stored half-address would be a credential nobody can use.
	if records := users.Records(context.Background()); len(records) != 1 || records[0].Email != "" {
		t.Fatalf("email column holds %q, want it untouched", records[0].Email)
	}
}

// Argon2 is memory-hard by design, which makes an unbounded password an
// amplifier rather than a strong one.
func TestAccountCreationRefusesAnUnusablePassword(t *testing.T) {
	users, _ := storeForTest(t)
	if err := users.Add(context.Background(), "ann", strings.Repeat("x", maxLoginPasswordBytes+1)); err == nil {
		t.Fatal("an oversized password was accepted")
	}
	if got := users.Count(context.Background()); got != 0 {
		t.Fatalf("Count = %d, want the account not to have been created", got)
	}
}
