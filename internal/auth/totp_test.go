package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nodevas/internal/identity"
)

// RFC 6238 Appendix B, SHA-1 rows, shortened to six digits (the eight-digit
// values mod 10^6, which is exactly what six-digit truncation is).
func TestTOTPMatchesTheRFC6238TestVectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	for _, vector := range []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	} {
		at := time.Unix(vector.unix, 0)
		if got := hotp(secret, totpCounter(at)); got != vector.want {
			t.Errorf("T=%d: code %s, want %s", vector.unix, got, vector.want)
		}
		if _, ok := matchTOTP(secret, vector.want, at); !ok {
			t.Errorf("T=%d: matchTOTP refused the right code", vector.unix)
		}
	}
}

func TestTOTPAcceptsOneStepOfSkewAndNoMore(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(1_700_000_000, 0)
	current := totpCounter(now)
	for delta, want := range map[int64]bool{-2: false, -1: true, 0: true, 1: true, 2: false} {
		code := hotp(secret, uint64(int64(current)+delta))
		counter, ok := matchTOTP(secret, code, now)
		if ok != want {
			t.Errorf("step %+d: accepted = %v, want %v", delta, ok, want)
		}
		if ok && counter != uint64(int64(current)+delta) {
			t.Errorf("step %+d: matched counter %d", delta, counter)
		}
	}
	// Spacing is forgiven; anything else that is not six digits is not.
	code := hotp(secret, current)
	if _, ok := matchTOTP(secret, code[:3]+" "+code[3:], now); !ok {
		t.Error("a code with a space in the middle was refused")
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef", code + "0"} {
		if _, ok := matchTOTP(secret, bad, now); ok {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestTOTPURIIsWhatAuthenticatorAppsRead(t *testing.T) {
	secret := []byte("12345678901234567890")
	uri, err := url.Parse(totpURI("ann@example.test", secret))
	if err != nil {
		t.Fatal(err)
	}
	if uri.Scheme != "otpauth" || uri.Host != "totp" || uri.Path != "/Nodevas:ann@example.test" {
		t.Fatalf("uri = %s", uri)
	}
	query := uri.Query()
	if query.Get("secret") != "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" || query.Get("issuer") != "Nodevas" ||
		query.Get("digits") != "6" || query.Get("period") != "30" || query.Get("algorithm") != "SHA1" {
		t.Fatalf("query = %v", query)
	}
	if strings.Contains(encodeTOTPSecret(make([]byte, totpSecretBytes)), "=") {
		t.Fatal("the secret carries base32 padding")
	}
}

// totpFixture is ann with a session, an email step-up, and a clock the test
// moves.
type totpFixture struct {
	sessions *SessionAuth
	users    *UserStore
	request  *http.Request
	clock    time.Time
}

func newTOTPFixture(t *testing.T) *totpFixture {
	t.Helper()
	t.Setenv("NODEVAS_SECRET_KEY", "")
	sessions, users := otpStoreForTest(t)
	users.UseSecretKey(filepath.Join(t.TempDir(), "master.key"))
	f := &totpFixture{sessions: sessions, users: users, clock: time.Unix(1_800_000_000, 0)}
	sessions.now = func() time.Time { return f.clock }
	f.request = signedInRequest(t, sessions)
	return f
}

// stepUpByEmail steps the fixture's session up with a mailed passcode.
func (f *totpFixture) stepUpByEmail(t *testing.T) {
	t.Helper()
	pastCooldown(f.sessions)
	challenge, err := f.sessions.RequestStepUp(f.request)
	if err != nil {
		t.Fatalf("RequestStepUp: %v", err)
	}
	if _, err := f.sessions.VerifyStepUp(f.request, challenge.Code); err != nil {
		t.Fatalf("VerifyStepUp: %v", err)
	}
}

// code is the authenticator's code at the fixture's clock.
func (f *totpFixture) code(secret []byte) string { return hotp(secret, totpCounter(f.clock)) }

// tick moves the clock to the next time step, so a fresh code is not a replay.
func (f *totpFixture) tick() { f.clock = f.clock.Add(TOTPPeriod) }

// enroll turns ann's authenticator on and returns its secret and recovery
// codes.
func (f *totpFixture) enroll(t *testing.T) ([]byte, []string) {
	t.Helper()
	if active, _ := f.sessions.StepUpState(f.request); !active {
		f.stepUpByEmail(t)
	}
	enrollment, err := f.sessions.BeginTOTP(f.request)
	if err != nil {
		t.Fatalf("BeginTOTP: %v", err)
	}
	secret, err := totpEncoding.DecodeString(enrollment.Secret)
	if err != nil || len(secret) != totpSecretBytes {
		t.Fatalf("secret %q: %v", enrollment.Secret, err)
	}
	if !strings.Contains(enrollment.URI, "secret="+enrollment.Secret) || enrollment.Account != annEmail {
		t.Fatalf("enrollment = %+v", enrollment)
	}
	codes, err := f.sessions.ConfirmTOTP(f.request, f.code(secret))
	if err != nil {
		t.Fatalf("ConfirmTOTP: %v", err)
	}
	if len(codes) != RecoveryCodeCount {
		t.Fatalf("%d recovery codes, want %d", len(codes), RecoveryCodeCount)
	}
	f.tick()
	return secret, codes
}

// passcodeFor requests and returns a fresh sign-in passcode for ann.
func (f *totpFixture) passcode(t *testing.T) string {
	t.Helper()
	pastCooldown(f.sessions)
	challenge, err := f.sessions.RequestOTP(nil, annEmail)
	if err != nil {
		t.Fatalf("RequestOTP: %v", err)
	}
	return challenge.Code
}

func TestEnrollmentNeedsAStepUpAndAConfirmingCode(t *testing.T) {
	f := newTOTPFixture(t)
	if _, err := f.sessions.BeginTOTP(f.request); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("BeginTOTP without step-up: %v", err)
	}
	f.stepUpByEmail(t)
	enrollment, err := f.sessions.BeginTOTP(f.request)
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := totpEncoding.DecodeString(enrollment.Secret)
	// Begun is not on: sign-in is still one factor.
	if enabled, _ := f.users.TOTPEnabled(context.Background(), annID(t, f.users)); enabled {
		t.Fatal("an unconfirmed enrollment is already required")
	}
	state, err := f.sessions.TOTPStatus(f.request)
	if err != nil || state.Enabled || !state.Pending {
		t.Fatalf("state after begin = %+v, %v", state, err)
	}
	if _, err := f.sessions.ConfirmTOTP(f.request, "000000"); !errors.Is(err, ErrTOTPCode) && f.code(secret) != "000000" {
		t.Fatalf("wrong confirming code: %v", err)
	}
	codes, err := f.sessions.ConfirmTOTP(f.request, f.code(secret))
	if err != nil {
		t.Fatal(err)
	}
	state, _ = f.sessions.TOTPStatus(f.request)
	if !state.Enabled || state.RecoveryCodesLeft != RecoveryCodeCount {
		t.Fatalf("state after confirm = %+v", state)
	}
	if _, err := f.sessions.BeginTOTP(f.request); !errors.Is(err, ErrTOTPAlreadyEnabled) {
		t.Fatalf("second enrollment over a working one: %v", err)
	}
	for _, code := range codes {
		if len(code) != recoveryCodeLength+3 || strings.Count(code, "-") != 3 {
			t.Fatalf("recovery code %q is not XXXX-XXXX-XXXX-XXXX", code)
		}
	}
}

func TestTurningTheFactorOnSignsOutTheOtherSessions(t *testing.T) {
	f := newTOTPFixture(t)
	// A second one-factor session, as an intruder with the mailbox would hold.
	pastCooldown(f.sessions)
	other := signedInRequestKeepingOthers(t, f.sessions)
	f.enroll(t)
	if _, err := f.sessions.Authenticate(other); err == nil {
		t.Fatal("a one-factor session survived turning two-factor on")
	}
	if _, err := f.sessions.Authenticate(f.request); err != nil {
		t.Fatalf("the enrolling session was signed out: %v", err)
	}
}

// signedInRequestKeepingOthers opens a second session for ann without ending
// the first, the way a session on another device already exists.
func signedInRequestKeepingOthers(t *testing.T, sessions *SessionAuth) *http.Request {
	t.Helper()
	users, _ := sessions.users.accounts(context.Background(), accountByEmail, 1, annEmail)
	actor, token, _, err := sessions.openSession(identityOf(users[0]), userRevision(users[0]))
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/graph", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
	return r.WithContext(WithActor(r.Context(), actor))
}

func identityOf(user UserRecord) identity.Actor {
	return identity.Actor{ID: user.ID, Name: user.Name, Role: user.Role}
}

func TestTheSecretIsEncryptedAtRest(t *testing.T) {
	f := newTOTPFixture(t)
	secret, codes := f.enroll(t)
	var stored string
	if err := f.users.database.QueryRow(`SELECT secret FROM account_totp`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == "" || strings.Contains(stored, encodeTOTPSecret(secret)) || strings.Contains(stored, string(secret)) {
		t.Fatalf("the column holds the secret in a readable form: %q", stored)
	}
	rows, err := f.users.database.Query(`SELECT code_hash FROM account_recovery_codes`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			t.Fatal(err)
		}
		for _, code := range codes {
			if strings.Contains(hash, code) || strings.Contains(hash, strings.ReplaceAll(code, "-", "")) {
				t.Fatalf("recovery code stored in plain text")
			}
		}
		count++
	}
	if count != RecoveryCodeCount {
		t.Fatalf("%d recovery rows", count)
	}

	// A different machine key cannot open it: the sign-in fails closed.
	f.users.UseSecretKey(filepath.Join(t.TempDir(), "other.key"))
	actor, _, _ := f.users.ActorRevision(context.Background(), annID(t, f.users))
	if _, err := f.users.verifyTOTPCode(context.Background(), actor.ID, f.code(secret), f.clock); !errors.Is(err, ErrTOTPUnreadable) {
		t.Fatalf("verify under another key: %v", err)
	}
}

func annID(t *testing.T, users *UserStore) string {
	t.Helper()
	id, _, ok := users.FindAccount(context.Background(), "ann")
	if !ok {
		t.Fatal("ann not found")
	}
	return id
}

func TestAnAuthenticatorCodeCannotBeReplayed(t *testing.T) {
	f := newTOTPFixture(t)
	secret, _ := f.enroll(t)
	id := annID(t, f.users)
	ctx := context.Background()
	code := f.code(secret)
	if ok, err := f.users.verifyTOTPCode(ctx, id, code, f.clock); !ok || err != nil {
		t.Fatalf("first use: %v %v", ok, err)
	}
	if ok, _ := f.users.verifyTOTPCode(ctx, id, code, f.clock); ok {
		t.Fatal("the same code was accepted twice")
	}
	// The previous step's code is inside the skew window but older than what
	// was accepted, so it is refused too.
	previous := hotp(secret, totpCounter(f.clock)-1)
	if ok, _ := f.users.verifyTOTPCode(ctx, id, previous, f.clock); ok {
		t.Fatal("an earlier step was accepted after a later one")
	}
	// The enrollment's confirming code cannot sign in either: its step is
	// the guard's starting point.
	f.tick()
	if ok, _ := f.users.verifyTOTPCode(ctx, id, f.code(secret), f.clock); !ok {
		t.Fatal("the next step's code was refused")
	}
}

func TestSignInWithAnAuthenticatorNeedsBothFactors(t *testing.T) {
	f := newTOTPFixture(t)
	secret, _ := f.enroll(t)
	before := liveSessions(f.sessions)

	// The passcode alone opens nothing and signs nobody out.
	if _, _, _, err := f.sessions.LoginWithOTP(nil, annEmail, f.passcode(t)); !errors.Is(err, ErrSecondFactorRequired) {
		t.Fatalf("LoginWithOTP: %v", err)
	}
	result, err := f.sessions.SignInWithOTP(nil, annEmail, f.passcode(t))
	if err != nil || result.Pending == nil || result.Token != "" {
		t.Fatalf("SignInWithOTP = %+v, %v", result, err)
	}
	if liveSessions(f.sessions) != before {
		t.Fatal("the passcode step changed the sessions")
	}
	ticket := result.Pending.Ticket

	if _, err := f.sessions.CompleteSignIn(nil, "not-a-ticket", f.code(secret)); !errors.Is(err, ErrSignInExpired) {
		t.Fatalf("unknown ticket: %v", err)
	}
	wrong := "000000"
	if wrong == f.code(secret) {
		wrong = "111111"
	}
	if _, err := f.sessions.CompleteSignIn(nil, ticket, wrong); !errors.Is(err, ErrTOTPCode) {
		t.Fatalf("wrong code: %v", err)
	}
	done, err := f.sessions.CompleteSignIn(nil, ticket, f.code(secret))
	if err != nil || done.Token == "" || done.Proof.Method != SecondFactorTOTP || done.Actor.Name != "ann" {
		t.Fatalf("CompleteSignIn = %+v, %v", done, err)
	}
	// It ended the earlier session, as any sign-in does, and left one.
	if liveSessions(f.sessions) != 1 {
		t.Fatalf("%d live sessions after the sign-in", liveSessions(f.sessions))
	}
	if _, err := f.sessions.Authenticate(f.request); err == nil {
		t.Fatal("the earlier session survived the sign-in")
	}
	// The ticket is single use.
	f.tick()
	if _, err := f.sessions.CompleteSignIn(nil, ticket, f.code(secret)); !errors.Is(err, ErrSignInExpired) {
		t.Fatalf("ticket replay: %v", err)
	}
}

func TestAPendingSignInDiesAfterAFewWrongCodes(t *testing.T) {
	f := newTOTPFixture(t)
	secret, _ := f.enroll(t)
	result, err := f.sessions.SignInWithOTP(nil, annEmail, f.passcode(t))
	if err != nil {
		t.Fatal(err)
	}
	wrong := "000000"
	if wrong == f.code(secret) {
		wrong = "111111"
	}
	for range maxSecondFactorAttempts {
		if _, err := f.sessions.CompleteSignIn(nil, result.Pending.Ticket, wrong); !errors.Is(err, ErrTOTPCode) {
			t.Fatalf("wrong code: %v", err)
		}
	}
	if _, err := f.sessions.CompleteSignIn(nil, result.Pending.Ticket, f.code(secret)); !errors.Is(err, ErrSignInExpired) && !errors.Is(err, ErrTooManyLogins) {
		t.Fatalf("burnt ticket: %v", err)
	}
}

func TestWrongAuthenticatorCodesAreBudgetedPerAccount(t *testing.T) {
	f := newTOTPFixture(t)
	secret, _ := f.enroll(t)
	wrong := "000000"
	if wrong == f.code(secret) {
		wrong = "111111"
	}
	id := annID(t, f.users)
	for range totpFailureLimit {
		if _, err := f.sessions.proveSecondFactor(context.Background(), id, wrong); !errors.Is(err, ErrTOTPCode) {
			t.Fatalf("wrong code: %v", err)
		}
	}
	if _, err := f.sessions.proveSecondFactor(context.Background(), id, f.code(secret)); !errors.Is(err, ErrTooManyLogins) {
		t.Fatalf("the right code past the budget: %v", err)
	}
	// The short window passes; the right code works again.
	f.clock = f.clock.Add(totpFailureWindow + time.Second)
	if _, err := f.sessions.proveSecondFactor(context.Background(), id, f.code(secret)); err != nil {
		t.Fatalf("after the window: %v", err)
	}
}

func TestRecoveryCodesAreSingleUse(t *testing.T) {
	f := newTOTPFixture(t)
	_, codes := f.enroll(t)
	result, err := f.sessions.SignInWithOTP(nil, annEmail, f.passcode(t))
	if err != nil {
		t.Fatal(err)
	}
	// Lower case and without dashes, the way a person types it.
	typed := strings.ToLower(strings.ReplaceAll(codes[3], "-", ""))
	done, err := f.sessions.CompleteSignIn(nil, result.Pending.Ticket, typed)
	if err != nil || done.Proof.Method != SecondFactorRecovery || done.Proof.RecoveryCodesLeft != RecoveryCodeCount-1 {
		t.Fatalf("recovery sign-in = %+v, %v", done.Proof, err)
	}
	again, err := f.sessions.SignInWithOTP(nil, annEmail, f.passcode(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.sessions.CompleteSignIn(nil, again.Pending.Ticket, codes[3]); !errors.Is(err, ErrTOTPCode) {
		t.Fatalf("a spent recovery code: %v", err)
	}
	if _, err := f.sessions.CompleteSignIn(nil, again.Pending.Ticket, codes[4]); err != nil {
		t.Fatalf("another recovery code: %v", err)
	}
}

func TestStepUpWithAnAuthenticatorSendsNoMail(t *testing.T) {
	f := newTOTPFixture(t)
	secret, _ := f.enroll(t)
	// A fresh session, not stepped up.
	result, err := f.sessions.SignInWithOTP(nil, annEmail, f.passcode(t))
	if err != nil {
		t.Fatal(err)
	}
	done, err := f.sessions.CompleteSignIn(nil, result.Pending.Ticket, f.code(secret))
	if err != nil {
		t.Fatal(err)
	}
	f.tick()
	r := httptest.NewRequest(http.MethodPost, "/api/auth/step-up", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: done.Token})
	r = r.WithContext(WithActor(r.Context(), done.Actor))

	if method, err := f.sessions.StepUpMethod(r); err != nil || method != SecondFactorTOTP {
		t.Fatalf("StepUpMethod = %q, %v", method, err)
	}
	challenge, err := f.sessions.RequestStepUp(r)
	if err != nil || challenge.Method != SecondFactorTOTP || challenge.Code != "" || challenge.Email != "" {
		t.Fatalf("RequestStepUp = %+v, %v", challenge, err)
	}
	if active, _ := f.sessions.StepUpState(r); active {
		t.Fatal("stepped up before any code")
	}
	// An eight-character mailed-passcode shape is not an authenticator code.
	if _, err := f.sessions.VerifyStepUp(r, "ABCDEFGH"); err == nil {
		t.Fatal("a passcode-shaped code stepped up a two-factor account")
	}
	proof, err := f.sessions.VerifyStepUpProof(r, f.code(secret))
	if err != nil || proof.Method != SecondFactorTOTP || !proof.Until.Equal(f.clock.Add(f.sessions.StepUpWindow())) {
		t.Fatalf("VerifyStepUpProof = %+v, %v", proof, err)
	}
	if active, _ := f.sessions.StepUpState(r); !active {
		t.Fatal("not stepped up after the right code")
	}
	// Replay of the same code is refused.
	if _, err := f.sessions.VerifyStepUp(r, f.code(secret)); err == nil {
		t.Fatal("the same authenticator code stepped up twice")
	}
}

func TestDisableAndRegenerateNeedACurrentCode(t *testing.T) {
	f := newTOTPFixture(t)
	secret, codes := f.enroll(t)
	if _, err := f.sessions.RegenerateRecoveryCodes(f.request, codes[0]); !errors.Is(err, ErrTOTPCode) {
		t.Fatalf("regenerate with a recovery code: %v", err)
	}
	fresh, err := f.sessions.RegenerateRecoveryCodes(f.request, f.code(secret))
	if err != nil || len(fresh) != RecoveryCodeCount {
		t.Fatalf("regenerate: %v", err)
	}
	f.tick()
	// The old set died with the regeneration.
	if _, err := f.sessions.DisableTOTP(f.request, codes[1]); !errors.Is(err, ErrTOTPCode) {
		t.Fatalf("disable with an old recovery code: %v", err)
	}
	proof, err := f.sessions.DisableTOTP(f.request, fresh[0])
	if err != nil || proof.Method != SecondFactorRecovery {
		t.Fatalf("disable with a recovery code: %+v %v", proof, err)
	}
	if enabled, _ := f.users.TOTPEnabled(context.Background(), annID(t, f.users)); enabled {
		t.Fatal("still enabled after disable")
	}
	// One factor again.
	if _, _, _, err := f.sessions.LoginWithOTP(nil, annEmail, f.passcode(t)); err != nil {
		t.Fatalf("one-factor sign-in after disable: %v", err)
	}
}

func TestResetTOTPFindsTheAccountByAddressIDOrName(t *testing.T) {
	f := newTOTPFixture(t)
	ctx := context.Background()
	id := annID(t, f.users)
	sessions := func() int {
		var n int
		if err := f.users.database.QueryRowContext(ctx, `SELECT count(*) FROM sessions WHERE user_id = ?`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, who := range []string{"ANN@example.test", id, "ann"} {
		f.enroll(t)
		// A session someone may hold: the reset drops its persisted copy.
		if _, err := f.users.database.ExecContext(ctx, `INSERT INTO sessions (token_hash, user_id, created_at, expires_at)
VALUES (?, ?, '2026-10-25T00:00:00Z', '2099-01-01T00:00:00Z')`, "hash-"+who, id); err != nil {
			t.Fatal(err)
		}
		actor, wasEnabled, err := f.users.ResetTOTP(ctx, who)
		if err != nil || !wasEnabled || actor.ID != id {
			t.Fatalf("ResetTOTP(%q) = %+v %v %v", who, actor, wasEnabled, err)
		}
		if enabled, _ := f.users.TOTPEnabled(ctx, id); enabled {
			t.Fatalf("still enabled after ResetTOTP(%q)", who)
		}
		if n := sessions(); n != 0 {
			t.Fatalf("%d saved sessions left after ResetTOTP(%q)", n, who)
		}
	}
	if _, _, err := f.users.ResetTOTP(ctx, "nobody@example.test"); !errors.Is(err, ErrNoSuchAccountToReset) {
		t.Fatalf("unknown account: %v", err)
	}
	if _, wasEnabled, err := f.users.ResetTOTP(ctx, "ann"); err != nil || wasEnabled {
		t.Fatalf("reset with nothing on: %v %v", wasEnabled, err)
	}
}

func TestRemovingAnAccountRemovesItsAuthenticator(t *testing.T) {
	f := newTOTPFixture(t)
	f.enroll(t)
	ctx := context.Background()
	if err := f.users.Add(ctx, "bob", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	if err := f.users.SetRole(ctx, "bob", "admin"); err != nil {
		t.Fatal(err)
	}
	if err := f.users.Remove(ctx, "ann"); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := f.users.database.QueryRow(
		`SELECT (SELECT COUNT(*) FROM account_totp) + (SELECT COUNT(*) FROM account_recovery_codes)`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("%d authenticator rows outlived the account", rows)
	}
}
