package logging

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

// requestIDHeader is echoed back so a user reporting a problem can quote it and
// the operator can find the exact record.
const requestIDHeader = "X-Request-Id"

type requestKey struct{}

// requestInfo is what the middleware knows about one request, shared through
// the context with the layers inside it: they can read the id and the user
// agent for their own records, and say who the request turned out to act as,
// which the request's line then carries. Authentication happens deep inside
// the chain, on a context the middleware never sees again, so a pointer is the
// only way that answer can travel back out.
type requestInfo struct {
	id        string
	userAgent string

	mu        sync.Mutex
	userID    string
	userName  string
	userEmail string
}

func requestFrom(ctx context.Context) *requestInfo {
	if ctx == nil {
		return nil
	}
	info, _ := ctx.Value(requestKey{}).(*requestInfo)
	return info
}

// RequestIDFrom returns the id assigned to this request by Middleware, or ""
// when the request did not pass through it.
func RequestIDFrom(ctx context.Context) string {
	if info := requestFrom(ctx); info != nil {
		return info.id
	}
	return ""
}

// SetUser records who the request acts as, once authentication has decided,
// so its line in the request log says. It forgets an address SetUserEmail
// recorded for anybody before. Outside Middleware it does nothing.
func SetUser(ctx context.Context, id, name string) {
	info := requestFrom(ctx)
	if info == nil {
		return
	}
	info.mu.Lock()
	info.userID, info.userName, info.userEmail = id, name, ""
	info.mu.Unlock()
}

// SetUserEmail records the sign-in address of the account SetUser named, when
// whoever authenticated the request read it on the way, for the request's
// line and the audit lines of what it wrote (UserEmailFrom). Outside
// Middleware it does nothing.
func SetUserEmail(ctx context.Context, email string) {
	info := requestFrom(ctx)
	if info == nil {
		return
	}
	info.mu.Lock()
	info.userEmail = email
	info.mu.Unlock()
}

// UserEmailFrom is the address SetUserEmail recorded for the request, or "".
func UserEmailFrom(ctx context.Context) string {
	info := requestFrom(ctx)
	if info == nil {
		return ""
	}
	_, _, email := info.user()
	return email
}

func (info *requestInfo) user() (id, name, email string) {
	info.mu.Lock()
	defer info.mu.Unlock()
	return info.userID, info.userName, info.userEmail
}

// Request reports the request a record was written while serving: its
// "http.request.id", which finds the request's own line, and its
// "user_agent.original". A record written outside any request gets nothing.
func Request(ctx context.Context) slog.Attr {
	info := requestFrom(ctx)
	if info == nil {
		return slog.Attr{}
	}
	attrs := []slog.Attr{slog.String("http.request.id", info.id)}
	if info.userAgent != "" {
		attrs = append(attrs, UserAgent(info.userAgent))
	}
	return inline(attrs...)
}

// Options configures the request logger. Every field is injectable so the
// middleware can be exercised with a plain http.Handler and a recording
// handler, without a listening server.
type Options struct {
	// Logger receives one record per request. Defaults to slog.Default.
	Logger *slog.Logger

	// ClientIP resolves the caller's address. The middleware deliberately does
	// not interpret X-Forwarded-For itself: whether that header can be trusted
	// depends on the deployment's proxy configuration, which auth.ClientIP
	// already knows about. Defaults to the peer address of the connection.
	ClientIP func(*http.Request) string

	// Quiet reports a request whose line is not worth keeping when it
	// succeeds: the container's health probe, or a static asset. It is asked
	// only about successful GET and HEAD requests, so a write, a refusal and a
	// failure are recorded whatever it says. Nil keeps every line.
	Quiet func(*http.Request) bool
}

// Middleware logs one record per request with the default options.
func Middleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return MiddlewareWithOptions(Options{Logger: logger})
}

// MiddlewareWithOptions logs one record per request. It assigns a request id,
// exposes it on the context and on the X-Request-Id response header, and picks
// the record's level from the response status.
func MiddlewareWithOptions(opts Options) func(http.Handler) http.Handler {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	clientIP := opts.ClientIP
	if clientIP == nil {
		clientIP = peerAddress
	}
	quiet := func(r *http.Request, status int) bool {
		return opts.Quiet != nil && status < http.StatusBadRequest &&
			(r.Method == http.MethodGet || r.Method == http.MethodHead) && opts.Quiet(r)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			info := &requestInfo{id: newRequestID(), userAgent: r.UserAgent()}
			w.Header().Set(requestIDHeader, info.id)
			r = r.WithContext(context.WithValue(r.Context(), requestKey{}, info))

			recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			started := time.Now()

			// The record is emitted from a defer so that a panicking handler is
			// still accounted for. Recovering here would hide the panic from
			// the router's recovery middleware, which owns the 500 response, so
			// the panic is re-raised untouched.
			panicked := true
			defer func() {
				status := recorder.status
				if panicked {
					status = http.StatusInternalServerError
				}
				if quiet(r, status) {
					return
				}
				userID, userName, userEmail := info.user()
				// Only the path is logged. A query string is where session
				// tokens, reset links and API keys end up, and the request body
				// of a sign-in endpoint is an address and a passcode; neither
				// belongs in a log that is shipped off the host and retained for
				// months.
				logger.LogAttrs(r.Context(), levelForStatus(status), "http request",
					Event(CategoryWeb, "http.request", ""),
					HTTPRequest(r.Method, r.URL.Path, status),
					ClientIP(clientIP(r)),
					User(userID, userName),
					UserEmail(userEmail),
					UserAgent(r.UserAgent()),
					EventDuration(time.Since(started)),
					slog.String("http.request.id", info.id),
				)
			}()

			next.ServeHTTP(recorder, r)
			panicked = false
		})
	}
}

func levelForStatus(status int) slog.Level {
	switch {
	case status >= 500:
		return slog.LevelError
	case status >= 400:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

// newRequestID returns a random hex id. It falls back to a timestamp-derived id
// rather than failing the request, because an unlogged request is worse than a
// non-unique one.
func newRequestID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(buf[:])
}

func peerAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// statusRecorder remembers the status code so the record can be levelled by it.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (s *statusRecorder) WriteHeader(status int) {
	if !s.written {
		s.status = status
		s.written = true
	}
	s.ResponseWriter.WriteHeader(status)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.written = true
	return s.ResponseWriter.Write(b)
}

// Flush and Hijack are forwarded because the server serves SSE streams and
// upgrades WebSocket connections through this same chain; swallowing those
// interfaces would break both.
func (s *statusRecorder) Flush() {
	if flusher, ok := s.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap lets http.ResponseController reach the connection underneath, so a
// long-lived response (the copilot's turn stream, the 10-minute write
// deadline in cmd/nodevas) can move its write deadline through this wrapper.
func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}

func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("logging: response writer does not support hijacking")
	}
	s.written = true
	return hijacker.Hijack()
}
