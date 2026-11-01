package system

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// A sibling subdomain can set a nodevas_signin cookie for the parent domain,
// which the browser sends before the hub's own: the second step would then
// run on the sibling's ticket, and the person could not finish signing in
// while it lived. Over TLS the ticket is a __Host- cookie, which no sibling
// can set, and a plain-named one counts for nothing; over plain HTTP a
// request carrying two is refused.
func TestSignInTicketCookieCannotBeTossed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "https://hub.example.test/api/auth/login", nil)
	setSignInTicket(c, "mine", 300)
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("set %d cookies, want 1", len(cookies))
	}
	ticket := cookies[0]
	if ticket.Name != "__Host-nodevas_signin" || ticket.Value != "mine" || !ticket.Secure || ticket.Path != "/" ||
		ticket.Domain != "" || !ticket.HttpOnly || ticket.SameSite != http.SameSiteStrictMode {
		t.Fatalf("ticket cookie over TLS = %+v, want __Host-nodevas_signin, Secure, HttpOnly, Strict, Path=/, no Domain", ticket)
	}

	read := func(target, cookie string) string {
		request := httptest.NewRequest(http.MethodPost, target, nil)
		request.Header.Set("Cookie", cookie)
		return signInTicket(request)
	}
	if got := read("https://hub.example.test/api/auth/login/totp", "nodevas_signin=tossed; __Host-nodevas_signin=mine"); got != "mine" {
		t.Fatalf("ticket over TLS beside a tossed plain cookie = %q, want mine", got)
	}
	if got := read("https://hub.example.test/api/auth/login/totp", "nodevas_signin=tossed"); got != "" {
		t.Fatalf("a plain ticket cookie over TLS = %q, want none", got)
	}
	if got := read("/api/auth/login/totp", "nodevas_signin=tossed; nodevas_signin=mine"); got != "" {
		t.Fatalf("two ticket cookies over HTTP = %q, want none", got)
	}
	if got := read("/api/auth/login/totp", "nodevas_signin=mine"); got != "mine" {
		t.Fatalf("one ticket cookie over HTTP = %q, want mine", got)
	}
}
