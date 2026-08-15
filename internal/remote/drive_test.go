package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// driveMock is a minimal stand-in for the Drive v3 REST API plus the OAuth
// token endpoint, enough to drive push / list / pull and token refresh.
type driveMock struct {
	server        *httptest.Server
	bundle        []byte
	tokenHits     int
	uploadHits    int
	listHits      int
	folderQueries []string
	// failListUntil makes the first N list calls answer 401 so the 401-retry
	// Path can be exercised.
	failListUntil int
	failPushUntil int
}

func newDriveMock(t *testing.T) *driveMock {
	t.Helper()
	m := &driveMock{}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		m.tokenHits++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fresh-access",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})
	mux.HandleFunc("/files", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "no auth", http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodPost: // upload
			m.uploadHits++
			if m.uploadHits <= m.failPushUntil {
				http.Error(w, "expired", http.StatusUnauthorized)
				return
			}
			body, _ := io.ReadAll(r.Body)
			m.bundle = extractMultipartMedia(body)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"id":           "file-123",
				"name":         "bundle.veproj",
				"size":         "42",
				"modifiedTime": time.Now().UTC().Format(time.RFC3339),
			})
		case http.MethodGet: // list
			query := r.URL.Query().Get("q")
			if strings.Contains(query, "mimeType = '"+driveFolderMimeType+"'") {
				m.folderQueries = append(m.folderQueries, query)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"files": []map[string]any{
						{"id": "folder-1", "name": "Projects"},
						{"id": "folder-2", "name": "素材"},
					},
				})
				return
			}
			m.listHits++
			if m.listHits <= m.failListUntil {
				http.Error(w, "expired", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"files": []map[string]any{{
					"id":           "file-123",
					"name":         "bundle.veproj",
					"size":         "42",
					"modifiedTime": time.Now().UTC().Format(time.RFC3339),
				}},
			})
		}
	})
	mux.HandleFunc("/files/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("alt") != "media" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Write([]byte("bundle-bytes"))
	})
	m.server = httptest.NewServer(mux)
	t.Cleanup(m.server.Close)
	return m
}

// extractMultipartMedia pulls the second (media) part out of a multipart/related
// body without a full parser: good enough to prove the bytes survived.
func extractMultipartMedia(body []byte) []byte {
	marker := []byte(driveBundleMimeType)
	idx := bytes.LastIndex(body, marker)
	if idx < 0 {
		return nil
	}
	rest := body[idx+len(marker):]
	if start := bytes.Index(rest, []byte("\r\n\r\n")); start >= 0 {
		rest = rest[start+4:]
	}
	if end := bytes.Index(rest, []byte("\r\n--")); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

func driveRemoteForTest(t *testing.T, mock *driveMock, tok DriveToken) *driveRemote {
	t.Helper()
	tokens := NewDriveTokenStore(t.TempDir(), "ws")
	if err := tokens.Save(tok); err != nil {
		t.Fatal(err)
	}
	creds := oauthCreds{
		ClientID:     "id",
		ClientSecret: "secret",
		TokenURL:     mock.server.URL + "/token",
		Scope:        driveScope,
	}
	Remote := newDriveRemote(creds, tokens, "folder-parent")
	Remote.apiBase = mock.server.URL
	Remote.uploadBase = mock.server.URL
	return Remote
}

func TestDrivePushListPull(t *testing.T) {
	mock := newDriveMock(t)
	Remote := driveRemoteForTest(t, mock, DriveToken{
		AccessToken:  "still-good",
		RefreshToken: "r",
		Expiry:       time.Now().Add(time.Hour),
	})

	payload := []byte("PK the bundle")
	ref, err := Remote.Push(context.Background(), "my proj", bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if ref.ID != "file-123" {
		t.Fatalf("push ref = %+v", ref)
	}
	if !bytes.Equal(mock.bundle, payload) {
		t.Fatalf("upload lost the media bytes: %q", mock.bundle)
	}
	if mock.tokenHits != 0 {
		t.Fatalf("a fresh token should not refresh, got %d refreshes", mock.tokenHits)
	}

	refs, err := Remote.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(refs) != 1 || refs[0].ID != "file-123" || refs[0].Size != 42 {
		t.Fatalf("list = %+v", refs)
	}

	reader, err := Remote.Pull(context.Background(), "file-123")
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	got, _ := io.ReadAll(reader)
	reader.Close()
	if string(got) != "bundle-bytes" {
		t.Fatalf("pull = %q", got)
	}
}

func TestDriveRefreshesExpiredToken(t *testing.T) {
	mock := newDriveMock(t)
	Remote := driveRemoteForTest(t, mock, DriveToken{
		AccessToken:  "stale",
		RefreshToken: "r",
		Expiry:       time.Now().Add(-time.Hour),
	})
	if _, err := Remote.List(context.Background()); err != nil {
		t.Fatalf("list: %v", err)
	}
	if mock.tokenHits != 1 {
		t.Fatalf("expected one refresh, got %d", mock.tokenHits)
	}
	// The refreshed token must be persisted so the next call reuses it.
	stored, _ := Remote.tokens.Load()
	if stored.AccessToken != "fresh-access" {
		t.Fatalf("refreshed token not saved: %+v", stored)
	}
}

func TestDriveRetriesOn401(t *testing.T) {
	mock := newDriveMock(t)
	mock.failListUntil = 1 // first list 401s, forcing a refresh + retry
	Remote := driveRemoteForTest(t, mock, DriveToken{
		AccessToken:  "still-good",
		RefreshToken: "r",
		Expiry:       time.Now().Add(time.Hour),
	})
	refs, err := Remote.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("list after retry = %+v", refs)
	}
	if mock.tokenHits != 1 {
		t.Fatalf("401 should trigger exactly one refresh, got %d", mock.tokenHits)
	}
}

func TestDrivePushReplaysStreamAfter401(t *testing.T) {
	mock := newDriveMock(t)
	mock.failPushUntil = 1
	remote := driveRemoteForTest(t, mock, DriveToken{
		AccessToken:  "still-good",
		RefreshToken: "r",
		Expiry:       time.Now().Add(time.Hour),
	})
	payload := bytes.Repeat([]byte("streamed-payload"), 4096)
	// bytes.Buffer is intentionally one-shot: Push must spool it rather than
	// retaining the whole body just to make the retry replayable.
	if _, err := remote.Push(context.Background(), "retry", bytes.NewBuffer(payload), int64(len(payload))); err != nil {
		t.Fatalf("push: %v", err)
	}
	if mock.uploadHits != 2 || mock.tokenHits != 1 {
		t.Fatalf("upload hits=%d token hits=%d", mock.uploadHits, mock.tokenHits)
	}
	if !bytes.Equal(mock.bundle, payload) {
		t.Fatal("retry changed the streamed payload")
	}
}

func TestDriveNotConnected(t *testing.T) {
	mock := newDriveMock(t)
	Remote := driveRemoteForTest(t, mock, DriveToken{}) // no refresh token
	if _, err := Remote.List(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "not connected") {
		t.Fatalf("expected not-connected error, got %v", err)
	}
}

func TestDriveListFoldersEscapesParentID(t *testing.T) {
	mock := newDriveMock(t)
	Remote := driveRemoteForTest(t, mock, DriveToken{
		AccessToken:  "still-good",
		RefreshToken: "r",
		Expiry:       time.Now().Add(time.Hour),
	})

	parentID := `folder'\injection`
	folders, err := Remote.ListFolders(context.Background(), parentID)
	if err != nil {
		t.Fatalf("list folders: %v", err)
	}
	if len(folders) != 2 || folders[0].ID != "folder-1" || folders[1].Name != "素材" {
		t.Fatalf("folders = %+v", folders)
	}
	if len(mock.folderQueries) != 1 {
		t.Fatalf("folder queries = %v", mock.folderQueries)
	}
	wantQuery := `mimeType = 'application/vnd.google-apps.folder' and trashed = false and 'folder\'\\injection' in parents`
	if mock.folderQueries[0] != wantQuery {
		t.Fatalf("folder query = %q, want %q", mock.folderQueries[0], wantQuery)
	}
}

func TestDriveScopeIncludesMetadataAccess(t *testing.T) {
	if !strings.Contains(driveScope, driveFileScope) {
		t.Fatalf("Drive scope lost drive.file: %q", driveScope)
	}
	if !strings.Contains(driveScope, driveMetadataScope) {
		t.Fatalf("Drive scope lost metadata access: %q", driveScope)
	}
}

func TestDriveListPaginationIsBounded(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"nextPageToken": fmt.Sprintf("page-%d", hits),
			"files": []map[string]any{{
				"id":           fmt.Sprintf("file-%d", hits),
				"name":         "bundle.veproj",
				"size":         "1",
				"modifiedTime": time.Now().UTC().Format(time.RFC3339),
			}},
		})
	}))
	t.Cleanup(server.Close)
	mock := &driveMock{server: server}
	remote := driveRemoteForTest(t, mock, DriveToken{
		AccessToken:  "still-good",
		RefreshToken: "r",
		Expiry:       time.Now().Add(time.Hour),
	})
	refs, err := remote.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if hits != driveListMaxPages || len(refs) != driveListMaxPages {
		t.Fatalf("hits=%d refs=%d", hits, len(refs))
	}
}

func TestDriveListItemsAreBounded(t *testing.T) {
	files := make([]map[string]any, driveListMaxItems+50)
	for i := range files {
		files[i] = map[string]any{"id": fmt.Sprintf("file-%d", i), "name": "bundle.veproj"}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"nextPageToken": "more", "files": files})
	}))
	t.Cleanup(server.Close)
	mock := &driveMock{server: server}
	remote := driveRemoteForTest(t, mock, DriveToken{
		AccessToken:  "still-good",
		RefreshToken: "r",
		Expiry:       time.Now().Add(time.Hour),
	})
	refs, err := remote.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != driveListMaxItems {
		t.Fatalf("refs=%d, want %d", len(refs), driveListMaxItems)
	}
}
