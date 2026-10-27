package system

import (
	"context"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"rsc.io/qr"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
)

// Two-factor authentication (an authenticator app, TOTP). See
// internal/auth/totp_session.go for what each step means.
//
//	POST /api/auth/login {email, otp}       → {ok:false, secondFactor:"totp", expiresAt}
//	                                          plus the HttpOnly sign-in ticket cookie,
//	                                          for an account with an authenticator
//	POST /api/auth/login/totp {code}        → {ok, actor} (code: authenticator or recovery)
//	GET  /api/auth/totp                     → auth.TOTPState
//	POST /api/auth/totp/begin               → {secret, uri, qr, account}   (needs step-up)
//	POST /api/auth/totp/confirm {code}      → {enabled:true, recoveryCodes}  (needs step-up)
//	POST /api/auth/totp/disable {code}      → {enabled:false}
//	POST /api/auth/totp/recovery-codes {code} → {recoveryCodes}
//
// Every response carrying a secret or recovery codes is no-store.

// signInTicketCookie carries a pending two-factor sign-in. HttpOnly: page
// script never needs it, so an injected script cannot lift it either. Its
// path is the one endpoint that reads it.
const (
	signInTicketCookie = "nodevas_signin"
	signInTicketPath   = "/api/auth/login/totp"
)

// answerPendingSignIn tells the browser the passcode was right and the
// authenticator code is next.
func (a *API) answerPendingSignIn(c *gin.Context, result auth.SignInResult) {
	a.recordAuthEvent(c, audit.ActionSignInPending, result.Actor, map[string]any{"secondFactor": auth.SecondFactorTOTP})
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     signInTicketCookie,
		Value:    result.Pending.Ticket,
		Path:     signInTicketPath,
		HttpOnly: true,
		Secure:   auth.RequestIsSecure(c.Request),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(auth.SignInTicketTTL.Seconds()),
	})
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, map[string]any{
		"ok":           false,
		"secondFactor": auth.SecondFactorTOTP,
		"expiresAt":    result.Pending.Expires.UTC().Format(time.RFC3339),
	})
}

func clearSignInTicket(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     signInTicketCookie,
		Value:    "",
		Path:     signInTicketPath,
		HttpOnly: true,
		Secure:   auth.RequestIsSecure(c.Request),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// postLoginTOTP finishes a two-factor sign-in.
func (a *API) postLoginTOTP(c *gin.Context) {
	accounts, ok := a.auth.(*auth.SessionAuth)
	if !ok {
		httpx.Err(c, http.StatusNotFound, errors.New("this server does not use accounts"))
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if !httpx.DecodeJSONLimit(c, &body, auth.MaxLoginBodyBytes) {
		return
	}
	ticket := ""
	if cookie, err := c.Request.Cookie(signInTicketCookie); err == nil {
		ticket = cookie.Value
	}
	code := strings.TrimSpace(body.Code)
	result, err := accounts.CompleteSignIn(c.Request, ticket, code)
	if err != nil {
		// The step takes an authenticator code or a recovery code; the log
		// line says which kind was refused, judged by its shape alone.
		method := authMethod(auth.SecondFactorKind(code))
		if errors.Is(err, auth.ErrSignInExpired) {
			clearSignInTicket(c)
			httpx.Coded(c, http.StatusUnauthorized, "SIGNIN_EXPIRED", err, nil)
			a.recordAuth(c, refusedSignIn("signin-expired", method, result.Refusal))
			return
		}
		a.finishSignIn(c, method, result, err, nil)
		return
	}
	clearSignInTicket(c)
	a.recordRecoveryUse(c, result.Actor.ID, result.Actor.Name, result.Proof, "signin")
	a.finishSignIn(c, authMethod(result.Proof.Method), result, nil,
		map[string]any{"secondFactor": result.Proof.Method})
}

// authMethod names what proved a sign-in step, or was offered to, in the
// log's words.
func authMethod(proof string) string {
	switch proof {
	case auth.SecondFactorTOTP:
		return audit.MethodTOTP
	case auth.SecondFactorRecovery:
		return audit.MethodRecoveryCode
	default:
		// auth.StepUpMethodEmail, or a one-factor sign-in: the mailed passcode.
		return audit.MethodOTP
	}
}

// recordRecoveryUse writes the trail line for a spent recovery code. It is a
// separate event from the action it enabled, because "a recovery code was
// used" is the thing an account holder who still has their phone needs to
// find.
func (a *API) recordRecoveryUse(c *gin.Context, actorID, actorName string, proof auth.SecondFactorProof, purpose string) {
	if proof.Method != auth.SecondFactorRecovery {
		return
	}
	a.recordAuthEventAs(c, audit.ActionTOTPRecoveryUsed, actorID, actorName, map[string]any{
		"purpose": purpose, "remaining": proof.RecoveryCodesLeft,
	})
}

// recordAuthEventAs is recordAuthEvent for a caller holding the actor's ID
// and name rather than an identity.Actor.
func (a *API) recordAuthEventAs(c *gin.Context, action, actorID, actorName string, detail map[string]any) {
	a.recordAuth(c, audit.Event{ActorID: actorID, ActorName: actorName, Action: action, Detail: detail})
}

// totpAccounts is the accounts authenticator, or the answer for a server
// without one.
func (a *API) totpAccounts(c *gin.Context) (*auth.SessionAuth, bool) {
	accounts, ok := a.auth.(*auth.SessionAuth)
	if !ok {
		httpx.Err(c, http.StatusNotFound, errors.New("this server does not use accounts, so there is no second factor to set up"))
		return nil, false
	}
	return accounts, true
}

// totpError answers a failed two-factor request.
func (a *API) totpError(c *gin.Context, err error, purpose string) {
	actor := auth.ActorFrom(c.Request)
	switch {
	case errors.Is(err, auth.ErrPersonOnly):
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", err, nil)
	case errors.Is(err, auth.ErrUnauthenticated):
		httpx.Err(c, http.StatusUnauthorized, err)
	case errors.Is(err, auth.ErrStepUpRequired):
		httpx.Coded(c, http.StatusForbidden, "STEP_UP_REQUIRED", err, nil)
	case errors.Is(err, auth.ErrTooManyLogins):
		a.recordAuthEvent(c, audit.ActionTOTPFailed, actor, map[string]any{"purpose": purpose, "reason": "throttled"})
		httpx.Err(c, http.StatusTooManyRequests, err)
	case errors.Is(err, auth.ErrTOTPCode):
		a.recordAuthEvent(c, audit.ActionTOTPFailed, actor, map[string]any{"purpose": purpose, "reason": "bad-code"})
		httpx.Coded(c, http.StatusUnauthorized, "BAD_CODE", err, nil)
	case errors.Is(err, auth.ErrTOTPAlreadyEnabled), errors.Is(err, auth.ErrTOTPNotEnabled),
		errors.Is(err, auth.ErrTOTPNoEnrollment), errors.Is(err, auth.ErrTOTPUnreadable):
		httpx.Coded(c, http.StatusConflict, "FAILED_PRECONDITION", err, nil)
	default:
		httpx.Err(c, http.StatusInternalServerError, err)
	}
}

func (a *API) getTOTP(c *gin.Context) {
	accounts, ok := a.totpAccounts(c)
	if !ok {
		return
	}
	state, err := accounts.TOTPStatus(c.Request)
	if err != nil {
		a.totpError(c, err, "status")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, state)
}

func (a *API) postTOTPBegin(c *gin.Context) {
	accounts, ok := a.totpAccounts(c)
	if !ok {
		return
	}
	enrollment, err := accounts.BeginTOTP(c.Request)
	if err != nil {
		a.totpError(c, err, "enroll")
		return
	}
	image, err := qrDataURL(enrollment.URI)
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	a.recordAuthEvent(c, audit.ActionTOTPEnrollBegin, auth.ActorFrom(c.Request), nil)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, map[string]any{
		"secret":  enrollment.Secret,
		"uri":     enrollment.URI,
		"qr":      image,
		"account": enrollment.Account,
		"issuer":  auth.TOTPIssuer,
		"digits":  auth.TOTPDigits,
		"period":  int(auth.TOTPPeriod / time.Second),
	})
}

// codeBody reads the {code} every confirming request carries.
func codeBody(c *gin.Context) (string, bool) {
	var body struct {
		Code string `json:"code"`
	}
	if !httpx.DecodeJSONLimit(c, &body, auth.MaxLoginBodyBytes) {
		return "", false
	}
	return strings.TrimSpace(body.Code), true
}

func (a *API) postTOTPConfirm(c *gin.Context) {
	accounts, ok := a.totpAccounts(c)
	if !ok {
		return
	}
	code, ok := codeBody(c)
	if !ok {
		return
	}
	codes, err := accounts.ConfirmTOTP(c.Request, code)
	if err != nil {
		a.totpError(c, err, "enable")
		return
	}
	a.recordAuthEvent(c, audit.ActionTOTPEnable, auth.ActorFrom(c.Request), nil)
	a.noticeTOTPChange(c, accounts, "Two-factor authentication was turned on for your Nodevas account.")
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, map[string]any{"enabled": true, "recoveryCodes": codes})
}

func (a *API) postTOTPDisable(c *gin.Context) {
	accounts, ok := a.totpAccounts(c)
	if !ok {
		return
	}
	code, ok := codeBody(c)
	if !ok {
		return
	}
	proof, err := accounts.DisableTOTP(c.Request, code)
	if err != nil {
		a.totpError(c, err, "disable")
		return
	}
	actor := auth.ActorFrom(c.Request)
	a.recordRecoveryUse(c, actor.ID, actor.Name, proof, "disable")
	a.recordAuthEvent(c, audit.ActionTOTPDisable, actor, map[string]any{"method": proof.Method})
	a.noticeTOTPChange(c, accounts, "Two-factor authentication was turned off for your Nodevas account.")
	c.JSON(http.StatusOK, map[string]any{"enabled": false})
}

func (a *API) postTOTPRecoveryCodes(c *gin.Context) {
	accounts, ok := a.totpAccounts(c)
	if !ok {
		return
	}
	code, ok := codeBody(c)
	if !ok {
		return
	}
	codes, err := accounts.RegenerateRecoveryCodes(c.Request, code)
	if err != nil {
		a.totpError(c, err, "recovery-codes")
		return
	}
	a.recordAuthEvent(c, audit.ActionTOTPRecoveryRegenerated, auth.ActorFrom(c.Request), nil)
	a.noticeTOTPChange(c, accounts, "New recovery codes were made for your Nodevas account; the old ones no longer work.")
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, map[string]any{"recoveryCodes": codes})
}

// noticeTOTPChange mails the account holder about a change to their second
// factor, so a change they did not make (a stolen session, a borrowed laptop)
// does not go unseen. Like a passcode it is sent after the answer, on its own
// deadline, and a failure only reaches the log.
func (a *API) noticeTOTPChange(c *gin.Context, accounts *auth.SessionAuth, what string) {
	if a.mailer == nil {
		return
	}
	actor := auth.ActorFrom(c.Request)
	to := accounts.AccountEmail(c.Request.Context(), actor.ID)
	if to == "" {
		return
	}
	body := what + "\n\nWhen: " + time.Now().UTC().Format(time.RFC1123) + "\nFrom: " + auth.ClientIP(c.Request) +
		"\n\nIf this was not you, sign in, check Settings > Account, and ask an administrator to reset " +
		"two-factor authentication (nodevas account totp-reset) if you are locked out.\n"
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), otpMailTimeout)
		defer cancel()
		if err := a.mailer.Send(ctx, to, "Nodevas: two-factor authentication changed", body); err != nil {
			log.Printf("two-factor notice to %s failed: %v", actor.ID, err)
		}
	}()
}

// qrDataURL draws the otpauth:// URI as a QR code, as a PNG data URL the page
// can show without loading anything: the secret never goes to a third-party
// QR service, and the page needs no QR library.
func qrDataURL(uri string) (string, error) {
	code, err := qr.Encode(uri, qr.M)
	if err != nil {
		return "", err
	}
	code.Scale = 6
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG()), nil
}
