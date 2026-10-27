package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// newTestMiddleware wires the middleware to a buffer and a fixed client IP so
// the tests need neither a listener nor a proxy configuration.
func newTestMiddleware(t *testing.T, buf *bytes.Buffer) func(http.Handler) http.Handler {
	t.Helper()
	logger, err := Setup(buf, Config{Level: "debug"})
	if err != nil {
		t.Fatal(err)
	}
	return MiddlewareWithOptions(Options{
		Logger:   logger,
		ClientIP: func(*http.Request) string { return "198.51.100.4" },
	})
}

func serve(handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestMiddlewareAssignsARequestIDAndReturnsItToTheCaller(t *testing.T) {
	var buf bytes.Buffer
	var seen string
	handler := newTestMiddleware(t, &buf)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r.Context())
	}))

	rec := serve(handler, httptest.NewRequest(http.MethodGet, "/api/projects", nil))

	header := rec.Header().Get("X-Request-Id")
	if header == "" {
		t.Fatal("no X-Request-Id was returned; a user cannot quote an id they never saw")
	}
	if seen != header {
		t.Fatalf("context id %q differs from the header id %q", seen, header)
	}
	record := decodeOne(t, &buf)
	if record["http.request.id"] != header {
		t.Fatalf("http.request.id = %v, want %q", record["http.request.id"], header)
	}
	if RequestIDFrom(httptest.NewRequest(http.MethodGet, "/", nil).Context()) != "" {
		t.Fatal("a request that skipped the middleware reported an id")
	}
}

func TestMiddlewareLogsTheRequestWithECSFields(t *testing.T) {
	var buf bytes.Buffer
	handler := newTestMiddleware(t, &buf)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Millisecond)
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/projects", nil)
	req.Header.Set("User-Agent", "nodevas-ios/1.0")
	serve(handler, req)

	record := decodeOne(t, &buf)
	want := map[string]any{
		"event.category":            "web",
		"event.action":              "http.request",
		"http.request.method":       "POST",
		"url.path":                  "/api/projects",
		"http.response.status_code": float64(http.StatusCreated),
		"client.ip":                 "198.51.100.4",
		"user_agent.original":       "nodevas-ios/1.0",
		"log.level":                 "info",
	}
	for key, value := range want {
		if record[key] != value {
			t.Fatalf("%s = %v, want %v", key, record[key], value)
		}
	}
	duration, ok := record["event.duration"].(float64)
	if !ok || duration <= 0 {
		t.Fatalf("event.duration = %v, want a positive number of nanoseconds", record["event.duration"])
	}
	// ECS durations are nanoseconds: a millisecond of sleep must not read as 1.
	if duration < float64(time.Millisecond) {
		t.Fatalf("event.duration = %v ns, too small to be nanoseconds", duration)
	}
	// The same duration in milliseconds, for the dashboards that read that.
	millis, ok := record["event.duration_ms"].(float64)
	if !ok || millis < 1 || millis > duration/float64(time.Millisecond)+0.001 {
		t.Fatalf("event.duration_ms = %v, want %v ns in milliseconds", record["event.duration_ms"], duration)
	}
	// Nobody signed this request in, so it names nobody.
	for _, key := range []string{"user.id", "user.name", "user.email", "event.outcome"} {
		if _, present := record[key]; present {
			t.Fatalf("%s = %v on an anonymous request", key, record[key])
		}
	}
}

func TestMiddlewareNamesTheUserAnInnerLayerSignedIn(t *testing.T) {
	var buf bytes.Buffer
	handler := newTestMiddleware(t, &buf)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Authentication runs deep inside the chain, on a context of its own.
		inner := r.WithContext(context.WithValue(r.Context(), struct{ name string }{"inner"}, true))
		SetUser(inner.Context(), "u-1", "ann")
	}))
	serve(handler, httptest.NewRequest(http.MethodGet, "/api/graph", nil))

	record := decodeOne(t, &buf)
	if record["user.id"] != "u-1" || record["user.name"] != "ann" {
		t.Fatalf("user = %v / %v, want u-1 / ann", record["user.id"], record["user.name"])
	}
	if _, present := record["user.email"]; present {
		t.Fatalf("user.email = %v, but nobody said it", record["user.email"])
	}
	// Outside the middleware there is no line to name anybody on.
	SetUser(context.Background(), "u-2", "bea")
	SetUserEmail(context.Background(), "bea@example.test")
	if email := UserEmailFrom(context.Background()); email != "" {
		t.Fatalf("UserEmailFrom outside a request = %q", email)
	}
}

func TestMiddlewareNamesTheAddressOfTheUserSignedIn(t *testing.T) {
	var buf bytes.Buffer
	var seen string
	handler := newTestMiddleware(t, &buf)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// An address said for somebody else is forgotten with them.
		SetUser(r.Context(), "u-2", "bea")
		SetUserEmail(r.Context(), "bea@example.test")
		SetUser(r.Context(), "u-1", "ann")
		if email := UserEmailFrom(r.Context()); email != "" {
			t.Errorf("ann's address = %q before anybody said it", email)
		}
		SetUserEmail(r.Context(), "ann@example.test")
		seen = UserEmailFrom(r.Context())
	}))
	serve(handler, httptest.NewRequest(http.MethodPost, "/api/nodes", nil))

	record := decodeOne(t, &buf)
	if record["user.id"] != "u-1" || record["user.name"] != "ann" || record["user.email"] != "ann@example.test" {
		t.Fatalf("user = %v / %v / %v, want u-1 / ann / ann@example.test", record["user.id"], record["user.name"], record["user.email"])
	}
	if seen != "ann@example.test" {
		t.Fatalf("UserEmailFrom = %q, want ann's address", seen)
	}
}

func TestMiddlewareLeavesOutOnlyQuietSuccesses(t *testing.T) {
	var buf bytes.Buffer
	logger, err := Setup(&buf, Config{})
	if err != nil {
		t.Fatal(err)
	}
	status := http.StatusOK
	handler := MiddlewareWithOptions(Options{
		Logger: logger,
		Quiet:  func(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/assets/") },
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		serve(handler, httptest.NewRequest(method, "/assets/index-abc.js", nil))
	}
	if buf.Len() != 0 {
		t.Fatalf("a successful quiet request was logged: %q", buf.String())
	}
	// A write and a failure are recorded whatever Quiet says.
	serve(handler, httptest.NewRequest(http.MethodPost, "/assets/index-abc.js", nil))
	if record := decodeOne(t, &buf); record["http.request.method"] != "POST" {
		t.Fatalf("record = %v, want the POST", record)
	}
	buf.Reset()
	status = http.StatusNotFound
	serve(handler, httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil))
	if record := decodeOne(t, &buf); record["http.response.status_code"] != float64(http.StatusNotFound) {
		t.Fatalf("record = %v, want the 404", record)
	}
	buf.Reset()
	status = http.StatusOK
	serve(handler, httptest.NewRequest(http.MethodGet, "/api/graph", nil))
	if record := decodeOne(t, &buf); record["url.path"] != "/api/graph" {
		t.Fatalf("record = %v, want the API read", record)
	}
}

func TestRecordsWrittenDuringARequestCanNameIt(t *testing.T) {
	var buf bytes.Buffer
	handler := newTestMiddleware(t, &buf)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Default().LogAttrs(r.Context(), slog.LevelInfo, "inside", Request(r.Context()))
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 test")
	rec := serve(handler, req)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("wrote %d lines, want the handler's and the request's: %q", len(lines), buf.String())
	}
	var inside map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &inside); err != nil {
		t.Fatal(err)
	}
	if inside["http.request.id"] != rec.Header().Get("X-Request-Id") || inside["user_agent.original"] != "Mozilla/5.0 test" {
		t.Fatalf("record = %v, want the request's id and user agent", inside)
	}
	if attr := Request(context.Background()); !attr.Equal(slog.Attr{}) {
		t.Fatalf("Request outside a request = %v, want nothing", attr)
	}
}

func TestTheUserAgentIsCutAndCannotForgeALine(t *testing.T) {
	var buf bytes.Buffer
	handler := newTestMiddleware(t, &buf)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/api/graph", nil)
	forged := "evil\n{\"event.action\":\"auth.signin\",\"user.email\":\"x@example.test\"}\"" + strings.Repeat("é", 300)
	req.Header.Set("User-Agent", forged)
	serve(handler, req)

	// One line, still: the encoder escapes what the client wrote.
	record := decodeOne(t, &buf)
	if record["event.action"] != "http.request" {
		t.Fatalf("event.action = %v, the user agent forged a field", record["event.action"])
	}
	if _, present := record["user.email"]; present {
		t.Fatal("the user agent forged user.email")
	}
	agent, _ := record["user_agent.original"].(string)
	if utf8.RuneCountInString(agent) != 256 || !strings.HasPrefix(forged, agent) {
		t.Fatalf("user_agent.original has %d characters, want the first 256 of the header", utf8.RuneCountInString(agent))
	}
}

func TestMiddlewareLogsThePathWithoutTheQueryString(t *testing.T) {
	var buf bytes.Buffer
	handler := newTestMiddleware(t, &buf)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	// Query strings are where tokens end up; the log must not become a place
	// they are retained.
	serve(handler, httptest.NewRequest(http.MethodGet, "/api/share?token=super-secret&pin=4321", nil))

	if strings.Contains(buf.String(), "super-secret") || strings.Contains(buf.String(), "4321") {
		t.Fatalf("the query string reached the log: %q", buf.String())
	}
	if record := decodeOne(t, &buf); record["url.path"] != "/api/share" {
		t.Fatalf("url.path = %v, want /api/share", record["url.path"])
	}
}

func TestMiddlewareLevelsTheRecordByResponseStatus(t *testing.T) {
	for _, tc := range []struct {
		status int
		level  string
	}{
		{http.StatusOK, "info"},
		{http.StatusUnauthorized, "warn"},
		{http.StatusNotFound, "warn"},
		{http.StatusBadGateway, "error"},
	} {
		var buf bytes.Buffer
		status := tc.status
		handler := newTestMiddleware(t, &buf)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		serve(handler, httptest.NewRequest(http.MethodGet, "/api/session", nil))

		if record := decodeOne(t, &buf); record["log.level"] != tc.level {
			t.Fatalf("status %d logged at %v, want %s", tc.status, record["log.level"], tc.level)
		}
	}
}

func TestMiddlewareLogsAPanickingHandlerAndRepanics(t *testing.T) {
	var buf bytes.Buffer
	handler := newTestMiddleware(t, &buf)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("handler exploded")
	}))

	func() {
		// Recovery belongs to gin.Recovery, which owns the response; the
		// middleware must let the panic through untouched.
		defer func() {
			recovered := recover()
			if recovered == nil {
				t.Fatal("the middleware swallowed the panic")
			}
			if recovered != "handler exploded" {
				t.Fatalf("recovered %v, want the original panic value", recovered)
			}
		}()
		serve(handler, httptest.NewRequest(http.MethodGet, "/api/render", nil))
	}()

	record := decodeOne(t, &buf)
	if record["http.response.status_code"] != float64(http.StatusInternalServerError) {
		t.Fatalf("http.response.status_code = %v, want 500", record["http.response.status_code"])
	}
	if record["log.level"] != "error" {
		t.Fatalf("log.level = %v, want error", record["log.level"])
	}
}

func TestMiddlewareFallsBackToThePeerAddressWhenNoResolverIsGiven(t *testing.T) {
	var buf bytes.Buffer
	logger, err := Setup(&buf, Config{})
	if err != nil {
		t.Fatal(err)
	}
	handler := Middleware(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.9:51234"
	// A forwarding header must be ignored here: only the trusted-proxy-aware
	// resolver the caller injects may be believed.
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	serve(handler, req)

	if record := decodeOne(t, &buf); record["client.ip"] != "192.0.2.9" {
		t.Fatalf("client.ip = %v, want the peer address 192.0.2.9", record["client.ip"])
	}
}
