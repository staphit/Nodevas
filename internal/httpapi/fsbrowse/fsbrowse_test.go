package fsbrowse

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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
