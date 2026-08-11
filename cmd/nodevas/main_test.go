package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"nodevas/internal/auth"
	"os"
	"strings"
	"testing"
	"time"
)

type deadlineWriter struct {
	header     http.Header
	readCalls  []time.Time
	writeCalls []time.Time
	statusCode int
}

func (writer *deadlineWriter) Header() http.Header {
	if writer.header == nil {
		writer.header = make(http.Header)
	}
	return writer.header
}

func (writer *deadlineWriter) Write(data []byte) (int, error) { return len(data), nil }
func (writer *deadlineWriter) WriteHeader(statusCode int)     { writer.statusCode = statusCode }

func (writer *deadlineWriter) SetReadDeadline(deadline time.Time) error {
	writer.readCalls = append(writer.readCalls, deadline)
	return nil
}

func (writer *deadlineWriter) SetWriteDeadline(deadline time.Time) error {
	writer.writeCalls = append(writer.writeCalls, deadline)
	return nil
}

func TestProtectHTTPTransportSetsAndClearsOrdinaryDeadlines(t *testing.T) {
	handler := protectHTTPTransport(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	writer := &deadlineWriter{}
	handler.ServeHTTP(writer, httptest.NewRequest(http.MethodPost, "/api/auth/login", nil))
	if len(writer.readCalls) != 2 || writer.readCalls[0].IsZero() || !writer.readCalls[1].IsZero() {
		t.Fatalf("read deadlines = %v, want set then clear", writer.readCalls)
	}
	if len(writer.writeCalls) != 2 || writer.writeCalls[0].IsZero() || !writer.writeCalls[1].IsZero() {
		t.Fatalf("write deadlines = %v, want set then clear", writer.writeCalls)
	}
}

func TestProtectHTTPTransportLeavesWebSocketDeadlinesToHub(t *testing.T) {
	handler := protectHTTPTransport(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	writer := &deadlineWriter{}
	handler.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/ws", nil))
	if len(writer.readCalls) != 0 || len(writer.writeCalls) != 0 {
		t.Fatalf("websocket inherited HTTP deadlines: read=%v write=%v", writer.readCalls, writer.writeCalls)
	}
}

func TestProtectHTTPTransportCapsLoginBody(t *testing.T) {
	var readErr error
	handler := protectHTTPTransport(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		_, readErr = io.ReadAll(request.Body)
	}))
	writer := &deadlineWriter{}
	body := strings.NewReader(strings.Repeat("x", auth.MaxLoginBodyBytes+1))
	handler.ServeHTTP(writer, httptest.NewRequest(http.MethodPost, "/api/auth/login", body))
	if readErr == nil {
		t.Fatal("oversized login body was accepted")
	}
}

// withStdin replaces os.Stdin with a pipe holding content, so the stdin path
// can be exercised without a terminal.
func withStdin(t *testing.T, content string) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = original
		_ = reader.Close()
	})
	go func() {
		_, _ = io.WriteString(writer, content)
		_ = writer.Close()
	}()
}

func TestPasswordFromStdinIsTakenWholeAndTrimmedOnce(t *testing.T) {
	// `echo` appends a newline; `printf %s` does not. Both must produce the
	// same secret, and a password containing spaces must survive intact.
	for _, piped := range []string{"pa ss:word", "pa ss:word\n", "pa ss:word\r\n"} {
		withStdin(t, piped)
		secret, err := readPassword("", true)
		if err != nil {
			t.Fatalf("%q: %v", piped, err)
		}
		if secret != "pa ss:word" {
			t.Fatalf("%q read back as %q", piped, secret)
		}
	}
}

func TestPasswordStdinRefusesTheFlagAndAnEmptyPipe(t *testing.T) {
	withStdin(t, "")
	if _, err := readPassword("", true); err == nil {
		t.Fatal("an empty stdin was accepted as a password")
	}
	withStdin(t, "from-stdin\n")
	if _, err := readPassword("from-flag", true); err == nil {
		t.Fatal("--password and --password-stdin were accepted together")
	}
}

func TestPasswordStdinRejectsAnOversizedPipe(t *testing.T) {
	withStdin(t, strings.Repeat("x", maxPasswordBytes+1))
	if _, err := readPassword("", true); err == nil {
		t.Fatal("an oversized stdin password was accepted")
	}
}

// The deprecated flag must keep working: the point of the warning is that
// existing scripts are not broken by it.
func TestDeprecatedPasswordFlagStillWins(t *testing.T) {
	t.Setenv("NODEVAS_PASSWORD", "from-env")
	secret, err := readPassword("from-flag", false)
	if err != nil || secret != "from-flag" {
		t.Fatalf("readPassword = %q, %v", secret, err)
	}
	secret, err = readPassword("", false)
	if err != nil || secret != "from-env" {
		t.Fatalf("environment fallback = %q, %v", secret, err)
	}
}

// Every case here is a refusal to start. They are the last thing between a
// mistyped command line and a workspace on the open network, so each one is
// asserted on its own rather than through a single "it errors" check.
func TestValidateServeFlagsRefusesUnsafeDeployments(t *testing.T) {
	if err := validateServeFlags("0.0.0.0", 5666, nil, "cert.pem", "key.pem", false, false); err == nil {
		t.Fatal("a wildcard bind without --hostname was accepted")
	}
	if err := validateServeFlags("0.0.0.0", 5666, []string{"nodes.example.com"}, "", "", false, false); err == nil {
		t.Fatal("a networked plaintext listener was accepted without --allow-plaintext")
	}
	if err := validateServeFlags("127.0.0.1", 5666, nil, "cert.pem", "", false, false); err == nil {
		t.Fatal("--tls-cert was accepted without --tls-key")
	}
	if err := validateServeFlags("127.0.0.1", 0, nil, "", "", false, false); err == nil {
		t.Fatal("port 0 was accepted")
	}

	// The safe shapes: loopback needs nothing, a named wildcard with TLS is a
	// proper deployment, and a loopback listener behind a proxy that terminates
	// TLS is plaintext on purpose.
	if err := validateServeFlags("127.0.0.1", 5666, nil, "", "", false, false); err != nil {
		t.Fatalf("loopback plaintext refused: %v", err)
	}
	if err := validateServeFlags("0.0.0.0", 443, []string{"nodes.example.com"}, "cert.pem", "key.pem", false, false); err != nil {
		t.Fatalf("a named TLS deployment was refused: %v", err)
	}
	if err := validateServeFlags("127.0.0.1", 5666, []string{"nodes.example.com"}, "", "", true, false); err != nil {
		t.Fatalf("a same-host reverse proxy deployment was refused: %v", err)
	}
}

func TestServeArgValueSupportsBothFlagFormsAndLastValueWins(t *testing.T) {
	value, found, err := serveArgValue([]string{
		"-project", "first",
		"--project=second",
		"--config", "server.yaml",
	}, "project")
	if err != nil || !found || value != "second" {
		t.Fatalf("serveArgValue(project) = %q, %v, %v", value, found, err)
	}
	value, found, err = serveArgValue([]string{"--config=server.yaml"}, "config")
	if err != nil || !found || value != "server.yaml" {
		t.Fatalf("serveArgValue(config) = %q, %v, %v", value, found, err)
	}
}

func TestServeArgValueRejectsMissingValue(t *testing.T) {
	if _, _, err := serveArgValue([]string{"--config"}, "config"); err == nil {
		t.Fatal("missing config value was accepted")
	}
}

func TestWildcardAndLoopbackClassification(t *testing.T) {
	if !isLoopbackHost("127.0.0.1") || !isLoopbackHost("::1") || isLoopbackHost("0.0.0.0") {
		t.Fatal("loopback classification is unsafe")
	}
	if !isWildcardHost("") || !isWildcardHost("0.0.0.0") || !isWildcardHost("::") || isWildcardHost("127.0.0.1") {
		t.Fatal("wildcard classification is unsafe")
	}
	if !isRemoteDeployment("127.0.0.1", true) || isRemoteDeployment("127.0.0.1", false) {
		t.Fatal("same-host reverse proxy did not force remote authentication")
	}
}
