package system

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/runnerwire"
)

// Step-up (W6): a signed-in person re-proves, with a fresh mailed passcode,
// that they still hold the account, so their remote dispatches may be
// auto-accepted by a runner for a while. See internal/auth/stepup.go.
//
//	GET  /api/auth/step-up          → runnerwire.StepUpStatus
//	POST /api/auth/step-up/request  → 202; mails a passcode to the account
//	POST /api/auth/step-up {code}   → runnerwire.StepUpStatus, now active
//
// A loopback server has no accounts: it reports Required false, treats
// everyone as stepped up and has nothing to request or verify.

// getStepUp reports the caller's step-up.
func (a *API) getStepUp(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	accounts, ok := a.auth.(*auth.SessionAuth)
	if !ok {
		c.JSON(http.StatusOK, runnerwire.StepUpStatus{Required: false, Active: true})
		return
	}
	c.JSON(http.StatusOK, stepUpStatus(accounts, c.Request))
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
	if a.mailer == nil {
		httpx.Err(c, http.StatusServiceUnavailable, auth.ErrNoMailer)
		return
	}
	actor := auth.ActorFrom(c.Request)
	challenge, err := accounts.RequestStepUp(c.Request)
	a.recordAuthEvent(c, "auth.stepup.request", actor, map[string]any{"delivered": err == nil})
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
	go a.deliverStepUp(challenge)
	c.JSON(http.StatusAccepted, map[string]any{"ok": true})
}

func (a *API) deliverStepUp(challenge auth.Challenge) {
	ctx, cancel := context.WithTimeout(context.Background(), otpMailTimeout)
	defer cancel()
	if err := a.mailer.Send(ctx, challenge.Email, stepUpSubject, stepUpBody(challenge)); err != nil {
		log.Printf("step-up passcode delivery to %s failed: %v", challenge.Actor, err)
	}
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
	until, err := accounts.VerifyStepUp(c.Request, strings.TrimSpace(body.Code))
	if err != nil {
		status, reason := http.StatusUnauthorized, "bad-code"
		switch {
		case errors.Is(err, auth.ErrTooManyLogins):
			status, reason = http.StatusTooManyRequests, "throttled"
		case errors.Is(err, auth.ErrUnauthenticated):
			reason = "no-session"
		}
		a.recordAuthEvent(c, "auth.stepup.failed", actor, map[string]any{"reason": reason})
		httpx.Err(c, status, err)
		return
	}
	a.recordAuthEvent(c, "auth.stepup", actor, map[string]any{"until": until.UTC().Format(time.RFC3339)})
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, runnerwire.StepUpStatus{
		Required: true, Active: true, Until: until.UTC().Format(time.RFC3339),
	})
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
