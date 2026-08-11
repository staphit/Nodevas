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
	"time"
)

// requestIDHeader is echoed back so a user reporting a problem can quote it and
// the operator can find the exact record.
const requestIDHeader = "X-Request-Id"

type requestIDKey struct{}

// RequestIDFrom returns the id assigned to this request by Middleware, or ""
// when the request did not pass through it.
func RequestIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// Options configures the request logger. Both fields are injectable so the
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

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := newRequestID()
			w.Header().Set(requestIDHeader, id)
			r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id))

			recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			started := time.Now()

			// The record is emitted from a defer so that a panicking handler is
			// still accounted for. Recovering here would hide the panic from
			// gin.Recovery, which owns the 500 response, so the panic is
			// re-raised untouched.
			panicked := true
			defer func() {
				status := recorder.status
				if panicked {
					status = http.StatusInternalServerError
				}
				// Only the path is logged. A query string is where session
				// tokens, reset links and API keys end up, and the request body
				// of the passcode endpoint is a PIN; neither belongs in a log
				// that is shipped off the host and retained for months.
				logger.LogAttrs(r.Context(), levelForStatus(status), "http request",
					HTTPRequest(r.Method, r.URL.Path, status),
					ClientIP(clientIP(r)),
					UserAgent(r.UserAgent()),
					EventDuration(time.Since(started).Nanoseconds()),
					slog.String("http.request.id", id),
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

func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("logging: response writer does not support hijacking")
	}
	s.written = true
	return hijacker.Hijack()
}
