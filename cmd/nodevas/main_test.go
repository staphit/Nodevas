package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"nodevas/internal/auth"
	"nodevas/internal/db"
	"nodevas/internal/testpg"
	"os"
	"path/filepath"
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
	handler.ServeHTTP(writer, httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{}`)))
	if len(writer.readCalls) != 2 || writer.readCalls[0].IsZero() || !writer.readCalls[1].IsZero() {
		t.Fatalf("read deadlines = %v, want set then clear", writer.readCalls)
	}
	if len(writer.writeCalls) != 2 || writer.writeCalls[0].IsZero() || !writer.writeCalls[1].IsZero() {
		t.Fatalf("write deadlines = %v, want set then clear", writer.writeCalls)
	}

	// Without a body there is nothing to read, so no read deadline; the
	// response is still bounded.
	writer = &deadlineWriter{}
	handler.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/api/projects/export", nil))
	if len(writer.readCalls) != 0 {
		t.Fatalf("read deadlines = %v on a request without a body, want none", writer.readCalls)
	}
	if len(writer.writeCalls) != 2 || writer.writeCalls[0].IsZero() || !writer.writeCalls[1].IsZero() {
		t.Fatalf("write deadlines = %v, want set then clear", writer.writeCalls)
	}
}

// The read deadline still does its job for a body: a client that sends part
// of one and stalls does not hold the handler for longer than the timeout.
func TestProtectHTTPTransportCutsOffAStalledBody(t *testing.T) {
	shortReadTimeout(t, 200*time.Millisecond)
	readErr := make(chan error, 1)
	server := httptest.NewServer(protectHTTPTransport(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		_, err := io.ReadAll(request.Body)
		readErr <- err
	})))
	defer server.Close()

	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Ten bytes promised, three sent, and then nothing.
	if _, err := io.WriteString(conn, "POST /api/nodes HTTP/1.1\r\nHost: 127.0.0.1\r\n"+
		"Content-Type: application/json\r\nContent-Length: 10\r\n\r\n{\"a"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readErr:
		if err == nil {
			t.Fatal("a stalled body read to the end")
		}
	case <-time.After(10 * requestReadTimeout):
		t.Fatal("a stalled body held the handler past the read timeout")
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

// shortReadTimeout shortens requestReadTimeout for one test.
func shortReadTimeout(t *testing.T, timeout time.Duration) {
	t.Helper()
	original := requestReadTimeout
	requestReadTimeout = timeout
	t.Cleanup(func() { requestReadTimeout = original })
}

// A download is a request without a body, and nothing is read from it while
// the response streams. The read deadline must not cancel it: an export over a
// slow link takes longer than that deadline. Served over a real connection,
// because that is where Go's server reads in the background and a deadline on
// that read is what used to end the request.
func TestProtectHTTPTransportLetsABodilessRequestOutliveTheReadTimeout(t *testing.T) {
	shortReadTimeout(t, 200*time.Millisecond)
	outlived := make(chan error, 1)
	handler := protectHTTPTransport(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		done := time.NewTimer(5 * requestReadTimeout)
		defer done.Stop()
		tick := time.NewTicker(requestReadTimeout / 4)
		defer tick.Stop()
		for {
			select {
			case <-request.Context().Done():
				outlived <- request.Context().Err()
				return
			case <-tick.C:
				_, _ = writer.Write([]byte("x"))
				http.NewResponseController(writer).Flush()
			case <-done.C:
				outlived <- nil
				return
			}
		}
	}))
	server := httptest.NewServer(handler)
	defer server.Close()

	response, err := http.Get(server.URL + "/api/projects/export")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if err := <-outlived; err != nil {
		t.Fatalf("a GET was cancelled while it streamed (%v): the read deadline ended a request with no body", err)
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

func TestSecretEnvironmentReadsFileAndRejectsAmbiguousInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "smtp-password")
	if err := os.WriteFile(path, []byte("from-file\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODEVAS_SMTP_PASSWORD_FILE", path)
	secret, err := secretEnvironment("NODEVAS_SMTP_PASSWORD")
	if err != nil || secret != "from-file" {
		t.Fatalf("secretEnvironment file = %q, %v", secret, err)
	}

	t.Setenv("NODEVAS_SMTP_PASSWORD", "from-environment")
	if _, err := secretEnvironment("NODEVAS_SMTP_PASSWORD"); err == nil {
		t.Fatal("direct and file-backed SMTP passwords were accepted together")
	}
}

// Every case here is a refusal to start. They are the last thing between a
// mistyped command line and a workspace on the open network, so each one is
// asserted on its own rather than through a single "it errors" check.
func TestValidateServeFlagsRefusesUnsafeDeployments(t *testing.T) {
	if err := validateServeFlags("0.0.0.0", 5666, nil, "cert.pem", "key.pem", false, false, false); err == nil {
		t.Fatal("a wildcard bind without --hostname was accepted")
	}
	if err := validateServeFlags("0.0.0.0", 5666, []string{"nodes.example.com"}, "", "", false, false, false); err == nil {
		t.Fatal("a networked plaintext listener was accepted without --allow-plaintext")
	}
	if err := validateServeFlags("127.0.0.1", 5666, nil, "cert.pem", "", false, false, false); err == nil {
		t.Fatal("--tls-cert was accepted without --tls-key")
	}
	if err := validateServeFlags("127.0.0.1", 0, nil, "", "", false, false, false); err == nil {
		t.Fatal("port 0 was accepted")
	}

	// The safe shapes: loopback needs nothing, a named wildcard with TLS is a
	// proper deployment, and a loopback listener behind a proxy that terminates
	// TLS is plaintext on purpose.
	if err := validateServeFlags("127.0.0.1", 5666, nil, "", "", false, false, false); err != nil {
		t.Fatalf("loopback plaintext refused: %v", err)
	}
	if err := validateServeFlags("0.0.0.0", 443, []string{"nodes.example.com"}, "cert.pem", "key.pem", false, false, false); err != nil {
		t.Fatalf("a named TLS deployment was refused: %v", err)
	}
	if err := validateServeFlags("127.0.0.1", 5666, []string{"nodes.example.com"}, "", "", true, false, false); err != nil {
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
	if !isRemoteDeployment("127.0.0.1", true, nil, false) || isRemoteDeployment("127.0.0.1", false, nil, false) {
		t.Fatal("same-host reverse proxy did not force remote authentication")
	}
}

// A loopback listener is only private while nothing on this machine forwards
// to it. A public --hostname, or Cloudflare Access in front, says something
// does: a tunnel or a proxy brings other people's requests in over loopback,
// and a hub without accounts would take each of them for the person at the
// keyboard.
func TestALoopbackHubReachedFromElsewhereIsRemote(t *testing.T) {
	if !isRemoteDeployment("127.0.0.1", false, []string{"nodevas.example.com"}, false) {
		t.Error("a loopback hub answering on a public --hostname was classed as local")
	}
	if !isRemoteDeployment("127.0.0.1", false, []string{"localhost", "nodevas.example.com"}, false) {
		t.Error("a public name after a loopback one was not noticed")
	}
	if !isRemoteDeployment("127.0.0.1", false, nil, true) {
		t.Error("a loopback hub behind Cloudflare Access was classed as local")
	}
	// Loopback names add nobody.
	if isRemoteDeployment("127.0.0.1", false, []string{"localhost", "127.0.0.1", "::1"}, false) {
		t.Error("loopback --hostname values made a standalone hub remote")
	}
}

// Without --behind-proxy such a hub would not trust the proxy's scheme
// header and would get no accounts, so serve refuses it and says what to pass.
func TestValidateServeFlagsRefusesAForwardedLoopbackHubWithoutAProxy(t *testing.T) {
	err := validateServeFlags("127.0.0.1", 5666, []string{"nodevas.example.com"}, "", "", false, false, false)
	if err == nil {
		t.Fatal("a public --hostname on a loopback listener was accepted without --behind-proxy")
	}
	if !strings.Contains(err.Error(), "--behind-proxy") {
		t.Errorf("the refusal %q does not say to pass --behind-proxy", err)
	}
	err = validateServeFlags("127.0.0.1", 5666, nil, "", "", false, false, true)
	if err == nil {
		t.Fatal("Cloudflare Access on a loopback listener was accepted without --behind-proxy")
	}
	if !strings.Contains(err.Error(), "--behind-proxy") {
		t.Errorf("the refusal %q does not say to pass --behind-proxy", err)
	}

	// The shapes that stay: behind the proxy it names, and loopback names on
	// a standalone hub.
	if err := validateServeFlags("127.0.0.1", 5666, []string{"nodevas.example.com"}, "", "", true, false, true); err != nil {
		t.Fatalf("a same-host proxy with Access was refused: %v", err)
	}
	if err := validateServeFlags("127.0.0.1", 5666, []string{"localhost"}, "", "", false, false, false); err != nil {
		t.Fatalf("a loopback --hostname on a standalone hub was refused: %v", err)
	}
}

// serve itself applies the refusal: it ends before it opens the workspace's
// database, which this environment does not have.
func TestServeRefusesAPublicHostnameOnLoopbackWithoutAProxy(t *testing.T) {
	root := t.TempDir()
	out, code := runNodevas(t, isolatedEnv(t, "NODEVAS_REQUIRE_DATABASE_URL=1", db.URLEnv+"=", db.URLFileEnv+"="), "",
		"serve", "--project", root, "--port", fmt.Sprint(freePort(t)), "--no-runner", "--hostname", "nodevas.example.com")
	if code == 0 {
		t.Fatalf("serve exited 0:\n%s", out)
	}
	if !strings.Contains(out, "--behind-proxy") {
		t.Fatalf("serve went on past a public --hostname on loopback without --behind-proxy:\n%s", out)
	}
}

// Every sign-in endpoint gets the same body cap, not just the one that was
// there first.
func TestProtectHTTPTransportCapsEverySignInBody(t *testing.T) {
	for path := range auth.SignInPaths {
		var readErr error
		handler := protectHTTPTransport(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
			_, readErr = io.ReadAll(request.Body)
		}))
		body := strings.NewReader(strings.Repeat("x", auth.MaxLoginBodyBytes+1))
		handler.ServeHTTP(&deadlineWriter{}, httptest.NewRequest(http.MethodPost, path, body))
		if readErr == nil {
			t.Fatalf("oversized body at %s was accepted", path)
		}
	}
}

// `user list` is read by scripts — the deploy bootstrap cuts the first field —
// so its shape is pinned: name, role and address, tab-separated, with the
// address empty rather than missing for an account that cannot sign in.
func TestUserListPrintsNameRoleAndEmail(t *testing.T) {
	users := auth.NewUserStoreDB(testpg.New(t))
	ctx := context.Background()
	if err := users.AddWithEmail(ctx, "ann", "correct-horse-battery", "", "Ann@Example.test"); err != nil {
		t.Fatal(err)
	}
	if err := users.Add(ctx, "bob", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	printAccounts(&out, users.Records(ctx))
	want := "ann\tadmin\tAnn@Example.test\nbob\tmember\t\n"
	if out.String() != want {
		t.Fatalf("user list = %q, want %q", out.String(), want)
	}
}
