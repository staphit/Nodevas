package analytics

import (
	"context"
	"slices"
	"testing"
	"time"

	"nodevas/internal/audit"
)

// A refused sign-in, step-up, authenticator code or device-link redeem
// writes an audit row, and a stranger can make one a minute per source
// forever: those rows age out with the retention setting. The trail of
// what people did (a sign-in, an account removed) stays.
func TestPruneAgesOutAuditRefusals(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	old, recent := testNow.AddDate(0, 0, -200), testNow.AddDate(0, 0, -10)
	stamp := func(at time.Time) string { return at.UTC().Format(time.RFC3339) }
	refusals := []string{"auth.signin.failed", "auth.stepup.failed", "auth.totp.failed", "runner.link.failed"}
	for _, action := range refusals {
		f.exec(`INSERT INTO audit_events (at, action) VALUES (?, ?), (?, ?)`, stamp(old), action, stamp(recent), action)
	}
	for _, action := range []string{"auth.signin", "account.remove", "runner.link.redeem"} {
		f.exec(`INSERT INTO audit_events (at, action) VALUES (?, ?)`, stamp(old), action)
	}
	res, err := Prune(ctx, f.db, testNow, DefaultRetentionDays)
	if err != nil {
		t.Fatal(err)
	}
	if res.Refusals != len(refusals) {
		t.Errorf("Prune removed %d refusal rows, want %d", res.Refusals, len(refusals))
	}
	if n := f.count(`SELECT COUNT(*) FROM audit_events WHERE action IN ('auth.signin.failed','auth.stepup.failed','auth.totp.failed','runner.link.failed')`); n != len(refusals) {
		t.Errorf("refusal rows left = %d, want the %d recent ones", n, len(refusals))
	}
	if n := f.count(`SELECT COUNT(*) FROM audit_events WHERE action IN ('auth.signin','account.remove','runner.link.redeem')`); n != 3 {
		t.Errorf("other audit rows left = %d, want all 3", n)
	}
}

// The list names the audit package's refusal actions as it spells them.
func TestAuditRefusalActionsAreTheTrailsOwn(t *testing.T) {
	want := []string{audit.ActionSignInFailed, audit.ActionStepUpFailed, audit.ActionTOTPFailed, audit.ActionRunnerLinkFailed}
	if !slices.Equal(AuditRefusalActions, want) {
		t.Errorf("AuditRefusalActions = %q, want %q", AuditRefusalActions, want)
	}
}
