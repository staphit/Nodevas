package node

import (
	"mime"
	"net/http"
	"net/http/httptest"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// serveAttachment writes body as an attachment of node "n1" and returns the
// recorded response of GET /api/nodes/n1/files/<name>.
func serveAttachment(t *testing.T, name string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	st := store.NewStore(t.TempDir())
	dir := st.NodeFilesDir("n1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, store.AttachmentURL("n1", name), nil)
	request = request.WithContext(httpx.WithStore(request.Context(), st))
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = request
	context.Params = gin.Params{
		{Key: "id", Value: "n1"},
		{Key: "file", Value: name},
	}
	New(nil, nil).getNodeFile(context)
	return response
}

func assertDisposition(t *testing.T, response *httptest.ResponseRecorder, wantKind, wantName string) {
	t.Helper()
	kind, params, err := mime.ParseMediaType(response.Header().Get("Content-Disposition"))
	if err != nil {
		t.Fatalf("Content-Disposition = %q: %v", response.Header().Get("Content-Disposition"), err)
	}
	if kind != wantKind {
		t.Fatalf("disposition = %q, want %q", kind, wantKind)
	}
	if params["filename"] != wantName {
		t.Fatalf("filename = %q, want %q", params["filename"], wantName)
	}
}

// An attachment whose body looks like HTML must never be served as text/html,
// however it is named: that would execute attacker script on this origin.
func TestAttachmentWithHTMLBodyIsNeverServedAsHTML(t *testing.T) {
	htmlBody := []byte("<!DOCTYPE html><html><body><script>alert(document.cookie)</script></body></html>")
	for _, name := range []string{"payload", "payload.bin", "payload.unknownext"} {
		t.Run(name, func(t *testing.T) {
			response := serveAttachment(t, name, htmlBody)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if got := response.Header().Get("Content-Type"); got != "application/octet-stream" {
				t.Fatalf("Content-Type = %q, want application/octet-stream", got)
			}
			assertDisposition(t, response, "attachment", name)
			if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("X-Content-Type-Options = %q", got)
			}
			if got := response.Header().Get("Content-Security-Policy"); got != "sandbox" {
				t.Fatalf("Content-Security-Policy = %q", got)
			}
		})
	}
}

// A .js upload is the second half of the attack: it only needs to keep a
// script-ish type to be loadable through <script src>.
func TestScriptAttachmentIsServedAsOpaqueDownload(t *testing.T) {
	for _, name := range []string{"evil.js", "evil.mjs", "evil.html", "evil.svg", "evil.css", "evil.xml"} {
		t.Run(name, func(t *testing.T) {
			response := serveAttachment(t, name, []byte("alert(1)"))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if got := response.Header().Get("Content-Type"); got != "application/octet-stream" {
				t.Fatalf("Content-Type = %q, want application/octet-stream", got)
			}
			assertDisposition(t, response, "attachment", name)
		})
	}
}

// Allowlisted media still renders inline, with the exact declared type.
func TestAllowlistedAttachmentsRenderInline(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n0123456789abcdef")
	cases := []struct {
		name string
		body []byte
		want string
	}{
		{"photo.png", png, "image/png"},
		{"photo.PNG", png, "image/png"},
		{"photo.jpg", []byte("\xff\xd8\xff\xe0 jpeg"), "image/jpeg"},
		{"notes.txt", []byte("<!DOCTYPE html><script>alert(1)</script>"), "text/plain; charset=utf-8"},
		{"paper.pdf", []byte("%PDF-1.7\n"), "application/pdf"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := serveAttachment(t, tc.name, tc.body)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if got := response.Header().Get("Content-Type"); got != tc.want {
				t.Fatalf("Content-Type = %q, want %q", got, tc.want)
			}
			assertDisposition(t, response, "inline", tc.name)
			if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("X-Content-Type-Options = %q", got)
			}
		})
	}
}

func TestAttachmentGETRejectsSymlinkedFileAndDirectory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, linkDirectory := range []bool{false, true} {
		name := "leaf"
		if linkDirectory {
			name = "directory"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			outsideFile := filepath.Join(outside, "secret.txt")
			if err := os.WriteFile(outsideFile, []byte("outside-secret"), 0o644); err != nil {
				t.Fatal(err)
			}
			st := store.NewStore(root)
			filesDir := st.NodeFilesDir("n1")
			if linkDirectory {
				if err := os.MkdirAll(filepath.Dir(filesDir), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filesDir); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			} else {
				if err := os.MkdirAll(filesDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outsideFile, filepath.Join(filesDir, "secret.txt")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}

			request := httptest.NewRequest(http.MethodGet, store.AttachmentURL("n1", "secret.txt"), nil)
			request = request.WithContext(httpx.WithStore(request.Context(), st))
			response := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(response)
			context.Request = request
			context.Params = gin.Params{{Key: "id", Value: "n1"}, {Key: "file", Value: "secret.txt"}}
			New(nil, nil).getNodeFile(context)
			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "outside-secret") {
				t.Fatal("attachment response exposed symlink target")
			}
		})
	}
}
