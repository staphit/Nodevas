package audit

import (
	"context"
	"log/slog"
	"strings"

	"nodevas/internal/logging"
)

// The process log carries the trail too. Every event RecordOrLog records is
// also written as one structured line, so a log pipeline sees who signed in,
// who failed to and who changed what without a connection to this database,
// and an event the database refused is still in a log that leaves the
// machine. The row is untouched by any of this: this file decides only how an
// event reads in the log.
//
// The line speaks the vocabulary every service on the host shares (see the
// Category constants in internal/logging): an event.category, a canonical
// event.action and an event.outcome, with event.reason when it failed. The
// stored action rides along as nodevas.audit.action, so the translation loses
// nothing.

// Actions recorded by the sign-in handlers (internal/httpapi/system) and the
// device links (internal/httpapi/runners). The log knows them by name; see
// shapeOf.
const (
	// ActionSignIn: a session was opened.
	ActionSignIn = "auth.signin"
	// ActionSignInFailed: a sign-in step was refused; detail.reason says why.
	ActionSignInFailed = "auth.signin.failed"
	// ActionSignOut: a person signed out.
	ActionSignOut = "auth.signout"
	// ActionPasscodeRequest: a sign-in passcode was asked for. detail.delivered
	// says whether one was minted and handed to delivery.
	ActionPasscodeRequest = "auth.passcode.request"
	// ActionStepUpRequest, ActionStepUp, ActionStepUpFailed: a step-up
	// passcode asked for, a step-up completed, and one refused.
	ActionStepUpRequest = "auth.stepup.request"
	ActionStepUp        = "auth.stepup"
	ActionStepUpFailed  = "auth.stepup.failed"

	// ActionRunnerLinkCreate, ActionRunnerLinkRedeem, ActionRunnerLinkFailed:
	// a device link minted, one turned into a runner, and one refused.
	ActionRunnerLinkCreate = "runner.link.create"
	ActionRunnerLinkRedeem = "runner.link.redeem"
	ActionRunnerLinkFailed = "runner.link.failed"
	// ActionRunnerAccessTokenSet, ActionRunnerAccessTokenClear: the Cloudflare
	// Access service token that administrators' device links carry.
	ActionRunnerAccessTokenSet   = "runner.access-token.set"
	ActionRunnerAccessTokenClear = "runner.access-token.clear"

	// ActionAccountInvite, ActionAccountRole, ActionAccountRemove: an
	// administrator added an account from the web UI (and mailed the
	// invitation, detail.emailSent says whether it went), changed one's
	// server role, or removed one. The target is the account's ID.
	ActionAccountInvite = "account.invite"
	ActionAccountRole   = "account.role"
	ActionAccountRemove = "account.remove"
)

// How a sign-in step was made, as the log's nodevas.auth.method names it.
const (
	MethodOTP          = "otp"           // a mailed passcode
	MethodTOTP         = "totp"          // a code from an authenticator app
	MethodRecoveryCode = "recovery-code" // one of the printed recovery codes
	MethodDeviceLink   = "device-link"   // a one-time device link for a runner
)

// What a credential was presented for, as nodevas.auth.purpose names it: a
// sign-in, or a step-up, which re-proves a session that is already signed in.
const (
	PurposeSignIn = "sign-in"
	PurposeStepUp = "step-up"
)

// The canonical actions this server's events become.
const (
	actionSignIn        = "auth.signin"
	actionSignOut       = "auth.signout"
	actionCodeRequest   = "auth.code.request"
	actionAccountUpdate = "iam.account.update"
	actionConfigChange  = "config.change"
)

// What a step-up reads as. The shared vocabulary counts sign-ins with
// auth.signin and the passcodes mailed for them with auth.code.request, and a
// session proving itself again is neither, so a step-up is an audit event
// under this server's own names, modelled on those two.
const (
	actionStepUp            = "auth.stepup"
	actionStepUpCodeRequest = "auth.stepup.code.request"
)

// memberActions are the request middleware's rows for project membership
// (internal/httpapi/members): who may reach a project is access control, so
// they read as account changes rather than as plain writes. The handlers name
// the member as the row's target, and a new role in its detail (Annotate).
// Opening a project drops every member, so its row names the project alone.
var memberActions = map[string]bool{
	"PUT projects/members":       true,
	"DELETE projects/members":    true,
	"POST projects/members/open": true,
}

// settingsActions are the request middleware's rows for writes to settings:
// mail and notifications, the copilot, remote sync, and the runs' budgets,
// routing and prices. They read as configuration changes. Like every
// middleware row they say who wrote the settings and when, not what changed.
var settingsActions = map[string]bool{
	"PUT notify/settings":  true,
	"PUT copilot/settings": true,
	"PUT remote/config":    true,
	"PUT runs/budget":      true,
	"PUT runs/routing":     true,
	"PUT usage/prices":     true,
}

// reasonNames turns the reasons the handlers store into the log's kebab-case
// vocabulary. A reason not listed here is already in it and passes through.
var reasonNames = map[string]string{
	"persistence":     "error",
	"totp-unreadable": "error",
	// The client stopped waiting before the attempt was judged. Nothing was
	// refused, and the shared words for a failed sign-in have none closer.
	"cancelled":    "error",
	"bad-totp":     "bad-credentials",
	"bad-code":     "bad-credentials",
	"unknown code": "bad-credentials",
	// A pending two-factor sign-in that ran out, a device link made before
	// its account changed, and a step-up whose session ended first: all were
	// real once and are dead now.
	"signin-expired":  "expired-code",
	"account changed": "expired-code",
	"no-session":      "expired-code",
}

// signInReasons are the reasons a failed auth.signin gives, the same on every
// service on the host so that one alert rule reads them all.
var signInReasons = map[string]bool{
	"bad-credentials": true, "expired-code": true, "too-many-attempts": true, "throttled": true,
	"unknown-account": true, "seat-limit": true, "error": true,
}

// logShape is how one event reads in the process log.
type logShape struct {
	category string
	action   string
	outcome  string
	reason   string
	method   string
	purpose  string
}

// shapeOf maps a stored action onto the shared vocabulary. An action it does
// not know by name is an audit event under its own name, which is what every
// write the request middleware records is.
func shapeOf(event Event) logShape {
	shape := knownShape(event)
	// A caller that knows more than the action and the detail say wins.
	if event.AuthMethod != "" {
		shape.method = event.AuthMethod
	}
	if shape.outcome == logging.OutcomeFailure {
		if event.Reason != "" {
			shape.reason = event.Reason
		}
		// A failed sign-in says why in the shared words alone; a reason they
		// have no word for reads as error.
		if shape.action == actionSignIn && shape.reason != "" && !signInReasons[shape.reason] {
			shape.reason = "error"
		}
	} else {
		shape.reason = ""
	}
	return shape
}

func knownShape(event Event) logShape {
	const (
		success = logging.OutcomeSuccess
		failure = logging.OutcomeFailure
	)
	signIn := func(outcome, method string) logShape {
		return logShape{category: logging.CategoryAuthentication, action: actionSignIn, outcome: outcome,
			reason: storedReason(event.Detail), method: method, purpose: PurposeSignIn}
	}
	stepUp := func(outcome string) logShape {
		return logShape{category: logging.CategoryAudit, action: actionStepUp, outcome: outcome,
			reason: storedReason(event.Detail), purpose: PurposeStepUp}
	}
	switch event.Action {
	case ActionSignIn:
		return signIn(success, MethodOTP)
	case ActionSignInFailed:
		return signIn(failure, MethodOTP)
	case ActionStepUp:
		return stepUp(success)
	case ActionStepUpFailed:
		return stepUp(failure)
	case ActionRunnerLinkRedeem:
		return signIn(success, MethodDeviceLink)
	case ActionRunnerLinkFailed:
		return signIn(failure, MethodDeviceLink)
	case ActionPasscodeRequest:
		return codeRequest(event, logging.CategoryAuthentication, actionCodeRequest, PurposeSignIn)
	case ActionStepUpRequest:
		return codeRequest(event, logging.CategoryAudit, actionStepUpCodeRequest, PurposeStepUp)
	case ActionRunnerLinkCreate:
		// A device link is a one-time code that signs a new machine in, but
		// it is handed back rather than mailed, and auth.code.request counts
		// mailed codes: minting one is the trail's own business.
		return logShape{category: logging.CategoryAudit, action: event.Action, outcome: success,
			method: MethodDeviceLink}
	case ActionSignInPending:
		// The passcode was right and the authenticator code is still to come:
		// not a sign-in yet, so not counted as one.
		return logShape{category: logging.CategoryAudit, action: event.Action, outcome: success,
			method: MethodOTP, purpose: PurposeSignIn}
	case ActionSignOut:
		return logShape{category: logging.CategoryAuthentication, action: actionSignOut, outcome: success}
	case ActionTOTPEnable, ActionTOTPDisable, ActionTOTPReset, ActionTOTPRecoveryRegenerated,
		ActionAccountInvite, ActionAccountRole, ActionAccountRemove:
		return logShape{category: logging.CategoryIAM, action: actionAccountUpdate, outcome: success}
	case ActionTOTPFailed:
		// A refused code on the way to changing the account's second factor.
		return logShape{category: logging.CategoryIAM, action: actionAccountUpdate, outcome: failure,
			reason: storedReason(event.Detail)}
	case ActionRunnerAccessTokenSet, ActionRunnerAccessTokenClear:
		return logShape{category: logging.CategoryConfiguration, action: actionConfigChange, outcome: success}
	}
	if memberActions[event.Action] {
		return logShape{category: logging.CategoryIAM, action: actionAccountUpdate, outcome: success}
	}
	if settingsActions[event.Action] {
		return logShape{category: logging.CategoryConfiguration, action: actionConfigChange, outcome: success}
	}
	// Everything else, the enrollment that is not confirmed yet and the spent
	// recovery code among them, is the trail's own business. The request
	// middleware records only writes that succeeded; a merge a runner reports
	// as failed says so in its name. Its detail.reason is the runner's own
	// prose, which stays in the detail rather than becoming event.reason.
	outcome := success
	if strings.HasSuffix(event.Action, ".failed") {
		outcome = failure
	}
	// The middleware's actions hold the request path, so they are cleaned
	// like the column they are stored in.
	return logShape{category: logging.CategoryAudit, action: cleanString(event.Action), outcome: outcome}
}

// codeRequest is a passcode request: a success when a passcode was minted and
// handed to delivery, a failure otherwise. A passcode handed to delivery can
// still fail to go out; its line waits to say so (RecordOrLogLater).
func codeRequest(event Event, category, action, purpose string) logShape {
	shape := logShape{category: category, action: action, outcome: logging.OutcomeSuccess,
		method: MethodOTP, purpose: purpose}
	if delivered, _ := event.Detail["delivered"].(bool); !delivered {
		shape.outcome, shape.reason = logging.OutcomeFailure, "error"
	}
	return shape
}

// storedReason is detail.reason in the log's vocabulary, or "".
func storedReason(detail map[string]any) string {
	reason, _ := detail["reason"].(string)
	if name, ok := reasonNames[reason]; ok {
		return name
	}
	return reason
}

// logEvent writes the event's line. It is written whether or not the row then
// reaches the database.
func logEvent(ctx context.Context, event Event) {
	writeLine(ctx, logging.Request(ctx), event, nil)
}

// laterLine is the function RecordOrLogLater hands back: it writes the line
// of an event from the request ctx serves once its outcome is settled, after
// that request may have ended.
func laterLine(ctx context.Context, event Event) func(error) {
	request := logging.Request(ctx)
	return func(err error) { writeLine(context.Background(), request, event, err) }
}

// writeLine writes an event's line. request is the logging.Request of the
// request the event came from. failed, when not nil, is what made an event
// that was recorded as a success fail after all: the line is a failure, for
// reason error, with the error's text.
//
// Every value goes through the same cleaning the row's columns do, and the
// detail is the row's own sanitised JSON text, so the line can hold nothing
// the table would not. The JSON handler encodes each value, so nothing a
// client sent can end the line or add a field to it.
func writeLine(ctx context.Context, request slog.Attr, event Event, failed error) {
	shape := shapeOf(event)
	if failed != nil {
		shape.outcome, shape.reason = logging.OutcomeFailure, "error"
	}
	userID, userName := event.ActorID, event.ActorName
	if userID == "" && userName == "" {
		userID, userName = event.SubjectID, event.SubjectName
	}
	attrs := []slog.Attr{
		logging.Event(shape.category, shape.action, shape.outcome),
		logging.EventReason(cleanString(shape.reason)),
		logging.User(cleanString(userID), cleanString(userName)),
		logging.UserEmail(cleanString(event.ActorEmail)),
		logging.ClientIP(cleanString(event.ClientIP)),
		request,
		slog.String("nodevas.audit.action", cleanString(event.Action)),
	}
	optional := func(key, value string) {
		if value != "" {
			attrs = append(attrs, slog.String(key, value))
		}
	}
	optional("nodevas.audit.target", cleanString(event.Target))
	optional("nodevas.project", cleanColumn(event.Project))
	optional("nodevas.auth.method", shape.method)
	optional("nodevas.auth.purpose", shape.purpose)
	if detail, err := encodeDetail(event.Detail); err == nil {
		optional("nodevas.audit.detail", detail)
	}
	attrs = append(attrs, logging.Err(failed))
	slog.Default().LogAttrs(ctx, levelOf(event, shape), shape.action+" "+shape.outcome, attrs...)
}

// levelOf is the level of an event's line. A success is info. A failure is
// warn, the level every service on the host gives a refused sign-in, so the
// warnings a dashboard counts include them; and error when the fault was this
// server's (reason error), unless it was a client that stopped waiting.
func levelOf(event Event, shape logShape) slog.Level {
	switch {
	case shape.outcome != logging.OutcomeFailure:
		return slog.LevelInfo
	case shape.reason == "error" && !cancelled(event):
		return slog.LevelError
	default:
		return slog.LevelWarn
	}
}

// cancelled reports an event that failed because its client stopped waiting:
// its reason says so, the caller's or else the stored one.
func cancelled(event Event) bool {
	reason := event.Reason
	if reason == "" {
		reason, _ = event.Detail["reason"].(string)
	}
	return reason == "cancelled"
}
