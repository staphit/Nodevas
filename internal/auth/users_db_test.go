package auth

import (
	"context"
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

// Rotating the PIN or the address its passcodes go to has to end the sessions
// the old pair authorised.
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
		{"pin and email", func() error {
			return users.SetPin(context.Background(), "ann", "ann-pin-long-enough", "ann@example.test")
		}},
		{"email only", func() error {
			return users.SetPin(context.Background(), "ann", "ann-pin-long-enough", "elsewhere@example.test")
		}},
		{"pin cleared", func() error { return users.ClearPin(context.Background(), "ann") }},
		{"password", func() error { return users.SetPassword(context.Background(), "ann", "another-long-password") }},
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
	if err := users.SetPin(context.Background(), "ann", "ann-pin-long-enough", "ann@example.test"); err != nil {
		t.Fatal(err)
	}
	records := users.Records(context.Background())
	if len(records) != 1 {
		t.Fatalf("records = %+v", records)
	}
	if records[0].Hash != "" || records[0].PinHash != "" {
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

// VerifyPin has no name to look up, so it verifies against every account that
// carries a PIN. The ceiling that keeps that bounded has to be enforced by the
// query, not after the whole table is already in memory.
func TestPinCandidateReadStopsAtTheCeiling(t *testing.T) {
	users, _ := storeForTest(t)
	ctx := context.Background()
	// Rows written directly: the point is how many the SELECT returns, and
	// hashing a hundred PINs would cost minutes for no extra coverage.
	for i := 0; i < maxPinAccounts+10; i++ {
		if _, err := users.database.ExecContext(ctx,
			`INSERT INTO accounts (id, name, role, password_hash, pin_hash, email, created_at)
			 VALUES (?, ?, 'member', '', 'not-a-real-hash', '', ?)`,
			fmt.Sprintf("id-%d", i), fmt.Sprintf("user%d", i), db.Now()); err != nil {
			t.Fatal(err)
		}
	}

	candidates, err := users.accounts(ctx, accountsWithPins, maxPinAccounts+1)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != maxPinAccounts+1 {
		t.Fatalf("read %d candidates, want the ceiling plus one", len(candidates))
	}
	// One past the ceiling is what makes the refusal below reachable at all.
	if _, _, _, ok := users.VerifyPin(context.Background(), "whatever-pin-value"); ok {
		t.Fatal("a workspace past the PIN ceiling still authenticated")
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
func TestSettingAPinRefusesAnUnusableEmailAddress(t *testing.T) {
	users, _ := storeForTest(t)
	if err := users.Add(context.Background(), "ann", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	huge := strings.Repeat("a", maxEmailBytes) + "@example.test"
	if err := users.SetPin(context.Background(), "ann", "ann-pin-long-enough", huge); err == nil {
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
