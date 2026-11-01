package server

import (
	"net/http"
	"testing"
)

// On a server with accounts, deadline reminders about a project go only to
// the addresses of accounts that can read it.
func TestRemindersGoOnlyToAccountsThatCanReadTheProject(t *testing.T) {
	f := newMembersFixture(t)
	if response := f.setMember(t, f.bea, "cal", "viewer"); response.Code != http.StatusOK {
		t.Fatalf("bea restricting other: %d %s", response.Code, response.Body)
	}
	notifier := f.server.notifier
	for _, check := range []struct {
		address string
		want    bool
	}{
		{"cal@example.test", true},    // a member
		{"bea@example.test", true},    // its owner
		{testEmail, true},             // an administrator
		{"dan@example.test", false},   // an account, not a member
		{"outsider@evil.test", false}, // no account at all
		{"", false},
	} {
		if got := notifier.MayRemind(f.otherRoot, check.address); got != check.want {
			t.Errorf("restricted project, %q: MayRemind = %v, want %v", check.address, got, check.want)
		}
	}
	main := f.server.pm.Store().Root()
	if !notifier.MayRemind(main, "dan@example.test") {
		t.Error("an open project's reminder refused a signed-in account")
	}
	if notifier.MayRemind(main, "outsider@evil.test") {
		t.Error("an open project's reminder went to an address with no account")
	}
}
