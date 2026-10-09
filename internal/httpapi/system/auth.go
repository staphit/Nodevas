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
//
// "visitor" says whether the shared read-only credential is switched on, so
// the sign-in screen offers that door only when it leads somewhere. It is not
// a secret: the credential exists to be published, and a stranger learns
// nothing from it they could not learn by trying it.
func (a *API) getAuthStatus(c *gin.Context) {
	mode := "local"
	if a.auth.Remote() {
		mode = "accounts"
	}
	payload := map[string]any{
		"mode":          mode,
		"authenticated": true,
		"actor":         identity.Local,
		"visitor":       false,
	}
	if a.auth.Remote() {
		actor, err := a.auth.Authenticate(c.Request)
		payload["authenticated"] = err == nil
		payload["actor"] = actor
		if accounts, ok := a.auth.(*auth.SessionAuth); ok {
			payload["visitor"] = accounts.VisitorEnabled(c.Request.Context())
		}
	}
	c.JSON(http.StatusOK, payload)
}

// postLogin completes an account sign-in: the address and the passcode that
// was mailed to it. There is deliberately no name-and-password branch here,
// and no visitor branch either — the visitor has its own door, so this one
// only ever opens onto a named account.
func (a *API) postLogin(c *gin.Context) {
	accounts, ok := a.auth.(*auth.SessionAuth)
	if !ok {
		httpx.Err(c, http.StatusNotFound, errors.New("this server does not use accounts"))
		return
	}
	var body struct {
		Email string `json:"email"`
		OTP   string `json:"otp"`
	}
	if !httpx.DecodeJSONLimit(c, &body, auth.MaxLoginBodyBytes) {
		return
	}
	actor, token, csrf, err := accounts.LoginWithOTP(
		c.Request, strings.TrimSpace(body.Email), strings.TrimSpace(body.OTP))
	a.finishSignIn(c, actor, token, csrf, err)
}

// postVisitorLogin signs in with the shared read-only credential. It answers
// in exactly the shapes postLogin does, so the client handles both doors with
// one piece of code and a stranger learns nothing from which shape came back.
func (a *API) postVisitorLogin(c *gin.Context) {
	accounts, ok := a.auth.(*auth.SessionAuth)
	if !ok {
		httpx.Err(c, http.StatusNotFound, errors.New("this server does not use accounts"))
		return
	}
	var body struct {
		Pin      string `json:"pin"`
		Passcode string `json:"passcode"`
	}
	if !httpx.DecodeJSONLimit(c, &body, auth.MaxLoginBodyBytes) {
		return
	}
	actor, token, csrf, err := accounts.LoginVisitor(
		c.Request, strings.TrimSpace(body.Pin), strings.TrimSpace(body.Passcode))
	a.finishSignIn(c, actor, token, csrf, err)
}

// finishSignIn turns the outcome of either sign-in into the response, the
// audit line and the cookie pair. One function, so the two doors cannot drift
// apart in what they set or what they record.
func (a *API) finishSignIn(c *gin.Context, actor identity.Actor, token, csrf string, err error) {
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

// postRequestOTP emails a one-time passcode to the account registered with an
// address.
//
// It answers 202 whether or not the address is registered. Any other shape — a
// 404, a different message, a measurably different delay — turns this
// endpoint into a directory of who has an account here, and addresses are
// exactly the kind of thing an attacker already has a list of.
//
// The delay is the part that takes care. Sending mail takes seconds and
// looking up an unknown address takes microseconds, so delivery runs after
// the answer has been decided, on its own goroutine and its own deadline, and
// the 202 goes out without waiting for it.
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
		Email string `json:"email"`
	}
	if !httpx.DecodeJSONLimit(c, &body, auth.MaxLoginBodyBytes) {
		return
	}

	challenge, err := accounts.RequestOTP(c.Request, strings.TrimSpace(body.Email))
	// Recorded whatever the outcome. A run of requests for addresses that are
	// not registered is what somebody probing for accounts looks like, and it
	// is invisible to the client by design — the trail is the only place it
	// shows up. "delivered" has always meant a passcode was minted and handed
	// to delivery — the key is kept so older rows read the same — and whether
	// the relay then accepted it is the delivery log line's business.
	a.recordAuthEvent(c, "auth.passcode.request", identity.Actor{Name: challenge.Actor},
		map[string]any{"delivered": err == nil})
	if errors.Is(err, auth.ErrTooManyOTPRequests) {
		// The one failure worth telling the client about: the budgets are
		// charged identically for registered and unregistered addresses, so it
		// describes the server's budget rather than the address, and without
		// it a throttled person would sit waiting for mail that is not coming.
		httpx.Err(c, http.StatusTooManyRequests, err)
		return
	}
	if err == nil {
		// challenge.Email is the address stored on the account, never the
		// string in the request body: mail goes where the administrator
		// registered it, however the person at the door chose to type it.
		go a.deliverPasscode(challenge)
	}
	// Unknown address, malformed address, or a passcode that could not be
	// generated: none of these are the client's business, and a delivered one
	// looks the same from here.
	c.JSON(http.StatusAccepted, map[string]any{"ok": true})
}

// deliverPasscode sends one passcode. It owns its deadline rather than
// borrowing the request's: the request is answered, and its context is
// cancelled, before the relay has even been dialled. A failure reaches the
// operator through the log; it never reaches the client, because "the mail
// bounced" would confirm the address.
func (a *API) deliverPasscode(challenge auth.Challenge) {
	ctx, cancel := context.WithTimeout(context.Background(), otpMailTimeout)
	defer cancel()
	if err := a.mailer.Send(ctx, challenge.Email, otpSubject, otpBody(challenge)); err != nil {
		log.Printf("passcode delivery to %s failed: %v", challenge.Actor, err)
	}
}

// recordAuthEvent writes a sign-in-related event to the trail. These belong to
// no project, which is why they live in the database trail and had nowhere to
// go in the per-project files.
//
// The passcode never appears here, and neither does the address typed at the
// door. The audit package drops credential-shaped keys as a backstop, but a
// caller that relies on that has already written the credential into a
// structure it did not have to.
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
//
// The last lines are the account holder's alarm. Anyone can ask for a passcode
// to be sent to any address, so an unrequested one is not by itself a break-in
// — but it is worth an administrator knowing about, and the passcode is the
// only thing between that request and the account.
func otpBody(challenge auth.Challenge) string {
	return strings.Join([]string{
		"你的 Nodevas 登入驗證碼：",
		"",
		"    " + challenge.Code,
		"",
		fmt.Sprintf("這組驗證碼在 %s 前有效，只能使用一次。",
			challenge.Expires.Format("15:04")),
		"登入成功後，這個帳號在其他裝置上的登入會被登出。",
		"",
		"若不是你本人要求，請忽略這封信，不要把驗證碼告訴任何人，並通知管理員。",
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
