package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// conditionalGet asks for target, optionally presenting a tag the caller
// claims to already hold.
func conditionalGet(t *testing.T, server *Server, target, ifNoneMatch string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	if ifNoneMatch != "" {
		request.Header.Set("If-None-Match", ifNoneMatch)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

// The tag is a hash of the response body, and the reasoning that this is
// enough rests on the bodies actually differing. ?project= puts two different
// graphs behind one URL, so if that reasoning were wrong this is where one
// project would be handed the other's 304 — and, being a 304, would keep
// showing the graph it already had.
func TestTwoProjectsDoNotShareAnETagForTheSameURL(t *testing.T) {
	server, _ := twoProjectServer(t)

	main := conditionalGet(t, server, "/api/graph?project=main", "")
	if main.Code != http.StatusOK {
		t.Fatalf("main status = %d, body = %s", main.Code, main.Body)
	}
	other := conditionalGet(t, server, "/api/graph?project=other", "")
	if other.Code != http.StatusOK {
		t.Fatalf("other status = %d, body = %s", other.Code, other.Body)
	}
	if main.Header().Get("ETag") == "" || other.Header().Get("ETag") == "" {
		t.Fatal("/api/graph did not carry an ETag")
	}
	if main.Header().Get("ETag") == other.Header().Get("ETag") {
		t.Fatalf("both projects share the ETag %q", main.Header().Get("ETag"))
	}

	// Presenting one project's tag to the other must yield that other
	// project's data, not a 304.
	crossed := conditionalGet(t, server, "/api/graph?project=other", main.Header().Get("ETag"))
	if crossed.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 when another project's tag is presented", crossed.Code)
	}
	if !strings.Contains(crossed.Body.String(), "only-in-other") {
		t.Fatalf("the wrong project's graph came back: %s", crossed.Body)
	}

	// And the honest tag still works for the project it belongs to.
	repeat := conditionalGet(t, server, "/api/graph?project=other", other.Header().Get("ETag"))
	if repeat.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304 for the project's own tag", repeat.Code)
	}
}

// Spot-check that the read endpoints the client hammers after every mutation
// are actually wrapped. A route added later without the wrapper silently
// re-downloads its whole body forever, and nothing else would notice.
func TestTheHotReadEndpointsCarryAnETag(t *testing.T) {
	server, _ := twoProjectServer(t)
	create := httptest.NewRequest(
		http.MethodPost, "/api/nodes", strings.NewReader(`{"id":"cached","title":"Cached","body":"hi"}`))
	create.Header.Set("Content-Type", "application/json")
	created := httptest.NewRecorder()
	server.Handler().ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create node: status = %d, body = %s", created.Code, created.Body)
	}

	for _, target := range []string{
		"/api/graph",
		"/api/state",
		"/api/nodes/cached",
		"/api/nodes/cached/pages",
		"/api/folders",
		"/api/trash",
		"/api/links?project=main&node=cached",
		"/api/links/targets",
		"/api/projects",
		"/api/workspaces/statuses",
		"/api/workspaces/order",
	} {
		response := conditionalGet(t, server, target, "")
		if response.Code != http.StatusOK {
			t.Errorf("%s: status = %d, body = %s", target, response.Code, response.Body)
			continue
		}
		if response.Header().Get("ETag") == "" {
			t.Errorf("%s: no ETag", target)
		}
		if response.Header().Get("Cache-Control") != "private, no-cache" {
			t.Errorf("%s: Cache-Control = %q", target, response.Header().Get("Cache-Control"))
		}
		if conditionalGet(t, server, target, response.Header().Get("ETag")).Code != http.StatusNotModified {
			t.Errorf("%s: a repeat request with the tag did not get a 304", target)
		}
	}
}

// /api/auth/status and /api/search are deliberately untouched: one reports a
// session whose expiry the server must be free to re-evaluate, the other
// answers a query rather than serving a resource.
func TestTheExcludedEndpointsGetNoCachingHeaders(t *testing.T) {
	server, _ := twoProjectServer(t)

	for _, target := range []string{"/api/auth/status", "/api/search?q=x", "/api/validate"} {
		response := conditionalGet(t, server, target, "")
		if tag := response.Header().Get("ETag"); tag != "" {
			t.Errorf("%s: carried ETag %q", target, tag)
		}
		if control := response.Header().Get("Cache-Control"); control != "" {
			t.Errorf("%s: carried Cache-Control %q", target, control)
		}
	}
}

// A history version is a finished snapshot: it is written once, never
// rewritten, and its timestamped name is never reissued. That is the only kind
// of resource in this app that has earned a lifetime, so a second request for
// it should not happen at all.
func TestAHistoryVersionIsServedAsImmutable(t *testing.T) {
	server, pm := twoProjectServer(t)
	store := pm.Store()

	// Two versioned writes: the second snapshots what the first left on disk,
	// which is the thing this endpoint serves.
	for _, content := range []string{"version: 1\nnodes: []\n", "version: 1\nnodes: []\n# later\n"} {
		if err := store.WriteVersioned(store.GraphPath(), []byte(content)); err != nil {
			t.Fatalf("WriteVersioned: %v", err)
		}
	}
	versions, err := store.ListHistory("graph.yaml")
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(versions) == 0 {
		t.Fatal("no history snapshot was taken")
	}

	target := "/api/history/version?path=graph.yaml&version=" + versions[0].Name
	response := conditionalGet(t, server, target, "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	if got := response.Header().Get("Cache-Control"); got != "private, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q", got)
	}
	// Private, because the snapshot is a document behind a login; a shared
	// cache keeping it for a year would be the worst version of that leak.
	if strings.Contains(response.Header().Get("Cache-Control"), "public") {
		t.Error("a history version was offered to shared caches")
	}
}

// The lifetime is only for the success path. A 400 that told the browser to
// keep the answer for a year could not be taken back.
func TestAFailedHistoryVersionReadGetsNoLifetime(t *testing.T) {
	server, _ := twoProjectServer(t)

	response := conditionalGet(t, server, "/api/history/version?path=graph.yaml&version=nope", "")
	if response.Code == http.StatusOK {
		t.Fatalf("an invalid version name was accepted: %s", response.Body)
	}
	if control := response.Header().Get("Cache-Control"); strings.Contains(control, "max-age") {
		t.Fatalf("an error response carried %q", control)
	}
}

// An attachment's URL is stable but its contents are not: the user can upload
// a new file over the same name. It revalidates, and it must never be told to
// skip asking.
func TestAnAttachmentRevalidatesRatherThanBeingImmutable(t *testing.T) {
	server, pm := twoProjectServer(t)
	directory := pm.Store().NodeFilesDir("node-0001")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	target := "/api/nodes/node-0001/files/note.txt"
	response := conditionalGet(t, server, target, "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	tag := response.Header().Get("ETag")
	if tag == "" {
		t.Fatal("the attachment carried no ETag")
	}
	// Weak on purpose: the tag is size and modification time, not a hash of
	// the bytes, so it is a claim about the file's identity rather than its
	// content, and W/ is the honest way to say that.
	if !strings.HasPrefix(tag, `W/"`) {
		t.Errorf("attachment ETag = %q, want a weak tag", tag)
	}
	if got := response.Header().Get("Cache-Control"); got != "private, no-cache" {
		t.Fatalf("Cache-Control = %q, want %q", got, "private, no-cache")
	}
	if strings.Contains(response.Header().Get("Cache-Control"), "immutable") {
		t.Fatal("an attachment was declared immutable")
	}

	// http.ServeFile answers the conditional request itself once the tag is set.
	repeat := conditionalGet(t, server, target, tag)
	if repeat.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304 for an unchanged attachment", repeat.Code)
	}
	if repeat.Body.Len() != 0 {
		t.Errorf("304 carried a body: %q", repeat.Body.String())
	}
}
