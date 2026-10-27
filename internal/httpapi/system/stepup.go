package system

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/runnerwire"
	"nodevas/internal/safego"
)

// Step-up (W6): a signed-in person re-proves, with a fresh mailed passcode,
// that they still hold the account, so their remote dispatches may be
// auto-accepted by a runner for a while. See internal/auth/stepup.go.
//
//	GET  /api/auth/step-up          → runnerwire.StepUpStatus + method ("email"|"totp")
//	POST /api/auth/step-up/request  → 202 {method:"email"}; mails a passcode to the account,
//	                                  or 200 {method:"totp"} and nothing is sent
//	POST /api/auth/step-up {code}   → runnerwire.StepUpStatus + method, now active
//
// An account with an authenticator steps up with an authenticator (or
// recovery) code instead of a mailed passcode.
//
// A loopback server has no accounts: it reports Required false, treats
// everyone as stepped up and has nothing to request or verify.

// stepUpStatusBody is runnerwire.StepUpStatus plus how this session steps
// up, so the dialog can ask for an authenticator code without first asking
// for a mail that will never come. Embedded, so the wire shape stays a
// superset of runnerwire.StepUpStatus.
type stepUpStatusBody struct {
	runnerwire.StepUpStatus
	// Method is "email" or "totp"; absent on a server without accounts.
	Method string `json:"method,omitempty"`
}

// getStepUp reports the caller's step-up.
func (a *API) getStepUp(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	accounts, ok := a.auth.(*auth.SessionAuth)
	if !ok {
		c.JSON(http.StatusOK, runnerwire.StepUpStatus{Required: false, Active: true})
		return
	}
	body := stepUpStatusBody{StepUpStatus: stepUpStatus(accounts, c.Request)}
	// An agent or a request without a session has no method to report; the
	// status already says it is not stepped up.
	if method, err := accounts.StepUpMethod(c.Request); err == nil {
		body.Method = method
	}
	c.JSON(http.StatusOK, body)
}

func stepUpStatus(accounts *auth.SessionAuth, r *http.Request) runnerwire.StepUpStatus {
	status := runnerwire.StepUpStatus{Required: true}
	if active, until := accounts.StepUpState(r); active {
		status.Active = true
		status.Until = until.UTC().Format(time.RFC3339)
	}
	return status
}

// stepUpAccounts is the accounts authenticator, or answers for a server
// without one, or for an agent.
func (a *API) stepUpAccounts(c *gin.Context) (*auth.SessionAuth, bool) {
	accounts, ok := a.auth.(*auth.SessionAuth)
	if !ok {
		httpx.Err(c, http.StatusNotFound, errors.New("this server does not use accounts, so there is nothing to step up"))
		return nil, false
	}
	if auth.ActorFrom(c.Request).IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", auth.ErrStepUpAgent, nil)
		return nil, false
	}
	return accounts, true
}

// postStepUpRequest mails a step-up passcode to the signed-in account. Unlike
// the sign-in request it can say what went wrong: the caller is already
// signed in as the account, so there is nothing to enumerate.
func (a *API) postStepUpRequest(c *gin.Context) {
	accounts, ok := a.stepUpAccounts(c)
	if !ok {
		return
	}
	actor := auth.ActorFrom(c.Request)
	// An account with an authenticator steps up with it: nothing to mail, so
	// a server without a relay can still step it up.
	if method, err := accounts.StepUpMethod(c.Request); err == nil && method == auth.SecondFactorTOTP {
		c.JSON(http.StatusOK, map[string]any{"ok": true, "method": auth.SecondFactorTOTP})
		return
	}
	if a.mailer == nil {
		httpx.Err(c, http.StatusServiceUnavailable, auth.ErrNoMailer)
		return
	}
	challenge, err := accounts.RequestStepUp(c.Request)
	if err == nil && challenge.Method == auth.SecondFactorTOTP {
		// Turned on between the two reads above: still nothing to send.
		c.JSON(http.StatusOK, map[string]any{"ok": true, "method": auth.SecondFactorTOTP})
		return
	}
	requested := audit.Event{
		ActorID: actor.ID, ActorName: actor.Name, Action: audit.ActionStepUpRequest,
		Detail: map[string]any{"delivered": err == nil}, Reason: deliveryFailureReason(c.Request.Context(), err),
	}
	if err != nil {
		a.recordAuth(c, requested)
	}
	switch {
	case err == nil:
	case errors.Is(err, auth.ErrTooManyOTPRequests):
		httpx.Err(c, http.StatusTooManyRequests, err)
		return
	case errors.Is(err, auth.ErrStepUpNoEmail):
		httpx.Coded(c, http.StatusConflict, "FAILED_PRECONDITION", err, nil)
		return
	case errors.Is(err, auth.ErrUnauthenticated):
		httpx.Err(c, http.StatusUnauthorized, err)
		return
	default:
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	// Like a sign-in passcode's: the row now, the line once the relay has
	// answered.
	sent := a.recordAuthLater(c, requested)
	safego.Go("step-up mail", func() { a.deliverStepUp(challenge, sent) })
	c.JSON(http.StatusAccepted, map[string]any{"ok": true, "method": auth.StepUpMethodEmail})
}

// deliverStepUp is deliverPasscode for a step-up passcode.
func (a *API) deliverStepUp(challenge auth.Challenge, sent func(error)) {
	ctx, cancel := context.WithTimeout(context.Background(), otpMailTimeout)
	defer cancel()
	sent(a.mailer.Send(ctx, challenge.Email, stepUpSubject, stepUpBody(challenge)))
}

// postStepUp verifies a step-up passcode and marks the session stepped up.
func (a *API) postStepUp(c *gin.Context) {
	accounts, ok := a.stepUpAccounts(c)
	if !ok {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if !httpx.DecodeJSONLimit(c, &body, auth.MaxLoginBodyBytes) {
		return
	}
	actor := auth.ActorFrom(c.Request)
	code := strings.TrimSpace(body.Code)
	result, err := accounts.VerifyStepUpProof(c.Request, code)
	if err != nil {
		status, reason := http.StatusUnauthorized, "bad-code"
		switch {
		case errors.Is(err, auth.ErrTooManyLogins):
			status, reason = http.StatusTooManyRequests, "throttled"
		case errors.Is(err, auth.ErrUnauthenticated):
			reason = "no-session"
		case errors.Is(err, auth.ErrStepUpCode), errors.Is(err, auth.ErrTOTPCode):
		case errors.Is(err, auth.ErrTOTPUnreadable):
			status, reason = http.StatusConflict, "totp-unreadable"
		default:
			status, reason = http.StatusInternalServerError, "error"
		}
		a.recordAuth(c, audit.Event{
			ActorID: actor.ID, ActorName: actor.Name, Action: audit.ActionStepUpFailed,
			Detail: map[string]any{"reason": reason}, AuthMethod: stepUpAttemptMethod(accounts, c.Request, code),
		})
		httpx.Err(c, status, err)
		return
	}
	until := result.Until.UTC().Format(time.RFC3339)
	a.recordRecoveryUse(c, actor.ID, actor.Name, auth.SecondFactorProof{
		Method: result.Method, RecoveryCodesLeft: result.RecoveryCodesLeft}, "stepup")
	a.recordAuth(c, audit.Event{
		ActorID: actor.ID, ActorName: actor.Name, Action: audit.ActionStepUp,
		Detail: map[string]any{"until": until, "method": result.Method}, AuthMethod: authMethod(result.Method),
	})
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, stepUpStatusBody{
		StepUpStatus: runnerwire.StepUpStatus{Required: true, Active: true, Until: until},
		Method:       stepUpMethodFor(result.Method),
	})
}

// stepUpAttemptMethod is what a refused step-up code was offered as, for the
// log line: the mailed passcode, or for an account with an authenticator,
// whichever kind of code it is shaped like. "" when the session cannot say.
func stepUpAttemptMethod(accounts *auth.SessionAuth, r *http.Request, code string) string {
	method, err := accounts.StepUpMethod(r)
	switch {
	case err != nil:
		return ""
	case method == auth.SecondFactorTOTP:
		return authMethod(auth.SecondFactorKind(code))
	default:
		return audit.MethodOTP
	}
}

// stepUpMethodFor is the method a session steps up with, given what proved
// this step-up: a recovery code stands in for the authenticator.
func stepUpMethodFor(proved string) string {
	if proved == auth.SecondFactorRecovery {
		return auth.SecondFactorTOTP
	}
	return proved
}

const stepUpSubject = "Nodevas 派工驗證碼"

// stepUpBody is the step-up mail: like the sign-in one, plain text, and it
// says what the passcode unlocks so an unexpected one reads as the alarm it
// is.
func stepUpBody(challenge auth.Challenge) string {
	return strings.Join([]string{
		"你的 Nodevas 派工驗證碼（step-up）：",
		"",
		"    " + challenge.Code,
		"",
		fmt.Sprintf("這組驗證碼在 %s 前有效，只能使用一次。", challenge.Expires.Format("15:04")),
		"輸入後，這個瀏覽器在一段時間內派出的工作可以由 runner 自動接受。",
		"",
		"若不是你本人要求，代表有人正在使用你的登入，請立刻重新登入（會登出其他裝置）並通知管理員。",
	}, "\n")
}
