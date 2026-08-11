package logging

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
