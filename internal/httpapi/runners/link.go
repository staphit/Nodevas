package runners

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/identity"
	"nodevas/internal/locale"
	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
	"nodevas/internal/safego"
)

// Device links (runnerwire/link.go):
//
//	POST   /api/runners/links         {name}  → CreateLinkResponse, once; needs a fresh step-up
//	POST   /api/runner/link           {code, name?, os?} → RedeemLinkResponse, once; no session,
//	                                  no runner token: the code is the credential
//	GET    /api/runners/access-token  → AccessTokenStatus (admin; never the secret)
//	PUT    /api/runners/access-token  {clientId, clientSecret} → AccessTokenStatus (admin, fresh step-up)
//	DELETE /api/runners/access-token  → 204 (admin, fresh step-up)

// OwnerAccount is an account as it is now, for checking a link's owner at
// redeem time.
type OwnerAccount struct {
	Actor    identity.Actor
	Revision string
	// Email is where the redeem notice goes; "" sends none.
	Email string
}

// LinkOptions wires device links into the API.
type LinkOptions struct {
	Links  *runners.Links
	Access *runners.AccessTokens
	// StepUpRequired makes link creation and the access-token writes ask for
	// a fresh step-up, read with SteppedUpAt. False only on a server without
	// accounts, where there is nobody to step up.
	StepUpRequired bool
	SteppedUpAt    func(*http.Request) (time.Time, bool)
	// Owner reads an account as it is now; nil on a server without accounts.
	Owner func(ctx context.Context, id string) (OwnerAccount, bool)
	// Audit records one event; nil records nothing.
	Audit func(r *http.Request, action string, actor identity.Actor, target string, detail map[string]any)
	// Mail sends the owner a plain-text notice; nil sends none.
	Mail func(ctx context.Context, to, subject, body string) error
}

// WithLinks enables device links. It returns a for chaining.
func (a *API) WithLinks(opts LinkOptions) *API {
	a.link = &opts
	return a
}

func (a *API) registerLinks(api *gin.RouterGroup) {
	if a.link == nil || a.link.Links == nil {
		return
	}
	api.POST("/runners/links", a.createLink)
	api.GET("/runners/access-token", a.getAccessToken)
	api.PUT("/runners/access-token", a.putAccessToken)
	api.DELETE("/runners/access-token", a.deleteAccessToken)
	api.POST("/runner/link", a.redeemLink)
}

// ErrStepUpRequired refuses a request that needs a fresh step-up.
var ErrStepUpRequired = locale.Errorf("verify it's you first (step-up), then try again", "請先驗證身分（step-up），再試一次")

const linkMailTimeout = 30 * time.Second

func (a *API) audit(r *http.Request, action string, actor identity.Actor, target string, detail map[string]any) {
	if a.link.Audit != nil {
		a.link.Audit(r, action, actor, target, detail)
	}
}

// freshStepUp answers 403 STEP_UP_REQUIRED unless the session stepped up
// within the window, the same check runs and autopilots record.
func (a *API) freshStepUp(c *gin.Context) bool {
	if !a.link.StepUpRequired {
		return true
	}
	if a.link.SteppedUpAt != nil {
		if _, ok := a.link.SteppedUpAt(c.Request); ok {
			return true
		}
	}
	httpx.Coded(c, http.StatusForbidden, "STEP_UP_REQUIRED", ErrStepUpRequired, nil)
	return false
}

// person refuses agents: a device link mints a runner, which acts as its
// owner; an agent minting one hands itself a credential that outlives it.
func person(c *gin.Context) (identity.Actor, bool) {
	actor := auth.ActorFrom(c.Request)
	if actor.IsAgent() || strings.TrimSpace(actor.ID) == "" {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", locale.Errorf("only a signed-in person can link a device", "只有已登入的人可以連結裝置"), nil)
		return actor, false
	}
	return actor, true
}

func admin(c *gin.Context) (identity.Actor, bool) {
	actor := auth.ActorFrom(c.Request)
	if actor.IsAgent() || !actor.IsAdmin() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", locale.Errorf("only an administrator can manage the runner access token", "只有管理員可以管理 runner 存取權杖"), nil)
		return actor, false
	}
	return actor, true
}

func (a *API) createLink(c *gin.Context) {
	actor, ok := person(c)
	if !ok || !a.freshStepUp(c) {
		return
	}
	var body runnerwire.CreateLinkRequest
	if !httpx.DecodeJSONLimit(c, &body, maxCreateBody) {
		return
	}
	name, err := runners.CleanName(body.Name)
	if err != nil {
		fail(c, err)
		return
	}
	revision := ""
	if a.link.Owner != nil {
		account, ok := a.link.Owner(c.Request.Context(), actor.ID)
		if !ok {
			httpx.Err(c, http.StatusUnauthorized, locale.Errorf("this account no longer exists", "這個帳號已不存在"))
			return
		}
		revision = account.Revision
	}
	clientID, clientSecret, err := a.link.Access.Get()
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, locale.Errorf("runner access token: %w", "runner 存取權杖：%w", err))
		return
	}
	// The service token passes the Cloudflare edge for the whole hostname, so
	// only an administrator's links carry it; a member's new machine gets the
	// pair from an administrator (runner login --access-client-id).
	if !actor.IsAdmin() {
		clientID, clientSecret = "", ""
	}
	code, expires, err := a.link.Links.Issue(actor, revision, name)
	if errors.Is(err, runners.ErrTooManyLinks) {
		httpx.Coded(c, http.StatusTooManyRequests, "RESOURCE_EXHAUSTED", err, nil)
		return
	}
	if err != nil {
		fail(c, err)
		return
	}
	hub := hubURL(c.Request)
	link, err := runnerwire.EncodeLink(runnerwire.LinkPayload{
		Hub: hub, Code: code, AccessClientID: clientID, AccessClientSecret: clientSecret,
	})
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	at := expires.UTC().Format(time.RFC3339)
	a.audit(c.Request, audit.ActionRunnerLinkCreate, actor, name, map[string]any{
		"expiresAt": at, "access": clientID != "",
	})
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, runnerwire.CreateLinkResponse{
		Link: link, Name: name, Hub: hub, ExpiresAt: at, Access: clientID != "",
	})
}

func (a *API) redeemLink(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var body runnerwire.RedeemLinkRequest
	if !httpx.DecodeJSONLimit(c, &body, maxCreateBody) {
		return
	}
	source := auth.ClientIP(c.Request)
	device, deviceOS := label(body.Name), label(body.OS)
	refuse := func(reason string, actor identity.Actor, err error) {
		a.audit(c.Request, audit.ActionRunnerLinkFailed, actor, "", map[string]any{
			"reason": reason, "device": device, "os": deviceOS,
		})
		if errors.Is(err, runners.ErrLinkThrottled) {
			c.Header("Retry-After", strconv.Itoa(int((10 * time.Minute).Seconds())))
			httpx.Coded(c, http.StatusTooManyRequests, "RESOURCE_EXHAUSTED", err, nil)
			return
		}
		httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", runners.ErrLinkNotFound, nil)
	}
	link, err := a.link.Links.Redeem(body.Code, source)
	switch {
	case errors.Is(err, runners.ErrLinkThrottled):
		refuse("throttled", identity.Actor{}, err)
		return
	case err != nil:
		refuse("unknown code", identity.Actor{}, err)
		return
	}
	owner := link.Owner
	email := ""
	if a.link.Owner != nil {
		// The code is spent either way: an account that changed since (removed,
		// renamed, new password, address or role) does not get a runner from a
		// link made before.
		account, ok := a.link.Owner(c.Request.Context(), owner.ID)
		if !ok || account.Actor.ID != owner.ID || account.Revision != link.Revision {
			refuse("account changed", owner, runners.ErrLinkNotFound)
			return
		}
		owner, email = account.Actor, account.Email
	}
	runner, token, err := a.reg.Create(c.Request.Context(), owner, link.Name)
	if err != nil {
		fail(c, err)
		return
	}
	a.audit(c.Request, audit.ActionRunnerLinkRedeem, owner, runner.ID, map[string]any{
		"name": runner.Name, "device": device, "os": deviceOS,
	})
	if a.link.Mail != nil && email != "" {
		at := time.Now()
		safego.Go("device link mail", func() { a.mailRedeem(email, runner, device, deviceOS, source, at) })
	}
	c.JSON(http.StatusCreated, runnerwire.RedeemLinkResponse{RunnerID: runner.ID, Name: runner.Name, Token: token})
}

func (a *API) mailRedeem(to string, runner runnerwire.Runner, device, deviceOS, source string, at time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), linkMailTimeout)
	defer cancel()
	var b strings.Builder
	fmt.Fprintf(&b, "A new device was linked to your Nodevas account as runner %q.\n", runner.Name)
	fmt.Fprintf(&b, "一台新裝置已用「%s」這個 runner 名稱連結到你的 Nodevas 帳號。\n\n", runner.Name)
	if device != "" || deviceOS != "" {
		fmt.Fprintf(&b, "Device: %s %s\n", device, deviceOS)
	}
	fmt.Fprintf(&b, "From: %s\nAt: %s\n\n", source, at.UTC().Format(time.RFC1123))
	b.WriteString("If this was not you, revoke the runner on the Runners page now: it can run agents on your behalf.\n")
	b.WriteString("如果不是你，請立即到 Runners 頁面撤銷它：它能以你的名義執行 agent。\n")
	if err := a.link.Mail(ctx, to, "Nodevas 新裝置已連結 / New device linked", b.String()); err != nil {
		slog.Warn("device link notice not sent", slog.String("runner", runner.ID), slog.Any("error", err))
	}
}

func (a *API) accessStatus() (runnerwire.AccessTokenStatus, error) {
	id, secret, err := a.link.Access.Get()
	if err != nil {
		return runnerwire.AccessTokenStatus{}, err
	}
	return runnerwire.AccessTokenStatus{ClientID: id, Set: secret != ""}, nil
}

func (a *API) getAccessToken(c *gin.Context) {
	if _, ok := admin(c); !ok {
		return
	}
	status, err := a.accessStatus()
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, status)
}

func (a *API) putAccessToken(c *gin.Context) {
	actor, ok := admin(c)
	if !ok || !a.freshStepUp(c) {
		return
	}
	var body runnerwire.SetAccessTokenRequest
	if !httpx.DecodeJSONLimit(c, &body, maxCreateBody) {
		return
	}
	if err := a.link.Access.Set(body.ClientID, body.ClientSecret); err != nil {
		fail(c, err)
		return
	}
	status, err := a.accessStatus()
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	a.audit(c.Request, audit.ActionRunnerAccessTokenSet, actor, status.ClientID, nil)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, status)
}

func (a *API) deleteAccessToken(c *gin.Context) {
	actor, ok := admin(c)
	if !ok || !a.freshStepUp(c) {
		return
	}
	if err := a.link.Access.Clear(); err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	a.audit(c.Request, audit.ActionRunnerAccessTokenClear, actor, "", nil)
	c.Status(http.StatusNoContent)
}

// hubURL is the hub's base URL as this request reached it. The Host has
// passed the server's allowed-hosts check by now. Plain http only for a hub
// on loopback: a runner refuses anything else over http, and an edge in
// front of a remote hub speaks https.
func hubURL(r *http.Request) string {
	scheme := "https"
	if !auth.RequestIsSecure(r) && loopbackHost(r.Host) {
		scheme = "http"
	}
	return scheme + "://" + r.Host
}

func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// label is a caller-supplied device name or OS made safe for the audit trail
// and a mail: printable, one line, at most runners.MaxNameRunes runes; "" when
// nothing printable is left.
func label(value string) string {
	var b strings.Builder
	n := 0
	for _, c := range strings.TrimSpace(value) {
		if n >= runners.MaxNameRunes {
			break
		}
		if !unicode.IsPrint(c) {
			continue
		}
		b.WriteRune(c)
		n++
	}
	return strings.TrimSpace(b.String())
}
