package mail

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func validConfig() Config {
	return Config{
		Host:     "smtp.example.com",
		Port:     587,
		Username: "no-reply@example.com",
		Password: "hunter2-hunter2",
		From:     "Nodevas <no-reply@example.com>",
	}
}

func TestValidateRejectsConfigurationsThatCannotDeliverSafely(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{name: "the default empty security means starttls and is accepted", mutate: func(c *Config) {}},
		{name: "an explicit starttls configuration is accepted", mutate: func(c *Config) { c.Security = "starttls" }},
		{name: "implicit tls on the submissions port is accepted", mutate: func(c *Config) { c.Port = 465; c.Security = "implicit" }},
		{name: "a bare from address without a display name is accepted", mutate: func(c *Config) { c.From = "no-reply@example.com" }},
		{name: "an anonymous relay without credentials is accepted", mutate: func(c *Config) { c.Username = ""; c.Password = "" }},

		{name: "a missing host is rejected", mutate: func(c *Config) { c.Host = "" }, wantErr: true},
		{name: "a host of only whitespace is rejected", mutate: func(c *Config) { c.Host = "   " }, wantErr: true},
		{name: "a host carrying a newline is rejected", mutate: func(c *Config) { c.Host = "smtp.example.com\r\n" }, wantErr: true},
		{name: "port zero is rejected", mutate: func(c *Config) { c.Port = 0 }, wantErr: true},
		{name: "a negative port is rejected", mutate: func(c *Config) { c.Port = -1 }, wantErr: true},
		{name: "a port above the sixteen bit range is rejected", mutate: func(c *Config) { c.Port = 70000 }, wantErr: true},
		{name: "a missing from address is rejected", mutate: func(c *Config) { c.From = "" }, wantErr: true},
		{name: "an unparseable from address is rejected", mutate: func(c *Config) { c.From = "Nodevas <not an address>" }, wantErr: true},
		{name: "a username without a password is rejected", mutate: func(c *Config) { c.Password = "" }, wantErr: true},
		{name: "an unknown security value is rejected rather than assumed", mutate: func(c *Config) { c.Security = "ssl" }, wantErr: true},

		// The whole point of the passcode mail is that only the account holder
		// reads it; a plaintext hop to a remote relay hands it to the network.
		{name: "security none against a remote relay is rejected", mutate: func(c *Config) { c.Security = "none" }, wantErr: true},
		{name: "security none against a private but remote relay is rejected", mutate: func(c *Config) { c.Security = "none"; c.Host = "10.0.0.4" }, wantErr: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := validConfig()
			testCase.mutate(&cfg)
			err := cfg.Validate()
			if testCase.wantErr && err == nil {
				t.Fatalf("Validate(%+v) = nil, want an error", cfg)
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("Validate(%+v) = %v, want nil", cfg, err)
			}
		})
	}
}

func TestValidateAllowsSecurityNoneOnlyForALoopbackRelay(t *testing.T) {
	// A relay on this machine, such as a MailHog in development, is never on
	// the wire, so plaintext costs nothing there.
	for _, host := range []string{"127.0.0.1", "::1", "localhost", "LocalHost"} {
		cfg := validConfig()
		cfg.Host = host
		cfg.Port = 1025
		cfg.Security = "none"
		cfg.Username = ""
		cfg.Password = ""
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate with loopback host %q = %v, want nil", host, err)
		}
	}
}

func TestValidateErrorNamesTheUnencryptedHopProblem(t *testing.T) {
	cfg := validConfig()
	cfg.Security = "none"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("a remote plaintext relay was accepted")
	}
	if !strings.Contains(err.Error(), "unencrypted") {
		t.Fatalf("Validate error = %q, want it to name the unencrypted hop", err)
	}
}

func TestSendRejectsHeaderInjectionBeforeOpeningAConnection(t *testing.T) {
	// Point the sender at a port nothing is listening on: if any of these cases
	// reached the network the error would be a dial failure instead.
	deadPort := unusedLoopbackPort(t)
	sender := newLoopbackSender(t, deadPort)

	cases := []struct {
		name string
		to   string
	}{
		{name: "a recipient containing a line feed is rejected", to: "user@example.com\nBcc: attacker@example.com"},
		{name: "a recipient containing a carriage return is rejected", to: "user@example.com\rBcc: attacker@example.com"},
		{name: "a recipient containing a bare control byte is rejected", to: "user@example.com\x00"},
		{name: "a recipient that is not an address is rejected", to: "not an address"},
		{name: "an empty recipient is rejected", to: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := sender.Send(context.Background(), testCase.to, "subject", "body")
			if err == nil {
				t.Fatalf("Send to %q = nil, want an error", testCase.to)
			}
			if strings.Contains(err.Error(), "dial") {
				t.Fatalf("Send to %q opened a connection before validating: %v", testCase.to, err)
			}
		})
	}
}

func TestSubjectIsEncodedAsAWordAndCannotBreakOutOfItsHeader(t *testing.T) {
	sender := newLoopbackSender(t, 1025)
	recipient, err := parseRecipient("user@example.com")
	if err != nil {
		t.Fatal(err)
	}

	chinese := "您的登入驗證碼"
	message := string(sender.compose(recipient, chinese, "body"))
	if strings.Contains(message, chinese) {
		t.Fatalf("subject went out as raw UTF-8 bytes:\n%s", message)
	}
	if !strings.Contains(message, "Subject: =?utf-8?") {
		t.Fatalf("subject was not encoded as an encoded-word:\n%s", message)
	}

	// An unencoded newline in an ASCII subject is the injection that matters:
	// QEncoding would pass it through untouched, so compose must flatten it.
	injected := string(sender.compose(recipient, "Passcode\r\nBcc: attacker@example.com", "body"))
	headers, _, found := strings.Cut(injected, "\r\n\r\n")
	if !found {
		t.Fatalf("composed message has no header/body separator:\n%s", injected)
	}
	// The injected text is inert as long as it stays on the Subject line; what
	// must never happen is a second header line starting with it.
	for _, line := range strings.Split(headers, "\r\n") {
		if strings.HasPrefix(line, "Subject: ") {
			continue
		}
		if strings.HasPrefix(line, "Bcc:") {
			t.Fatalf("a newline in the subject started a new header:\n%s", headers)
		}
	}
}

func TestSendDeliversAWellFormedMessageToALoopbackRelay(t *testing.T) {
	server := startFakeSMTP(t)
	sender := newLoopbackSender(t, server.port)

	// A body line of a single period must survive: net/smtp's DotWriter is
	// responsible for stuffing it, and this asserts that rather than trusting it.
	body := "您的驗證碼是 123456\n.\nlast line"
	if err := sender.Send(context.Background(), "Account Holder <user@example.com>", "驗證碼", body); err != nil {
		t.Fatalf("Send = %v, want nil", err)
	}

	got := <-server.messages
	if got.from != "<no-reply@example.com>" {
		t.Fatalf("MAIL FROM = %q, want the bare envelope address", got.from)
	}
	if len(got.to) != 1 || got.to[0] != "<user@example.com>" {
		t.Fatalf("RCPT TO = %v, want exactly one recipient", got.to)
	}

	headers, payload, found := strings.Cut(got.data, "\r\n\r\n")
	if !found {
		t.Fatalf("delivered message has no header/body separator:\n%s", got.data)
	}
	for _, want := range []string{
		"From: \"Nodevas\" <no-reply@example.com>",
		"To: \"Account Holder\" <user@example.com>",
		"Subject: =?utf-8?",
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"Date: ",
	} {
		if !strings.Contains(headers, want) {
			t.Fatalf("headers missing %q:\n%s", want, headers)
		}
	}
	if !strings.Contains(payload, "您的驗證碼是 123456") {
		t.Fatalf("body missing the passcode text:\n%s", payload)
	}
	if !strings.Contains(payload, "\r\n..\r\n") {
		t.Fatalf("the lone period line was not dot-stuffed by net/smtp:\n%q", payload)
	}
}

func TestSendReturnsImmediatelyWhenTheContextIsAlreadyCancelled(t *testing.T) {
	sender := newLoopbackSender(t, unusedLoopbackPort(t))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sender.Send(ctx, "user@example.com", "subject", "body"); err == nil {
		t.Fatal("Send with a cancelled context = nil, want an error")
	}
}

func TestSendUnblocksWhenTheContextIsCancelledMidConversation(t *testing.T) {
	// A relay that accepts the connection and then says nothing would hang
	// net/smtp forever; the watchdog has to close the socket underneath it.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- conn
		}
	}()

	sender := newLoopbackSender(t, listener.Addr().(*net.TCPAddr).Port)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- sender.Send(ctx, "user@example.com", "subject", "body") }()

	conn := <-accepted
	t.Cleanup(func() { _ = conn.Close() })
	cancel()

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("Send against a silent relay = nil, want an error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Send did not return after its context was cancelled")
	}
}

func TestErrorsNeverCarryTheConfiguredPassword(t *testing.T) {
	// A relay controls its own reply text and can echo an AUTH argument back,
	// so the password is stripped on the way out no matter where it came from.
	sender := newLoopbackSender(t, 1025)
	sender.cfg.Password = "hunter2-hunter2"
	redacted := sender.redact(errors.New("smtp authentication failed: 535 bad hunter2-hunter2"))
	if strings.Contains(redacted.Error(), "hunter2-hunter2") {
		t.Fatalf("error leaked the password: %v", redacted)
	}
	if !strings.Contains(redacted.Error(), "[redacted]") {
		t.Fatalf("redacted error = %q, want a redaction marker", redacted)
	}
}

func newLoopbackSender(t *testing.T, port int) *Sender {
	t.Helper()
	sender, err := New(Config{
		Host:     "127.0.0.1",
		Port:     port,
		From:     "Nodevas <no-reply@example.com>",
		Security: "none",
	})
	if err != nil {
		t.Fatal(err)
	}
	return sender
}

func unusedLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

// received is one message as the fake relay saw it.
type received struct {
	from string
	to   []string
	data string
}

type fakeSMTP struct {
	port     int
	messages chan received
}

// startFakeSMTP speaks just enough ESMTP to accept a single message over a
// plaintext loopback connection. It advertises no STARTTLS and no AUTH, which
// is exactly what a Security "none" sender expects to find.
func startFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	server := &fakeSMTP{
		port:     listener.Addr().(*net.TCPAddr).Port,
		messages: make(chan received, 1),
	}
	go server.serve(listener)
	return server
}

func (f *fakeSMTP) serve(listener net.Listener) {
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	reader := bufio.NewReader(conn)
	reply := func(text string) bool {
		_, writeErr := io.WriteString(conn, text)
		return writeErr == nil
	}
	if !reply("220 fake ESMTP\r\n") {
		return
	}

	var message received
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			return
		}
		command := strings.TrimSpace(line)
		upper := strings.ToUpper(command)
		switch {
		case strings.HasPrefix(upper, "EHLO"):
			reply("250-fake greets you\r\n250 SIZE 1048576\r\n")
		case strings.HasPrefix(upper, "HELO"):
			reply("250 fake\r\n")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			message.from = strings.TrimSpace(command[len("MAIL FROM:"):])
			reply("250 sender ok\r\n")
		case strings.HasPrefix(upper, "RCPT TO:"):
			message.to = append(message.to, strings.TrimSpace(command[len("RCPT TO:"):]))
			reply("250 recipient ok\r\n")
		case upper == "DATA":
			reply("354 end with a lone period\r\n")
			var payload strings.Builder
			for {
				dataLine, dataErr := reader.ReadString('\n')
				if dataErr != nil {
					return
				}
				if dataLine == ".\r\n" {
					break
				}
				payload.WriteString(dataLine)
			}
			message.data = payload.String()
			f.messages <- message
			reply("250 queued\r\n")
		case upper == "QUIT":
			reply("221 bye\r\n")
			return
		default:
			reply("500 unrecognised\r\n")
		}
	}
}
