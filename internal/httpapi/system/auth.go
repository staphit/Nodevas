package system

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"nodevas/internal/audit"
	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/identity"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// authMode describes how the server identifies callers, so the UI can show a
// login screen instead of a wall of 401s.
func (a *API) getAuthStatus(c *gin.Context) {
	mode := "local"
	if a.auth.Remote() {
		mode = "accounts"
	}
	payload := map[string]any{
		"mode":          mode,
		"authenticated": true,
		"actor":         identity.Local,
	}
	if a.auth.Remote() {
		actor, err := a.auth.Authenticate(c.Request)
		payload["authenticated"] = err == nil
		payload["actor"] = actor
	}
	c.JSON(http.StatusOK, payload)
}

func (a *API) postLogin(c *gin.Context) {
	accounts, ok := a.auth.(*auth.SessionAuth)
	if !ok {
		httpx.Err(c, http.StatusNotFound, errors.New("this server does not use accounts"))
		return
	}
	var body struct {
		Pin string `json:"pin"`
		OTP string `json:"otp"`
	}
	if !httpx.DecodeJSONLimit(c, &body, auth.MaxLoginBodyBytes) {
		return
	}
	// Both factors, always. There is deliberately no name-and-password branch
	// left here: one would be a way past the passcode, and a second way in is
	// the same as no second factor.
	actor, token, csrf, err := accounts.LoginWithOTP(
		c.Request, strings.TrimSpace(body.Pin), strings.TrimSpace(body.OTP))
	if err != nil {
		status := http.StatusUnauthorized
		switch {
		case errors.Is(err, auth.ErrTooManyLogins):
			status = http.StatusTooManyRequests
		case errors.Is(err, auth.ErrTooManyActiveUsers):
			// 409, not 403: nothing is wrong with the credentials, the server
			// is simply full, and the person can get in when a seat frees.
			status = http.StatusConflict
		case errors.Is(err, auth.ErrSessionPersistence):
			status = http.StatusInternalServerError
		}
		// A failed sign-in is the event an operator most wants after the fact,
		// and the one with no actor to attribute it to, so the address it came
		// from is all there is to record. The reason is a category, never the
		// credential that was offered.
		a.recordAuthEvent(c, "auth.signin.failed", identity.Actor{}, map[string]any{
			"reason": authFailureReason(err),
		})
		httpx.Err(c, status, err)
		return
	}
	a.recordAuthEvent(c, "auth.signin", actor, nil)
	secure := auth.RequestIsSecure(c.Request)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(auth.SessionTTL.Seconds()),
	})
	// The CSRF cookie is readable by same-origin script on purpose: the check
	// is that the value comes back in a header, which cross-site pages cannot
	// set.
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     auth.CSRFCookieName,
		Value:    csrf,
		Path:     "/",
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(auth.SessionTTL.Seconds()),
	})
	c.JSON(http.StatusOK, map[string]any{"ok": true, "actor": actor})
}

// postRequestOTP emails a one-time passcode to the address bound to a PIN.
//
// It answers 202 whether or not the PIN exists. Any other shape — a 404, a
// different message, a measurably different delay — turns this endpoint into
// an oracle that finds valid PINs without ever needing the mailbox, which is
// the one thing the second factor is there to prevent.
func (a *API) postRequestOTP(c *gin.Context) {
	accounts, ok := a.auth.(*auth.SessionAuth)
	if !ok {
		httpx.Err(c, http.StatusNotFound, errors.New("this server does not use accounts"))
		return
	}
	if a.mailer == nil {
		httpx.Err(c, http.StatusServiceUnavailable, auth.ErrNoMailer)
		return
	}
	var body struct {
		Pin string `json:"pin"`
	}
	if !httpx.DecodeJSONLimit(c, &body, auth.MaxLoginBodyBytes) {
		return
	}

	accepted := map[string]any{"ok": true}
	challenge, err := accounts.RequestOTP(c.Request, strings.TrimSpace(body.Pin))
	// Recorded whatever the outcome. A run of requests against PINs that do not
	// exist is what somebody probing for one looks like, and it is invisible to
	// the client by design — the trail is the only place it shows up.
	a.recordAuthEvent(c, "auth.passcode.request", identity.Actor{Name: challenge.Actor},
		map[string]any{"delivered": err == nil})
	switch {
	case errors.Is(err, auth.ErrTooManyOTPRequests):
		// The one failure worth telling the client about: it describes the
		// server's budget, not whether the PIN was real, and without it a
		// throttled person would sit waiting for mail that is not coming.
		httpx.Err(c, http.StatusTooManyRequests, err)
		return
	case err != nil:
		// Unknown PIN, no address on file, or a passcode that could not be
		// generated. None of these are the client's business.
		c.JSON(http.StatusAccepted, accepted)
		return
	}

	// Delivery happens on the request so a mail failure is not silent to the
	// operator, but its outcome still does not reach the client: "the mail
	// bounced" would confirm the PIN.
	ctx, cancel := context.WithTimeout(c.Request.Context(), otpMailTimeout)
	defer cancel()
	if err := a.mailer.Send(ctx, challenge.Email, otpSubject, otpBody(challenge)); err != nil {
		log.Printf("passcode delivery to %s failed: %v", challenge.Actor, err)
	}
	c.JSON(http.StatusAccepted, accepted)
}

// recordAuthEvent writes a sign-in-related event to the trail. These belong to
// no project, which is why they live in the database trail and had nowhere to
// go in the per-project files.
//
// The PIN and the passcode never appear here. The audit package drops
// credential-shaped keys as a backstop, but a caller that relies on that has
// already written the credential into a structure it did not have to.
func (a *API) recordAuthEvent(c *gin.Context, action string, actor identity.Actor, detail map[string]any) {
	if a.audit == nil {
		return
	}
	a.audit.RecordOrLog(c.Request.Context(), audit.Event{
		At:        time.Now(),
		ActorID:   actor.ID,
		ActorName: actor.Name,
		Action:    action,
		ClientIP:  auth.ClientIP(c.Request),
		Detail:    detail,
	})
}

// authFailureReason turns a sign-in error into a category safe to store. The
// error itself is written for the person at the keyboard and must not become a
// permanent record of what was tried.
func authFailureReason(err error) string {
	switch {
	case errors.Is(err, auth.ErrTooManyLogins):
		return "throttled"
	case errors.Is(err, auth.ErrTooManyActiveUsers):
		return "seat-limit"
	case errors.Is(err, auth.ErrSessionPersistence):
		return "persistence"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		// The client stopped waiting before the account lookup finished. No
		// credential was rejected, and filing it as one would leave the trail
		// showing sign-in attempts that were never judged.
		return "cancelled"
	default:
		return "bad-credentials"
	}
}

const otpMailTimeout = 20 * time.Second

const otpSubject = "Nodevas 登入驗證碼"

// otpBody is plain text on purpose: an HTML mail invites a client to make the
// code a link, and there is nothing here worth styling.
func otpBody(challenge auth.Challenge) string {
	return strings.Join([]string{
		"你的 Nodevas 登入驗證碼：",
		"",
		"    " + challenge.Code,
		"",
		fmt.Sprintf("這組驗證碼在 %s 前有效，只能使用一次。",
			challenge.Expires.Format("15:04")),
		"",
		"若不是你本人要求，請立即聯絡管理員更換 PIN — 對方可能已經知道你的 PIN。",
		"這封信不會包含你的 PIN，Nodevas 也永遠不會用信件索取 PIN。",
	}, "\n")
}

func (a *API) postLogout(c *gin.Context) {
	if accounts, ok := a.auth.(*auth.SessionAuth); ok {
		if cookie, err := c.Request.Cookie(auth.SessionCookieName); err == nil {
			if err := accounts.Logout(cookie.Value); err != nil {
				httpx.Err(c, http.StatusInternalServerError,
					fmt.Errorf("sign out could not be persisted: %w", err))
				return
			}
		}
	}
	for _, name := range []string{auth.SessionCookieName, auth.CSRFCookieName} {
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: name == auth.SessionCookieName,
			Secure:   auth.RequestIsSecure(c.Request),
			SameSite: http.SameSiteStrictMode,
			MaxAge:   -1,
		})
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true})
}
