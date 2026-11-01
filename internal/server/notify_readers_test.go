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
		if _, got := notifier.Recipient(f.otherRoot, check.address); got != check.want {
			t.Errorf("restricted project, %q: Recipient = %v, want %v", check.address, got, check.want)
		}
	}
	main := f.server.pm.Store().Root()
	if _, ok := notifier.Recipient(main, "dan@example.test"); !ok {
		t.Error("an open project's reminder refused a signed-in account")
	}
	if _, ok := notifier.Recipient(main, "outsider@evil.test"); ok {
		t.Error("an open project's reminder went to an address with no account")
	}
}

// The address an editor typed into the graph only names the account: the
// reminder goes to the address stored on it, so no display name or comment
// the editor wrapped around it reaches the hub's mail.
func TestRemindersGoToTheReadersStoredAddress(t *testing.T) {
	f := newMembersFixture(t)
	if response := f.setMember(t, f.bea, "cal", "viewer"); response.Code != http.StatusOK {
		t.Fatalf("bea restricting other: %d %s", response.Code, response.Body)
	}
	for _, typed := range []string{
		`"Pay the invoice at https://evil.test" <cal@example.test>`,
		`cal@example.test (evil@evil.test)`,
		` CAL@example.test`,
	} {
		to, ok := f.server.notifier.Recipient(f.otherRoot, typed)
		if !ok || to != "cal@example.test" {
			t.Errorf("Recipient(%q) = %q, %v; want cal's stored address", typed, to, ok)
		}
	}
}
