package notify

import (
	"bufio"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeSMTP is a minimal ESMTP server that records what actually reached the
// wire. The helper checks in this file assert on that recording rather than on
// SendMail's return value: a guard that rejects a message but has already
// spoken to the relay is not a guard.
type fakeSMTP struct {
	addr string

	mu          sync.Mutex
	connections int
	recipients  []string
	data        string
}

func startFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &fakeSMTP{addr: listener.Addr().String()}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			server.mu.Lock()
			server.connections++
			server.mu.Unlock()
			go server.serve(conn)
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})
	return server
}

func (f *fakeSMTP) serve(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	write := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
	write("220 fake ESMTP")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(command, "EHLO"):
			// Deliberately advertises no STARTTLS: that is the shape of the
			// internal relay the loopback policy has to keep working with.
			write("250-fake")
			write("250 SIZE 10240000")
		case strings.HasPrefix(command, "HELO"):
			write("250 fake")
		case strings.HasPrefix(command, "MAIL FROM"):
			write("250 OK")
		case strings.HasPrefix(command, "RCPT TO"):
			f.mu.Lock()
			f.recipients = append(f.recipients, strings.TrimSpace(line))
			f.mu.Unlock()
			write("250 OK")
		case command == "DATA":
			write("354 send it")
			var body strings.Builder
			for {
				dataLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dataLine, "\r\n") == "." {
					break
				}
				body.WriteString(dataLine)
			}
			f.mu.Lock()
			f.data = body.String()
			f.mu.Unlock()
			write("250 queued")
		case command == "QUIT":
			write("221 bye")
			return
		default:
			write("250 OK")
		}
	}
}

func (f *fakeSMTP) snapshot() (connections int, recipients []string, data string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connections, append([]string(nil), f.recipients...), f.data
}

func (f *fakeSMTP) settings(t *testing.T) NotifySettings {
	t.Helper()
	host, port, err := net.SplitHostPort(f.addr)
	if err != nil {
		t.Fatal(err)
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	settings := DefaultNotifySettings()
	settings.SMTPHost = host
	settings.SMTPPort = number
	settings.From = "vised@example.com"
	settings.allowPrivateSMTP = true
	return settings
}

// The whole point of validating the recipient is that an injected header never
// reaches the relay, so this asserts the server saw no connection at all.
func TestSendMailNeverDialsWithAnInjectedRecipient(t *testing.T) {
	server := startFakeSMTP(t)
	settings := server.settings(t)

	err := SendMail(settings, "victim@example.com\r\nBcc: attacker@example.com", "subject", "body")
	if err == nil {
		t.Fatal("SendMail accepted a recipient carrying a header break")
	}
	if !strings.Contains(err.Error(), "line break") {
		t.Fatalf("unexpected error: %v", err)
	}
	connections, recipients, data := server.snapshot()
	if connections != 0 {
		t.Fatalf("the relay was contacted %d times before the recipient was rejected", connections)
	}
	if len(recipients) != 0 || data != "" {
		t.Fatalf("something reached the wire: recipients=%v data=%q", recipients, data)
	}
}

// The positive control: the same path delivers a legitimate message, and the
// delivered headers carry exactly one recipient.
func TestSendMailDeliversAndCarriesOneRecipient(t *testing.T) {
	server := startFakeSMTP(t)
	settings := server.settings(t)

	if err := SendMail(settings, "ming@example.com", "測試", "body line"); err != nil {
		t.Fatalf("SendMail: %v", err)
	}
	connections, recipients, data := server.snapshot()
	if connections != 1 {
		t.Fatalf("connections = %d, want 1", connections)
	}
	if len(recipients) != 1 || !strings.Contains(recipients[0], "ming@example.com") {
		t.Fatalf("recipients = %v", recipients)
	}
	if count := strings.Count(data, "To:"); count != 1 {
		t.Fatalf("delivered message carries %d To headers:\n%s", count, data)
	}
	if strings.Contains(strings.ToLower(data), "bcc:") {
		t.Fatalf("delivered message carries a Bcc header:\n%s", data)
	}
	// The subject travels encoded, so the raw UTF-8 must not appear as-is.
	if !strings.Contains(data, "Subject: =?UTF-8?") {
		t.Fatalf("subject was not encoded:\n%s", data)
	}
}

// The loopback relay above is exactly what a networked server must not reach.
// Which guard fires first is not the property worth pinning down — that no
// connection happens is, so this asserts the outcome rather than the message.
//
// The STARTTLS requirement itself sits behind these two and cannot be reached
// from a unit test: getting there needs a host that resolves to a public
// address on one of the four approved ports. Its counterpart, the loopback
// policy delivering to a relay that offers no STARTTLS, is covered by
// TestSendMailDeliversAndCarriesOneRecipient.
func TestSendMailRefusesALoopbackRelayWhenNetworked(t *testing.T) {
	server := startFakeSMTP(t)
	networked := server.settings(t)
	networked.allowPrivateSMTP = false

	err := SendMail(networked, "ming@example.com", "subject", "body")
	if err == nil {
		t.Fatal("a networked server delivered to a loopback relay")
	}
	if connections, _, _ := server.snapshot(); connections != 0 {
		t.Fatalf("the relay was contacted %d times before the send was refused", connections)
	}

	// On an approved port the private-address check is what stops it, so the
	// port allowlist is not the only thing standing in the way.
	networked.SMTPPort = 587
	err = SendMail(networked, "ming@example.com", "subject", "body")
	if err == nil || !strings.Contains(err.Error(), "private or local address") {
		t.Fatalf("unexpected error on an approved port: %v", err)
	}
}
