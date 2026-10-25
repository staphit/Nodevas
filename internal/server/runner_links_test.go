package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

func (s *runnerTestServer) makeLink(t *testing.T, b browser, name string) (runnerwire.CreateLinkResponse, runnerwire.LinkPayload) {
	t.Helper()
	response := s.do(t, &b, http.MethodPost, "/api/runners/links", `{"name":"`+name+`"}`, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("create link = %d %s", response.Code, response.Body)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("a link response may be cached")
	}
	var created runnerwire.CreateLinkResponse
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	payload, err := runnerwire.DecodeLink(created.Link)
	if err != nil {
		t.Fatalf("decode %q: %v", created.Link, err)
	}
	return created, payload
}

func (s *runnerTestServer) redeem(t *testing.T, code string) (int, runnerwire.RedeemLinkResponse) {
	t.Helper()
	body, _ := json.Marshal(runnerwire.RedeemLinkRequest{Code: code, Name: "new-laptop", OS: "linux"})
	response := s.do(t, nil, http.MethodPost, runnerwire.LinkPath, string(body), nil)
	var out runnerwire.RedeemLinkResponse
	_ = json.Unmarshal(response.Body.Bytes(), &out)
	return response.Code, out
}

func TestDeviceLinkRedeemsOnceForItsCreator(t *testing.T) {
	s := newRunnerTestServer(t)
	bea := s.signIn(t, "bea@example.test")

	// No link without a fresh step-up.
	response := s.do(t, &bea, http.MethodPost, "/api/runners/links", `{"name":"laptop"}`, nil)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "STEP_UP_REQUIRED") {
		t.Fatalf("link without step-up = %d %s", response.Code, response.Body)
	}
	if code := s.do(t, nil, http.MethodPost, "/api/runners/links", `{"name":"laptop"}`, nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("signed-out link = %d", code)
	}

	stepUp(t, s, bea)
	created, payload := s.makeLink(t, bea, "laptop")
	if created.Name != "laptop" || created.Access || payload.AccessClientID != "" || payload.AccessClientSecret != "" {
		t.Fatalf("link = %+v %+v", created, payload)
	}
	if payload.Hub != created.Hub || !strings.HasPrefix(payload.Hub, "https://") {
		t.Fatalf("hub = %q / %q", payload.Hub, created.Hub)
	}
	if expires, err := time.Parse(time.RFC3339, created.ExpiresAt); err != nil ||
		expires.Sub(time.Now()) > 10*time.Minute+time.Minute || expires.Sub(time.Now()) < 9*time.Minute {
		t.Fatalf("expiresAt = %q", created.ExpiresAt)
	}

	mails := s.inbox.count()
	code, redeemed := s.redeem(t, payload.Code)
	if code != http.StatusCreated || redeemed.RunnerID == "" || !strings.HasPrefix(redeemed.Token, runnerwire.TokenPrefix) {
		t.Fatalf("redeem = %d %+v", code, redeemed)
	}
	runner, err := s.server.runners.Authenticate(context.Background(), redeemed.Token)
	if err != nil {
		t.Fatalf("token does not authenticate: %v", err)
	}
	if runner.ID != redeemed.RunnerID || runner.Name != "laptop" || runner.OwnerName != "bea" {
		t.Fatalf("runner = %+v", runner)
	}
	// The owner hears about it.
	s.inbox.waitFor(t, mails+1)
	if mail := s.inbox.last(t); mail.to != "bea@example.test" || !strings.Contains(mail.body, "laptop") || !strings.Contains(mail.body, "new-laptop") {
		t.Fatalf("notice = %+v", mail)
	}
	// The runner is bea's: on her list, not cal's.
	listed := func(b browser) bool {
		response := s.do(t, &b, http.MethodGet, "/api/runners", "", nil)
		return strings.Contains(response.Body.String(), redeemed.RunnerID)
	}
	if !listed(bea) {
		t.Fatal("bea does not see her linked runner")
	}
	if listed(s.signIn(t, "cal@example.test")) {
		t.Fatal("cal sees bea's linked runner")
	}

	// Single use: the same code again is a plain 404.
	if code, again := s.redeem(t, payload.Code); code != http.StatusNotFound || again.Token != "" {
		t.Fatalf("replay = %d %+v", code, again)
	}

	ann := s.signIn(t, testEmail)
	trail := s.do(t, &ann, http.MethodGet, "/api/audit?scope=server&limit=200", "", nil).Body.String()
	for _, action := range []string{"runner.link.create", "runner.link.redeem", "runner.link.failed"} {
		if !strings.Contains(trail, action) {
			t.Errorf("audit lacks %s", action)
		}
	}
	if strings.Contains(trail, payload.Code) || strings.Contains(trail, redeemed.Token) {
		t.Fatal("a credential reached the audit trail")
	}
}

func TestDeviceLinkExpires(t *testing.T) {
	s := newRunnerTestServer(t)
	bea := s.signIn(t, "bea@example.test")
	stepUp(t, s, bea)
	_, payload := s.makeLink(t, bea, "laptop")
	later := time.Now().Add(runnerwire.LinkTTLSeconds*time.Second + time.Second)
	s.server.links.SetClock(func() time.Time { return later })
	if code, _ := s.redeem(t, payload.Code); code != http.StatusNotFound {
		t.Fatalf("expired redeem = %d", code)
	}
}

func TestDeviceLinkWrongCodesAreThrottled(t *testing.T) {
	s := newRunnerTestServer(t)
	bea := s.signIn(t, "bea@example.test")
	stepUp(t, s, bea)
	_, payload := s.makeLink(t, bea, "laptop")
	for i := 0; i < 5; i++ {
		if code, _ := s.redeem(t, "wrong-code-"+string(rune('a'+i))); code != http.StatusNotFound {
			t.Fatalf("wrong code %d = %d", i, code)
		}
	}
	// Locked out now, even with the right code.
	if code, _ := s.redeem(t, payload.Code); code != http.StatusTooManyRequests {
		t.Fatalf("after five misses = %d", code)
	}
}

func TestDeviceLinksPerPersonAreCapped(t *testing.T) {
	s := newRunnerTestServer(t)
	bea := s.signIn(t, "bea@example.test")
	stepUp(t, s, bea)
	for i := 0; i < runnerwire.MaxLinksPerUser; i++ {
		s.makeLink(t, bea, "laptop")
	}
	if code := s.do(t, &bea, http.MethodPost, "/api/runners/links", `{"name":"laptop"}`, nil).Code; code != http.StatusTooManyRequests {
		t.Fatalf("link over the cap = %d", code)
	}
	// Someone else's cap is their own.
	cal := s.signIn(t, "cal@example.test")
	stepUp(t, s, cal)
	s.makeLink(t, cal, "desk")
}

func TestDeviceLinkDiesWithAnAccountChange(t *testing.T) {
	s := newRunnerTestServer(t)
	bea := s.signIn(t, "bea@example.test")
	stepUp(t, s, bea)
	_, payload := s.makeLink(t, bea, "laptop")
	if err := s.server.users.SetEmail(context.Background(), "bea", "bea2@example.test"); err != nil {
		t.Fatal(err)
	}
	if code, _ := s.redeem(t, payload.Code); code != http.StatusNotFound {
		t.Fatalf("redeem after the account changed = %d", code)
	}
}

func TestRunnerAccessTokenIsAdminOnlyAndNeverReadBack(t *testing.T) {
	s := newRunnerTestServer(t)
	const secret = "s3cr3t-access-secret-value"
	body := `{"clientId":"abc123.access","clientSecret":"` + secret + `"}`

	bea := s.signIn(t, "bea@example.test")
	stepUp(t, s, bea)
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		payload := ""
		if method == http.MethodPut {
			payload = body
		}
		if code := s.do(t, &bea, method, "/api/runners/access-token", payload, nil).Code; code != http.StatusForbidden {
			t.Fatalf("member %s = %d", method, code)
		}
	}

	ann := s.signIn(t, testEmail)
	get := func() (int, string) {
		response := s.do(t, &ann, http.MethodGet, "/api/runners/access-token", "", nil)
		return response.Code, response.Body.String()
	}
	if code, out := get(); code != http.StatusOK || !strings.Contains(out, `"set":false`) {
		t.Fatalf("GET before = %d %s", code, out)
	}
	if code := s.do(t, &ann, http.MethodPut, "/api/runners/access-token", body, nil).Code; code != http.StatusForbidden {
		t.Fatalf("PUT without step-up = %d", code)
	}
	stepUp(t, s, ann)
	response := s.do(t, &ann, http.MethodPut, "/api/runners/access-token", body, nil)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), secret) {
		t.Fatalf("PUT = %d %s", response.Code, response.Body)
	}
	code, out := get()
	if code != http.StatusOK || !strings.Contains(out, `"set":true`) || !strings.Contains(out, "abc123.access") || strings.Contains(out, secret) {
		t.Fatalf("GET after = %d %s", code, out)
	}
	// Encrypted at rest.
	files, _ := filepath.Glob(filepath.Join(s.server.pm.CatalogRoot(), "secrets", "runner-access-*.enc"))
	if len(files) != 1 {
		t.Fatalf("secrets files = %v", files)
	}
	if raw, _ := os.ReadFile(files[0]); strings.Contains(string(raw), secret) || strings.Contains(string(raw), "abc123") {
		t.Fatal("the access token is stored in the clear")
	}
	trail := s.do(t, &ann, http.MethodGet, "/api/audit?scope=server&limit=200", "", nil).Body.String()
	if !strings.Contains(trail, "runner.access-token.set") || strings.Contains(trail, secret) {
		t.Fatal("audit missing the set, or holding the secret")
	}

	// An administrator's links now carry it; a member's never do.
	created, payload := s.makeLink(t, ann, "laptop")
	if !created.Access || payload.AccessClientID != "abc123.access" || payload.AccessClientSecret != secret {
		t.Fatalf("link = %+v %+v", created, payload)
	}
	created, payload = s.makeLink(t, bea, "laptop")
	if created.Access || payload.AccessClientID != "" || payload.AccessClientSecret != "" {
		t.Fatalf("a member's link carries the access token: %+v %+v", created, payload)
	}

	if code := s.do(t, &ann, http.MethodDelete, "/api/runners/access-token", "", nil).Code; code != http.StatusNoContent {
		t.Fatalf("DELETE = %d", code)
	}
	if code, out := get(); code != http.StatusOK || !strings.Contains(out, `"set":false`) {
		t.Fatalf("GET after clear = %d %s", code, out)
	}
	_, payload = s.makeLink(t, ann, "laptop")
	if payload.AccessClientSecret != "" {
		t.Fatal("a cleared token still rides on links")
	}
}
