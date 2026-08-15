// Package audit is the workspace's record of who did what: sign-ins, writes,
// and anything else an operator would need to reconstruct after the fact.
//
// It exists as its own package, backed by the database, because the
// questions asked of an audit trail are asked after something has gone wrong,
// and they cross projects: "what did this account touch last Tuesday" cannot be
// answered by a file that only one project's store can open, that has no index,
// and that a project deletion takes with it. It also gives server-wide events —
// a sign-in has no project — somewhere to live at all.
//
// Everything here is written so an operator can read it in DB Browser for
// SQLite without this program: timestamps are RFC3339 UTC text, and Detail is
// JSON text that is sanitised on the way in so a row is never a wall of escaped
// control characters, and never a credential.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"nodevas/internal/db"
)

// Event is one recorded action.
type Event struct {
	At        time.Time
	Project   string // "" for server-wide events such as a sign-in
	ActorID   string
	ActorName string
	Action    string // e.g. "node.update", "auth.signin", "auth.passcode.request"
	Target    string
	ClientIP  string
	Detail    map[string]any // free-form, stored as JSON text
}

// Paging bounds. The default keeps an unbounded caller from pulling a
// multi-year trail into memory; the maximum is a hard ceiling because the only
// caller that would ask for more is one with a bug or one being probed.
const (
	DefaultLimit = 200
	MaxLimit     = 1000
	// MaxOffset is how deep paging may go. SQLite reaches OFFSET n by counting
	// out n rows and throwing them away, so an unbounded offset is a full table
	// scan a caller can order by typing a large number into a query string.
	MaxOffset = 100_000
)

// Filter string bounds. A filter value is a bound parameter and cannot inject,
// but ActionPrefix becomes a LIKE pattern that SQLite evaluates once per
// candidate row, so a megabyte of it is a megabyte of comparison per row. These
// are refused rather than truncated: a truncated filter silently answers a
// different question than the one that was asked.
const (
	maxFilterBytes = 256
	// A project filter holds a filesystem root, which is longer than anything
	// else a filter carries. Well past PATH_MAX, so no real root reaches it.
	maxProjectBytes = 4 << 10
)

// Filter selects a slice of the trail. The zero value means "the most recent
// DefaultLimit events, everywhere".
type Filter struct {
	// Project matches one project's events. Empty means every project, which
	// is why selecting only the server-wide events (project = '') needs its own
	// flag rather than an empty string.
	Project    string
	ServerWide bool

	ActorID string

	// ActionPrefix matches on the dotted namespace, so "auth." finds every
	// sign-in event without the caller listing them.
	ActionPrefix string

	// Since is inclusive, Until exclusive. Both are compared as RFC3339 UTC
	// text, the same form the column holds, so a bound with sub-second
	// precision is rounded down to the second it falls in.
	Since time.Time
	Until time.Time

	Limit int

	// Offset pages backwards through the result. An id cursor would be
	// steadier under concurrent writes, but Event deliberately has no id field
	// — the trail is read by humans, not joined against — and new events arrive
	// at the newest end, which is the end a pager has already passed.
	Offset int
}

const (
	HealthHealthy      = "healthy"
	HealthDegraded     = "degraded"
	HealthUnconfigured = "unconfigured"
)

var (
	// ErrWritesDegraded means a fallback has been observed and no later audit
	// database write has proved that persistence is available again.
	ErrWritesDegraded = errors.New("audit database writes have not recovered")
	// ErrFallbackEventsChanged is the compare-and-swap failure returned when
	// an operator tries to acknowledge a snapshot while a newer fallback has
	// arrived. They must reconcile the new event before retrying.
	ErrFallbackEventsChanged = errors.New("audit fallback count changed")
)

// Health separates current database write availability from outstanding
// reconciliation. Overall Status remains degraded after writes recover until
// an operator acknowledges the exact fallback counter they reconciled.
type Health struct {
	Status                     string     `json:"status"`
	WriteStatus                string     `json:"writeStatus"`
	FallbackEvents             uint64     `json:"fallbackEvents"`
	AcknowledgedFallbackEvents uint64     `json:"acknowledgedFallbackEvents"`
	UnreconciledEvents         uint64     `json:"unreconciledEvents"`
	LastFailureAt              *time.Time `json:"lastFailureAt,omitempty"`
	LastError                  string     `json:"lastError,omitempty"`
}

// Store reads and writes the trail.
type Store struct {
	db *db.DB

	healthMu                   sync.RWMutex
	writesDegraded             bool
	fallbackEvents             uint64
	acknowledgedFallbackEvents uint64
	lastFailureAt              time.Time
	lastError                  string
}

// Health reports both whether RecordOrLog most recently reached audit_events
// and whether every fallback observed by this process has been reconciled. It
// is deliberately independent of Query: an operator still needs this signal
// when the audit database itself is the component that cannot answer.
func (s *Store) Health() Health {
	if s == nil || s.db == nil {
		return Health{Status: HealthUnconfigured, WriteStatus: HealthUnconfigured}
	}
	s.healthMu.RLock()
	health := s.healthLocked()
	s.healthMu.RUnlock()
	return health
}

// healthLocked returns one coherent snapshot while either health lock is held.
func (s *Store) healthLocked() Health {
	writeStatus := HealthHealthy
	if s.writesDegraded {
		writeStatus = HealthDegraded
	}
	unreconciled := s.fallbackEvents - s.acknowledgedFallbackEvents
	status := writeStatus
	if unreconciled > 0 {
		status = HealthDegraded
	}
	health := Health{
		Status:                     status,
		WriteStatus:                writeStatus,
		FallbackEvents:             s.fallbackEvents,
		AcknowledgedFallbackEvents: s.acknowledgedFallbackEvents,
		UnreconciledEvents:         unreconciled,
		LastError:                  s.lastError,
	}
	if !s.lastFailureAt.IsZero() {
		at := s.lastFailureAt
		health.LastFailureAt = &at
	}
	return health
}

func (s *Store) markFallback(at time.Time, err error) Health {
	if s == nil {
		return Health{Status: HealthUnconfigured, WriteStatus: HealthUnconfigured}
	}
	s.healthMu.Lock()
	s.writesDegraded = true
	s.fallbackEvents++
	s.lastFailureAt = at
	s.lastError = err.Error()
	health := s.healthLocked()
	s.healthMu.Unlock()
	return health
}

func (s *Store) markRecovered() (Health, bool) {
	if s == nil {
		return Health{Status: HealthUnconfigured, WriteStatus: HealthUnconfigured}, false
	}
	s.healthMu.Lock()
	recovered := s.writesDegraded
	s.writesDegraded = false
	health := s.healthLocked()
	s.healthMu.Unlock()
	return health, recovered
}

// AcknowledgeFallbacks records that an operator has reconciled every fallback
// through expectedFallbackEvents. The expected counter is a compare-and-swap:
// a fallback that races with reconciliation cannot be silently acknowledged.
// Current database writes must already have recovered; acknowledgement is not
// a health override and cannot hide an active persistence failure.
func (s *Store) AcknowledgeFallbacks(expectedFallbackEvents uint64) (Health, error) {
	if s == nil || s.db == nil {
		return Health{Status: HealthUnconfigured, WriteStatus: HealthUnconfigured},
			errors.New("audit store is not configured")
	}
	s.healthMu.Lock()
	if s.writesDegraded {
		health := s.healthLocked()
		s.healthMu.Unlock()
		return health, ErrWritesDegraded
	}
	if expectedFallbackEvents != s.fallbackEvents {
		health := s.healthLocked()
		s.healthMu.Unlock()
		return health, ErrFallbackEventsChanged
	}
	changed := s.acknowledgedFallbackEvents != expectedFallbackEvents
	s.acknowledgedFallbackEvents = expectedFallbackEvents
	health := s.healthLocked()
	s.healthMu.Unlock()
	if changed {
		slog.Info("audit fallback reconciliation acknowledged",
			"event.code", "audit.reconciliation_acknowledged",
			"audit.health", health.Status,
			"audit.fallback_count", health.FallbackEvents,
		)
	}
	return health, nil
}

// New returns a store over an open workspace database.
func New(database *db.DB) *Store { return &Store{db: database} }

// Record writes one event.
//
// It returns the error rather than swallowing it, and the caller decides what
// to do about the request that caused it. That split is the point: the audit
// trail is what a breach is reconstructed from, so a lost write has to be
// visible somewhere, but refusing a user's save because a logging table is
// unavailable turns an accounting problem into an outage. Callers on a request
// path should therefore use RecordOrLog, which keeps the request alive and
// keeps the event — in the process log — instead of dropping it silently.
func (s *Store) Record(ctx context.Context, event Event) error {
	if s == nil || s.db == nil {
		return errors.New("audit store is not configured")
	}
	if event.Action == "" {
		return errors.New("audit event has no action")
	}
	detail, err := encodeDetail(event.Detail)
	if err != nil {
		return fmt.Errorf("encode audit detail: %w", err)
	}
	// Every column but detail was previously stored exactly as handed over.
	// None of them is a value this package chooses: the request middleware puts
	// the request path into Action and the node the client named into Target, so
	// a row could be as large as the request that produced it, and could carry
	// control characters and invalid UTF-8 into the table an operator reads.
	_, err = s.db.ExecContext(ctx, `
INSERT INTO audit_events (at, project, actor_id, actor_name, action, target, client_ip, detail)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		formatTime(event.At), cleanColumn(event.Project), cleanString(event.ActorID),
		cleanString(event.ActorName), cleanString(event.Action), cleanString(event.Target),
		cleanString(event.ClientIP), detail)
	if err != nil {
		return fmt.Errorf("record audit event %q: %w", event.Action, err)
	}
	return nil
}

// RecordOrLog records the event and, when the database write fails, writes the
// whole event to the process log at error level instead. It also exposes a
// degraded write-health signal until a later audit database write succeeds.
// Overall health remains degraded until the fallback counter is acknowledged.
//
// This is the failure mode the package is designed around: the request that
// produced the event still succeeds, but the event is not lost — it lands in
// the operator's log, tagged, with every field it would have had in the table,
// so the trail can be reconciled by hand. Silently returning is not an option
// a security trail can offer.
func (s *Store) RecordOrLog(ctx context.Context, event Event) {
	err := s.Record(ctx, event)
	if err == nil {
		health, recovered := s.markRecovered()
		if recovered {
			slog.InfoContext(ctx, "audit database writes recovered; reconcile prior fallback events",
				"event.code", "audit.persistence_recovered",
				"audit.health", health.Status,
				"audit.write_health", health.WriteStatus,
				"audit.fallback_count", health.FallbackEvents,
				"audit.unreconciled_count", health.UnreconciledEvents,
			)
		}
		return
	}
	failureAt := time.Now().UTC()
	health := s.markFallback(failureAt, err)
	detail, _ := encodeDetail(event.Detail)
	slog.ErrorContext(ctx, "audit write failed, event preserved in this log only",
		"event.code", "audit.persistence_fallback",
		"audit.health", health.Status,
		"audit.write_health", health.WriteStatus,
		"audit.fallback_count", health.FallbackEvents,
		"audit.unreconciled_count", health.UnreconciledEvents,
		"audit_failure_at", formatTime(failureAt),
		"error", err,
		"audit_at", formatTime(event.At),
		"audit_project", event.Project,
		"audit_actor_id", event.ActorID,
		"audit_actor_name", event.ActorName,
		"audit_action", event.Action,
		"audit_target", event.Target,
		"audit_client_ip", event.ClientIP,
		"audit_detail", detail,
	)
}

// Query returns matching events, newest first.
func (s *Store) Query(ctx context.Context, filter Filter) ([]Event, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("audit store is not configured")
	}
	if err := checkFilterBounds(filter); err != nil {
		return nil, err
	}
	where := []string{"1 = 1"}
	args := []any{}
	switch {
	case filter.ServerWide:
		where = append(where, "project = ''")
	case filter.Project != "":
		where = append(where, "project = ?")
		args = append(args, filter.Project)
	}
	if filter.ActorID != "" {
		where = append(where, "actor_id = ?")
		args = append(args, filter.ActorID)
	}
	if filter.ActionPrefix != "" {
		// ESCAPE, because an action prefix is caller input and a bare _ in
		// LIKE is a wildcard that would quietly widen the match.
		where = append(where, `action LIKE ? ESCAPE '\'`)
		args = append(args, escapeLike(filter.ActionPrefix)+"%")
	}
	if !filter.Since.IsZero() {
		where = append(where, "at >= ?")
		args = append(args, formatTime(filter.Since))
	}
	if !filter.Until.IsZero() {
		where = append(where, "at < ?")
		args = append(args, formatTime(filter.Until))
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > MaxOffset {
		offset = MaxOffset
	}
	args = append(args, limit, offset)

	// id breaks ties: several events can share a second, and without it the
	// order within that second is whatever the query planner felt like, which
	// makes paging skip and repeat rows.
	query := `
SELECT at, project, actor_id, actor_name, action, target, client_ip, detail
FROM audit_events
WHERE ` + strings.Join(where, " AND ") + `
ORDER BY at DESC, id DESC
LIMIT ? OFFSET ?`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query audit events: %w", err)
	}
	defer rows.Close()

	events := []Event{}
	for rows.Next() {
		var (
			event Event
			at    string
			text  string
		)
		if err := rows.Scan(&at, &event.Project, &event.ActorID, &event.ActorName,
			&event.Action, &event.Target, &event.ClientIP, &text); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}
		// A row an operator hand-edited into an unparseable timestamp or
		// detail is still evidence: keep the row and lose only the field.
		if parsed, perr := time.Parse(time.RFC3339, at); perr == nil {
			event.At = parsed
		}
		event.Detail = decodeDetail(text)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read audit events: %w", err)
	}
	return events, nil
}

// formatTime is the one place the column's format is decided, so Record, Query
// bounds and Import cannot drift apart and stop comparing as text.
func formatTime(at time.Time) string {
	if at.IsZero() {
		return db.Now()
	}
	return at.UTC().Format(time.RFC3339)
}

// checkFilterBounds refuses a filter value no honest caller sends. ActorID and
// ActionPrefix are bound parameters, so this is not about injection: it is
// about what one request may cost the server, since a LIKE pattern is compared
// against every row the planner has to look at.
func checkFilterBounds(filter Filter) error {
	if len(filter.Project) > maxProjectBytes {
		return errors.New("audit filter project is too long")
	}
	if len(filter.ActorID) > maxFilterBytes {
		return errors.New("audit filter actor is too long")
	}
	if len(filter.ActionPrefix) > maxFilterBytes {
		return errors.New("audit filter action is too long")
	}
	return nil
}

func escapeLike(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(s)
}

// Detail limits. The cap is not about storage — it is about a detail cell an
// operator can actually read in a table view, and about one oversized request
// body not becoming an unreadable row.
const (
	maxDetailBytes = 8 << 10
	maxValueBytes  = 1 << 10
	maxDetailDepth = 8
)

// Reserved keys. They start with an underscore so they cannot be confused with
// a caller's own field, and they are recorded rather than implied: an operator
// looking at a redacted row must be able to tell redaction from absence.
const (
	droppedKey   = "_dropped_keys"
	truncatedKey = "_truncated_keys"
)

// credentialWords are the tokens that mark a field as a credential. Matching is
// per token of the key, so "clientSecret" and "pin_hash" are dropped while
// "pinned_at" is not, and it is substring-based for the three that appear glued
// to another word ("accesstoken"). A false positive costs one field in a log; a
// false negative writes a working credential into a table an operator opens in
// a GUI and a backup copies off the machine.
var credentialWords = map[string]bool{
	"pin": true, "otp": true, "password": true, "passcode": true,
	"secret": true, "token": true, "credential": true, "credentials": true,
	"apikey": true,
}

var credentialSubstrings = []string{"password", "secret", "token"}

// isCredentialKey reports whether a key names something that must never be
// stored.
func isCredentialKey(key string) bool {
	tokens := keyTokens(key)
	// The joined form catches keys whose separator is what hid the word:
	// "apiKey" tokenises to api + key, and only "apikey" is the credential.
	for _, token := range append(tokens, strings.Join(tokens, "")) {
		if credentialWords[token] {
			return true
		}
		for _, needle := range credentialSubstrings {
			if strings.Contains(token, needle) {
				return true
			}
		}
	}
	return false
}

// keyTokens splits a key on separators and camelCase humps, lowercasing the
// result, so one matcher covers pin_hash, PinHash, pin-hash and pinHash.
func keyTokens(key string) []string {
	var tokens []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, strings.ToLower(current.String()))
			current.Reset()
		}
	}
	runes := []rune(key)
	for i, r := range runes {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			if r >= 'A' && r <= 'Z' && i > 0 && runes[i-1] >= 'a' && runes[i-1] <= 'z' {
				flush()
			}
			current.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return tokens
}

// encodeDetail turns a caller's map into the JSON text the column holds,
// dropping credentials and anything that would make the cell unreadable.
func encodeDetail(detail map[string]any) (string, error) {
	if len(detail) == 0 {
		return "", nil
	}
	var dropped []string
	clean := sanitiseMap(detail, 0, &dropped)
	if len(dropped) > 0 {
		sort.Strings(dropped)
		clean[droppedKey] = dropped
	}

	encoded, err := json.Marshal(clean)
	if err != nil {
		return "", err
	}
	if len(encoded) <= maxDetailBytes {
		return string(encoded), nil
	}
	return shrink(clean)
}

// shrink keeps as many fields as fit in the cap and names the ones it left out,
// so an oversized detail degrades into a shorter readable row rather than into
// a truncated string that is no longer JSON.
func shrink(clean map[string]any) (string, error) {
	keys := make([]string, 0, len(clean))
	for key := range clean {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	kept := map[string]any{}
	var omitted []string
	for _, key := range keys {
		candidate := make(map[string]any, len(kept)+1)
		for k, v := range kept {
			candidate[k] = v
		}
		candidate[key] = clean[key]
		encoded, err := json.Marshal(candidate)
		if err != nil || len(encoded) > maxDetailBytes {
			omitted = append(omitted, key)
			continue
		}
		kept = candidate
	}
	if len(omitted) > 0 {
		kept[truncatedKey] = omitted
	}
	encoded, err := json.Marshal(kept)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func sanitiseMap(in map[string]any, depth int, dropped *[]string) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		if isCredentialKey(key) {
			*dropped = append(*dropped, key)
			continue
		}
		out[cleanString(key)] = sanitiseValue(value, depth+1, dropped)
	}
	return out
}

func sanitiseValue(value any, depth int, dropped *[]string) any {
	if depth > maxDetailDepth {
		// Deeper than any real detail goes, and a nested structure a reader
		// cannot follow is not evidence anyone will use.
		return "<nested too deeply>"
	}
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return cleanString(typed)
	case []byte:
		return cleanString(string(typed))
	case map[string]any:
		return sanitiseMap(typed, depth, dropped)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = sanitiseValue(item, depth+1, dropped)
		}
		return out
	}
	// Anything else has to survive json.Marshal: a channel, a func, a NaN or a
	// cyclic value would otherwise fail the whole write, losing the event over
	// one bad field.
	if _, err := json.Marshal(value); err != nil {
		return fmt.Sprintf("<unencodable %T>", value)
	}
	return value
}

// cleanString makes a value safe to read in a database GUI: valid UTF-8, no
// control characters, and short enough to see.
func cleanString(s string) string { return cleanTo(s, maxValueBytes) }

// cleanColumn is cleanString for the project column, which holds a filesystem
// root and so needs more room than any other value. It is matched on with
// `project = ?`, and truncating a real root would make that project's own
// events unfindable — maxProjectBytes is past PATH_MAX so that cannot happen.
func cleanColumn(s string) string { return cleanTo(s, maxProjectBytes) }

func cleanTo(s string, limit int) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if len(s) > limit {
		s = strings.ToValidUTF8(s[:limit], "") + "…"
	}
	return s
}

// decodeDetail reads the column back. A cell that is not an object — one an
// operator edited by hand, most likely — is returned as raw text rather than
// dropped, because the row is still evidence.
func decodeDetail(text string) map[string]any {
	if text == "" {
		return nil
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(text), &detail); err != nil || detail == nil {
		return map[string]any{"_raw": text}
	}
	return detail
}
