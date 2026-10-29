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
	"nodevas/internal/logging"
	"nodevas/internal/safego"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/locale"
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

// postLogin completes an account sign-in: the address and the passcode that
// was mailed to it. There is deliberately no name-and-password branch here:
// the only way in is a named account.
func (a *API) postLogin(c *gin.Context) {
	accounts, ok := a.auth.(*auth.SessionAuth)
	if !ok {
		httpx.Err(c, http.StatusNotFound, locale.Errorf("this server does not use accounts", "這個伺服器不使用帳號"))
		return
	}
	var body struct {
		Email string `json:"email"`
		OTP   string `json:"otp"`
	}
	if !httpx.DecodeJSONLimit(c, &body, auth.MaxLoginBodyBytes) {
		return
	}
	result, err := accounts.SignInWithOTP(
		c.Request, strings.TrimSpace(body.Email), strings.TrimSpace(body.OTP))
	if err == nil && result.Pending != nil {
		a.answerPendingSignIn(c, result)
		return
	}
	if err != nil && result.Refusal.Account.ID == "" {
		// No account was read, so there is no registered address to name.
		result.Refusal.Email = auth.TypedAddress(body.Email)
	}
	a.finishSignIn(c, audit.MethodOTP, result, err, nil)
}

// finishSignIn turns the outcome of a sign-in into the response, the audit
// line and the cookie pair. method is what the step asked for, in the log's
// words (audit.MethodOTP, ...).
func (a *API) finishSignIn(c *gin.Context, method string, result auth.SignInResult, err error, detail map[string]any) {
	if err != nil {
		ctx := c.Request.Context()
		status, refusal, answer := http.StatusUnauthorized, result.Refusal, err
		switch {
		case errors.Is(err, auth.ErrTooManyLogins):
			status = http.StatusTooManyRequests
		case errors.Is(err, auth.ErrTooManyActiveUsers):
			// 409, not 403: nothing is wrong with the credentials, the server
			// is simply full, and the person can get in when a seat frees.
			status = http.StatusConflict
		case errors.Is(err, auth.ErrSessionPersistence):
			status = http.StatusInternalServerError
		case errors.Is(err, auth.ErrTOTPUnreadable):
			// Nothing the person can type fixes it; the message says who can.
			status = http.StatusConflict
		case errors.Is(err, auth.ErrBadCredentials), errors.Is(err, auth.ErrTOTPCode),
			errors.Is(err, auth.ErrSignInExpired):
		case stoppedWaiting(ctx, err):
			// Nobody is left to read the answer, and the error may be a
			// driver's, so what goes back is only that the request ended.
			answer = ctx.Err()
		default:
			// A database read that failed mid sign-in, say: the server's
			// fault, and its text is not the client's business. Its row has
			// always filed it under bad-credentials; its log line says error,
			// so an alert on refused credentials does not fire for an outage.
			status = http.StatusInternalServerError
			refusal.Reason = "error"
		}
		// A failed sign-in is the event an operator most wants after the fact,
		// and the one with no actor to attribute it to, so the address it came
		// from is all its row records; its log line also says whose account
		// the attempt named (refusedSignIn). The reason is a category, never
		// the credential that was offered.
		a.recordAuth(c, refusedSignIn(authFailureReason(ctx, err), method, refusal))
		httpx.Err(c, status, answer)
		return
	}
	actor := result.Actor
	signedIn := a.authEvent(c, audit.Event{
		ActorID: actor.ID, ActorName: actor.Name, Action: audit.ActionSignIn, Detail: detail, AuthMethod: method,
	})
	if a.audit != nil {
		a.audit.RecordOrLog(c.Request.Context(), signedIn)
	}
	// The request log line for this request names who it signed in.
	logging.SetUser(c.Request.Context(), actor.ID, actor.Name)
	logging.SetUserEmail(c.Request.Context(), signedIn.ActorEmail)
	secure := auth.RequestIsSecure(c.Request)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    result.Token,
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
		Value:    result.CSRF,
		Path:     "/",
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(auth.SessionTTL.Seconds()),
	})
	c.JSON(http.StatusOK, map[string]any{"ok": true, "actor": actor})
}

// refusedSignIn is the event for a refused sign-in step. Its row is what such
// rows have always been: no actor, and the stored reason. Its line also names
// the account the attempt was for, when there was one, or else the address
// typed, and the refusal's finer reason: an unknown address, a passcode that
// had run out, the guess that used one up.
func refusedSignIn(reason, method string, refusal auth.SignInRefusal) audit.Event {
	return audit.Event{
		Action:      audit.ActionSignInFailed,
		Detail:      map[string]any{"reason": reason},
		AuthMethod:  method,
		Reason:      refusal.Reason,
		SubjectID:   refusal.Account.ID,
		SubjectName: refusal.Account.Name,
		ActorEmail:  refusal.Email,
	}
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
		httpx.Err(c, http.StatusNotFound, locale.Errorf("this server does not use accounts", "這個伺服器不使用帳號"))
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
	// to delivery — the key is kept so older rows read the same. The log line
	// says whether one went out, and if none did, why; it names the account's
	// own address, or with no account found, the one typed, once it parsed as
	// an address.
	requested := audit.Event{
		ActorName:  challenge.Actor,
		ActorEmail: challenge.Email,
		Action:     audit.ActionPasscodeRequest,
		Detail:     map[string]any{"delivered": err == nil},
		Reason:     deliveryFailureReason(c.Request.Context(), err),
	}
	if requested.ActorEmail == "" {
		requested.ActorEmail = auth.TypedAddress(body.Email)
	}
	if err != nil {
		a.recordAuth(c, requested)
	}
	if requested.Reason == "error" {
		// The line says only that the server failed; the cause is for the
		// operator, as a failed sign-in's is, and never for the client, which
		// gets the same 202 below. No passcode is in it: what failed is the
		// account read or the generator, before any passcode existed.
		log.Printf("passcode request failed: %v", err)
	}
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
		// The row is written now; the line once the relay has answered.
		sent := a.recordAuthLater(c, requested)
		language := httpx.Language(c.Request)
		safego.Go("passcode mail", func() { a.deliverPasscode(challenge, language, sent) })
	}
	// Unknown address, malformed address, or a passcode that could not be
	// generated: none of these are the client's business, and a delivered one
	// looks the same from here.
	c.JSON(http.StatusAccepted, map[string]any{"ok": true})
}

// deliverPasscode sends one passcode and hands sent the relay's answer, which
// writes the request's log line. It owns its deadline rather than borrowing
// the request's: the request is answered, and its context is cancelled,
// before the relay has even been dialled. A failure reaches the operator
// through that line; it never reaches the client, because "the mail bounced"
// would confirm the address. The error says what the relay answered; the
// message itself, passcode and all, is not in it.
//
// It is written in the language of the browser that asked: that is the
// person about to type the passcode.
func (a *API) deliverPasscode(challenge auth.Challenge, language locale.Language, sent func(error)) {
	ctx, cancel := context.WithTimeout(context.Background(), otpMailTimeout)
	defer cancel()
	sent(a.mailer.Send(ctx, challenge.Email, otpSubject(language), otpBody(challenge, language)))
}

// recordAuthEvent writes a sign-in-related event to the trail. These belong to
// no project, which is why they live in the database trail and had nowhere to
// go in the per-project files.
//
// The passcode never appears here, and the row never holds the address typed
// at the door. The audit package drops credential-shaped keys as a backstop,
// but a caller that relies on that has already written the credential into a
// structure it did not have to.
func (a *API) recordAuthEvent(c *gin.Context, action string, actor identity.Actor, detail map[string]any) {
	a.recordAuth(c, audit.Event{ActorID: actor.ID, ActorName: actor.Name, Action: action, Detail: detail})
}

// recordAuth fills in what every sign-in-related event carries (authEvent)
// and writes it.
func (a *API) recordAuth(c *gin.Context, event audit.Event) {
	if a.audit == nil {
		return
	}
	a.audit.RecordOrLog(c.Request.Context(), a.authEvent(c, event))
}

// recordAuthLater is recordAuth for a passcode handed to delivery: the row is
// written now, and the line by the returned function, once the relay has
// answered (audit.Store.RecordOrLogLater).
func (a *API) recordAuthLater(c *gin.Context, event audit.Event) func(error) {
	if a.audit == nil {
		return func(error) {}
	}
	return a.audit.RecordOrLogLater(c.Request.Context(), a.authEvent(c, event))
}

// authEvent fills in what every sign-in-related event carries, when and from
// where. For the log line it also looks up the account's address: these
// events are rare, so the one read is cheap, and "who signed in" is answered
// by an address far sooner than by an account ID.
func (a *API) authEvent(c *gin.Context, event audit.Event) audit.Event {
	event.At = time.Now()
	event.ClientIP = auth.ClientIP(c.Request)
	account := event.ActorID
	if account == "" {
		account = event.SubjectID
	}
	if accounts, ok := a.auth.(*auth.SessionAuth); ok && event.ActorEmail == "" && account != "" {
		event.ActorEmail = accounts.AccountEmail(c.Request.Context(), account)
	}
	return event
}

// stoppedWaiting reports a failure that is the client hanging up: err is a
// context error and the request's own context is done. A context error alone
// is not enough. A driver puts deadlines of its own under a read, the
// connect timeout for one, and a server that hangs fails through them while
// the client is still waiting: that is the server's fault, and must neither
// read as a client that went away nor be answered like one.
func stoppedWaiting(ctx context.Context, err error) bool {
	return ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
}

// deliveryFailureReason is why no passcode went out, in the log's words. It
// reaches only the log line: the client is told nothing either way, and the
// row keeps the delivered flag it always had. ctx is the request's.
func deliveryFailureReason(ctx context.Context, err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, auth.ErrTooManyOTPRequests):
		return "throttled"
	case errors.Is(err, auth.ErrNoSuchAccount):
		return "unknown-account"
	case errors.Is(err, auth.ErrStepUpNoEmail):
		return "no-email"
	case errors.Is(err, auth.ErrUnauthenticated):
		return "no-session"
	case stoppedWaiting(ctx, err):
		return "cancelled"
	default:
		return "error"
	}
}

// authFailureReason turns a sign-in error into a category safe to store. The
// error itself is written for the person at the keyboard and must not become a
// permanent record of what was tried. ctx is the request's.
func authFailureReason(ctx context.Context, err error) string {
	switch {
	case errors.Is(err, auth.ErrTooManyLogins):
		return "throttled"
	case errors.Is(err, auth.ErrTooManyActiveUsers):
		return "seat-limit"
	case errors.Is(err, auth.ErrSessionPersistence):
		return "persistence"
	case errors.Is(err, auth.ErrTOTPCode):
		return "bad-totp"
	case errors.Is(err, auth.ErrSignInExpired):
		return "signin-expired"
	case errors.Is(err, auth.ErrTOTPUnreadable):
		return "totp-unreadable"
	case stoppedWaiting(ctx, err):
		// The client stopped waiting before the account lookup finished. No
		// credential was rejected, and filing it as one would leave the trail
		// showing sign-in attempts that were never judged.
		return "cancelled"
	default:
		return "bad-credentials"
	}
}

const otpMailTimeout = 20 * time.Second

// otpSubject is the sign-in passcode mail's subject in language.
func otpSubject(language locale.Language) string {
	if language == locale.Chinese {
		return "Nodevas 登入驗證碼"
	}
	return "Your Nodevas sign-in code"
}

// otpBody is plain text on purpose: an HTML mail invites a client to make the
// code a link, and there is nothing here worth styling.
//
// The last lines are the account holder's alarm. Anyone can ask for a passcode
// to be sent to any address, so an unrequested one is not by itself a break-in
// — but it is worth an administrator knowing about, and the passcode is the
// only thing between that request and the account.
func otpBody(challenge auth.Challenge, language locale.Language) string {
	if language != locale.Chinese {
		return strings.Join([]string{
			"Your Nodevas sign-in code:",
			"",
			"    " + challenge.Code,
			"",
			fmt.Sprintf("It works once, until %s.", challenge.Expires.Format("15:04")),
			"Signing in with it signs this account out on other devices.",
			"",
			"If you did not ask for it, ignore this mail, tell nobody the code, and let your administrator know.",
		}, "\n")
	}
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
					locale.Errorf("sign out could not be persisted: %w", "登出無法儲存：%w", err))
				return
			}
			// The session is gone, but who it belonged to is still on the
			// request: the auth middleware put it there to let this one in.
			a.recordAuthEvent(c, audit.ActionSignOut, auth.ActorFrom(c.Request), nil)
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
