package notify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The people in a project's graph are whatever an editor typed: on a server
// with accounts, the hub's mail must not carry an editor's words to any
// address they like. A reminder whose assignee is not an account that can
// read the project goes to the administrator's fallback address instead.
func TestRemindersOnAServerWithAccountsSkipAddressesThatAreNotReaders(t *testing.T) {
	now := time.Date(2026, 7, 30, 18, 0, 0, 0, time.Local)
	n, projectDir := notifyTestWorkspace(t, "2026-07-30T18:30", "in_progress")
	graph, err := os.ReadFile(filepath.Join(projectDir, "graph.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	graph = []byte(strings.Replace(string(graph), "ming@example.com", "outsider@evil.test", 1))
	if err := os.WriteFile(filepath.Join(projectDir, "graph.yaml"), graph, 0o644); err != nil {
		t.Fatal(err)
	}
	settings := n.Settings()
	settings.DefaultTo = "admin@example.com"
	if err := n.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	n.pm.SetRemote(true)

	var got []string
	orig := sendMailFn
	sendMailFn = func(_ NotifySettings, to, _, _ string) error {
		got = append(got, to)
		return nil
	}
	defer func() { sendMailFn = orig }()

	n.scan(now)
	if len(got) != 1 || got[0] != "admin@example.com" {
		t.Fatalf("reminders went to %v, want only the administrator's fallback address", got)
	}
}

// An assignee who is an account that can read the project is mailed as
// before; the server answers who that is.
func TestRemindersOnAServerWithAccountsReachTheProjectsReaders(t *testing.T) {
	now := time.Date(2026, 7, 30, 18, 0, 0, 0, time.Local)
	n, projectDir := notifyTestWorkspace(t, "2026-07-30T18:30", "in_progress")
	n.pm.SetRemote(true)
	var asked []string
	n.UseReaders(func(projectPath, address string) bool {
		asked = append(asked, filepath.Base(projectPath)+"|"+address)
		return address == "ming@example.com"
	})

	var got []string
	orig := sendMailFn
	sendMailFn = func(_ NotifySettings, to, _, _ string) error {
		got = append(got, to)
		return nil
	}
	defer func() { sendMailFn = orig }()

	n.scan(now)
	if len(got) != 1 || got[0] != "ming@example.com" {
		t.Fatalf("reminders went to %v, want the reader ming", got)
	}
	if len(asked) != 1 || asked[0] != filepath.Base(projectDir)+"|ming@example.com" {
		t.Fatalf("asked %v", asked)
	}
}
