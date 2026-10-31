package system

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/identity"
	"nodevas/internal/locale"
)

// The server's accounts, for its administrators, from the web UI: what
// `nodevas user list|add|role|remove` does on the server's shell.
//
//	GET    /api/auth/accounts            → accountsBody (admin)
//	POST   /api/auth/accounts            {name, email, role} → 201 inviteBody (admin, fresh step-up)
//	PUT    /api/auth/accounts/role       {id, role} → accountsBody (admin, fresh step-up)
//	DELETE /api/auth/accounts?id=        → accountsBody (admin, fresh step-up)
//
// They call the same UserStore methods the CLI does, so a name, an address, a
// duplicate and the last administrator are judged identically. An invitation
// is the account plus a plain mail saying where the server is and how to sign
// in to it: open the address, type your email, type the passcode mailed to
// you. Nothing in it signs anybody in; the mailbox the account names is, as
// for every account, the credential (internal/auth/email.go).
//
// The routes live under /api/auth/ because they are server-wide: the request
// audit middleware would file a write anywhere else under the active project.
// Each handler writes its own row instead, as the sign-in handlers do.
//
// Changing accounts asks for a fresh step-up, as minting a device link and
// writing the runner access token do (internal/httpapi/runners/link.go): a
// stolen session cookie alone must not be able to add an administrator. The
// list does not: it is a read, and an administrator already sees every
// account in the audit trail.
//
// On a server without accounts (loopback) every route answers 404.

// accountView is one account as the panel shows it. The password hash never
// leaves internal/auth (Records does not select it).
type accountView struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Email     string        `json:"email"`
	Role      identity.Role `json:"role"`
	CreatedAt string        `json:"createdAt"`
	// TwoFactor is whether the account signs in with an authenticator too.
	TwoFactor bool `json:"twoFactor"`
}

// seatsView is the --max-active-users limit and how much of it is taken.
// It caps people signed in at once, not accounts: an invitation is never
// refused for it, exactly as `nodevas user add` is not, but the person it
// names is turned away at sign-in while the server is full.
type seatsView struct {
	Active int `json:"active"`
	// Max is zero when there is no limit.
	Max int `json:"max"`
}

type accountsBody struct {
	Accounts []accountView `json:"accounts"`
	// Self is the caller's own account ID: the panel does not offer to
	// remove or demote it.
	Self string `json:"self"`
	// MailConfigured is whether this server can send mail at all; without it
	// nobody can be mailed a passcode, so nobody can sign in.
	MailConfigured bool      `json:"mailConfigured"`
	SignInURL      string    `json:"signInUrl"`
	Seats          seatsView `json:"seats"`
}

// Invitation delivery, as inviteBody.Email.Status reports it.
const (
	inviteSent         = "sent"
	inviteUnconfigured = "unconfigured" // the server has no outgoing mail
	inviteFailed       = "failed"       // the relay refused it or never answered
)

type inviteEmail struct {
	Status string `json:"status"`
	To     string `json:"to"`
}

type inviteBody struct {
	Account   accountView `json:"account"`
	Email     inviteEmail `json:"email"`
	SignInURL string      `json:"signInUrl"`
	Seats     seatsView   `json:"seats"`
}

// inviteMailTimeout bounds the relay inside the request, short of the web
// client's own 30-second deadline, so the panel hears "it failed" rather
// than giving up first.
const inviteMailTimeout = 15 * time.Second

// maxAccountBody bounds an account request: a name, an address and a role.
const maxAccountBody = 4 << 10

var (
	errNoAccounts = locale.Errorf("this server does not use accounts; add people on a networked server", "這個伺服器不使用帳號；請在網路伺服器上加入成員")
	errAdminOnly  = locale.Errorf("only a server administrator can manage accounts", "只有伺服器管理員可以管理帳號")
	errNoSuchUser = locale.Errorf("no such account", "沒有這個帳號")
	errSelfRemove = locale.Errorf("you cannot remove your own account; ask another administrator", "你不能移除自己的帳號；請找另一位管理員")
	errSelfDemote = locale.Errorf("you cannot change your own role; ask another administrator", "你不能更改自己的角色；請找另一位管理員")
	errNeedsEmail = locale.Errorf("an email address is required: it is how the person signs in", "需要電子郵件地址：對方用它登入")
	errInviteRole = locale.Errorf("role must be admin or member", "角色須為 admin 或 member")
)

// UseUsers enables the account routes. Without it (loopback) they answer 404.
func (a *API) UseUsers(users *auth.UserStore) {
	a.users = users
}

// accountsAdmin admits a signed-in person who is a server administrator on a
// server with accounts, and answers for everyone else. An agent is refused
// even for its administrator: the list holds every sign-in address, and an
// agent has no business adding the people who may reach the server.
func (a *API) accountsAdmin(c *gin.Context) (*auth.SessionAuth, identity.Actor, bool) {
	accounts, ok := a.auth.(*auth.SessionAuth)
	if !ok || a.users == nil {
		httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", errNoAccounts, nil)
		return nil, identity.Actor{}, false
	}
	actor := auth.ActorFrom(c.Request)
	if actor.IsAgent() || !actor.IsAdmin() || actor.ID == "" {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", errAdminOnly, nil)
		return nil, identity.Actor{}, false
	}
	return accounts, actor, true
}

// freshStepUp answers 403 STEP_UP_REQUIRED unless the session stepped up
// within the window: the check runner links and the access token make.
func freshStepUp(c *gin.Context, accounts *auth.SessionAuth) bool {
	if _, ok := accounts.SteppedUpAt(c.Request); ok {
		return true
	}
	httpx.Coded(c, http.StatusForbidden, "STEP_UP_REQUIRED", auth.ErrStepUpRequired, nil)
	return false
}

func (a *API) getAccounts(c *gin.Context) {
	accounts, actor, ok := a.accountsAdmin(c)
	if !ok {
		return
	}
	a.respondAccounts(c, accounts, actor)
}

func (a *API) respondAccounts(c *gin.Context, accounts *auth.SessionAuth, actor identity.Actor) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, accountsBody{
		Accounts:       a.accountViews(c.Request.Context()),
		Self:           actor.ID,
		MailConfigured: a.mailer != nil,
		SignInURL:      signInURL(c.Request),
		Seats:          seats(accounts),
	})
}

func (a *API) accountViews(ctx context.Context) []accountView {
	records := a.users.Records(ctx)
	views := make([]accountView, 0, len(records))
	for _, record := range records {
		view := accountView{ID: record.ID, Name: record.Name, Email: record.Email,
			Role: record.Role, CreatedAt: record.CreatedAt}
		// An unreadable authenticator row reads as off here; sign-in itself
		// fails closed on it (totp_store.go).
		view.TwoFactor, _ = a.users.TOTPEnabled(ctx, record.ID)
		views = append(views, view)
	}
	return views
}

func (a *API) findAccount(ctx context.Context, id string) (accountView, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return accountView{}, false
	}
	for _, view := range a.accountViews(ctx) {
		if view.ID == id {
			return view, true
		}
	}
	return accountView{}, false
}

func seats(accounts *auth.SessionAuth) seatsView {
	return seatsView{Active: accounts.ActiveUsers(), Max: accounts.MaxActiveUsers()}
}

func (a *API) postAccount(c *gin.Context) {
	accounts, actor, ok := a.accountsAdmin(c)
	if !ok || !freshStepUp(c, accounts) {
		return
	}
	var body struct {
		Name  string `json:"name"`
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if !httpx.DecodeJSONLimit(c, &body, maxAccountBody) {
		return
	}
	if strings.TrimSpace(body.Email) == "" {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", errNeedsEmail, nil)
		return
	}
	role, valid := inviteRole(body.Role)
	if !valid {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", errInviteRole, nil)
		return
	}
	// AddWithEmail wants a password, and web sign-in never asks for one: the
	// mailed passcode is the sign-in. A random one nobody is told is the same
	// account `nodevas user add` makes with a password nobody types.
	unused, err := auth.RandomToken()
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	ctx := c.Request.Context()
	if err := a.users.AddWithEmail(ctx, body.Name, unused, role, body.Email); err != nil {
		switch {
		case errors.Is(err, auth.ErrEmailTaken), errors.Is(err, auth.ErrAccountExists):
			httpx.Coded(c, http.StatusConflict, "ALREADY_EXISTS", err, nil)
		case errors.Is(err, auth.ErrInvalidAccount):
			httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", err, nil)
		default:
			httpx.Err(c, http.StatusInternalServerError, err)
		}
		return
	}
	id, _, found := a.users.FindAccount(ctx, strings.TrimSpace(body.Name))
	created, readBack := a.findAccount(ctx, id)
	if !found || !readBack {
		httpx.Err(c, http.StatusInternalServerError, locale.Errorf("the account was created but could not be read back", "帳號已建立，但無法讀回"))
		return
	}

	address := signInURL(c.Request)
	status := a.mailInvitation(created, actor, address, httpx.Language(c.Request))
	a.recordAccountEvent(c, audit.ActionAccountInvite, actor, created.ID, map[string]any{
		"account": created.Name, "role": string(created.Role),
		"emailSent": status == inviteSent, "delivery": status,
	})
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, inviteBody{
		Account:   created,
		Email:     inviteEmail{Status: status, To: created.Email},
		SignInURL: address,
		Seats:     seats(accounts),
	})
}

// inviteRole reads an invitation's role. Empty is a member: the first
// account is the only one AddWithEmail makes an administrator unasked, and an
// invitation is never the first.
func inviteRole(value string) (identity.Role, bool) {
	switch identity.Role(strings.ToLower(strings.TrimSpace(value))) {
	case "", identity.RoleMember:
		return identity.RoleMember, true
	case identity.RoleAdmin:
		return identity.RoleAdmin, true
	}
	return "", false
}

// mailInvitation sends the invitation and says how it went. It waits for the
// relay, unlike a sign-in passcode: an administrator asked, the answer is
// about an account they just made, and "it did not go out" is exactly what
// they need to hear. The relay's own words go to the log, not the client.
//
// The invitee has no browser here yet to say what they read, so it is
// written in the inviting administrator's language: the one person known to
// share a workplace with them.
func (a *API) mailInvitation(account accountView, inviter identity.Actor, address string, language locale.Language) string {
	if a.mailer == nil {
		return inviteUnconfigured
	}
	ctx, cancel := context.WithTimeout(context.Background(), inviteMailTimeout)
	defer cancel()
	if err := a.mailer.Send(ctx, account.Email, inviteSubject(language), inviteText(account, inviter, address, language)); err != nil {
		log.Printf("invitation to account %s was not sent: %v", account.ID, err)
		return inviteFailed
	}
	return inviteSent
}

// inviteSubject is the invitation's subject in language.
func inviteSubject(language locale.Language) string {
	if language == locale.Chinese {
		return "Nodevas 邀請"
	}
	return "You're invited to Nodevas"
}

// inviteText is plain text, as every mail this server sends: there is no link
// to sign in with, only the address of the server, so nothing in it is worth
// stealing and nothing in it can be made to look like a sign-in button.
func inviteText(account accountView, inviter identity.Actor, address string, language locale.Language) string {
	if language != locale.Chinese {
		role := "member"
		if account.Role == identity.RoleAdmin {
			role = "administrator"
		}
		return strings.Join([]string{
			fmt.Sprintf("%s added you to a Nodevas server as %s %q.", inviter.Name, role, account.Name),
			"",
			"To sign in:",
			"  1. Open " + address,
			"  2. Enter this email address: " + account.Email,
			"  3. Type the one-time passcode that is then emailed to you.",
			"",
			"There is no password. If you did not expect this, ignore this mail and tell " + inviter.Name + ".",
		}, "\n")
	}
	roleZH := "成員"
	if account.Role == identity.RoleAdmin {
		roleZH = "管理員"
	}
	return strings.Join([]string{
		fmt.Sprintf("%s 已把你加入一台 Nodevas 伺服器，帳號為 %q，角色是%s。", inviter.Name, account.Name, roleZH),
		"",
		"登入方式：",
		"  1. 開啟 " + address,
		"  2. 輸入這個 email：" + account.Email,
		"  3. 輸入接著寄給你的一次性驗證碼。",
		"",
		"不需要密碼。若你沒有預期收到這封信，請忽略它並告知 " + inviter.Name + "。",
	}, "\n")
}

func (a *API) putAccountRole(c *gin.Context) {
	accounts, actor, ok := a.accountsAdmin(c)
	if !ok || !freshStepUp(c, accounts) {
		return
	}
	var body struct {
		ID   string `json:"id"`
		Role string `json:"role"`
	}
	if !httpx.DecodeJSONLimit(c, &body, maxAccountBody) {
		return
	}
	role := identity.Role(strings.ToLower(strings.TrimSpace(body.Role)))
	if role != identity.RoleAdmin && role != identity.RoleMember {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", errInviteRole, nil)
		return
	}
	ctx := c.Request.Context()
	target, found := a.findAccount(ctx, body.ID)
	if !found {
		httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", errNoSuchUser, nil)
		return
	}
	if target.Role == role {
		a.respondAccounts(c, accounts, actor)
		return
	}
	// The role is part of the session's revision, so changing your own ends
	// the session you are changing it from; and an administrator demoting
	// themselves by a misclick is one only another administrator can undo.
	if target.ID == actor.ID {
		httpx.Coded(c, http.StatusConflict, "FAILED_PRECONDITION", errSelfDemote, nil)
		return
	}
	if err := a.users.SetRole(ctx, target.Name, role); err != nil {
		a.accountChangeError(c, target.ID, err)
		return
	}
	a.recordAccountEvent(c, audit.ActionAccountRole, actor, target.ID, map[string]any{
		"account": target.Name, "role": string(role), "from": string(target.Role),
	})
	a.respondAccounts(c, accounts, actor)
}

func (a *API) deleteAccount(c *gin.Context) {
	accounts, actor, ok := a.accountsAdmin(c)
	if !ok || !freshStepUp(c, accounts) {
		return
	}
	ctx := c.Request.Context()
	target, found := a.findAccount(ctx, c.Query("id"))
	if !found {
		httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", errNoSuchUser, nil)
		return
	}
	// Removing yourself also covers the CLI's "never the only account": the
	// caller is always one account the table keeps.
	if target.ID == actor.ID {
		httpx.Coded(c, http.StatusConflict, "FAILED_PRECONDITION", errSelfRemove, nil)
		return
	}
	if err := a.users.Remove(ctx, target.Name); err != nil {
		a.accountChangeError(c, target.ID, err)
		return
	}
	// Its sessions end on their next request: Authenticate re-reads the row.
	a.recordAccountEvent(c, audit.ActionAccountRemove, actor, target.ID, map[string]any{
		"account": target.Name, "role": string(target.Role),
	})
	a.respondAccounts(c, accounts, actor)
}

func (a *API) accountChangeError(c *gin.Context, id string, err error) {
	switch {
	case errors.Is(err, auth.ErrLastAdministrator), errors.Is(err, auth.ErrLastAdministratorRole):
		httpx.Coded(c, http.StatusConflict, "FAILED_PRECONDITION", err, nil)
	default:
		// Removed by someone else between the read and the write.
		if _, still := a.findAccount(c.Request.Context(), id); !still {
			httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", errNoSuchUser, nil)
			return
		}
		httpx.Err(c, http.StatusInternalServerError, err)
	}
}

// recordAccountEvent writes one account change to the trail, server-wide,
// naming the account changed as its target.
func (a *API) recordAccountEvent(c *gin.Context, action string, actor identity.Actor, target string, detail map[string]any) {
	a.recordAuth(c, audit.Event{ActorID: actor.ID, ActorName: actor.Name, Action: action,
		Target: target, Detail: detail})
}

// signInURL is the server's address as the administrator's browser reached
// it: the request's Origin when it names this Host (the browser knows whether
// an edge in front speaks https), otherwise the Host with the scheme the
// runner links use. The Host has passed the allowed-hosts check by now.
func signInURL(r *http.Request) string {
	if origin, err := url.Parse(r.Header.Get("Origin")); err == nil &&
		(origin.Scheme == "https" || origin.Scheme == "http") &&
		origin.Host != "" && strings.EqualFold(origin.Host, r.Host) {
		return origin.Scheme + "://" + origin.Host
	}
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
