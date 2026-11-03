package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestServeStaticMissingAssetIsUncached404(t *testing.T) {
	s := &Server{webFS: fstest.MapFS{
		"index.html":      {Data: []byte("<!doctype html><title>app</title>")},
		"assets/app-1.js": {Data: []byte("console.log(1)")},
		"favicon.svg":     {Data: []byte("<svg/>")},
	}}
	serve := func(path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		s.serveStatic(ginContext(response, httptest.NewRequest(http.MethodGet, path, nil)))
		return response
	}

	for _, path := range []string{"/assets/app-old.js", "/assets/style-old.css", "/missing.png"} {
		response := serve(path)
		if response.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, response.Code)
		}
		if got := response.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control %q, want no-store", path, got)
		}
		if strings.Contains(response.Body.String(), "<!doctype html>") {
			t.Errorf("%s: answered with the app shell", path)
		}
	}

	// Client-side routes still load the shell.
	for _, path := range []string{"/", "/popout", "/projects/main"} {
		response := serve(path)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "<title>app</title>") {
			t.Errorf("%s: status %d body %q, want the app shell", path, response.Code, response.Body.String())
		}
		if got := response.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("%s: Cache-Control %q, want no-cache", path, got)
		}
	}

	if response := serve("/assets/app-1.js"); response.Code != http.StatusOK ||
		!strings.Contains(response.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("existing asset: status %d Cache-Control %q", response.Code, response.Header().Get("Cache-Control"))
	}
}
