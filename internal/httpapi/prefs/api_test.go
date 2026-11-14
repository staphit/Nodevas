package prefs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/identity"
	"nodevas/internal/prefs"
	"nodevas/internal/testpg"
)

func TestMain(m *testing.M) { os.Exit(testpg.Main(m)) }

var actors = map[string]identity.Actor{
	"local": identity.Local,
	"bea":   {ID: "u-bea", Name: "bea", Role: identity.RoleMember},
	"cal":   {ID: "u-cal", Name: "cal", Role: identity.RoleMember},
	"agent": {ID: "u-bea", Name: "bea", Role: identity.RoleMember, Agent: identity.AgentWorker},
	"blank": {},
}

func testRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	// Stands in for withAuth: the actor rides on the request context. No
	// X-Test-Actor header attaches nobody, as on a public route.
	router.Use(func(c *gin.Context) {
		if actor, ok := actors[c.GetHeader("X-Test-Actor")]; ok {
			c.Request = c.Request.WithContext(auth.WithActor(c.Request.Context(), actor))
		}
		c.Next()
	})
	New(prefs.NewStore(testpg.New(t))).Register(router.Group("/api"))
	return router
}

func call(router http.Handler, who, method, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, Path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if who != "" {
		request.Header.Set("X-Test-Actor", who)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func snapshot(t *testing.T, response *httptest.ResponseRecorder) prefs.Snapshot {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	var out prefs.Snapshot
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func code(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	return body.Code
}

func TestReadAndChangeOwnPreferences(t *testing.T) {
	router := testRouter(t)
	if got := snapshot(t, call(router, "local", http.MethodGet, "")); len(got.Values) != 0 {
		t.Fatalf("fresh = %+v", got)
	}
	got := snapshot(t, call(router, "local", http.MethodPatch, `{"set":{"theme":"\"dark\"","textScale":1.1,"copilot":{"voice":true}}}`))
	if string(got.Values["theme"]) != `"\"dark\""` || string(got.Values["textScale"]) != "1.1" ||
		string(got.Values["copilot"]) != `{"voice":true}` || got.UpdatedAt["theme"] == "" {
		t.Fatalf("after set = %+v", got)
	}
	// The doc's shape and the shorthand: remove names keys, a null drops one.
	got = snapshot(t, call(router, "local", http.MethodPatch, `{"set":{"theme":null},"remove":["textScale"]}`))
	if len(got.Values) != 1 || got.Values["copilot"] == nil {
		t.Fatalf("after remove = %+v", got)
	}
	if got := snapshot(t, call(router, "local", http.MethodGet, "")); len(got.Values) != 1 {
		t.Fatalf("read back = %+v", got)
	}
	// An empty change changes nothing and answers what there is.
	if got := snapshot(t, call(router, "local", http.MethodPatch, `{}`)); len(got.Values) != 1 {
		t.Fatalf("empty change = %+v", got)
	}
}

func TestEachPersonHasTheirOwn(t *testing.T) {
	router := testRouter(t)
	snapshot(t, call(router, "bea", http.MethodPatch, `{"set":{"theme":"dark"}}`))
	snapshot(t, call(router, "cal", http.MethodPatch, `{"set":{"theme":"light","language":"en"}}`))
	snapshot(t, call(router, "local", http.MethodPatch, `{"set":{"language":"zh-TW"}}`))
	bea := snapshot(t, call(router, "bea", http.MethodGet, ""))
	cal := snapshot(t, call(router, "cal", http.MethodGet, ""))
	local := snapshot(t, call(router, "local", http.MethodGet, ""))
	if len(bea.Values) != 1 || string(bea.Values["theme"]) != `"dark"` {
		t.Fatalf("bea = %v", bea.Values)
	}
	if len(cal.Values) != 2 || string(cal.Values["theme"]) != `"light"` {
		t.Fatalf("cal = %v", cal.Values)
	}
	if len(local.Values) != 1 || string(local.Values["language"]) != `"zh-TW"` {
		t.Fatalf("local = %v", local.Values)
	}
}

// An agent has no preferences, whoever it acts for; neither has a request
// nobody signed in to.
func TestAgentsAndNobodyAreRefused(t *testing.T) {
	router := testRouter(t)
	snapshot(t, call(router, "bea", http.MethodPatch, `{"set":{"theme":"dark"}}`))
	for _, who := range []string{"agent", "blank", ""} {
		for _, method := range []string{http.MethodGet, http.MethodPatch} {
			body := ""
			if method == http.MethodPatch {
				body = `{"set":{"theme":"light"}}`
			}
			response := call(router, who, method, body)
			if response.Code != http.StatusForbidden || code(t, response) != "PERMISSION_DENIED" {
				t.Errorf("%q %s = %d %s", who, method, response.Code, response.Body)
			}
			if strings.Contains(response.Body.String(), "dark") {
				t.Errorf("%q %s read bea's preferences: %s", who, method, response.Body)
			}
		}
	}
	if got := snapshot(t, call(router, "bea", http.MethodGet, "")); string(got.Values["theme"]) != `"dark"` {
		t.Fatalf("bea's theme = %s", got.Values["theme"])
	}
}

func TestBadChangesAreRefused(t *testing.T) {
	router := testRouter(t)
	for name, body := range map[string]string{
		"bad key":       `{"set":{"no spaces":1}}`,
		"long value":    `{"set":{"theme":"` + strings.Repeat("x", prefs.MaxValueBytes) + `"}}`,
		"unknown field": `{"set":{"theme":1},"replace":true}`,
		"not an object": `["theme"]`,
		"two values":    `{"set":{}} {"set":{}}`,
		"both ways":     `{"set":{"theme":1},"remove":["theme"]}`,
		"remove a list": `{"remove":"theme"}`,
	} {
		response := call(router, "local", http.MethodPatch, body)
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, response.Code, response.Body)
		}
	}
	keys := make([]string, 0, prefs.MaxKeysPerChange+1)
	for i := range prefs.MaxKeysPerChange + 1 {
		keys = append(keys, fmt.Sprintf(`"k%d":1`, i))
	}
	response := call(router, "local", http.MethodPatch, `{"set":{`+strings.Join(keys, ",")+`}}`)
	if response.Code != http.StatusBadRequest || code(t, response) != "INVALID_ARGUMENT" {
		t.Fatalf("too many keys: %d %s", response.Code, response.Body)
	}
	// A body past any change's size is cut off before it is read.
	huge := `{"set":{"theme":"` + strings.Repeat("x", maxChangeBody) + `"}}`
	if response := call(router, "local", http.MethodPatch, huge); response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("huge body: %d", response.Code)
	}
	if got := snapshot(t, call(router, "local", http.MethodGet, "")); len(got.Values) != 0 {
		t.Fatalf("a refused change was kept: %v", got.Values)
	}
}

// A change at its largest fits the body limit; an account past its limits
// is told so with the limits.
func TestLimits(t *testing.T) {
	router := testRouter(t)
	value := `"` + strings.Repeat(`\"`, (prefs.MaxValueBytes-2)/2) + `"`
	pairs := make([]string, 0, prefs.MaxKeysPerChange)
	for i := range prefs.MaxKeysPerChange {
		pairs = append(pairs, fmt.Sprintf(`"key.%02d.%s":%s`, i, strings.Repeat("x", prefs.MaxKeyBytes-7), value))
	}
	response := call(router, "local", http.MethodPatch, `{"set":{`+strings.Join(pairs[:31], ",")+`}}`)
	if response.Code != http.StatusOK {
		t.Fatalf("31 values at the limit: %d %s", response.Code, response.Body)
	}
	response = call(router, "local", http.MethodPatch, `{"set":{`+strings.Join(pairs, ",")+`}}`)
	if response.Code != http.StatusRequestEntityTooLarge || code(t, response) != "RESOURCE_EXHAUSTED" ||
		!strings.Contains(response.Body.String(), `"maxKeys":200`) || !strings.Contains(response.Body.String(), `"maxBytes":131072`) {
		t.Fatalf("past the account's bytes: %d %s", response.Code, response.Body)
	}
}
