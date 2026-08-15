package notify

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nodevas/internal/project"
	"nodevas/internal/realtime"
)

func TestParseDeadline(t *testing.T) {
	if _, err := ParseDeadline("2026-13-40"); err == nil {
		t.Fatal("expected error for invalid deadline")
	}
	exact, err := ParseDeadline("2026-07-30T18:30")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 7, 30, 18, 30, 0, 0, time.Local)
	if !exact.Equal(want) {
		t.Fatalf("got %v want %v", exact, want)
	}
	// A bare date means "due by the end of that day".
	day, err := ParseDeadline("2026-07-30")
	if err != nil {
		t.Fatal(err)
	}
	if !day.Equal(time.Date(2026, 7, 31, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("bare date should resolve to end of day, got %v", day)
	}
}

func TestOutboundMailQuotaBoundsScheduledAbuse(t *testing.T) {
	notifier := &Notifier{}
	now := time.Now()
	for index := 0; index < mailRecipientLimit; index++ {
		if err := notifier.reserveMail("victim@example.com", now); err != nil {
			t.Fatalf("attempt %d: %v", index, err)
		}
	}
	if err := notifier.reserveMail("victim@example.com", now); !errors.Is(err, ErrMailRateLimited) {
		t.Fatalf("recipient quota error = %v, want ErrMailRateLimited", err)
	}
	if len(notifier.mailAttempts) > mailGlobalLimit {
		t.Fatalf("mail attempt state grew beyond its cap: %d", len(notifier.mailAttempts))
	}
}

func notifyTestWorkspace(t *testing.T, deadline string, status string) (*Notifier, string) {
	t.Helper()
	workspace := t.TempDir()
	projectDir := filepath.Join(workspace, "alpha")
	if err := os.MkdirAll(filepath.Join(projectDir, "run"), 0o755); err != nil {
		t.Fatal(err)
	}
	graph := `version: 1
users:
  - id: u1
    name: 小明
    email: ming@example.com
nodes:
  - id: node-0001
    title: 拍攝
    assignee: u1
    deadline: "` + deadline + `"
`
	if err := os.WriteFile(filepath.Join(projectDir, "graph.yaml"), []byte(graph), 0o644); err != nil {
		t.Fatal(err)
	}
	state := map[string]any{"nodes": map[string]any{"node-0001": map[string]any{"status": status}}}
	stateData, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(projectDir, "run", "state.json"), stateData, 0o644); err != nil {
		t.Fatal(err)
	}
	pm, err := project.NewManagerAt(workspace, realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	n := NewNotifier(pm)
	settings := DefaultNotifySettings()
	settings.Enabled = true
	settings.LeadMinutes = 60
	settings.SMTPHost = "smtp.example.com"
	settings.From = "vised@example.com"
	if err := n.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	return n, projectDir
}

func TestScanSendsOnceInsideLeadWindow(t *testing.T) {
	now := time.Date(2026, 7, 30, 18, 0, 0, 0, time.Local)
	deadline := "2026-07-30T18:30"
	n, _ := notifyTestWorkspace(t, deadline, "in_progress")

	var got []string
	orig := sendMailFn
	sendMailFn = func(_ NotifySettings, to, subject, _ string) error {
		got = append(got, to+"|"+subject)
		return nil
	}
	defer func() { sendMailFn = orig }()

	n.scan(now)
	if len(got) != 1 {
		t.Fatalf("expected 1 mail, got %d: %v", len(got), got)
	}
	if got[0] != "ming@example.com|[Nodevas] 截止提醒：拍攝" {
		t.Fatalf("unexpected mail: %s", got[0])
	}
	// Second scan inside the same window must not re-send.
	n.scan(now.Add(time.Minute))
	if len(got) != 1 {
		t.Fatalf("dedup failed, got %d mails", len(got))
	}
}

func TestScanUsesDonePlanAsDeadline(t *testing.T) {
	workspace := t.TempDir()
	projectDir := filepath.Join(workspace, "alpha")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	graph := `version: 1
users:
  - id: u1
    name: 小明
    email: ming@example.com
nodes:
  - id: node-0001
    title: 拍攝
    assignee: u1
ui:
  plans:
    node-0001:
      - date: "2026-07-30"
        status: done
`
	if err := os.WriteFile(filepath.Join(projectDir, "graph.yaml"), []byte(graph), 0o644); err != nil {
		t.Fatal(err)
	}
	pm, err := project.NewManagerAt(workspace, realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	n := NewNotifier(pm)
	settings := DefaultNotifySettings()
	settings.Enabled = true
	settings.LeadMinutes = 60
	settings.SMTPHost = "smtp.example.com"
	settings.From = "vised@example.com"
	if err := n.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}

	var sends int
	orig := sendMailFn
	sendMailFn = func(NotifySettings, string, string, string) error {
		sends++
		return nil
	}
	defer func() { sendMailFn = orig }()

	// Done milestone on 7/30 is due end-of-day; 23:30 is inside the 60m lead.
	n.scan(time.Date(2026, 7, 30, 23, 30, 0, 0, time.Local))
	if sends != 1 {
		t.Fatalf("expected plan-based deadline to notify once, got %d", sends)
	}
}

func TestScanSkipsOutsideWindowAndDoneNodes(t *testing.T) {
	deadline := "2026-07-30T18:30"

	var sends int
	orig := sendMailFn
	sendMailFn = func(NotifySettings, string, string, string) error {
		sends++
		return nil
	}
	defer func() { sendMailFn = orig }()

	// Too early: lead window (60m) not reached yet.
	n, _ := notifyTestWorkspace(t, deadline, "in_progress")
	n.scan(time.Date(2026, 7, 30, 17, 0, 0, 0, time.Local))
	if sends != 0 {
		t.Fatalf("mail sent before lead window: %d", sends)
	}
	// Past deadline: stay silent.
	n.scan(time.Date(2026, 7, 30, 18, 30, 0, 0, time.Local))
	if sends != 0 {
		t.Fatalf("mail sent after deadline: %d", sends)
	}
	// Done nodes never notify, even inside the window.
	n2, _ := notifyTestWorkspace(t, deadline, "done")
	n2.scan(time.Date(2026, 7, 30, 18, 0, 0, 0, time.Local))
	if sends != 0 {
		t.Fatalf("mail sent for done node: %d", sends)
	}
}

// The projectDir directory is what gets synced to a cloud drive or handed to a
// colleague. A plaintext SMTP password must not be in it.
func TestSMTPPasswordStaysOutOfTheProjectDirectory(t *testing.T) {
	notifier, _ := notifyTestWorkspace(t, "2026-07-30T18:30", "in_progress")

	settings := notifier.loadSettings()
	settings.SMTPPass = "hunter2-hunter2"
	if err := notifier.saveSettings(settings); err != nil {
		t.Fatal(err)
	}

	stored, err := os.ReadFile(notifier.settingsPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "hunter2") {
		t.Fatalf("json holds the SMTP password: %s", stored)
	}
	secret, err := os.ReadFile(notifier.secretPath())
	if err != nil {
		t.Fatalf("read secret file: %v", err)
	}
	if strings.Contains(string(secret), "hunter2-hunter2") {
		t.Fatalf("the encrypted secret file holds the password in plaintext: %s", secret)
	}
	if len(secret) == 0 {
		t.Fatal("the encrypted secret file is empty")
	}
	if reloaded := notifier.loadSettings(); reloaded.SMTPPass != "hunter2-hunter2" {
		t.Fatalf("password did not survive the round trip: %q", reloaded.SMTPPass)
	}
}

func TestSaveSettingsRequiresPasswordForChangedSMTPEndpoint(t *testing.T) {
	notifier, _ := notifyTestWorkspace(t, "2026-07-30T18:30", "in_progress")
	settings := notifier.Settings()
	settings.SMTPUser = "mailer"
	settings.SMTPPass = "original-password"
	if err := notifier.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}

	changed := settings
	changed.SMTPHost = "attacker.example.com"
	changed.SMTPPass = ""
	if err := notifier.SaveSettings(changed); !errors.Is(err, ErrSMTPPasswordRequired) {
		t.Fatalf("changed endpoint error = %v, want %v", err, ErrSMTPPasswordRequired)
	}
	kept := notifier.Settings()
	if kept.SMTPHost != settings.SMTPHost || kept.SMTPPass != "original-password" {
		t.Fatalf("rejected update changed settings: host=%q pass=%q", kept.SMTPHost, kept.SMTPPass)
	}

	changed.SMTPPass = "replacement-password"
	if err := notifier.SaveSettings(changed); err != nil {
		t.Fatalf("save with replacement password: %v", err)
	}
	updated := notifier.Settings()
	if updated.SMTPHost != changed.SMTPHost || updated.SMTPPass != "replacement-password" {
		t.Fatalf("updated settings = host %q pass %q", updated.SMTPHost, updated.SMTPPass)
	}
}

func TestStoredSMTPPasswordIsBoundToEndpointIdentity(t *testing.T) {
	notifier, _ := notifyTestWorkspace(t, "2026-07-30T18:30", "in_progress")
	settings := notifier.Settings()
	settings.SMTPUser = "mailer"
	settings.SMTPPass = "bound-password"
	if err := notifier.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(notifier.settingsPath())
	if err != nil {
		t.Fatal(err)
	}
	var tampered NotifySettings
	if err := json.Unmarshal(data, &tampered); err != nil {
		t.Fatal(err)
	}
	tampered.SMTPHost = "attacker.example.com"
	data, err = json.MarshalIndent(tampered, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notifier.settingsPath(), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := notifier.Settings().SMTPPass; got != "" {
		t.Fatalf("tampered endpoint recovered bound password %q", got)
	}
}

func TestTestMailRateLimitsActorRecipientAndGlobalAttempts(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)

	recipientLimited := &Notifier{}
	for index := 0; index < testMailRecipientLimit; index++ {
		if err := recipientLimited.reserveTestMail("actor-a", "same@example.com", now); err != nil {
			t.Fatalf("recipient attempt %d: %v", index+1, err)
		}
	}
	if err := recipientLimited.reserveTestMail("actor-b", "same@example.com", now); !errors.Is(err, ErrMailRateLimited) {
		t.Fatalf("recipient limit error = %v", err)
	}

	actorLimited := &Notifier{}
	for index := 0; index < testMailActorLimit; index++ {
		to := fmt.Sprintf("person-%d@example.com", index)
		if err := actorLimited.reserveTestMail("actor-a", to, now); err != nil {
			t.Fatalf("actor attempt %d: %v", index+1, err)
		}
	}
	if err := actorLimited.reserveTestMail("actor-a", "extra@example.com", now); !errors.Is(err, ErrMailRateLimited) {
		t.Fatalf("actor limit error = %v", err)
	}

	globalLimited := &Notifier{}
	for index := 0; index < testMailGlobalLimit; index++ {
		actor := fmt.Sprintf("actor-%d", index)
		to := fmt.Sprintf("person-%d@example.com", index)
		if err := globalLimited.reserveTestMail(actor, to, now); err != nil {
			t.Fatalf("global attempt %d: %v", index+1, err)
		}
	}
	if err := globalLimited.reserveTestMail("new-actor", "new@example.com", now); !errors.Is(err, ErrMailRateLimited) {
		t.Fatalf("global limit error = %v", err)
	}

	if err := globalLimited.reserveTestMail("new-actor", "new@example.com", now.Add(testMailWindow+time.Second)); err != nil {
		t.Fatalf("expired window remained limited: %v", err)
	}
}
