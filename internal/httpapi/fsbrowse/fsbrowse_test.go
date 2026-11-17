package fsbrowse

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"nodevas/internal/identity"
)

func TestPostFSMkdirCreatesDirectory(t *testing.T) {
	parent := t.TempDir()
	body, err := json.Marshal(map[string]string{
		"path": parent,
		"name": "new-folder",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/fs/mkdir", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	New(nil, nil).postFSMkdir(ginContext(rec, req))

	if rec.Code != http.StatusOK {
		t.Fatalf("postFSMkdir status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	info, err := os.Stat(filepath.Join(parent, "new-folder"))
	if err != nil {
		t.Fatalf("created directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("created path is not a directory")
	}
}

func TestPostFSMkdirRejectsPathInName(t *testing.T) {
	parent := t.TempDir()
	body, err := json.Marshal(map[string]string{
		"path": parent,
		"name": filepath.Join("nested", "folder"),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/fs/mkdir", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	New(nil, nil).postFSMkdir(ginContext(rec, req))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("postFSMkdir status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if _, err := os.Stat(filepath.Join(parent, "nested")); !os.IsNotExist(err) {
		t.Fatalf("invalid name created a path: %v", err)
	}
}

func TestPostFSOpenUsesValidatedDirectory(t *testing.T) {
	target := t.TempDir()
	body, err := json.Marshal(map[string]string{"path": target})
	if err != nil {
		t.Fatal(err)
	}
	var opened string
	api := New(nil, func(path string) error {
		opened = path
		return nil
	})
	req := httptest.NewRequest(http.MethodPost, "/api/fs/open", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.postFSOpen(ginContext(rec, req))

	if rec.Code != http.StatusOK {
		t.Fatalf("postFSOpen status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	want, err := filepath.Abs(target)
	if err != nil {
		t.Fatal(err)
	}
	if opened != want {
		t.Fatalf("opened path = %q, want %q", opened, want)
	}
}

// dirsAnswer is GET /api/fs/dirs with query on api, decoded with each field
// kept raw so a test can tell an absent field from an empty one.
func dirsAnswer(t *testing.T, api *API, query string) (int, map[string]json.RawMessage) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/fs/dirs?"+query, nil)
	rec := httptest.NewRecorder()
	api.getFSDirs(ginContext(rec, req))
	var out map[string]json.RawMessage
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func names(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var entries []fsDirEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	out := []string{}
	for _, entry := range entries {
		out = append(out, entry.Name)
	}
	return out
}

// The add-agent file picker: only regular files named like the agent's
// program, with one ending at most; dot folders only when asked; the home
// folder always said.
func TestGetFSDirsFilesAndHidden(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"claude", "claude.exe", "Claude.CMD", "claude-old", "claude.tar.gz", "notes.txt", "codex.exe"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{".local", "claude.app", "bin"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	api := New(nil, nil)

	status, got := dirsAnswer(t, api, "path="+url.QueryEscape(dir)+"&files=claude")
	if status != http.StatusOK {
		t.Fatalf("files=claude: %d %v", status, got)
	}
	files := names(t, got["files"])
	slices.Sort(files)
	if want := []string{"Claude.CMD", "claude", "claude.exe"}; !slices.Equal(files, want) {
		t.Fatalf("files = %q, want %q", files, want)
	}
	if dirs := names(t, got["dirs"]); slices.Contains(dirs, ".local") || !slices.Contains(dirs, "claude.app") {
		t.Fatalf("dirs = %q", dirs)
	}
	home, _ := os.UserHomeDir()
	var gotHome string
	if err := json.Unmarshal(got["home"], &gotHome); err != nil || gotHome != home {
		t.Fatalf("home = %s, want %q", got["home"], home)
	}

	status, got = dirsAnswer(t, api, "path="+url.QueryEscape(dir))
	if _, has := got["files"]; status != http.StatusOK || has {
		t.Fatalf("no files asked: %d %v", status, got)
	}
	if _, has := got["home"]; !has {
		t.Fatal("no home without files")
	}
	status, got = dirsAnswer(t, api, "path="+url.QueryEscape(dir)+"&hidden=1")
	if dirs := names(t, got["dirs"]); status != http.StatusOK || !slices.Contains(dirs, ".local") {
		t.Fatalf("hidden=1: %d %q", status, dirs)
	}
	// The drive list (Windows) or / says home too.
	if status, got := dirsAnswer(t, api, ""); status != http.StatusOK || got["home"] == nil {
		t.Fatalf("top: %d %v", status, got)
	}

	for _, stem := range []string{"../x", "Claude", "", "a/b", "-x", "x.exe", strings.Repeat("a", 33), "cl aude"} {
		if status, got := dirsAnswer(t, api, "path="+url.QueryEscape(dir)+"&files="+url.QueryEscape(stem)); status != http.StatusBadRequest {
			t.Errorf("files=%q: %d %v", stem, status, got)
		}
	}
}

// At most 50 files are listed.
func TestGetFSDirsFilesCapped(t *testing.T) {
	dir := t.TempDir()
	for i := range 60 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("agy.%02d", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, got := dirsAnswer(t, New(nil, nil), "path="+url.QueryEscape(dir)+"&files=agy")
	if files := names(t, got["files"]); len(files) != 50 {
		t.Fatalf("%d files", len(files))
	}
}

// remoteAuth is a networked server's authenticator.
type remoteAuth struct{}

func (remoteAuth) Authenticate(*http.Request) (identity.Actor, error) { return identity.Actor{}, nil }
func (remoteAuth) NeedsCSRF() bool                                    { return true }
func (remoteAuth) Remote() bool                                       { return true }

// A networked server lists nothing, whatever is asked.
func TestGetFSDirsRemoteRefusedForEveryParameter(t *testing.T) {
	dir := t.TempDir()
	api := New(remoteAuth{}, nil)
	for _, query := range []string{"", "path=" + url.QueryEscape(dir), "path=" + url.QueryEscape(dir) + "&files=claude",
		"path=" + url.QueryEscape(dir) + "&hidden=1", "files=claude&hidden=1", "files=../x"} {
		if status, got := dirsAnswer(t, api, query); status != http.StatusForbidden || got["files"] != nil || got["home"] != nil {
			t.Errorf("%q: %d %v", query, status, got)
		}
	}
}

func TestPostFSOpenRejectsFile(t *testing.T) {
	target := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(target, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"path": target})
	if err != nil {
		t.Fatal(err)
	}
	api := New(nil, func(string) error {
		t.Fatal("file opener must not run for a file path")
		return nil
	})
	req := httptest.NewRequest(http.MethodPost, "/api/fs/open", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.postFSOpen(ginContext(rec, req))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("postFSOpen status = %d, want %d; body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}
