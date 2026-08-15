// Package mail sends the one-time passcodes the sign-in flow mails to account
// holders. A passcode is a bearer credential for the lifetime of a login, so
// this package refuses any configuration that would put one on the wire in
// clear text and never offers a switch to skip certificate verification.
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// dialTimeout bounds a Send whose caller passed a context without a deadline,
// so a black-holed relay cannot pin a request goroutine forever.
const dialTimeout = 15 * time.Second

// Security modes an operator may configure.
const (
	SecurityStartTLS = "starttls"
	SecurityImplicit = "implicit"
	SecurityNone     = "none"
)

// Config is what an operator supplies to enable outgoing mail.
type Config struct {
	Host     string // SMTP host, e.g. smtp.fastmail.com
	Port     int    // 587 for STARTTLS, 465 for implicit TLS
	Username string
	Password string
	From     string // envelope and header From, e.g. "Nodevas <no-reply@example.com>"
	// Security is "starttls" (default), "implicit", or "none".
	Security string
}

// Validate reports why the config cannot be used, or nil.
func (c Config) Validate() error {
	if containsControl(c.Host) {
		return errors.New("smtp host contains a control character")
	}
	host := strings.TrimSpace(c.Host)
	if host == "" {
		return errors.New("smtp host is required")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("smtp port %d is out of range", c.Port)
	}
	if strings.TrimSpace(c.From) == "" {
		return errors.New("smtp from address is required")
	}
	if _, err := mail.ParseAddress(c.From); err != nil {
		return fmt.Errorf("smtp from address %q is not a valid address: %w", c.From, err)
	}
	if c.Username != "" && c.Password == "" {
		return errors.New("smtp password is required when a username is set")
	}
	switch security(c.Security) {
	case SecurityStartTLS, SecurityImplicit:
	case SecurityNone:
		// Without TLS the passcode, and any SMTP AUTH credentials, travel in
		// clear text. Only a relay on this machine is out of reach of the
		// network, so only that is allowed.
		if !isLoopback(host) {
			return fmt.Errorf("smtp security %q sends the passcode unencrypted and is only allowed for a loopback relay, not %q", SecurityNone, host)
		}
	default:
		return fmt.Errorf("unknown smtp security %q, want %q, %q or %q", c.Security, SecurityStartTLS, SecurityImplicit, SecurityNone)
	}
	return nil
}

// Sender delivers mail over SMTP. The zero value is not usable; call New.
type Sender struct {
	cfg  Config
	from *mail.Address
}

func New(cfg Config) (*Sender, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	from, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return nil, fmt.Errorf("smtp from address %q is not a valid address: %w", cfg.From, err)
	}
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.Security = security(cfg.Security)
	return &Sender{cfg: cfg, from: from}, nil
}

// Send delivers one plain-text message. It must respect ctx cancellation and
// must not block past it.
func (s *Sender) Send(ctx context.Context, to, subject, body string) error {
	// The recipient and subject come from elsewhere in the program, so they are
	// checked before anything is dialled: a bad address must never cost a
	// connection, and a header break must never reach the relay.
	recipient, err := parseRecipient(to)
	if err != nil {
		return err
	}
	message := s.compose(recipient, subject, body)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("smtp send cancelled: %w", err)
	}
	return s.redact(s.deliver(ctx, recipient.Address, message))
}

// parseRecipient rejects anything that could turn one recipient into two, or
// smuggle a header past compose.
func parseRecipient(to string) (*mail.Address, error) {
	if strings.TrimSpace(to) == "" {
		return nil, errors.New("recipient address is required")
	}
	if containsControl(to) {
		return nil, fmt.Errorf("recipient address contains a control character")
	}
	address, err := mail.ParseAddress(to)
	if err != nil {
		return nil, fmt.Errorf("recipient address %q is not a valid address: %w", to, err)
	}
	if containsControl(address.Address) || containsControl(address.Name) {
		return nil, errors.New("recipient address contains a control character")
	}
	return address, nil
}

// compose builds the RFC 5322 message. Only the headers are at risk of
// injection: the body is delivered through net/smtp's DotWriter, which
// dot-stuffs leading periods and terminates the payload itself, so a body line
// of "." cannot end the DATA phase early.
func (s *Sender) compose(recipient *mail.Address, subject, body string) []byte {
	var builder strings.Builder
	writeHeader := func(name, value string) {
		builder.WriteString(name)
		builder.WriteString(": ")
		builder.WriteString(value)
		builder.WriteString("\r\n")
	}
	writeHeader("Date", time.Now().Format(time.RFC1123Z))
	writeHeader("From", s.from.String())
	writeHeader("To", recipient.String())
	writeHeader("Subject", encodeSubject(subject))
	writeHeader("MIME-Version", "1.0")
	writeHeader("Content-Type", "text/plain; charset=utf-8")
	builder.WriteString("\r\n")
	builder.WriteString(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n"))
	return []byte(builder.String())
}

// encodeSubject folds the subject into an encoded-word. QEncoding passes plain
// ASCII through untouched, so control characters are flattened first: an
// unencoded newline would otherwise start a header of the caller's choosing.
func encodeSubject(subject string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, subject)
	return mime.QEncoding.Encode("utf-8", cleaned)
}

func (s *Sender) deliver(ctx context.Context, recipient string, message []byte) error {
	address := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	dialer := net.Dialer{Timeout: dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("dial smtp %s: %w", address, err)
	}
	defer func() { _ = conn.Close() }()

	// net/smtp is context-unaware, so cancellation is enforced by closing the
	// socket underneath it. conn is local to this call, so the watchdog can
	// never reach a later Send's connection, and finished is closed on every
	// return path so the goroutine cannot outlive us.
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-finished:
		}
	}()

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(dialTimeout)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("set smtp deadline: %w", err)
	}

	client, err := s.connect(conn)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	if err := s.authenticate(client); err != nil {
		return err
	}
	if err := client.Mail(s.from.Address); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := client.Rcpt(recipient); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := writer.Write(message); err != nil {
		return fmt.Errorf("write smtp message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close smtp message: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("smtp quit: %w", err)
	}
	return nil
}

// connect brings the dialled socket up to the configured security level.
func (s *Sender) connect(conn net.Conn) (*smtp.Client, error) {
	if s.cfg.Security == SecurityImplicit {
		tlsConn := tls.Client(conn, s.tlsConfig())
		client, err := smtp.NewClient(tlsConn, s.cfg.Host)
		if err != nil {
			return nil, fmt.Errorf("smtp over tls handshake: %w", err)
		}
		return client, nil
	}

	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return nil, fmt.Errorf("smtp greeting: %w", err)
	}
	if s.cfg.Security == SecurityNone {
		return client, nil
	}
	// Opportunistic STARTTLS is no protection: anyone who can rewrite the EHLO
	// response can delete the advertisement and watch the passcode go by. A
	// missing advertisement is therefore an error, not a downgrade.
	if ok, _ := client.Extension("STARTTLS"); !ok {
		_ = client.Close()
		return nil, fmt.Errorf("smtp server %s does not advertise STARTTLS; refusing to send unencrypted", s.cfg.Host)
	}
	if err := client.StartTLS(s.tlsConfig()); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("smtp starttls: %w", err)
	}
	return client, nil
}

// tlsConfig is deliberately the only TLS configuration in this package: there
// is no operator switch for InsecureSkipVerify.
func (s *Sender) tlsConfig() *tls.Config {
	return &tls.Config{
		ServerName: s.cfg.Host,
		MinVersion: tls.VersionTLS12,
	}
}

func (s *Sender) authenticate(client *smtp.Client) error {
	if s.cfg.Username == "" {
		return nil
	}
	if ok, _ := client.Extension("AUTH"); !ok {
		return fmt.Errorf("smtp server %s does not offer authentication", s.cfg.Host)
	}
	// smtp.PlainAuth itself refuses to hand the password to an unencrypted
	// connection unless the host is localhost, which matches Validate.
	auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("smtp authentication failed: %w", err)
	}
	return nil
}

// redact is the last gate before an error reaches a log or an HTTP response. A
// relay is free to echo a failed AUTH argument back at us, so the password is
// stripped even at the cost of the error's wrapping chain.
func (s *Sender) redact(err error) error {
	if err == nil || s.cfg.Password == "" {
		return err
	}
	text := err.Error()
	if !strings.Contains(text, s.cfg.Password) {
		return err
	}
	return errors.New(strings.ReplaceAll(text, s.cfg.Password, "[redacted]"))
}

func security(value string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "" {
		return SecurityStartTLS
	}
	return trimmed
}

func containsControl(value string) bool {
	return strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// isLoopback reports whether a host can only be reached from this machine.
func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
