package audit

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/logging"
	"nodevas/internal/logging/logtest"
)

func TestEveryStoredActionReadsAsTheSharedVocabulary(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event Event
		want  logShape
	}{
		{"one-factor sign-in", Event{Action: ActionSignIn},
			logShape{"authentication", "auth.signin", "success", "", "otp", "sign-in"}},
		{"two-factor sign-in", Event{Action: ActionSignIn, Detail: map[string]any{"secondFactor": "totp"}, AuthMethod: MethodTOTP},
			logShape{"authentication", "auth.signin", "success", "", "totp", "sign-in"}},
		{"wrong passcode", Event{Action: ActionSignInFailed, Detail: map[string]any{"reason": "bad-credentials"}},
			logShape{"authentication", "auth.signin", "failure", "bad-credentials", "otp", "sign-in"}},
		{"throttled", Event{Action: ActionSignInFailed, Detail: map[string]any{"reason": "throttled"}},
			logShape{"authentication", "auth.signin", "failure", "throttled", "otp", "sign-in"}},
		{"server full", Event{Action: ActionSignInFailed, Detail: map[string]any{"reason": "seat-limit"}},
			logShape{"authentication", "auth.signin", "failure", "seat-limit", "otp", "sign-in"}},
		{"gave up waiting", Event{Action: ActionSignInFailed, Detail: map[string]any{"reason": "cancelled"}},
			logShape{"authentication", "auth.signin", "failure", "error", "otp", "sign-in"}},
		{"session store down", Event{Action: ActionSignInFailed, Detail: map[string]any{"reason": "persistence"}},
			logShape{"authentication", "auth.signin", "failure", "error", "otp", "sign-in"}},
		{"wrong authenticator code", Event{Action: ActionSignInFailed, Detail: map[string]any{"reason": "bad-totp"}, AuthMethod: MethodTOTP},
			logShape{"authentication", "auth.signin", "failure", "bad-credentials", "totp", "sign-in"}},
		{"pending sign-in ran out", Event{Action: ActionSignInFailed, Detail: map[string]any{"reason": "signin-expired"}, AuthMethod: MethodRecoveryCode},
			logShape{"authentication", "auth.signin", "failure", "expired-code", "recovery-code", "sign-in"}},
		{"unreadable authenticator", Event{Action: ActionSignInFailed, Detail: map[string]any{"reason": "totp-unreadable"}},
			logShape{"authentication", "auth.signin", "failure", "error", "otp", "sign-in"}},
		{"second factor next", Event{Action: ActionSignInPending, Detail: map[string]any{"secondFactor": "totp"}},
			logShape{"audit", "auth.signin.pending", "success", "", "otp", "sign-in"}},
		{"sign-out", Event{Action: ActionSignOut},
			logShape{"authentication", "auth.signout", "success", "", "", ""}},
		{"passcode mailed", Event{Action: ActionPasscodeRequest, Detail: map[string]any{"delivered": true}},
			logShape{"authentication", "auth.code.request", "success", "", "otp", "sign-in"}},
		{"passcode for nobody", Event{Action: ActionPasscodeRequest, Detail: map[string]any{"delivered": false}, Reason: "unknown-account"},
			logShape{"authentication", "auth.code.request", "failure", "unknown-account", "otp", "sign-in"}},
		{"passcode not sent", Event{Action: ActionPasscodeRequest, Detail: map[string]any{"delivered": false}},
			logShape{"authentication", "auth.code.request", "failure", "error", "otp", "sign-in"}},
		// A step-up is neither a sign-in nor a passcode mailed for one, which
		// is what the shared actions count.
		{"step-up passcode mailed", Event{Action: ActionStepUpRequest, Detail: map[string]any{"delivered": true}},
			logShape{"audit", "auth.stepup.code.request", "success", "", "otp", "step-up"}},
		{"step-up", Event{Action: ActionStepUp, Detail: map[string]any{"method": "totp"}, AuthMethod: MethodTOTP},
			logShape{"audit", "auth.stepup", "success", "", "totp", "step-up"}},
		{"wrong step-up code", Event{Action: ActionStepUpFailed, Detail: map[string]any{"reason": "bad-code"}, AuthMethod: MethodOTP},
			logShape{"audit", "auth.stepup", "failure", "bad-credentials", "otp", "step-up"}},
		{"step-up without a session", Event{Action: ActionStepUpFailed, Detail: map[string]any{"reason": "no-session"}},
			logShape{"audit", "auth.stepup", "failure", "expired-code", "", "step-up"}},
		{"step-up passcode without a session", Event{Action: ActionStepUpRequest, Detail: map[string]any{"delivered": false}, Reason: "no-session"},
			logShape{"audit", "auth.stepup.code.request", "failure", "no-session", "otp", "step-up"}},
		{"enrollment begun", Event{Action: ActionTOTPEnrollBegin},
			logShape{"audit", "auth.totp.enroll", "success", "", "", ""}},
		{"authenticator on", Event{Action: ActionTOTPEnable},
			logShape{"iam", "iam.account.update", "success", "", "", ""}},
		{"authenticator off", Event{Action: ActionTOTPDisable, Detail: map[string]any{"method": "recovery"}},
			logShape{"iam", "iam.account.update", "success", "", "", ""}},
		{"authenticator reset by an operator", Event{Action: ActionTOTPReset},
			logShape{"iam", "iam.account.update", "success", "", "", ""}},
		{"new recovery codes", Event{Action: ActionTOTPRecoveryRegenerated},
			logShape{"iam", "iam.account.update", "success", "", "", ""}},
		{"refused authenticator code", Event{Action: ActionTOTPFailed, Detail: map[string]any{"purpose": "disable", "reason": "bad-code"}},
			logShape{"iam", "iam.account.update", "failure", "bad-credentials", "", ""}},
		{"authenticator changes throttled", Event{Action: ActionTOTPFailed, Detail: map[string]any{"reason": "throttled"}},
			logShape{"iam", "iam.account.update", "failure", "throttled", "", ""}},
		{"account invited", Event{Action: ActionAccountInvite, Detail: map[string]any{"role": "member", "emailSent": false}},
			logShape{"iam", "iam.account.update", "success", "", "", ""}},
		{"server role changed", Event{Action: ActionAccountRole, Detail: map[string]any{"role": "admin"}},
			logShape{"iam", "iam.account.update", "success", "", "", ""}},
		{"account removed", Event{Action: ActionAccountRemove},
			logShape{"iam", "iam.account.update", "success", "", "", ""}},
		{"recovery code spent", Event{Action: ActionTOTPRecoveryUsed, Detail: map[string]any{"purpose": "signin", "remaining": 9}},
			logShape{"audit", "auth.totp.recovery.used", "success", "", "", ""}},
		// Handed back, not mailed: not a sign-in code the shared action counts.
		{"device link minted", Event{Action: ActionRunnerLinkCreate},
			logShape{"audit", "runner.link.create", "success", "", "device-link", ""}},
		{"device linked", Event{Action: ActionRunnerLinkRedeem},
			logShape{"authentication", "auth.signin", "success", "", "device-link", "sign-in"}},
		{"device link throttled", Event{Action: ActionRunnerLinkFailed, Detail: map[string]any{"reason": "throttled"}},
			logShape{"authentication", "auth.signin", "failure", "throttled", "device-link", "sign-in"}},
		{"unknown device link", Event{Action: ActionRunnerLinkFailed, Detail: map[string]any{"reason": "unknown code"}},
			logShape{"authentication", "auth.signin", "failure", "bad-credentials", "device-link", "sign-in"}},
		{"device link outlived its account", Event{Action: ActionRunnerLinkFailed, Detail: map[string]any{"reason": "account changed"}},
			logShape{"authentication", "auth.signin", "failure", "expired-code", "device-link", "sign-in"}},
		{"access token set", Event{Action: ActionRunnerAccessTokenSet, Target: "abc.access"},
			logShape{"configuration", "config.change", "success", "", "", ""}},
		{"access token cleared", Event{Action: ActionRunnerAccessTokenClear},
			logShape{"configuration", "config.change", "success", "", "", ""}},
		{"member added", Event{Action: "PUT projects/members"},
			logShape{"iam", "iam.account.update", "success", "", "", ""}},
		{"member removed", Event{Action: "DELETE projects/members"},
			logShape{"iam", "iam.account.update", "success", "", "", ""}},
		{"project opened to everyone", Event{Action: "POST projects/members/open"},
			logShape{"iam", "iam.account.update", "success", "", "", ""}},
		{"mail settings", Event{Action: "PUT notify/settings"},
			logShape{"configuration", "config.change", "success", "", "", ""}},
		{"copilot settings", Event{Action: "PUT copilot/settings"},
			logShape{"configuration", "config.change", "success", "", "", ""}},
		{"remote sync settings", Event{Action: "PUT remote/config"},
			logShape{"configuration", "config.change", "success", "", "", ""}},
		{"a run budget", Event{Action: "PUT runs/budget"},
			logShape{"configuration", "config.change", "success", "", "", ""}},
		{"run routing", Event{Action: "PUT runs/routing"},
			logShape{"configuration", "config.change", "success", "", "", ""}},
		{"model prices", Event{Action: "PUT usage/prices"},
			logShape{"configuration", "config.change", "success", "", "", ""}},
		{"a refusal for an unknown address", Event{Action: ActionSignInFailed, Detail: map[string]any{"reason": "bad-credentials"}, Reason: "unknown-account"},
			logShape{"authentication", "auth.signin", "failure", "unknown-account", "otp", "sign-in"}},
		{"the guess that burnt the passcode", Event{Action: ActionSignInFailed, Detail: map[string]any{"reason": "bad-credentials"}, Reason: "too-many-attempts"},
			logShape{"authentication", "auth.signin", "failure", "too-many-attempts", "otp", "sign-in"}},
		{"a reason on a success is no reason", Event{Action: ActionSignIn, Reason: "unknown-account"},
			logShape{"authentication", "auth.signin", "success", "", "otp", "sign-in"}},
		// A failed sign-in gives only the shared words for why.
		{"a stored reason with no shared word", Event{Action: ActionRunnerLinkFailed, Detail: map[string]any{"reason": "made-up"}},
			logShape{"authentication", "auth.signin", "failure", "error", "device-link", "sign-in"}},
		{"a finer reason with no shared word", Event{Action: ActionSignInFailed, Detail: map[string]any{"reason": "bad-credentials"}, Reason: "made-up"},
			logShape{"authentication", "auth.signin", "failure", "error", "otp", "sign-in"}},
		{"a write", Event{Action: "POST nodes"},
			logShape{"audit", "POST nodes", "success", "", "", ""}},
		{"a failed merge", Event{Action: "run.merge.failed", Detail: map[string]any{"reason": "conflict in\nmain.go"}},
			logShape{"audit", "run.merge.failed", "failure", "", "", ""}},
		{"a merge", Event{Action: "run.merge.done"},
			logShape{"audit", "run.merge.done", "success", "", "", ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shapeOf(tc.event); got != tc.want {
				t.Fatalf("shapeOf(%s) = %+v, want %+v", tc.event.Action, got, tc.want)
			}
		})
	}
}

// A failure is a warning, as every service on the host logs one, and this
// server's own fault is an error; a client that stopped waiting is neither.
func TestALineIsLevelledByItsOutcome(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event Event
		want  slog.Level
	}{
		{"a sign-in", Event{Action: ActionSignIn}, slog.LevelInfo},
		{"a wrong passcode", Event{Action: ActionSignInFailed, Detail: map[string]any{"reason": "bad-credentials"}}, slog.LevelWarn},
		{"throttled", Event{Action: ActionSignInFailed, Detail: map[string]any{"reason": "throttled"}}, slog.LevelWarn},
		{"the session store down", Event{Action: ActionSignInFailed, Detail: map[string]any{"reason": "persistence"}}, slog.LevelError},
		{"a database read failed", Event{Action: ActionSignInFailed, Detail: map[string]any{"reason": "bad-credentials"}, Reason: "error"}, slog.LevelError},
		{"gave up waiting", Event{Action: ActionSignInFailed, Detail: map[string]any{"reason": "cancelled"}}, slog.LevelWarn},
		{"a passcode for nobody", Event{Action: ActionPasscodeRequest, Detail: map[string]any{"delivered": false}, Reason: "unknown-account"}, slog.LevelWarn},
		{"no passcode could be made", Event{Action: ActionPasscodeRequest, Detail: map[string]any{"delivered": false}, Reason: "error"}, slog.LevelError},
		{"a passcode request given up on", Event{Action: ActionPasscodeRequest, Detail: map[string]any{"delivered": false}, Reason: "cancelled"}, slog.LevelWarn},
		{"a wrong step-up code", Event{Action: ActionStepUpFailed, Detail: map[string]any{"reason": "bad-code"}}, slog.LevelWarn},
		{"a refused authenticator change", Event{Action: ActionTOTPFailed, Detail: map[string]any{"reason": "bad-code"}}, slog.LevelWarn},
		{"a failed merge", Event{Action: "run.merge.failed", Detail: map[string]any{"reason": "conflict"}}, slog.LevelWarn},
		{"a write", Event{Action: "POST nodes"}, slog.LevelInfo},
	} {
		if got := levelOf(tc.event, shapeOf(tc.event)); got != tc.want {
			t.Errorf("%s: level %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A passcode's row is written when it is handed to delivery and its line once
// the relay has answered, so the log has one line per request, with the
// outcome that stuck.
func TestRecordOrLogLaterWritesTheRowNowAndTheLineOnceSettled(t *testing.T) {
	s := newStore(t)
	logs := logtest.Capture(t)
	var sent, refused func(error)
	handler := logging.MiddlewareWithOptions(logging.Options{Logger: slog.Default()})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, settle := range []*func(error){&sent, &refused} {
				*settle = s.RecordOrLogLater(r.Context(), Event{
					ActorName: "ann", ActorEmail: "ann@example.test", Action: ActionPasscodeRequest,
					ClientIP: "203.0.113.7", Detail: map[string]any{"delivered": true},
				})
			}
		}))
	request := httptest.NewRequest(http.MethodPost, "/api/auth/otp/request", nil)
	request.Header.Set("User-Agent", "Mozilla/5.0 (X11) test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	rows, err := s.Query(context.Background(), Filter{ActionPrefix: ActionPasscodeRequest})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Detail["delivered"] != true {
		t.Fatalf("rows = %+v", rows)
	}
	if found := logs.Find(t, map[string]any{"nodevas.audit.action": ActionPasscodeRequest}); len(found) != 0 {
		t.Fatalf("a line before the relay answered: %v", found)
	}

	sent(nil)
	refused(errors.New("relay refused the message"))
	common := map[string]any{
		"event.category": "authentication", "event.action": "auth.code.request",
		"user.name": "ann", "user.email": "ann@example.test", "client.ip": "203.0.113.7",
		"http.request.id":      response.Header().Get("X-Request-Id"),
		"user_agent.original":  "Mozilla/5.0 (X11) test",
		"nodevas.audit.detail": `{"delivered":true}`,
	}
	mailed := logs.One(t, map[string]any{"nodevas.audit.action": ActionPasscodeRequest, "event.outcome": "success"})
	logtest.Want(t, mailed, common)
	logtest.Want(t, mailed, map[string]any{"log.level": "info", "message": "auth.code.request success"})
	logtest.Absent(t, mailed, "event.reason", "error.message")
	bounced := logs.One(t, map[string]any{"nodevas.audit.action": ActionPasscodeRequest, "event.outcome": "failure"})
	logtest.Want(t, bounced, common)
	logtest.Want(t, bounced, map[string]any{
		"log.level": "error", "message": "auth.code.request failure", "event.reason": "error",
		"error.message": "relay refused the message",
	})
}

func TestRecordOrLogWritesOneLineAndTheRowAsBefore(t *testing.T) {
	s := newStore(t)
	logs := logtest.Capture(t)
	at := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	s.RecordOrLog(context.Background(), Event{
		At: at, ActorID: "u-1", ActorName: "ann", Action: ActionSignIn, ClientIP: "203.0.113.7",
		Detail: map[string]any{"secondFactor": "recovery"}, ActorEmail: "ann@example.test",
		AuthMethod: MethodRecoveryCode,
	})

	record := logs.One(t, map[string]any{"nodevas.audit.action": ActionSignIn})
	logtest.Want(t, record, map[string]any{
		"log.level":            "info",
		"message":              "auth.signin success",
		"service.name":         "nodevas",
		"event.category":       "authentication",
		"event.action":         "auth.signin",
		"event.outcome":        "success",
		"user.id":              "u-1",
		"user.name":            "ann",
		"user.email":           "ann@example.test",
		"client.ip":            "203.0.113.7",
		"nodevas.auth.method":  "recovery-code",
		"nodevas.auth.purpose": "sign-in",
		"nodevas.audit.detail": `{"secondFactor":"recovery"}`,
	})
	if stamp, _ := record["@timestamp"].(string); !strings.HasSuffix(stamp, "Z") {
		t.Errorf("@timestamp = %q, want UTC", stamp)
	}
	// No request, so nothing claims to be one; no project, target or reason.
	logtest.Absent(t, record, "http.request.id", "user_agent.original", "nodevas.project",
		"nodevas.audit.target", "event.reason")

	// The row is what it always was: the log-only fields have no column.
	events, err := s.Query(context.Background(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("%d rows, want 1", len(events))
	}
	row := events[0]
	if row.Action != ActionSignIn || row.ActorID != "u-1" || row.ActorName != "ann" || row.ClientIP != "203.0.113.7" ||
		!row.At.Equal(at) || row.Detail["secondFactor"] != "recovery" || len(row.Detail) != 1 ||
		row.ActorEmail != "" || row.AuthMethod != "" || row.Reason != "" {
		t.Fatalf("row = %+v", row)
	}
}

func TestARefusalNamesItsSubjectInTheLineAndNotInTheRow(t *testing.T) {
	s := newStore(t)
	logs := logtest.Capture(t)
	ctx := context.Background()
	s.RecordOrLog(ctx, Event{
		Action: ActionSignInFailed, ClientIP: "203.0.113.7", Detail: map[string]any{"reason": "bad-credentials"},
		SubjectID: "u-1", SubjectName: "ann", ActorEmail: "ann@example.test",
		Reason: "too-many-attempts", AuthMethod: MethodOTP,
	})
	// With an actor, the actor is the line's user.
	s.RecordOrLog(ctx, Event{Action: "POST nodes", ActorID: "u-2", ActorName: "bea", SubjectID: "u-1", SubjectName: "ann"})

	refused := logs.One(t, map[string]any{"nodevas.audit.action": ActionSignInFailed})
	logtest.Want(t, refused, map[string]any{
		"log.level":    "warn",
		"event.action": "auth.signin", "event.outcome": "failure", "event.reason": "too-many-attempts",
		"user.id": "u-1", "user.name": "ann", "user.email": "ann@example.test", "client.ip": "203.0.113.7",
		"nodevas.audit.detail": `{"reason":"bad-credentials"}`,
	})
	logtest.Want(t, logs.One(t, map[string]any{"nodevas.audit.action": "POST nodes"}),
		map[string]any{"user.id": "u-2", "user.name": "bea"})

	// The rows are what they always were: a refusal has no actor, and keeps
	// the stored reason.
	rows, err := s.Query(ctx, Filter{ActionPrefix: ActionSignInFailed})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ActorID != "" || rows[0].ActorName != "" || rows[0].ClientIP != "203.0.113.7" ||
		rows[0].Detail["reason"] != "bad-credentials" || len(rows[0].Detail) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestAFailedWriteKeepsTheLineAndAddsTheFallback(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(database)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	logs := logtest.Capture(t)
	s.RecordOrLog(context.Background(), Event{
		Project: "/workspace/alpha", ActorID: "u-2", ActorName: "bea", Action: "PUT projects/members",
		ClientIP: "198.51.100.9",
	})

	record := logs.One(t, map[string]any{"event.action": "iam.account.update"})
	logtest.Want(t, record, map[string]any{
		"log.level": "info", "event.category": "iam", "event.outcome": "success",
		"nodevas.audit.action": "PUT projects/members", "nodevas.project": "/workspace/alpha",
		"user.id": "u-2", "client.ip": "198.51.100.9",
	})
	// The fallback line is still there, for the reconciliation it exists for.
	fallback := logs.One(t, map[string]any{"event.code": "audit.persistence_fallback"})
	logtest.Want(t, fallback, map[string]any{"log.level": "error", "audit_action": "PUT projects/members"})
}

func TestTheLogLineHoldsNoCredential(t *testing.T) {
	s := newStore(t)
	logs := logtest.Capture(t)
	const (
		passcode = "K7Q2M9XP"
		session  = "session-token-value-do-not-log"
		jwt      = "eyJhbGciOiJSUzI1NiJ9.e30.c2ln"
	)
	s.RecordOrLog(context.Background(), Event{
		ActorName: "ann\n{\"event.action\":\"forged\"}", Action: ActionSignInFailed,
		Detail: map[string]any{
			"reason":        "bad-credentials",
			"otp":           passcode,
			"passcode":      passcode,
			"session_token": session,
			"cookie":        "nodevas_session=" + session,
			"authorization": "Bearer " + session,
			"nested":        map[string]any{"accessJwt": jwt, "sessionToken": session, "kept": "visible"},
		},
	})

	logs.Never(t, passcode, session, jwt)
	record := logs.One(t, map[string]any{"nodevas.audit.action": ActionSignInFailed})
	// The name a caller handed over cannot add a line or a field: it is
	// cleaned like the column it came from, and encoded.
	if record["event.action"] != "auth.signin" || record["user.name"] != `ann {"event.action":"forged"}` {
		t.Fatalf("record = %v", record)
	}
	detail, _ := record["nodevas.audit.detail"].(string)
	for _, want := range []string{`"_dropped_keys":`, `"kept":"visible"`, `"reason":"bad-credentials"`} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail %s lacks %s", detail, want)
		}
	}
}

func TestTheLineNamesTheRequestThatCausedIt(t *testing.T) {
	s := newStore(t)
	logs := logtest.Capture(t)
	handler := logging.MiddlewareWithOptions(logging.Options{Logger: slog.Default()})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.RecordOrLog(r.Context(), Event{ActorID: "u-1", Action: "POST nodes", Target: "n1"})
		}))
	request := httptest.NewRequest(http.MethodPost, "/api/nodes", nil)
	request.Header.Set("User-Agent", "Mozilla/5.0 (X11) test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	record := logs.One(t, map[string]any{"nodevas.audit.action": "POST nodes"})
	logtest.Want(t, record, map[string]any{
		"event.category": "audit", "event.action": "POST nodes", "event.outcome": "success",
		"http.request.id":      response.Header().Get("X-Request-Id"),
		"user_agent.original":  "Mozilla/5.0 (X11) test",
		"nodevas.audit.target": "n1",
	})
	// And the request's own line is the one it names.
	logs.One(t, map[string]any{"event.action": "http.request", "http.request.id": record["http.request.id"]})
}
