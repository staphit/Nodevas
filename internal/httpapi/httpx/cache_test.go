package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// etagRouterForTest serves one ETagged GET whose body the test controls, going
// through a real gin engine rather than a bare context: the wrapper has to
// survive gin's own response writer, which is where the status and the header
// flush actually happen.
func etagRouterForTest(body *string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/thing", ETag(func(c *gin.Context) {
		c.JSON(http.StatusOK, map[string]string{"body": *body})
	}))
	return router
}

// get issues one request and returns what came back.
func get(t *testing.T, router *gin.Engine, target, ifNoneMatch string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	if ifNoneMatch != "" {
		request.Header.Set("If-None-Match", ifNoneMatch)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

// The first request has nothing to compare against, so it must carry the full
// body plus the tag and the headers that make the client come back to ask.
func TestAFirstReadReturns200WithAStrongETagAndRevalidateHeaders(t *testing.T) {
	body := "one"
	response := get(t, etagRouterForTest(&body), "/thing", "")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	tag := response.Header().Get("ETag")
	if tag == "" {
		t.Fatal("no ETag on a 200")
	}
	// Strong, not W/: the tag is a hash of the exact bytes being sent, so
	// claiming only semantic equivalence would understate what was checked.
	if strings.HasPrefix(tag, "W/") {
		t.Errorf("ETag = %q, want a strong tag", tag)
	}
	if !strings.HasPrefix(tag, `"`) || !strings.HasSuffix(tag, `"`) {
		t.Errorf("ETag = %q, want a quoted-string", tag)
	}
	// "no-cache" is "revalidate before every reuse", not "do not store"; and
	// "private" keeps a shared proxy from handing one account's data to the next.
	if got := response.Header().Get("Cache-Control"); got != "private, no-cache" {
		t.Errorf("Cache-Control = %q, want %q", got, "private, no-cache")
	}
	if response.Body.Len() == 0 {
		t.Error("the first read returned no body")
	}
}

// This is the whole point of the change: the client refetches after every
// mutation and every websocket event, and the response is almost always the
// one it already holds. That repeat must cost a status line, not a payload.
func TestARepeatReadWithTheSameETagReturns304AndNoBody(t *testing.T) {
	body := "one"
	router := etagRouterForTest(&body)
	first := get(t, router, "/thing", "")

	second := get(t, router, "/thing", first.Header().Get("ETag"))

	if second.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Errorf("304 carried a body: %q", second.Body.String())
	}
	// A 304 must not describe a body it is not sending. net/http strips these
	// in the transport, but a recorder does not, so the handler has to be the
	// one that is correct.
	if got := second.Header().Get("Content-Length"); got != "" {
		t.Errorf("304 carried Content-Length %q", got)
	}
	if got := second.Header().Get("Content-Type"); got != "" {
		t.Errorf("304 carried Content-Type %q", got)
	}
	// The tag has to come back, or the client has nothing to store for the
	// next revalidation.
	if second.Header().Get("ETag") != first.Header().Get("ETag") {
		t.Errorf("304 ETag = %q, want %q", second.Header().Get("ETag"), first.Header().Get("ETag"))
	}
}

// Files on disk are the truth and a watcher pushes changes. A tag that
// survived an edit would show a stale graph, which is the failure this whole
// design refuses to allow.
func TestAChangedResourceReturns200WithADifferentETag(t *testing.T) {
	body := "one"
	router := etagRouterForTest(&body)
	first := get(t, router, "/thing", "")

	body = "two"
	second := get(t, router, "/thing", first.Header().Get("ETag"))

	if second.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 after the resource changed", second.Code)
	}
	if second.Header().Get("ETag") == first.Header().Get("ETag") {
		t.Fatalf("the ETag did not change when the body did: %q", second.Header().Get("ETag"))
	}
	if !strings.Contains(second.Body.String(), "two") {
		t.Errorf("body = %q, want the new content", second.Body.String())
	}
}

// "*" asks "do you have this at all". A 200 is by definition an answer of yes,
// so it matches whatever the current tag happens to be.
func TestIfNoneMatchStarMatchesAnyExistingEntity(t *testing.T) {
	body := "one"
	response := get(t, etagRouterForTest(&body), "/thing", "*")

	if response.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304 for If-None-Match: *", response.Code)
	}
}

// If-None-Match is a list. A client that holds two tags for a URL sends both,
// and a whole-header string compare would miss the one that matches.
func TestIfNoneMatchMatchesOneTagInAList(t *testing.T) {
	body := "one"
	router := etagRouterForTest(&body)
	tag := get(t, router, "/thing", "").Header().Get("ETag")

	response := get(t, router, "/thing", `"stale-one", `+tag+`, "stale-two"`)

	if response.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304 when the list contains the current tag", response.Code)
	}
}

// A tag stored by something that weakened it still names the same entity:
// If-None-Match is compared with the weak function (RFC 9110 13.1.2).
func TestAWeakenedFormOfOurTagStillMatches(t *testing.T) {
	body := "one"
	router := etagRouterForTest(&body)
	tag := get(t, router, "/thing", "").Header().Get("ETag")

	response := get(t, router, "/thing", "W/"+tag)

	if response.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304 for the weakened form of the tag", response.Code)
	}
}

// Garbage in the header must fail towards sending the data. Answering 304 to
// something we could not parse would hand the client a body it never received.
func TestAMalformedIfNoneMatchGetsTheFullResponse(t *testing.T) {
	body := "one"
	router := etagRouterForTest(&body)

	for _, header := range []string{
		"not-a-tag",
		`"unterminated`,
		",,,",
		"   ",
		`W/`,
	} {
		response := get(t, router, "/thing", header)
		if response.Code != http.StatusOK {
			t.Errorf("If-None-Match %q: status = %d, want 200", header, response.Code)
		}
		if response.Body.Len() == 0 {
			t.Errorf("If-None-Match %q: no body", header)
		}
	}
}

// An error body is not data. Tagging it would let a client revalidate a
// transient failure and be told 304 — that the failure is still the resource.
func TestANonSuccessResponseGetsNoETag(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/thing", ETag(func(c *gin.Context) {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": "boom"})
	}))

	response := get(t, router, "/thing", "*")

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	if tag := response.Header().Get("ETag"); tag != "" {
		t.Errorf("error response carried ETag %q", tag)
	}
	if got := response.Header().Get("Cache-Control"); got != "" {
		t.Errorf("error response carried Cache-Control %q", got)
	}
	if !strings.Contains(response.Body.String(), "boom") {
		t.Errorf("the error body was lost: %q", response.Body.String())
	}
}

// Buffering costs memory per in-flight request on a small VM. Past the bound
// the response must still be delivered, whole and correct — just without a tag.
func TestAResponseTooLargeToBufferIsStreamedWithoutAnETag(t *testing.T) {
	oversized := make([]byte, maxETagBody+1)
	for index := range oversized {
		oversized[index] = 'x'
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/thing", ETag(func(c *gin.Context) {
		c.Data(http.StatusOK, "application/octet-stream", oversized)
	}))

	response := get(t, router, "/thing", "")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if tag := response.Header().Get("ETag"); tag != "" {
		t.Errorf("an unbuffered response carried ETag %q", tag)
	}
	if response.Body.Len() != len(oversized) {
		t.Fatalf("body length = %d, want %d", response.Body.Len(), len(oversized))
	}
}

// The tag must never claim a lifetime. Everything ETagged here can change the
// moment someone edits a file, so max-age on one of these responses would be
// the exact staleness bug this design exists to prevent.
func TestAnETaggedResponseNeverCarriesALifetime(t *testing.T) {
	body := "one"
	response := get(t, etagRouterForTest(&body), "/thing", "")

	control := response.Header().Get("Cache-Control")
	for _, forbidden := range []string{"max-age", "immutable", "public"} {
		if strings.Contains(control, forbidden) {
			t.Errorf("Cache-Control %q contains %q", control, forbidden)
		}
	}
}
