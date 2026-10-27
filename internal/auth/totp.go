// Time-based one-time passwords (RFC 6238): the authenticator-app factor.
//
// The mailed passcode (otp.go) proves the person can read the account's
// mailbox. On its own that makes the mailbox the whole boundary: whoever takes
// over the inbox takes over the account, sign-in and step-up alike, because
// both send their code to the same place. An authenticator app is a second,
// independent thing to steal. An account that enrolls one needs both to sign
// in, and the app alone to step up — so the stolen inbox no longer reaches
// runner auto-accept either.
//
// This file is only the arithmetic: the standard's parameters (HMAC-SHA-1, six
// digits, thirty-second steps — the ones every authenticator app defaults to),
// the code for a step, and matching a typed code within one step of the
// server's clock. Storage is totp_store.go; where it is asked for is
// totp_session.go.

package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- RFC 6238's default, and what authenticator apps implement; HMAC-SHA-1 is not broken by SHA-1 collisions.
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"net/url"
	"strings"
	"time"
)

const (
	// TOTPDigits, TOTPPeriod and the SHA-1 HMAC are RFC 6238's defaults. They
	// are not configurable: an otpauth:// URI that asks for anything else is
	// silently ignored by some apps, which then show codes that never work.
	TOTPDigits = 6
	TOTPPeriod = 30 * time.Second
	// TOTPIssuer names the server in the authenticator app's list.
	TOTPIssuer = "Nodevas"

	// totpSkew is how many steps either side of the server's clock a code may
	// be from: one, so a phone thirty seconds off, or a code typed as it
	// rolled over, still works. Each extra step would triple the guessing odds
	// for a gain nobody notices.
	totpSkew = 1
	// totpSecretBytes is RFC 4226's recommended 160 bits, which is also the
	// HMAC-SHA-1 block-friendly length every app expects.
	totpSecretBytes = 20
)

// totpEncoding is base32 without padding: what otpauth:// URIs carry and what
// a person types when the QR code cannot be scanned.
var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// newTOTPSecret draws a fresh shared secret.
func newTOTPSecret() ([]byte, error) {
	secret := make([]byte, totpSecretBytes)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return secret, nil
}

// encodeTOTPSecret is the secret as an authenticator app takes it.
func encodeTOTPSecret(secret []byte) string { return totpEncoding.EncodeToString(secret) }

// totpCounter is the RFC 6238 time step for a moment.
func totpCounter(at time.Time) uint64 {
	seconds := at.Unix()
	if seconds < 0 {
		return 0
	}
	return uint64(seconds) / uint64(TOTPPeriod/time.Second)
}

// hotp is RFC 4226's HOTP value for one counter, as TOTPDigits decimal
// digits, zero-padded.
func hotp(secret []byte, counter uint64) string {
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], counter)
	mac := hmac.New(sha1.New, secret)
	mac.Write(message[:])
	sum := mac.Sum(nil)
	// Dynamic truncation (RFC 4226 §5.3).
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	var modulus uint32 = 1
	for range TOTPDigits {
		modulus *= 10
	}
	value %= modulus
	out := make([]byte, TOTPDigits)
	for index := TOTPDigits - 1; index >= 0; index-- {
		out[index] = byte('0' + value%10)
		value /= 10
	}
	return string(out)
}

// normalizeTOTP drops the spaces and dashes an app or a person puts in the
// middle of a code ("123 456"). It returns "" for anything that is then not
// exactly TOTPDigits ASCII digits, so a caller can tell a TOTP code from a
// recovery code by shape alone.
func normalizeTOTP(code string) string {
	var b strings.Builder
	b.Grow(TOTPDigits)
	for _, r := range code {
		switch {
		case r == ' ' || r == '-' || r == '\t':
			continue
		case r >= '0' && r <= '9':
			if b.Len() == TOTPDigits {
				return ""
			}
			b.WriteRune(r)
		default:
			return ""
		}
	}
	if b.Len() != TOTPDigits {
		return ""
	}
	return b.String()
}

// SecondFactorKind says which kind of second-factor code a string is shaped
// like, by the same rule proveSecondFactor uses: SecondFactorTOTP for an
// authenticator code, SecondFactorRecovery for anything else. It judges the
// shape only, so a refused code can be described without being repeated.
func SecondFactorKind(code string) string {
	if normalizeTOTP(code) != "" {
		return SecondFactorTOTP
	}
	return SecondFactorRecovery
}

// matchTOTP reports the time step a code belongs to, if it is the code for a
// step within totpSkew of now. Every candidate step is computed and compared
// in constant time, whichever one matches, so how long the answer takes does
// not say how close a guess was.
//
// The step is returned rather than a bare yes so the caller can refuse a code
// from a step it has already accepted: the replay guard is the caller's,
// because it lives in the database (see verifyTOTPCode).
func matchTOTP(secret []byte, code string, now time.Time) (uint64, bool) {
	code = normalizeTOTP(code)
	if code == "" {
		return 0, false
	}
	current := totpCounter(now)
	var matched uint64
	found := 0
	for delta := -totpSkew; delta <= totpSkew; delta++ {
		if delta < 0 && current < uint64(-delta) {
			continue
		}
		counter := current + uint64(int64(delta)) // #nosec G115 -- delta is within ±totpSkew and guarded above.
		if subtle.ConstantTimeCompare([]byte(hotp(secret, counter)), []byte(code)) == 1 {
			// The latest matching step wins: it is the one the replay guard
			// must remember, so the earlier one can never be accepted after it.
			matched = counter
			found = 1
		}
	}
	return matched, found == 1
}

// totpURI is the otpauth:// URI an authenticator app enrolls from (the
// Google Authenticator "Key Uri Format"). The label is "Nodevas:<account>",
// with the issuer repeated as a parameter because some apps read only one of
// the two.
func totpURI(account string, secret []byte) string {
	label := url.PathEscape(TOTPIssuer + ":" + account)
	query := url.Values{}
	query.Set("secret", encodeTOTPSecret(secret))
	query.Set("issuer", TOTPIssuer)
	query.Set("algorithm", "SHA1")
	query.Set("digits", "6")
	query.Set("period", "30")
	return "otpauth://totp/" + label + "?" + query.Encode()
}
