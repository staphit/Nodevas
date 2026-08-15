package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSecurityHeadersOnEveryResponse pins what the middleware chain covers.
// The guards are registered on the router, so a path that matches no route
// has to get them too: an unmatched URL is exactly where a missing header
// would go unnoticed.
func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	handler := serverForTest(t, nil, nil, nil).Handler()
	// Paths a bare server can answer without a project: the point here is the
	// middleware chain, not the handlers.
	paths := []string{"/api/no-such-endpoint", "/", "/some/spa/route"}

	for _, path := range paths {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		for header, want := range map[string]string{
			"X-Content-Type-Options": "nosniff",
			"Referrer-Policy":        "no-referrer",
			"X-Frame-Options":        "DENY",
		} {
			if got := rec.Header().Get(header); got != want {
				t.Errorf("%s: %s = %q, want %q", path, header, got, want)
			}
		}
		if rec.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("%s: no Content-Security-Policy", path)
		}
	}
}

// TestUnsafeRequestRejectedBeforeRouting checks the media-type guard still
// runs ahead of the handler, including on a route that does not exist.
func TestUnsafeRequestRejectedBeforeRouting(t *testing.T) {
	handler := serverForTest(t, nil, nil, nil).Handler()

	for _, path := range []string{"/api/graph", "/api/no-such-endpoint"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("hello"))
		req.Header.Set("Content-Type", "text/plain")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("%s: status = %d, want %d", path, rec.Code, http.StatusUnsupportedMediaType)
		}
	}
}

func TestUnknownAPIEndpointReturnsNotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/sim", nil)
	rec := httptest.NewRecorder()

	serverForTest(t, nil, nil, nil).Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown API status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
