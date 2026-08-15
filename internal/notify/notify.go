package notify

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"net"
	"net/smtp"
	"nodevas/internal/project"
	secretstore "nodevas/internal/secrets"
	"nodevas/internal/store"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"nodevas/internal/engine"
)

// NotifySettings configures deadline reminder emails. Stored per workspace in
// .vised/notify.json. The server only ever connects out to the configured
// SMTP host; nothing here opens a listening port.
type NotifySettings struct {
	Enabled     bool   `json:"enabled"`
	LeadMinutes int    `json:"leadMinutes"` // how long before a deadline the email is sent
	SMTPHost    string `json:"smtpHost"`
	SMTPPort    int    `json:"smtpPort"`
	SMTPUser    string `json:"smtpUser"`
	SMTPPass    string `json:"smtpPass"`
	From        string `json:"from"`
	DefaultTo   string `json:"defaultTo"` // fallback recipient when the assignee has no email

	// allowPrivateSMTP is set only for a loopback-only server. It never comes
	// from JSON, so a network user cannot turn the SSRF guard off.
	allowPrivateSMTP bool `json:"-"`
}

func DefaultNotifySettings() NotifySettings {
	return NotifySettings{LeadMinutes: 24 * 60, SMTPPort: 587}
}

// Notifier periodically scans every project in the workspace for nodes whose
// deadline falls within the configured lead window and emails a reminder once
// per (project, node, deadline).
type Notifier struct {
	pm *project.ProjectManager

	mu sync.Mutex // guards settings + sent-log files

	testRateMu   sync.Mutex
	testAttempts []testMailAttempt
	mailRateMu   sync.Mutex
	mailAttempts []mailAttempt
}

func NewNotifier(pm *project.ProjectManager) *Notifier {
	return &Notifier{pm: pm}
}

func (n *Notifier) settingsPath() string {
	return filepath.Join(n.pm.Workspace(), store.DataDir, "notify.json")
}

// secretPath is where the SMTP password lives: the app config directory, not
// the project directory.
//
// The project directory is the thing users sync to Google Drive, hand to a
// colleague, or check into git. A plaintext mail password does not belong in
// anything that travels.
func (n *Notifier) secretPath() string {
	sum := sha256.Sum256([]byte(filepath.Clean(n.pm.Workspace())))
	name := "notify-" + hex.EncodeToString(sum[:])[:16] + ".enc"
	return filepath.Join(n.pm.CatalogRoot(), "secrets", name)
}

func (n *Notifier) secretStore() *secretstore.Store {
	return secretstore.New(
		n.secretPath(),
		filepath.Join(n.pm.CatalogRoot(), "secrets", "master.key"),
	)
}

type notifySecrets struct {
	SMTPPass     string `json:"smtpPass"`
	SMTPIdentity string `json:"smtpIdentity"`
}

// smtpCredentialIdentity binds a stored password to the relay identity it was
// entered for. The digest is not an authentication primitive; it prevents a
// workspace settings edit from silently reusing a secret against a new host.
func smtpCredentialIdentity(settings NotifySettings) string {
	endpoint := fmt.Sprintf("%s\x00%d\x00%s",
		strings.ToLower(strings.TrimSpace(settings.SMTPHost)),
		settings.SMTPPort,
		strings.TrimSpace(settings.SMTPUser),
	)
	sum := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(sum[:])
}

func (n *Notifier) loadSecrets(expectedIdentity string) notifySecrets {
	var secrets notifySecrets
	data, err := n.secretStore().Load()
	if err == nil && len(data) > 0 {
		if jsonErr := json.Unmarshal(data, &secrets); jsonErr != nil {
			log.Printf("notify: ignoring malformed encrypted secret: %v", jsonErr)
			return notifySecrets{}
		}
		if secrets.SMTPPass != "" && secrets.SMTPIdentity != expectedIdentity {
			// Older encrypted files had no identity. Requiring one password
			// re-entry is safer than binding that password to workspace metadata
			// that may have arrived from an untrusted sync source.
			log.Printf("notify: SMTP secret does not match the configured endpoint; password re-entry required")
			return notifySecrets{}
		}
		return secrets
	}
	if err != nil {
		log.Printf("notify: cannot decrypt SMTP secret: %v", err)
		return notifySecrets{}
	}

	return secrets
}

func (n *Notifier) saveSecrets(secrets notifySecrets) error {
	data, err := json.MarshalIndent(secrets, "", "  ")
	if err != nil {
		return err
	}
	return n.secretStore().Save(data)
}

func (n *Notifier) sentPath() string {
	return filepath.Join(n.pm.Workspace(), store.DataDir, "notify-sent.json")
}

// loadSettings returns the settings with the SMTP password merged back in
// from the encrypted config-directory store.
func (n *Notifier) loadSettings() NotifySettings {
	settings := DefaultNotifySettings()
	data, err := os.ReadFile(n.settingsPath())
	if err != nil {
		settings.SMTPPass = n.loadSecrets(smtpCredentialIdentity(settings)).SMTPPass
		return settings
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		log.Printf("notify: ignoring malformed %s: %v", n.settingsPath(), err)
		settings = DefaultNotifySettings()
		settings.SMTPPass = n.loadSecrets(smtpCredentialIdentity(settings)).SMTPPass
		return settings
	}
	settings.SMTPPass = ""
	settings.SMTPPass = n.loadSecrets(smtpCredentialIdentity(settings)).SMTPPass
	return settings
}

// writeMetaFile writes small workspace metadata via temp+rename. The watcher
// ignores .vised paths, so no self-write marking is needed.
func writeMetaFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// saveSettings splits the password off into the config directory and writes
// the rest to the project's notify.json.
func (n *Notifier) saveSettings(settings NotifySettings) error {
	secrets := notifySecrets{}
	if settings.SMTPPass != "" {
		secrets.SMTPPass = settings.SMTPPass
		secrets.SMTPIdentity = smtpCredentialIdentity(settings)
	}
	if err := n.saveSecrets(secrets); err != nil {
		return err
	}
	stored := settings
	stored.SMTPPass = ""
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	return writeMetaFile(n.settingsPath(), data)
}

func (n *Notifier) loadSent() map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(n.sentPath())
	if err != nil {
		return out
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]string{}
	}
	return out
}

func (n *Notifier) saveSent(sent map[string]string) error {
	data, err := json.MarshalIndent(sent, "", "  ")
	if err != nil {
		return err
	}
	return writeMetaFile(n.sentPath(), data)
}

// ParseDeadline accepts "2006-01-02T15:04" or "2006-01-02" (end of that day),
// both in the server's local time zone.
func ParseDeadline(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if t, err := time.ParseInLocation("2006-01-02T15:04", value, time.Local); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", value, time.Local); err == nil {
		return t.Add(24 * time.Hour), nil
	}
	return time.Time{}, fmt.Errorf("invalid deadline %q", value)
}

// Run scans once a minute until stop is closed.
func (n *Notifier) Run(stop <-chan struct{}) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			n.scan(time.Now())
		}
	}
}

type runStateFile struct {
	Nodes map[string]struct {
		Status string `json:"status"`
	} `json:"nodes"`
}

func (n *Notifier) scan(now time.Time) {
	n.mu.Lock()
	settings := n.loadSettings()
	n.mu.Unlock()
	if !settings.Enabled || settings.SMTPHost == "" || settings.From == "" {
		return
	}
	lead := time.Duration(settings.LeadMinutes) * time.Minute
	if lead <= 0 {
		lead = 24 * time.Hour
	}

	projects, err := n.pm.List()
	if err != nil {
		log.Printf("notify: list projects: %v", err)
		return
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	sent := n.loadSent()
	changed := false

	for _, project := range projects {
		graphData, err := os.ReadFile(filepath.Join(project.Path, "graph.yaml"))
		if err != nil {
			continue
		}
		graph, err := engine.ParseGraph(graphData)
		if err != nil {
			continue
		}
		emails := make(map[string]string, len(graph.Users))
		for _, user := range graph.Users {
			emails[user.ID] = strings.TrimSpace(user.Email)
		}
		statuses := map[string]string{}
		if stateData, err := os.ReadFile(filepath.Join(project.Path, "run", "state.json")); err == nil {
			var state runStateFile
			if json.Unmarshal(stateData, &state) == nil {
				for id, node := range state.Nodes {
					statuses[id] = node.Status
				}
			}
		}

		for _, node := range graph.Nodes {
			if node == nil {
				continue
			}
			// Deadline source: explicit node.deadline (with time of day) wins;
			// otherwise the timeline's "done" milestone (死線) counts as due at
			// the end of that day.
			deadlineValue := node.Deadline
			if deadlineValue == "" && graph.UI != nil {
				for _, milestone := range graph.UI.Plans[node.ID] {
					if milestone.Status == "done" {
						deadlineValue = milestone.Date
						break
					}
				}
			}
			if deadlineValue == "" {
				continue
			}
			status := statuses[node.ID]
			if status == "done" || status == "skipped" {
				continue
			}
			deadline, err := ParseDeadline(deadlineValue)
			if err != nil {
				continue
			}
			// Remind only inside the pre-deadline window; once the deadline
			// passes, silence beats a stale reminder.
			if now.Before(deadline.Add(-lead)) || !now.Before(deadline) {
				continue
			}
			key := project.Name + "|" + node.ID + "|" + deadlineValue
			if _, done := sent[key]; done {
				continue
			}
			to := emails[node.Assignee]
			if to == "" {
				to = strings.TrimSpace(settings.DefaultTo)
			}
			if to == "" {
				continue
			}
			title := node.Title
			if title == "" {
				title = node.ID
			}
			subject := fmt.Sprintf("[Nodevas] 截止提醒：%s", title)
			body := fmt.Sprintf(
				"專案:%s\r\n節點:%s(%s)\r\n截止:%s\r\n剩餘:約 %s\r\n\r\n此信由 Nodevas 依「截止前 %d 分鐘」設定自動寄出。\r\n",
				project.Label, title, node.ID,
				deadline.Format("2006-01-02 15:04"),
				formatRemaining(deadline.Sub(now)),
				settings.LeadMinutes,
			)
			if err := n.sendMail(settings, to, subject, body); err != nil {
				log.Printf("notify: send %s to %s: %v", key, to, err)
				continue
			}
			log.Printf("notify: sent deadline reminder %s to %s", key, to)
			sent[key] = now.Format(time.RFC3339)
			changed = true
		}
	}

	if changed {
		if err := n.saveSent(sent); err != nil {
			log.Printf("notify: save sent log: %v", err)
		}
	}
}

func formatRemaining(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Minute)
	if h := d / time.Hour; h >= 24 {
		return fmt.Sprintf("%d 天 %d 小時", h/24, h%24)
	} else if h >= 1 {
		return fmt.Sprintf("%d 小時 %d 分鐘", h, d%time.Hour/time.Minute)
	}
	return fmt.Sprintf("%d 分鐘", d/time.Minute)
}

// sendMailFn is indirect so tests can capture outgoing mail.
var sendMailFn = SendMail

// One outbound SMTP conversation at a time, enforced inside SendMail rather
// than around its callers: /api/notify/test reaches it directly, so a limiter
// on the Notifier would leave the one path an authenticated caller can trigger
// on demand unbounded.
var ErrMailBusy = errors.New("another notification email is already being sent")
var ErrMailRateLimited = errors.New("notification test rate limit exceeded")
var smtpSendMu = make(chan struct{}, 1)

const (
	testMailWindow         = 10 * time.Minute
	testMailGlobalLimit    = 10
	testMailActorLimit     = 3
	testMailRecipientLimit = 2
)

type testMailAttempt struct {
	at        time.Time
	actor     string
	recipient string
}

type mailAttempt struct {
	at        time.Time
	recipient string
}

const (
	mailWindow         = time.Hour
	mailGlobalLimit    = 200
	mailRecipientLimit = 20
)

func (n *Notifier) reserveMail(recipient string, now time.Time) error {
	recipient = strings.ToLower(strings.TrimSpace(recipient))
	cutoff := now.Add(-mailWindow)
	n.mailRateMu.Lock()
	defer n.mailRateMu.Unlock()
	kept := n.mailAttempts[:0]
	recipientCount := 0
	for _, attempt := range n.mailAttempts {
		if attempt.at.Before(cutoff) {
			continue
		}
		kept = append(kept, attempt)
		if attempt.recipient == recipient {
			recipientCount++
		}
	}
	n.mailAttempts = kept
	if len(kept) >= mailGlobalLimit || recipientCount >= mailRecipientLimit {
		return ErrMailRateLimited
	}
	n.mailAttempts = append(n.mailAttempts, mailAttempt{at: now, recipient: recipient})
	return nil
}

func (n *Notifier) reserveTestMail(actor, recipient string, now time.Time) error {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		actor = "unknown"
	}
	recipient = strings.ToLower(strings.TrimSpace(recipient))
	cutoff := now.Add(-testMailWindow)

	n.testRateMu.Lock()
	defer n.testRateMu.Unlock()
	kept := n.testAttempts[:0]
	actorCount := 0
	recipientCount := 0
	for _, attempt := range n.testAttempts {
		if attempt.at.Before(cutoff) {
			continue
		}
		kept = append(kept, attempt)
		if attempt.actor == actor {
			actorCount++
		}
		if attempt.recipient == recipient {
			recipientCount++
		}
	}
	n.testAttempts = kept
	if len(kept) >= testMailGlobalLimit || actorCount >= testMailActorLimit ||
		recipientCount >= testMailRecipientLimit {
		return ErrMailRateLimited
	}
	n.testAttempts = append(n.testAttempts, testMailAttempt{
		at: now, actor: actor, recipient: recipient,
	})
	return nil
}

// SendTestMail sends the operator-triggered test message. Unlike scheduled
// reminders this path has an authenticated actor, so both that actor and the
// chosen recipient are throttled in addition to the global cap.
func (n *Notifier) SendTestMail(actor, to, subject, body string) error {
	if err := validateSMTPField("to", to); err != nil {
		return err
	}
	if err := n.reserveTestMail(actor, to, time.Now()); err != nil {
		return err
	}
	return n.sendMail(n.Settings(), to, subject, body)
}

func (n *Notifier) sendMail(settings NotifySettings, to, subject, body string) error {
	if err := n.reserveMail(to, time.Now()); err != nil {
		return err
	}
	settings.allowPrivateSMTP = n.pm == nil || !n.pm.IsRemote()
	return sendMailFn(settings, to, subject, body)
}

func SendMail(settings NotifySettings, to, subject, body string) error {
	select {
	case smtpSendMu <- struct{}{}:
		defer func() { <-smtpSendMu }()
	default:
		return ErrMailBusy
	}
	from := strings.TrimSpace(settings.From)
	host := strings.TrimSpace(settings.SMTPHost)
	user := strings.TrimSpace(settings.SMTPUser)
	if settings.SMTPPort < 1 || settings.SMTPPort > 65535 {
		return errors.New("invalid SMTP port")
	}
	if !settings.allowPrivateSMTP && !allowedSMTPPort(settings.SMTPPort) {
		return errors.New("remote SMTP port must be 25, 465, 587, or 2525")
	}
	if err := validateSMTPField("from", from); err != nil {
		return err
	}
	if err := validateSMTPField("to", to); err != nil {
		return err
	}
	ips, err := resolveSMTPHost(host, settings.allowPrivateSMTP)
	if err != nil {
		return err
	}
	msg := strings.Join([]string{
		"From: " + from,
		"To: " + to,
		"Subject: " + mime.QEncoding.Encode("UTF-8", subject),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Date: " + time.Now().Format(time.RFC1123Z),
		"",
		body,
	}, "\r\n")

	var auth smtp.Auth
	if user != "" {
		auth = smtp.PlainAuth("", user, settings.SMTPPass, host)
	}

	// Port 465 speaks TLS from the first byte; everything else starts in SMTP
	// mode and must upgrade with STARTTLS before any message is delivered.
	if settings.SMTPPort == 465 {
		conn, err := dialSMTPIPs(ips, settings.SMTPPort)
		if err != nil {
			return err
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName: host,
			MinVersion: tls.VersionTLS12,
		})
		if err := tlsConn.Handshake(); err != nil {
			return err
		}
		client, err := smtp.NewClient(tlsConn, host)
		if err != nil {
			return err
		}
		defer client.Close()
		return smtpDeliver(client, auth, from, to, msg)
	}

	conn, err := dialSMTPIPs(ips, settings.SMTPPort)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{
			ServerName: host,
			MinVersion: tls.VersionTLS12,
		}); err != nil {
			return err
		}
	} else if !settings.allowPrivateSMTP {
		// A networked server talks to a relay across a network somebody else
		// can read, so plaintext there is not a choice the operator gets to
		// make. A loopback install is often wired to an internal relay that
		// never learned STARTTLS; refusing to send would break it for a risk
		// that stays on the same machine.
		return errors.New("SMTP server does not advertise STARTTLS")
	} else {
		log.Printf("notify: %s does not offer STARTTLS; sending in the clear", host)
	}
	return smtpDeliver(client, auth, from, to, msg)
}

func validateSMTPField(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("SMTP %s is required", name)
	}
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("SMTP %s contains a line break", name)
	}
	return nil
}

func resolveSMTPHost(host string, allowPrivate bool) ([]net.IP, error) {
	if host == "" || strings.ContainsAny(host, "\r\n\t /\\") {
		return nil, errors.New("invalid SMTP host")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("resolve SMTP host %q: %w", host, err)
	}
	for _, ip := range ips {
		if !allowPrivate && blockedSMTPIP(ip) {
			return nil, fmt.Errorf("SMTP host %q resolves to a private or local address", host)
		}
		if !ip.IsGlobalUnicast() && !(allowPrivate &&
			(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())) {
			return nil, fmt.Errorf("SMTP host %q resolves to a non-unicast address", host)
		}
	}
	return ips, nil
}

func blockedSMTPIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || !ip.IsGlobalUnicast()
}

func allowedSMTPPort(port int) bool {
	switch port {
	case 25, 465, 587, 2525:
		return true
	default:
		return false
	}
}

func dialSMTPIPs(ips []net.IP, port int) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var lastErr error
	for _, ip := range ips {
		conn, err := dialer.Dial("tcp", net.JoinHostPort(ip.String(), fmt.Sprintf("%d", port)))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		return nil, errors.New("no usable SMTP address")
	}
	return nil, lastErr
}

func smtpDeliver(client *smtp.Client, auth smtp.Auth, from, to, msg string) error {
	if auth != nil {
		if ok, _ := client.Extension("AUTH"); !ok {
			return errors.New("SMTP server does not advertise AUTH")
		}
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// ---------- HTTP handlers ----------

// Settings returns the stored settings.
func (n *Notifier) Settings() NotifySettings {
	n.mu.Lock()
	defer n.mu.Unlock()
	settings := n.loadSettings()
	settings.allowPrivateSMTP = n.pm == nil || !n.pm.IsRemote()
	return settings
}

// ErrSMTPPasswordRequired means the caller tried to move an existing password
// to a different relay identity without entering a password for that relay.
var ErrSMTPPasswordRequired = errors.New("SMTP password must be re-entered when host, port, or user changes")

// SaveSettings stores the settings. An empty SMTPPass keeps the stored one only
// when host, port and user are unchanged. A credential can never follow an
// endpoint edit implicitly.
func (n *Notifier) SaveSettings(settings NotifySettings) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	settings.SMTPHost = strings.TrimSpace(settings.SMTPHost)
	settings.SMTPUser = strings.TrimSpace(settings.SMTPUser)
	current := n.loadSettings()
	if settings.SMTPPass == "" {
		if current.SMTPPass != "" &&
			smtpCredentialIdentity(current) != smtpCredentialIdentity(settings) {
			return ErrSMTPPasswordRequired
		}
		settings.SMTPPass = current.SMTPPass
	}
	return n.saveSettings(settings)
}
