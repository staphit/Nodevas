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
)

// maxEmailBytes bounds the address before it is parsed, let alone stored or
// looked up. RFC 5321 caps a reverse-path at 256 octets, so nothing past this
// is a mailbox; without it, net/mail happily parses a multi-megabyte address
// and the whole thing lands in a TEXT column or a query parameter.
const maxEmailBytes = 320

// ErrEmailTaken is returned when an administrator registers an address that
// another account already signs in with. The comparison is case-insensitive,
// because the mailbox is.
var ErrEmailTaken = errors.New("that email address is already registered to another account")

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
		return "", errors.New("an email address is required to receive passcodes")
	}
	if len(address) > maxEmailBytes {
		return "", fmt.Errorf("an email address must be at most %d bytes", maxEmailBytes)
	}
	parsed, err := mail.ParseAddress(address)
	if err != nil {
		return "", fmt.Errorf("invalid email address %q: %w", address, err)
	}
	return parsed.Address, nil
}

// emailKey is the throttle key for an address: one bucket per mailbox, however
// its case was typed. It is never stored and never compared against the row.
func emailKey(address string) string { return strings.ToLower(address) }

// SetEmail registers, or changes, the address an account signs in with.
//
// The account's revision includes the address, so every session the old one
// authorised stops working on its next request — moving the mailbox without
// ending those would leave the old address's access behind. Setting the
// address it already has changes nothing, including the sessions.
func (u *UserStore) SetEmail(ctx context.Context, name, email string) error {
	name = strings.TrimSpace(name)
	if !userNamePattern.MatchString(name) {
		return fmt.Errorf("invalid user name %q", name)
	}
	address, err := normalizeEmail(email)
	if err != nil {
		return err
	}
	if err := u.updateAccount(ctx, name, setEmail, address); err != nil {
		if errors.Is(err, errNoAccount) {
			return fmt.Errorf("no such user %q", name)
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
			return fmt.Errorf("no such user %q", name)
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
// As in VerifyWithRevision, a false covers the read failing as well as the
// address being unknown, and a cancelled ctx lands in the same bucket. This
// path is reachable without authentication, so a caller must not treat that
// false as an offered-and-rejected credential without checking ctx.Err().
func (u *UserStore) AccountByEmail(ctx context.Context, email string) (identity.Actor, string, string, bool) {
	address, err := normalizeEmail(email)
	if err != nil {
		return identity.Actor{}, "", "", false
	}
	// One row: the partial unique index on email makes a second impossible, so
	// reading one is not a shortcut.
	users, err := u.accounts(ctx, accountByEmail, 1, address)
	if err != nil || len(users) == 0 {
		return identity.Actor{}, "", "", false
	}
	found := users[0]
	actor := identity.Actor{ID: found.ID, Name: found.Name, Role: found.Role}
	return actor, userRevision(found), found.Email, true
}
