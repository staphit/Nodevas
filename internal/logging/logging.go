// Package logging configures the structured logger used by the server.
//
// Every record is emitted as one JSON object per line on the writer given to
// Setup (stderr in production) so that a log shipper can forward it to
// Elasticsearch without a parsing or transformation step. Field names follow
// the Elastic Common Schema (ECS) — "@timestamp", "log.level", "message",
// "client.ip", "error.message" and so on — because Kibana's built-in views,
// alerting rules and the ECS index template all key off those exact names. A
// bespoke naming scheme would work only after someone hand-wrote an ingest
// pipeline, and that pipeline would be one more thing to keep in sync.
//
// The server logs sign-in attempts, passcode requests and mobile pairing, so
// records routinely travel next to credentials. Redaction is therefore a
// property of the handler (see Redacted) rather than a rule call sites are
// trusted to remember.
package logging

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"strings"
)

// Redacted is the placeholder substituted for any credential-shaped value. It
// is exported so a call site can pass it deliberately for a value the handler
// cannot recognise from its key alone.
const Redacted = "[redacted]"

// ecsTimeFormat is RFC3339 with milliseconds, the resolution ECS uses for
// "@timestamp". Second resolution is too coarse to order the records of a
// single request; nanoseconds are noise in Kibana.
const ecsTimeFormat = "2006-01-02T15:04:05.000Z07:00"

// Config selects the shape of the log stream. The zero value is valid and
// yields info-level JSON without source locations.
type Config struct {
	Level   string // debug|info|warn|error, default info
	Format  string // json|text, default json
	Source  bool   // include file:line
	Service string // service.name, stamped on every record
}

// Setup builds the logger, installs it as slog.Default and points the standard
// library log package at it. It returns an error only for an unusable Config;
// a caller that cannot log is better off failing at startup than running blind.
func Setup(w io.Writer, cfg Config) (*slog.Logger, error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}
	opts := &slog.HandlerOptions{
		Level:       level,
		AddSource:   cfg.Source,
		ReplaceAttr: replaceAttr,
	}

	var handler slog.Handler
	switch strings.ToLower(strings.TrimSpace(cfg.Format)) {
	case "", "json":
		handler = slog.NewJSONHandler(w, opts)
	case "text":
		// For someone watching a terminal. It keeps the ECS names so that a
		// field learned from one format is the same field in the other.
		handler = slog.NewTextHandler(w, opts)
	default:
		return nil, fmt.Errorf("logging: unknown format %q, want json or text", cfg.Format)
	}

	if cfg.Service != "" {
		handler = handler.WithAttrs([]slog.Attr{slog.String("service.name", cfg.Service)})
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)

	// Bridge the standard log package into the same handler. Much of this
	// codebase still calls log.Printf; without the bridge those lines would
	// bypass the pipeline entirely and reach the operator's log tool as
	// unparseable prose interleaved with the JSON stream. The flags are cleared
	// because the handler already stamps "@timestamp" and a second timestamp
	// inside "message" would defeat any query on it.
	//
	// This is a compatibility bridge, not the intended API: new code should take a
	// *slog.Logger and attach ECS attributes with the helpers below.
	log.SetFlags(0)
	log.SetPrefix("")
	log.SetOutput(slog.NewLogLogger(handler, slog.LevelInfo).Writer())

	return logger, nil
}

func parseLevel(name string) (slog.Level, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return slog.LevelInfo, nil
	}
	var level slog.Level
	// UnmarshalText accepts the names case-insensitively.
	if err := level.UnmarshalText([]byte(trimmed)); err != nil {
		return 0, fmt.Errorf("logging: unknown level %q, want debug, info, warn or error", name)
	}
	return level, nil
}

// replaceAttr renames slog's built-in keys to their ECS equivalents and blanks
// anything that looks like a credential.
func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	// The built-in keys only carry their special meaning at the top level; a
	// user attribute named "msg" inside a group is just an attribute.
	if len(groups) == 0 {
		switch a.Key {
		case slog.TimeKey:
			if a.Value.Kind() == slog.KindTime {
				return slog.String("@timestamp", a.Value.Time().Format(ecsTimeFormat))
			}
		case slog.LevelKey:
			return slog.String("log.level", strings.ToLower(a.Value.String()))
		case slog.MessageKey:
			return slog.String("message", a.Value.String())
		case slog.SourceKey:
			if source, ok := a.Value.Any().(*slog.Source); ok {
				return inline(
					slog.String("log.origin.file.name", source.File),
					slog.Int("log.origin.file.line", source.Line),
				)
			}
		}
	}
	if isSecretKey(a.Key) {
		return slog.String(a.Key, Redacted)
	}
	return a
}

// secretKeys are the whole-key matches that must never reach the log stream.
var secretKeys = map[string]bool{
	"pin":           true,
	"otp":           true,
	"passcode":      true,
	"password":      true,
	"secret":        true,
	"token":         true,
	"cookie":        true,
	"authorization": true,
	"api_key":       true,
	"apikey":        true,
}

func isSecretKey(key string) bool {
	lowered := strings.ToLower(key)
	if secretKeys[lowered] {
		return true
	}
	// Compound names such as "refresh_token" or "hmac_secret" are the common
	// way a credential sneaks in under a key nobody thought to enumerate.
	return strings.HasSuffix(lowered, "_token") || strings.HasSuffix(lowered, "_secret")
}

// inline returns a group with an empty key, which slog's handlers splice into
// the surrounding object. ECS field names are dotted paths rather than nested
// objects, so a helper that contributes several fields cannot use a real group.
func inline(attrs ...slog.Attr) slog.Attr {
	return slog.Attr{Value: slog.GroupValue(attrs...)}
}

// Err renders an error as ECS "error.message" and "error.type". The type comes
// from %T so that a sentinel and a wrapped I/O failure can be told apart in a
// dashboard without matching on message text, which changes far more often.
func Err(err error) slog.Attr {
	if err == nil {
		return slog.Attr{}
	}
	return inline(
		slog.String("error.message", err.Error()),
		slog.String("error.type", fmt.Sprintf("%T", err)),
	)
}

// HTTPRequest reports the request line and its outcome. The path must already
// be stripped of its query string; see the comment in Middleware.
func HTTPRequest(method, path string, statusCode int) slog.Attr {
	attrs := []slog.Attr{
		slog.String("http.request.method", method),
		slog.String("url.path", path),
	}
	if statusCode > 0 {
		attrs = append(attrs, slog.Int("http.response.status_code", statusCode))
	}
	return inline(attrs...)
}

// ClientIP reports the caller's address as ECS "client.ip".
func ClientIP(ip string) slog.Attr {
	return slog.String("client.ip", ip)
}

// User reports the acting account. Either field may be empty.
func User(id, name string) slog.Attr {
	var attrs []slog.Attr
	if id != "" {
		attrs = append(attrs, slog.String("user.id", id))
	}
	if name != "" {
		attrs = append(attrs, slog.String("user.name", name))
	}
	return inline(attrs...)
}

// EventAction names what happened, e.g. "sign-in" or "passcode-request". It is
// the field an operator filters on to answer "who tried to sign in".
func EventAction(action string) slog.Attr {
	return slog.String("event.action", action)
}

// UserAgent reports the raw User-Agent header as ECS "user_agent.original".
func UserAgent(value string) slog.Attr {
	return slog.String("user_agent.original", value)
}

// EventDuration reports how long something took. ECS stores durations in
// nanoseconds.
func EventDuration(nanoseconds int64) slog.Attr {
	return slog.Int64("event.duration", nanoseconds)
}
