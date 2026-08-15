package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"nodevas/internal/db"
)

// newStore opens a throwaway workspace database. Every test gets its own file
// so nothing here depends on the order the tests run in.
func newStore(t *testing.T) *Store {
	t.Helper()
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return New(database)
}

func TestRecordAndQueryReturnTheNewestEventFirst(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	for i, action := range []string{"node.create", "node.update", "auth.signin"} {
		if err := s.Record(ctx, Event{
			At:      base.Add(time.Duration(i) * time.Minute),
			Project: "alpha",
			ActorID: "u1",
			Action:  action,
		}); err != nil {
			t.Fatal(err)
		}
	}

	events, err := s.Query(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}
	// Newest first is the order an operator reads an incident in.
	if events[0].Action != "auth.signin" || events[2].Action != "node.create" {
		t.Fatalf("wrong order: %q ... %q", events[0].Action, events[2].Action)
	}
	if !events[0].At.Equal(base.Add(2 * time.Minute)) {
		t.Fatalf("timestamp round-tripped as %v", events[0].At)
	}
}

func TestRecordRejectsAnEventWithoutAnAction(t *testing.T) {
	// A row with no action is a row nobody can interpret later, so it is
	// refused at the door rather than stored as noise.
	if err := newStore(t).Record(context.Background(), Event{Project: "alpha"}); err == nil {
		t.Fatal("recorded an event with no action")
	}
}

func TestRecordReturnsAnErrorWhenTheDatabaseIsClosed(t *testing.T) {
	// The contract the request path depends on: a failed write is reported,
	// never swallowed, so RecordOrLog can put the event somewhere else.
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(database)
	database.Close()
	if err := s.Record(context.Background(), Event{Action: "node.update"}); err == nil {
		t.Fatal("a write to a closed database reported success")
	}
}

func TestRecordOrLogSignalsDeterministicDatabaseFallbackAndRecovery(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(database)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	event := Event{
		At:        time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC),
		Project:   "/workspace/project",
		ActorID:   "member-1",
		ActorName: "member",
		Action:    "POST nodes",
		ClientIP:  "192.0.2.10",
	}
	s.RecordOrLog(context.Background(), event)
	health := s.Health()
	if health.Status != HealthDegraded || health.WriteStatus != HealthDegraded ||
		health.FallbackEvents != 1 || health.UnreconciledEvents != 1 ||
		health.LastFailureAt == nil || health.LastError == "" {
		t.Fatalf("fallback health = %+v", health)
	}
	if _, err := s.AcknowledgeFallbacks(1); !errors.Is(err, ErrWritesDegraded) {
		t.Fatalf("acknowledge during write failure error = %v, want ErrWritesDegraded", err)
	}
	logged := output.String()
	for _, field := range []string{
		`"event.code":"audit.persistence_fallback"`,
		`"audit.health":"degraded"`,
		`"audit_action":"POST nodes"`,
	} {
		if !strings.Contains(logged, field) {
			t.Fatalf("fallback log missing %s: %s", field, logged)
		}
	}

	recoveredDB, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { recoveredDB.Close() })
	s.db = recoveredDB
	s.RecordOrLog(context.Background(), Event{At: event.At.Add(time.Minute), Action: "POST graph"})
	health = s.Health()
	if health.Status != HealthDegraded || health.WriteStatus != HealthHealthy ||
		health.FallbackEvents != 1 || health.UnreconciledEvents != 1 {
		t.Fatalf("recovered health = %+v", health)
	}
	if !strings.Contains(output.String(), `"event.code":"audit.persistence_recovered"`) {
		t.Fatalf("recovery log missing: %s", output.String())
	}
	if _, err := s.AcknowledgeFallbacks(0); !errors.Is(err, ErrFallbackEventsChanged) {
		t.Fatalf("stale acknowledgement error = %v, want ErrFallbackEventsChanged", err)
	}
	health, err = s.AcknowledgeFallbacks(1)
	if err != nil {
		t.Fatal(err)
	}
	if health.Status != HealthHealthy || health.WriteStatus != HealthHealthy ||
		health.FallbackEvents != 1 || health.AcknowledgedFallbackEvents != 1 ||
		health.UnreconciledEvents != 0 || health.LastFailureAt == nil || health.LastError == "" {
		t.Fatalf("acknowledged health = %+v", health)
	}
	if !strings.Contains(output.String(), `"event.code":"audit.reconciliation_acknowledged"`) {
		t.Fatalf("acknowledgement log missing: %s", output.String())
	}
	events, err := s.Query(context.Background(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Action != "POST graph" {
		t.Fatalf("recovered audit events = %+v", events)
	}
}

func TestQueryFiltersByProjectActorActionPrefixAndTimeRange(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	events := []Event{
		{At: base, Project: "alpha", ActorID: "u1", Action: "node.create"},
		{At: base.Add(time.Hour), Project: "beta", ActorID: "u2", Action: "node.update"},
		{At: base.Add(2 * time.Hour), Project: "", ActorID: "u1", Action: "auth.signin"},
		{At: base.Add(3 * time.Hour), Project: "alpha", ActorID: "u1", Action: "auth.passcode.request"},
	}
	for _, event := range events {
		if err := s.Record(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name   string
		filter Filter
		want   []string
	}{
		{"by project", Filter{Project: "alpha"}, []string{"auth.passcode.request", "node.create"}},
		{"by actor", Filter{ActorID: "u2"}, []string{"node.update"}},
		{"by action prefix", Filter{ActionPrefix: "auth."}, []string{"auth.passcode.request", "auth.signin"}},
		// A sign-in has no project, and it must still be findable.
		{"server-wide only", Filter{ServerWide: true}, []string{"auth.signin"}},
		{"time range excludes its upper bound", Filter{Since: base.Add(time.Hour), Until: base.Add(3 * time.Hour)}, []string{"auth.signin", "node.update"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.Query(ctx, tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			var actions []string
			for _, event := range got {
				actions = append(actions, event.Action)
			}
			if strings.Join(actions, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("got %v, want %v", actions, tc.want)
			}
		})
	}
}

func TestQueryClampsTheLimitAndPagesWithTheOffset(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		if err := s.Record(ctx, Event{At: base.Add(time.Duration(i) * time.Minute), Action: "node.update", Target: string(rune('a' + i))}); err != nil {
			t.Fatal(err)
		}
	}

	// A caller asking for more than the ceiling gets the ceiling, not a
	// database dump: the only callers that ask are buggy or hostile.
	page, err := s.Query(ctx, Filter{Limit: MaxLimit * 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 5 {
		t.Fatalf("got %d events, want all 5", len(page))
	}

	first, err := s.Query(ctx, Filter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Query(ctx, Filter{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("pages of %d and %d, want 2 and 2", len(first), len(second))
	}
	// Paging must not repeat a row; without the id tie-break it would, since
	// several of these share a second.
	if first[1].Target == second[0].Target {
		t.Fatalf("page boundary repeated %q", second[0].Target)
	}
}

func TestRecordDropsCredentialLookingDetailAndSaysSo(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.Record(ctx, Event{
		Action: "auth.signin",
		Detail: map[string]any{
			"pin":            "1234",
			"otpCode":        "998877",
			"password":       "hunter2",
			"session_token":  "abc",
			"client_secret":  "shh",
			"pinned_at":      "2026-04-01",
			"method":         "passcode",
			"nested":         map[string]any{"apiKey": "nested-api-key", "ok": 1},
			"passcode_hash":  "hash-value",
			"credential_ref": "credential-value",
		},
	}); err != nil {
		t.Fatal(err)
	}

	events, err := s.Query(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	detail := events[0].Detail
	for _, secret := range []string{"1234", "998877", "hunter2", "abc", "shh", "nested-api-key", "hash-value", "credential-value"} {
		encoded, _ := json.Marshal(detail)
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("credential %q was stored: %s", secret, encoded)
		}
	}
	// Fields that merely look like credentials to a careless reader must
	// survive, or the trail loses the context that makes it useful.
	if detail["method"] != "passcode" || detail["pinned_at"] != "2026-04-01" {
		t.Fatalf("dropped a non-credential field: %v", detail)
	}
	// Redaction has to be visible: an operator must be able to tell a dropped
	// field from a field that was never sent.
	dropped, _ := detail[droppedKey].([]any)
	if len(dropped) == 0 {
		t.Fatalf("no record of what was dropped: %v", detail)
	}
	nested, _ := detail["nested"].(map[string]any)
	if _, still := nested["apiKey"]; still {
		t.Fatalf("credential nested one level deep survived: %v", nested)
	}
}

func TestRecordKeepsDetailReadableInADatabaseTool(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.Record(ctx, Event{
		Action: "node.update",
		Detail: map[string]any{
			"control":     "before\x00\x07after",
			"invalidUTF8": string([]byte{0x66, 0xff, 0x6f}),
			"huge":        strings.Repeat("x", 4*maxValueBytes),
			"unencodable": make(chan int),
		},
	}); err != nil {
		t.Fatal(err)
	}

	events, err := s.Query(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	detail := events[0].Detail
	if strings.ContainsAny(detail["control"].(string), "\x00\x07") {
		t.Fatalf("control characters survived: %q", detail["control"])
	}
	if !strings.Contains(detail["invalidUTF8"].(string), "�") {
		t.Fatalf("invalid UTF-8 was not repaired: %q", detail["invalidUTF8"])
	}
	if len(detail["huge"].(string)) > maxValueBytes+8 {
		t.Fatalf("oversized value was not truncated: %d bytes", len(detail["huge"].(string)))
	}
	// One unencodable field must cost that field, not the whole event: losing
	// the event is the failure that matters.
	if !strings.HasPrefix(detail["unencodable"].(string), "<unencodable") {
		t.Fatalf("unencodable value stored as %v", detail["unencodable"])
	}
}

func TestRecordStoresDetailAsJSONTextAnOperatorCanRead(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.Record(ctx, Event{Action: "node.update", Detail: map[string]any{"title": "Chapter 1"}}); err != nil {
		t.Fatal(err)
	}
	// Read the raw column, because the promise is about what a DB tool shows,
	// not about what this package's decoder can make of it.
	var text string
	if err := s.db.QueryRowContext(ctx, `SELECT detail FROM audit_events`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if text != `{"title":"Chapter 1"}` {
		t.Fatalf("detail column reads as %q", text)
	}
}

// A filter value is a bound parameter, and the point of that is that SQL keeps
// being SQL no matter what the value spells.
func TestFilterValuesAreStoredAndMatchedAsLiteralText(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const attack = `'; DROP TABLE audit_events;--`

	if err := s.Record(ctx, Event{Action: "node.update", ActorID: attack, Target: attack}); err != nil {
		t.Fatal(err)
	}
	if err := s.Record(ctx, Event{Action: "node.update", ActorID: "u1"}); err != nil {
		t.Fatal(err)
	}

	events, err := s.Query(ctx, Filter{ActorID: attack})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Target != attack {
		t.Fatalf("got %d events, %+v; want the one row, stored verbatim", len(events), events)
	}
	// The table is what the statement was aimed at, so its survival is the
	// assertion that matters, not just that the read came back.
	var rows int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events`).Scan(&rows); err != nil {
		t.Fatalf("the audit_events table is gone: %v", err)
	}
	if rows != 2 {
		t.Fatalf("audit_events holds %d rows, want 2", rows)
	}
}

// ActionPrefix reaches SQLite as a LIKE pattern. A placeholder stops injection
// but does nothing about LIKE's own metacharacters, so a caller who types % or
// _ must match those characters rather than widen their own query.
func TestActionPrefixMatchesLikeWildcardsLiterally(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, action := range []string{"a%b.percent", "axb.wildcarded", "a_c.underscore", "azc.matched"} {
		if err := s.Record(ctx, Event{Action: action}); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		prefix string
		want   string
	}{
		// Unescaped, "a%" would match every action above.
		{"a%", "a%b.percent"},
		// Unescaped, "a_c" would also match "azc.matched".
		{"a_c", "a_c.underscore"},
		// A lone backslash must not eat the escape character's own meaning.
		{`a\`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.prefix, func(t *testing.T) {
			events, err := s.Query(ctx, Filter{ActionPrefix: tc.prefix})
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(events) != 0 {
					t.Fatalf("prefix %q matched %d events, want none", tc.prefix, len(events))
				}
				return
			}
			if len(events) != 1 || events[0].Action != tc.want {
				t.Fatalf("prefix %q matched %+v, want only %q", tc.prefix, events, tc.want)
			}
		})
	}
}

// The paging numbers come off a query string, so every degenerate one has to
// land somewhere defined rather than reaching SQLite as written.
func TestQueryHandlesDegenerateLimitsAndOffsets(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		if err := s.Record(ctx, Event{At: base.Add(time.Duration(i) * time.Minute), Action: "node.update"}); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name   string
		filter Filter
		want   int
	}{
		// Zero means "the caller did not choose", which is the default page.
		{"zero limit", Filter{Limit: 0}, 4},
		// A negative LIMIT is unlimited in SQLite, the opposite of the request.
		{"negative limit", Filter{Limit: -1}, 4},
		{"huge limit", Filter{Limit: 1 << 30}, 4},
		// A negative OFFSET is ignored by SQLite; make it mean the first page.
		{"negative offset", Filter{Offset: -5}, 4},
		// Past the ceiling, so it cannot be used to order a deep scan.
		{"huge offset", Filter{Offset: MaxOffset * 100}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events, err := s.Query(ctx, tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != tc.want {
				t.Fatalf("got %d events, want %d", len(events), tc.want)
			}
		})
	}
}

// The audit middleware puts the request path into Action and the node a client
// named into Target, so a row could otherwise be as large as the request that
// produced it — one oversized request per row, forever.
func TestRecordTruncatesOversizedColumnsRatherThanStoringThemWhole(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	huge := strings.Repeat("x", 4<<20)
	if err := s.Record(ctx, Event{
		Action:    "GET " + huge,
		Target:    huge,
		ActorName: "ann\x00\x07",
		ClientIP:  huge,
	}); err != nil {
		t.Fatal(err)
	}

	events, err := s.Query(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	event := events[0]
	for name, value := range map[string]string{
		"action": event.Action, "target": event.Target, "client_ip": event.ClientIP,
	} {
		if len(value) > maxValueBytes+8 {
			t.Fatalf("%s stored %d bytes, want it bounded", name, len(value))
		}
	}
	// The column is read in a database GUI; a NUL in it is a row an operator
	// cannot see the end of.
	if strings.ContainsAny(event.ActorName, "\x00\x07") {
		t.Fatalf("control characters survived into actor_name: %q", event.ActorName)
	}
}

// A project root is longer than anything else a row carries and is matched on
// with `project = ?`, so bounding it must not cost a real path its own events.
func TestRecordKeepsAFullLengthProjectRootFindable(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	root := "C:\\" + strings.Repeat("deep\\", 100) + "project"
	if err := s.Record(ctx, Event{Action: "node.update", Project: root}); err != nil {
		t.Fatal(err)
	}
	events, err := s.Query(ctx, Filter{Project: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("a %d-byte project root matched %d events, want 1", len(root), len(events))
	}
}

// ActionPrefix becomes a LIKE pattern SQLite compares against every candidate
// row. No honest caller sends a megabyte of one; refusing is cheaper than
// paying for it, and refusing beats truncating because a truncated filter
// answers a question nobody asked.
func TestQueryRefusesAFilterValueNoCallerWouldSend(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, filter := range []Filter{
		{ActionPrefix: strings.Repeat("a", maxFilterBytes+1)},
		{ActorID: strings.Repeat("u", maxFilterBytes+1)},
		{Project: strings.Repeat("p", maxProjectBytes+1)},
	} {
		if _, err := s.Query(ctx, filter); err == nil {
			t.Fatalf("an oversized filter was accepted: %+v", filter)
		}
	}
}
