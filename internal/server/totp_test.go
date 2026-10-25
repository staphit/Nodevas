package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1" // #nosec G505 -- RFC 6238's HMAC, recomputed independently of internal/auth.
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
)

// totpAt is an authenticator app: RFC 6238 over a base32 secret, written
// here from the standard rather than borrowed from the package under test.
func totpAt(t *testing.T, secret string, at time.Time, steps int64) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], uint64(at.Unix()/30+steps))
	mac := hmac.New(sha1.New, key)
	mac.Write(message[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1_000_000)
}

// authed sends a signed-in request with the CSRF header.
func authed(server *Server, cookies []*http.Cookie, csrf, method, path, body string) *httptest.ResponseRecorder {
	request := withCookies(httptest.NewRequest(method, path, strings.NewReader(body)), cookies)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(auth.CSRFHeaderName, csrf)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func totpDecode[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", response.Body, err)
	}
	return out
}

func TestTwoFactorOverHTTP(t *testing.T) {
	// A key of the test's own: the default would be the real user config
	// directory's machine key.
	t.Setenv("NODEVAS_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	server, _, inbox, database, _ := accountServerPartsForTest(t)
	cookies, csrf := signIn(t, server, inbox, testEmail)

	// Enrollment needs a step-up.
	if response := authed(server, cookies, csrf, http.MethodPost, "/api/auth/totp/begin", `{}`); response.Code != http.StatusForbidden ||
		!strings.Contains(response.Body.String(), "STEP_UP_REQUIRED") {
		t.Fatalf("begin without step-up = %d %s", response.Code, response.Body)
	}
	status := totpDecode[map[string]any](t, authed(server, cookies, csrf, http.MethodGet, "/api/auth/step-up", ""))
	if status["method"] != "email" {
		t.Fatalf("step-up status before = %v", status)
	}
	before := inbox.count()
	if response := authed(server, cookies, csrf, http.MethodPost, "/api/auth/step-up/request", `{}`); response.Code != http.StatusAccepted {
		t.Fatalf("step-up request = %d %s", response.Code, response.Body)
	}
	inbox.waitFor(t, before+1)
	if response := authed(server, cookies, csrf, http.MethodPost, "/api/auth/step-up", `{"code":"`+inbox.code(t)+`"}`); response.Code != http.StatusOK {
		t.Fatalf("step-up = %d %s", response.Code, response.Body)
	}

	begin := authed(server, cookies, csrf, http.MethodPost, "/api/auth/totp/begin", `{}`)
	if begin.Code != http.StatusOK || begin.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("begin = %d %s", begin.Code, begin.Body)
	}
	enrollment := totpDecode[struct {
		Secret, URI, QR, Account string
	}](t, begin)
	if !strings.HasPrefix(enrollment.URI, "otpauth://totp/Nodevas:") || enrollment.Account != testEmail {
		t.Fatalf("enrollment = %+v", enrollment)
	}
	const pngPrefix = "data:image/png;base64,"
	image, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(enrollment.QR, pngPrefix))
	if !strings.HasPrefix(enrollment.QR, pngPrefix) || err != nil || !bytes.HasPrefix(image, []byte("\x89PNG")) {
		t.Fatalf("qr is not a PNG data URL: %.40q %v", enrollment.QR, err)
	}

	// The sign-in below needs a fresh passcode, and one address gets one
	// every thirty seconds.
	time.Sleep(30 * time.Second)
	now := time.Now()
	noticesBefore := inbox.count()
	confirm := authed(server, cookies, csrf, http.MethodPost, "/api/auth/totp/confirm",
		`{"code":"`+totpAt(t, enrollment.Secret, now, 0)+`"}`)
	if confirm.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s", confirm.Code, confirm.Body)
	}
	recovery := totpDecode[struct {
		Enabled       bool
		RecoveryCodes []string
	}](t, confirm)
	if !recovery.Enabled || len(recovery.RecoveryCodes) != auth.RecoveryCodeCount {
		t.Fatalf("confirm body = %+v", recovery)
	}
	// The account holder hears about it, at the address passcodes go to.
	inbox.waitFor(t, noticesBefore+1)
	if notice := inbox.last(t); notice.to != testEmail || !strings.Contains(notice.body, "turned on") {
		t.Fatalf("enable notice = %+v", notice)
	}

	// The passcode now only gets as far as the second step.
	pending := login(server, testEmail, requestPasscode(t, server, inbox, testEmail))
	if pending.Code != http.StatusOK {
		t.Fatalf("login = %d %s", pending.Code, pending.Body)
	}
	if body := totpDecode[map[string]any](t, pending); body["secondFactor"] != "totp" || body["ok"] != false {
		t.Fatalf("login body = %v", body)
	}
	var ticket *http.Cookie
	for _, cookie := range pending.Result().Cookies() {
		switch cookie.Name {
		case auth.SessionCookieName, auth.CSRFCookieName:
			t.Fatalf("the passcode alone set %s", cookie.Name)
		case "nodevas_signin":
			ticket = cookie
		}
	}
	if ticket == nil || !ticket.HttpOnly || ticket.Path != "/api/auth/login/totp" || ticket.SameSite != http.SameSiteStrictMode {
		t.Fatalf("ticket cookie = %+v", ticket)
	}

	finish := func(code string, withTicket bool) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/auth/login/totp", strings.NewReader(`{"code":"`+code+`"}`))
		request.Header.Set("Content-Type", "application/json")
		if withTicket {
			request.AddCookie(ticket)
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response
	}
	if response := finish(totpAt(t, enrollment.Secret, now, 1), false); response.Code != http.StatusUnauthorized {
		t.Fatalf("no ticket = %d %s", response.Code, response.Body)
	}
	// The confirming code is spent; the next step's is not.
	if response := finish(totpAt(t, enrollment.Secret, now, 0), true); response.Code != http.StatusUnauthorized {
		t.Fatalf("replayed confirming code = %d %s", response.Code, response.Body)
	}
	done := finish(totpAt(t, enrollment.Secret, now, 1), true)
	if done.Code != http.StatusOK {
		t.Fatalf("second step = %d %s", done.Code, done.Body)
	}
	cookies = done.Result().Cookies()
	csrf = ""
	for _, cookie := range cookies {
		if cookie.Name == auth.CSRFCookieName {
			csrf = cookie.Value
		}
	}
	if csrf == "" {
		t.Fatal("the second step did not sign in")
	}

	// Step-up is now the authenticator's, and nothing is mailed.
	status = totpDecode[map[string]any](t, authed(server, cookies, csrf, http.MethodGet, "/api/auth/step-up", ""))
	if status["method"] != "totp" || status["active"] != false {
		t.Fatalf("step-up status after = %v", status)
	}
	mails := inbox.count()
	request := authed(server, cookies, csrf, http.MethodPost, "/api/auth/step-up/request", `{}`)
	if request.Code != http.StatusOK || !strings.Contains(request.Body.String(), `"totp"`) {
		t.Fatalf("step-up request = %d %s", request.Code, request.Body)
	}
	inbox.settle()
	if inbox.count() != mails {
		t.Fatal("a two-factor step-up mailed a passcode")
	}
	stepped := authed(server, cookies, csrf, http.MethodPost, "/api/auth/step-up", `{"code":"`+recovery.RecoveryCodes[0]+`"}`)
	if stepped.Code != http.StatusOK {
		t.Fatalf("step-up with a recovery code = %d %s", stepped.Code, stepped.Body)
	}
	if body := totpDecode[map[string]any](t, stepped); body["active"] != true || body["method"] != "totp" {
		t.Fatalf("step-up body = %v", body)
	}

	state := totpDecode[auth.TOTPState](t, authed(server, cookies, csrf, http.MethodGet, "/api/auth/totp", ""))
	if !state.Enabled || state.RecoveryCodesLeft != auth.RecoveryCodeCount-1 {
		t.Fatalf("state = %+v", state)
	}
	if response := authed(server, cookies, csrf, http.MethodPost, "/api/auth/totp/disable", `{"code":"000000x"}`); response.Code != http.StatusUnauthorized {
		t.Fatalf("disable with a bad code = %d %s", response.Code, response.Body)
	}
	if response := authed(server, cookies, csrf, http.MethodPost, "/api/auth/totp/disable",
		`{"code":"`+recovery.RecoveryCodes[1]+`"}`); response.Code != http.StatusOK {
		t.Fatalf("disable = %d %s", response.Code, response.Body)
	}

	events, err := audit.New(database).Query(context.Background(), audit.Filter{ActionPrefix: "auth."})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, event := range events {
		seen[event.Action]++
		for key, value := range event.Detail {
			if text, ok := value.(string); ok && (strings.Contains(text, enrollment.Secret) || strings.Contains(text, recovery.RecoveryCodes[0])) {
				t.Fatalf("audit %s.%s holds a credential", event.Action, key)
			}
		}
	}
	for _, want := range []string{
		audit.ActionTOTPEnrollBegin, audit.ActionTOTPEnable, audit.ActionSignInPending,
		audit.ActionTOTPDisable, audit.ActionTOTPFailed,
	} {
		if seen[want] == 0 {
			t.Errorf("no %s event; have %v", want, seen)
		}
	}
	if seen[audit.ActionTOTPRecoveryUsed] != 2 {
		t.Errorf("%d recovery-used events, want 2 (step-up, disable)", seen[audit.ActionTOTPRecoveryUsed])
	}
}
