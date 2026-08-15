// PIN storage and verification.
//
// A PIN is the knowledge factor of the two-factor sign-in: an administrator
// creates it and hands it over out of band, and it is never emailed, shown
// again, or recoverable. What is kept on disk is an argon2id hash — the same
// one-way, salted, memory-hard function the password field uses. Storing it
// reversibly would mean the server could produce the PIN again, which is
// exactly the property that must not exist: an operator, a backup, or anyone
// who reaches the file would then hold the factor itself rather than a hash of
// it.
//
// The PIN also has to identify the account, because the sign-in form asks for
// nothing else. There is no user name to look up, so verification reads every
// account that has a PIN and verifies against each — see maxPinAccounts for the
// bound that keeps that honest.

package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/mail"
	"strings"

	"nodevas/internal/identity"
)

const (
	// A PIN is typed rarely and protects everything, so it is not a four-digit
	// bank PIN. Twelve characters from the generator below is 60 bits; a
	// hand-chosen one has to clear the same length so it cannot be a word.
	MinPinLength = 12
	// The generator's default. Long enough that the argon2 cost is a backstop
	// rather than the only defence.
	GeneratedPinLength = 16

	// Verification has no account name to go on, so it argon2-verifies against
	// each account until one matches. That is bounded work only while the
	// number of accounts is small, which is what this deployment is: a handful
	// of named people an administrator provisions by hand. Past this many, PIN
	// sign-in is refused with an operator-facing error rather than quietly
	// becoming a way to spend the server's memory bandwidth.
	maxPinAccounts = 64

	// maxEmailBytes bounds the address before it is parsed, let alone stored.
	// RFC 5321 caps a reverse-path at 256 octets, so nothing past this is a
	// mailbox; without it, net/mail happily parses a multi-megabyte address and
	// the whole thing lands in a TEXT column.
	maxEmailBytes = 320
)

// ErrTooManyAccountsForPin tells an operator that the deployment has outgrown
// PIN-only sign-in. It is never shown to an unauthenticated client.
var ErrTooManyAccountsForPin = fmt.Errorf(
	"pin sign-in supports at most %d accounts; this workspace has more", maxPinAccounts)

// ErrPinTooShort is returned when an administrator sets a PIN that is not long
// enough to survive being the only thing between a stranger and a passcode
// request.
var ErrPinTooShort = fmt.Errorf("a pin must be at least %d characters", MinPinLength)

// ErrNoPin means the account cannot sign in to the web UI because no PIN has
// been set for it.
var ErrNoPin = errors.New("this account has no pin")

// pinAlphabet excludes I, O, 0 and 1: a PIN is read off a screen once and
// typed by hand, and those four are what get misread.
const pinAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789"

// GeneratePin returns a PIN an administrator can hand over. Preferring this to
// a chosen one is the difference between 60 bits and a memorable phrase.
func GeneratePin() (string, error) {
	limit := big.NewInt(int64(len(pinAlphabet)))
	out := make([]byte, GeneratedPinLength)
	for index := range out {
		pick, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		out[index] = pinAlphabet[pick.Int64()]
	}
	return string(out), nil
}

// validatePin is deliberately only a length rule. Composition rules push
// people towards patterns, and the generator above is the intended path.
func validatePin(pin string) error {
	if len([]rune(pin)) < MinPinLength {
		return ErrPinTooShort
	}
	if len(pin) > MaxPinBytes {
		return fmt.Errorf("a pin must be at most %d bytes", MaxPinBytes)
	}
	return nil
}

// normalizeEmail trims and validates the address a passcode will be sent to.
// A malformed one is refused here rather than at send time, where the failure
// would look like an outage instead of a typo.
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

// SetPin replaces an account's PIN and the address its passcodes go to.
//
// Both change together because they are one credential: a PIN that can no
// longer reach its holder's mailbox is not a way in, and moving the mailbox
// without rotating the PIN would hand the new address the old one's access.
func (u *UserStore) SetPin(ctx context.Context, name, pin, email string) error {
	name = strings.TrimSpace(name)
	if !userNamePattern.MatchString(name) {
		return fmt.Errorf("invalid user name %q", name)
	}
	if err := validatePin(pin); err != nil {
		return err
	}
	address, err := normalizeEmail(email)
	if err != nil {
		return err
	}
	hash, err := hashPassword(pin)
	if err != nil {
		return err
	}

	if err := u.updateAccount(ctx, name, setPinAndEmail, hash, address); err != nil {
		if errors.Is(err, errNoAccount) {
			return fmt.Errorf("no such user %q", name)
		}
		return err
	}
	return nil
}

// ClearPin removes an account's ability to sign in to the web UI without
// removing the account itself.
func (u *UserStore) ClearPin(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if err := u.updateAccount(ctx, name, clearPinHash); err != nil {
		if errors.Is(err, errNoAccount) {
			return fmt.Errorf("no such user %q", name)
		}
		return err
	}
	return nil
}

// VerifyPin resolves a PIN to the account that holds it, along with the
// address its passcodes go to.
//
// With no account name to look up, every account with a PIN is verified
// against until one matches. Every candidate is checked even after a hit, so
// the time taken does not reveal where in the table the match was — the same
// reason the password path verifies a decoy hash when the name is unknown.
//
// As in VerifyWithRevision, a false covers the candidate read failing as well
// as the PIN not matching, and a cancelled ctx lands in the same bucket. This
// path is reachable without authentication, so a caller must not treat that
// false as an offered-and-rejected credential without checking ctx.Err().
func (u *UserStore) VerifyPin(ctx context.Context, pin string) (identity.Actor, string, string, bool) {
	if len(pin) == 0 || len(pin) > MaxPinBytes {
		return identity.Actor{}, "", "", false
	}
	// One past the ceiling, so the refusal below is decided by a bounded read
	// rather than after every PIN-bearing row in the table is already in memory.
	candidates, err := u.accounts(ctx, accountsWithPins, maxPinAccounts+1)
	if err != nil {
		return identity.Actor{}, "", "", false
	}

	if len(candidates) > maxPinAccounts {
		// Refuse rather than spend the memory. An operator sees this in the
		// server log; the client sees only the usual failure.
		return identity.Actor{}, "", "", false
	}
	if len(candidates) == 0 {
		// Nothing to compare against, but a caller must not learn that from
		// how quickly the answer came back.
		_, _ = verifyPassword(decoyHash, pin)
		return identity.Actor{}, "", "", false
	}

	var found UserRecord
	matched := false
	for _, candidate := range candidates {
		ok, err := verifyPassword(candidate.PinHash, pin)
		if err == nil && ok && !matched {
			found = candidate
			matched = true
		}
	}
	if !matched {
		return identity.Actor{}, "", "", false
	}
	actor := identity.Actor{ID: found.ID, Name: found.Name, Role: found.Role}
	return actor, userRevision(found), found.Email, true
}
