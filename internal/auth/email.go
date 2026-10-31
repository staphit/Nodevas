// The sign-in address: how the web UI finds an account.
//
// Signing in to the web UI is one factor now: possession of the mailbox an
// administrator registered for the account. A person types that address, a
// passcode is mailed to it, and typing the passcode back is the whole sign-in.
// There used to be a PIN in front of that — something the administrator handed
// over out of band — and it is gone on purpose: the owner chose a single
// factor, so the mailbox is the boundary, and the account is exactly as safe
// as the inbox it points at. A mailbox with its own second factor is the
// operator's lever for making that strong.
//
// What follows from that choice is in this file and in otp.go:
//
//   - The address is not a contact field. Changing it changes who can sign in,
//     so it is part of the account revision (see userRevision) and changing it
//     ends the sessions the old address authorised.
//   - It is public in a way a PIN never was. People's addresses are known, so
//     nothing reachable without signing in may say whether one is registered:
//     not by status, not by body, not by how long the answer took. otp.go and
//     the HTTP handler keep that promise; this file only stores and finds.
//   - It is unique, compared case-insensitively, because it is now a lookup
//     key. Two accounts on one mailbox would make "which account did that
//     passcode sign in to" a question with no answer.
//
// The address is stored in the form the administrator gave (after trimming
// and parsing), and that stored form is where mail goes. What a person types
// at the sign-in form is only ever used to find the row; it is never the
// recipient, so "Ann@Example.test " typed at the door cannot redirect a
// passcode anywhere the administrator did not register.

package auth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"nodevas/internal/identity"
	"nodevas/internal/locale"
)

// maxEmailBytes bounds the address before it is parsed, let alone stored or
// looked up. RFC 5321 caps a reverse-path at 256 octets, so nothing past this
// is a mailbox; without it, net/mail happily parses a multi-megabyte address
// and the whole thing lands in a TEXT column or a query parameter.
const maxEmailBytes = 320

// ErrEmailTaken is returned when an administrator registers an address that
// another account already signs in with. The comparison is case-insensitive,
// because the mailbox is.
var ErrEmailTaken = locale.Errorf("that email address is already registered to another account", "這個電子郵件地址已登記給另一個帳號")

// errAccountLookup is a sign-in address lookup that could not be answered: the
// server's fault, whatever the address was.
var errAccountLookup = locale.Errorf("look up sign-in address", "查詢登入信箱")

// normalizeEmail trims and validates an address. A malformed one is refused
// here rather than at send time, where the failure would look like an outage
// instead of a typo.
//
// It returns the parsed address — "Ann <ann@example.test>" becomes
// "ann@example.test" — but does not fold case: the stored form is what the
// administrator typed, and the database compares it case-insensitively.
func normalizeEmail(address string) (string, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", locale.Errorf("an email address is required to receive passcodes", "需要電子郵件地址才能收到驗證碼")
	}
	if len(address) > maxEmailBytes {
		return "", locale.Errorf("an email address must be at most %d bytes", "電子郵件地址最多 %d 位元組", maxEmailBytes)
	}
	parsed, err := mail.ParseAddress(address)
	if err != nil {
		return "", locale.Errorf("invalid email address %q: %w", "電子郵件地址「%s」無效：%w", address, err)
	}
	return parsed.Address, nil
}

// emailKey is the throttle key for an address: one bucket per mailbox, however
// its case was typed. It is never stored and never compared against the row.
func emailKey(address string) string { return strings.ToLower(address) }

// TypedAddress is what a person typed at the sign-in form, as the server's
// log names it when no account was found for it: parsed like any sign-in
// address and lower-cased, so attempts on one mailbox read alike however it
// was typed, or "" when it is not an address at all, which keeps a passcode
// or a password typed into the wrong box out of the log. It is for the log
// alone: mail goes only to the address registered on an account.
func TypedAddress(typed string) string {
	address, err := normalizeEmail(typed)
	if err != nil {
		return ""
	}
	return emailKey(address)
}

// SetEmail registers, or changes, the address an account signs in with.
//
// The account's revision includes the address, so every session the old one
// authorised stops working on its next request — moving the mailbox without
// ending those would leave the old address's access behind. Setting the
// address it already has changes nothing, including the sessions.
func (u *UserStore) SetEmail(ctx context.Context, name, email string) error {
	name = strings.TrimSpace(name)
	if !userNamePattern.MatchString(name) {
		return locale.Errorf("invalid user name %q", "使用者名稱「%s」無效", name)
	}
	address, err := normalizeEmail(email)
	if err != nil {
		return err
	}
	if err := u.updateAccount(ctx, name, setEmail, address); err != nil {
		if errors.Is(err, errNoAccount) {
			return locale.Errorf("no such user %q", "沒有使用者「%s」", name)
		}
		return err
	}
	return nil
}

// ClearEmail removes an account's ability to sign in to the web UI without
// removing the account itself. Its sessions end with the revision change.
func (u *UserStore) ClearEmail(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if err := u.updateAccount(ctx, name, clearEmail); err != nil {
		if errors.Is(err, errNoAccount) {
			return locale.Errorf("no such user %q", "沒有使用者「%s」", name)
		}
		return err
	}
	return nil
}

// AccountByEmail resolves a sign-in address to the account registered with it,
// returning the account's revision and its stored address.
//
// The stored address is returned because it, not the caller's string, is where
// a passcode goes. The two compare equal case-insensitively and may differ in
// every other way the caller chose to type them.
//
// An address that is not registered, or not an address at all, gives
// ErrNoSuchAccount. A row that was found but cannot be read gives an error
// that wraps errUnreadableAccount. Any other error is the read itself failing,
// and a cancelled ctx is one of those: a caller must check ctx.Err() before
// reading it as the server's fault, and must never read it as an address that
// was offered and refused. Unlike VerifyWithRevision this lookup says which of
// these happened: a database that cannot be read fails for every address
// alike, so saying so reveals nothing about any of them, and reporting an
// outage as a run of unknown addresses would make it look like somebody
// probing for accounts.
func (u *UserStore) AccountByEmail(ctx context.Context, email string) (identity.Actor, string, string, error) {
	found, err := u.signInAccount(ctx, email)
	return found.actor, found.revision, found.email, err
}

// emailAccount is what a lookup at the door finds: the account, its revision,
// its stored address, and whether it must also give an authenticator code.
type emailAccount struct {
	actor        identity.Actor
	revision     string
	email        string
	secondFactor bool
}

// secondFactorColumn reads, in the account's own statement, when its
// authenticator was confirmed, empty for an account without one or with an
// enrollment still waiting for its confirming code.
//
// It is read here rather than by a second query because a sign-in must not
// take longer for an address that is registered. A second round trip that only
// a found account makes is a difference a stopwatch can see on every attempt,
// before any passcode has been checked; one statement costs the same either
// way, and a table that cannot be read fails it for every address alike.
const secondFactorColumn = `COALESCE((SELECT enabled_at FROM account_totp WHERE account_totp.user_id = accounts.id), '')`

// withSecondFactor hands scanAccount a row with secondFactorColumn after the
// columns it reads.
type withSecondFactor struct {
	row       rowScanner
	enabledAt *string
}

func (w withSecondFactor) Scan(dest ...any) error {
	return w.row.Scan(append(dest, w.enabledAt)...)
}

// signInAccount is AccountByEmail with the second-factor flag, read in the
// same statement, with the same errors.
func (u *UserStore) signInAccount(ctx context.Context, email string) (emailAccount, error) {
	address, err := normalizeEmail(email)
	if err != nil || !u.ready() {
		return emailAccount{}, ErrNoSuchAccount
	}
	// Not the address in any error's text: the error is the server's to log,
	// and the address is already on the line that reports it.
	//
	// The driver's error is kept as text, not wrapped. A server that hangs
	// fails a read through the driver's own connect timeout, which wraps
	// context.DeadlineExceeded while the request is still waiting, and a
	// caller that matched it would take an outage for a client that hung up.
	// The caller checks its own ctx for that; past that check this is only
	// ever the server's fault.
	lookupFailed := func(err error) (emailAccount, error) {
		return emailAccount{}, locale.Errorf("%w: read accounts: %v", "%w：讀取帳號：%v", errAccountLookup, err)
	}
	// One row: the partial unique index on email makes a second impossible, so
	// reading one is not a shortcut.
	rows, err := u.database.QueryContext(ctx,
		`SELECT `+accountColumns+`, `+secondFactorColumn+` FROM accounts`+
			string(accountByEmail)+orderByCreation+` LIMIT 1`, address)
	if err != nil {
		return lookupFailed(err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return lookupFailed(err)
		}
		return emailAccount{}, ErrNoSuchAccount
	}
	var enabledAt string
	found, err := scanAccount(withSecondFactor{row: rows, enabledAt: &enabledAt})
	if err != nil {
		// About the row, not the connection: the row arrived.
		return emailAccount{}, fmt.Errorf("%w: %w", errAccountLookup, err)
	}
	if err := rows.Close(); err != nil {
		return lookupFailed(err)
	}
	return emailAccount{
		actor:        identity.Actor{ID: found.ID, Name: found.Name, Role: found.Role},
		revision:     userRevision(found),
		email:        found.Email,
		secondFactor: enabledAt != "",
	}, nil
}
