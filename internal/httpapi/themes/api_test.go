package themes

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/identity"
	"nodevas/internal/testpg"
	themestore "nodevas/internal/themes"
)

func TestMain(m *testing.M) { os.Exit(testpg.Main(m)) }

var actors = map[string]identity.Actor{
	"alice": {ID: "alice", Name: "alice", Role: identity.RoleMember},
	"bob":   {ID: "bob", Name: "bob", Role: identity.RoleMember},
	"root":  {ID: "root", Name: "root", Role: identity.RoleAdmin},
	"agent": {ID: "root", Name: "root", Role: identity.RoleAdmin, Agent: identity.AgentWorker},
	"roled": {ID: "alice", Name: "alice", Role: identity.RoleMember, Agent: identity.AgentOrchestrator, AgentRole: "planner"},
}

var (
	webpImage = []byte("RIFF\x10\x00\x00\x00WEBPVP8 body")
	pngImage  = []byte("\x89PNG\r\n\x1a\nbody")
)

const manifestFor = `{
  "schema": 1, "name": {"en": "ID", "zh-TW": "魔法世界"}, "version": "1.0.0", "author": "someone",
  "canvas": [1536, 864], "rooms": {"lounge": "rooms/lounge.webp"},
  "characters": {"claude": {"match": {"agents": ["claude"], "models": ["claude*"]}, "scenes": {
    "read": {"room": "lounge", "image": "figures/read.png", "x": 100, "y": 200, "w": 300, "h": 500, "box": [120, 210, 380, 700]}}}}}`

func packZip(t *testing.T, id string, extra map[string][]byte) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	files := map[string][]byte{
		"theme.json":        []byte(strings.Replace(manifestFor, `"ID"`, `"`+id+`"`, 1)),
		"rooms/lounge.webp": webpImage,
		"figures/read.png":  pngImage,
	}
	for name, data := range extra {
		files[name] = data
	}
	for name, data := range files {
		w, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

type fixture struct {
	router  *gin.Engine
	store   *themestore.Store
	dir     string
	changed []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	f := &fixture{dir: filepath.Join(t.TempDir(), "themes")}
	f.store = themestore.NewStore(testpg.New(t), f.dir)
	f.router = gin.New()
	f.router.Use(func(c *gin.Context) {
		if actor, ok := actors[c.GetHeader("X-Test-Actor")]; ok {
			c.Request = c.Request.WithContext(auth.WithActor(c.Request.Context(), actor))
		}
		c.Next()
	})
	New(f.store, func(id string) { f.changed = append(f.changed, id) }).Register(f.router.Group("/api"))
	return f
}

func (f *fixture) call(t *testing.T, actor, method, path string, body []byte, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if actor != "" {
		request.Header.Set("X-Test-Actor", actor)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range header {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	f.router.ServeHTTP(recorder, request)
	return recorder
}

func multipartBody(t *testing.T, field string, data []byte) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile(field, "pack.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), writer.FormDataContentType()
}

func (f *fixture) upload(t *testing.T, actor string, archive []byte) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := multipartBody(t, "file", archive)
	return f.call(t, actor, http.MethodPost, "/api/themes", body, map[string]string{"Content-Type": contentType})
}

func (f *fixture) mustUpload(t *testing.T, actor, id string) {
	t.Helper()
	if rec := f.upload(t, actor, packZip(t, id, nil)); rec.Code != http.StatusCreated {
		t.Fatalf("upload %s = %d %s", id, rec.Code, rec.Body)
	}
}

type listing struct {
	Packs   []themestore.Pack `json:"packs"`
	Default string            `json:"default"`
}

func (f *fixture) list(t *testing.T, actor string) listing {
	t.Helper()
	rec := f.call(t, actor, http.MethodGet, "/api/themes", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	var out listing
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestUploadAnswersTheContractsPack(t *testing.T) {
	f := newFixture(t)
	rec := f.upload(t, "alice", packZip(t, "magic", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d %s", rec.Code, rec.Body)
	}
	var fields map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "name", "version", "author", "owner", "sizeBytes", "createdAt", "updatedAt", "mine", "canDelete"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("no %q in %s", key, rec.Body)
		}
	}
	if len(fields) != 10 {
		t.Errorf("%d fields in %s", len(fields), rec.Body)
	}
	name := fields["name"].(map[string]any)
	if name["en"] != "magic" || name["zh-TW"] != "魔法世界" {
		t.Errorf("name = %v", name)
	}
	if fields["mine"] != true || fields["canDelete"] != true || fields["owner"] != "alice" {
		t.Errorf("for the uploader: %s", rec.Body)
	}
	if len(f.changed) != 1 || f.changed[0] != "magic" {
		t.Errorf("changed = %v", f.changed)
	}
}

func TestListIsSortedAndAnswersPerViewer(t *testing.T) {
	f := newFixture(t)
	if got := f.list(t, "alice"); got.Packs == nil || len(got.Packs) != 0 || got.Default != "" {
		t.Fatalf("empty list = %+v", got)
	}
	f.mustUpload(t, "alice", "zebra")
	f.mustUpload(t, "bob", "apple")
	got := f.list(t, "alice")
	if len(got.Packs) != 2 || got.Packs[0].ID != "apple" || got.Packs[1].ID != "zebra" {
		t.Fatalf("list = %+v", got)
	}
	if got.Packs[0].Mine || got.Packs[0].CanDelete || !got.Packs[1].Mine || !got.Packs[1].CanDelete {
		t.Fatalf("for alice: %+v", got.Packs)
	}
	if asAdmin := f.list(t, "root"); asAdmin.Packs[0].Mine || !asAdmin.Packs[0].CanDelete {
		t.Fatalf("for an administrator: %+v", asAdmin.Packs)
	}
}

func TestUploadRefusals(t *testing.T) {
	f := newFixture(t)
	f.mustUpload(t, "alice", "magic")
	f.changed = nil

	if rec := f.upload(t, "bob", packZip(t, "magic", nil)); rec.Code != http.StatusConflict {
		t.Fatalf("somebody else's id = %d %s", rec.Code, rec.Body)
	}
	rec := f.upload(t, "alice", packZip(t, "other", map[string][]byte{"evil.svg": []byte("<svg/>")}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("an SVG = %d %s", rec.Code, rec.Body)
	}
	if body := rec.Body.String(); !strings.Contains(body, "error") {
		t.Fatalf("no error in %s", body)
	}
	body, contentType := multipartBody(t, "other-field", packZip(t, "other", nil))
	if rec := f.call(t, "alice", http.MethodPost, "/api/themes", body, map[string]string{"Content-Type": contentType}); rec.Code != http.StatusBadRequest {
		t.Fatalf("no file field = %d %s", rec.Code, rec.Body)
	}
	if rec := f.call(t, "alice", http.MethodPost, "/api/themes", []byte(`{}`), nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("not multipart = %d %s", rec.Code, rec.Body)
	}
	if len(f.changed) != 0 {
		t.Fatalf("a refusal told the browsers: %v", f.changed)
	}

	// Refused by language too.
	rec = f.call(t, "bob", http.MethodPost, "/api/themes", body, map[string]string{"Content-Type": contentType, "Accept-Language": "zh-TW"})
	if rec.Code != http.StatusBadRequest || !hasChinese(rec.Body.String()) {
		t.Fatalf("for a Chinese reader = %d %s", rec.Code, rec.Body)
	}
}

func hasChinese(text string) bool {
	var decoded map[string]string
	if json.Unmarshal([]byte(text), &decoded) != nil {
		return false
	}
	for _, r := range decoded["error"] {
		if r >= 0x4e00 && r <= 0x9fff {
			return true
		}
	}
	return false
}

func TestAnUploadOverTheLimitIsRefused(t *testing.T) {
	f := newFixture(t)
	limits := themestore.DefaultLimits()
	limits.MaxZipBytes = 1 << 10
	f.store.SetLimits(limits)

	// Over the zip limit but within the room left for the multipart envelope.
	rec := f.upload(t, "alice", append(packZip(t, "magic", nil), make([]byte, 2<<10)...))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over the zip limit = %d %s", rec.Code, rec.Body)
	}
	// Far over, and saying so in its headers.
	huge := make([]byte, 4<<20)
	rec = f.upload(t, "alice", huge)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("far over = %d %s", rec.Code, rec.Body)
	}
	// Far over, and not saying so.
	body, contentType := multipartBody(t, "file", huge)
	request := httptest.NewRequest(http.MethodPost, "/api/themes", struct{ *bytes.Reader }{bytes.NewReader(body)})
	request.Header.Set("X-Test-Actor", "alice")
	request.Header.Set("Content-Type", contentType)
	request.ContentLength = -1
	recorder := httptest.NewRecorder()
	f.router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("far over, length unknown = %d %s", recorder.Code, recorder.Body)
	}
}

// Every route, whether or not it would have found anything.
func everyRoute() []struct{ method, path string } {
	return []struct{ method, path string }{
		{http.MethodGet, "/api/themes"},
		{http.MethodPost, "/api/themes"},
		{http.MethodGet, "/api/themes/magic"},
		{http.MethodGet, "/api/themes/magic/files/rooms/lounge.webp"},
		{http.MethodDelete, "/api/themes/magic"},
		{http.MethodPut, "/api/themes/default"},
	}
}

func TestAnAgentIsRefusedOnEveryRoute(t *testing.T) {
	f := newFixture(t)
	f.mustUpload(t, "alice", "magic")
	f.changed = nil
	for _, name := range []string{"agent", "roled"} {
		for _, route := range everyRoute() {
			var body []byte
			if route.method == http.MethodPut {
				body = []byte(`{"id":"magic"}`)
			}
			rec := f.call(t, name, route.method, route.path, body, nil)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s %s = %d %s", name, route.method, route.path, rec.Code, rec.Body)
			}
		}
	}
	if got := f.list(t, "alice"); len(got.Packs) != 1 || got.Default != "" || len(f.changed) != 0 {
		t.Fatalf("an agent changed something: %+v %v", got, f.changed)
	}
}

func TestNobodySignedInIsRefusedOnEveryRoute(t *testing.T) {
	f := newFixture(t)
	for _, route := range everyRoute() {
		if rec := f.call(t, "", route.method, route.path, nil, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d %s", route.method, route.path, rec.Code, rec.Body)
		}
	}
}

func TestDeleteNeedsTheOwnerOrAnAdministrator(t *testing.T) {
	f := newFixture(t)
	f.mustUpload(t, "alice", "magic")
	f.changed = nil
	if rec := f.call(t, "bob", http.MethodDelete, "/api/themes/magic", nil, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("a stranger = %d %s", rec.Code, rec.Body)
	}
	if rec := f.call(t, "bob", http.MethodDelete, "/api/themes/nothing", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("nothing = %d %s", rec.Code, rec.Body)
	}
	if len(f.changed) != 0 {
		t.Fatalf("changed = %v", f.changed)
	}
	if rec := f.call(t, "alice", http.MethodDelete, "/api/themes/magic", nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("the owner = %d %s", rec.Code, rec.Body)
	}
	if len(f.changed) != 1 || f.changed[0] != "magic" {
		t.Fatalf("changed = %v", f.changed)
	}
	f.mustUpload(t, "alice", "magic")
	if rec := f.call(t, "root", http.MethodDelete, "/api/themes/magic", nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("an administrator = %d %s", rec.Code, rec.Body)
	}
}

func TestOnlyAnAdministratorSetsTheDefault(t *testing.T) {
	f := newFixture(t)
	f.mustUpload(t, "alice", "magic")
	f.changed = nil
	put := func(actor, body string) *httptest.ResponseRecorder {
		return f.call(t, actor, http.MethodPut, "/api/themes/default", []byte(body), nil)
	}
	if rec := put("alice", `{"id":"magic"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("the pack's owner = %d %s", rec.Code, rec.Body)
	}
	if rec := put("root", `{"id":"nothing"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("an unknown id = %d %s", rec.Code, rec.Body)
	}
	if rec := put("root", `{"id":"magic","x":1}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("an unknown field = %d %s", rec.Code, rec.Body)
	}
	if len(f.changed) != 0 {
		t.Fatalf("changed = %v", f.changed)
	}
	if rec := put("root", `{"id":"magic"}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"default":"magic"`) {
		t.Fatalf("an administrator = %d %s", rec.Code, rec.Body)
	}
	if got := f.list(t, "bob"); got.Default != "magic" {
		t.Fatalf("default = %q", got.Default)
	}
	if rec := put("root", `{"id":""}`); rec.Code != http.StatusOK {
		t.Fatalf("clearing = %d %s", rec.Code, rec.Body)
	}
	if got := f.list(t, "bob"); got.Default != "" {
		t.Fatalf("cleared = %q", got.Default)
	}
	if len(f.changed) != 2 || f.changed[0] != "magic" || f.changed[1] != "" {
		t.Fatalf("changed = %v", f.changed)
	}
}

func TestAnImageIsServedSafely(t *testing.T) {
	f := newFixture(t)
	f.mustUpload(t, "alice", "magic")
	rec := f.call(t, "bob", http.MethodGet, "/api/themes/magic/files/rooms/lounge.webp", nil, nil)
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), webpImage) {
		t.Fatalf("image = %d %q", rec.Code, rec.Body)
	}
	for header, want := range map[string]string{
		"Content-Type":            "image/webp",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "sandbox; default-src 'none'",
		"Cache-Control":           "private, max-age=3600",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	again := f.call(t, "bob", http.MethodGet, "/api/themes/magic/files/rooms/lounge.webp", nil, map[string]string{"If-None-Match": etag})
	if again.Code != http.StatusNotModified || again.Body.Len() != 0 {
		t.Fatalf("revalidation = %d %q", again.Code, again.Body)
	}

	// A replaced pack is a new tag.
	f.mustUpload(t, "alice", "magic")
	fresh := f.call(t, "bob", http.MethodGet, "/api/themes/magic/files/rooms/lounge.webp", nil, map[string]string{"If-None-Match": etag})
	if fresh.Code != http.StatusOK || fresh.Header().Get("ETag") == etag {
		t.Fatalf("after replacing = %d %q", fresh.Code, fresh.Header().Get("ETag"))
	}
}

// The browser reloads the manifest on themes-changed, so unlike an image's it
// must not answer from a cache without asking.
func TestTheManifestIsServedAndAlwaysRevalidated(t *testing.T) {
	f := newFixture(t)
	f.mustUpload(t, "alice", "magic")
	rec := f.call(t, "bob", http.MethodGet, "/api/themes/magic", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("manifest = %d %s", rec.Code, rec.Body)
	}
	var manifest map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest["id"] != "magic" || manifest["schema"] != float64(1) {
		t.Fatalf("manifest = %s", rec.Body)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("type = %q", got)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Content-Security-Policy") != "sandbox; default-src 'none'" {
		t.Fatalf("headers = %v", rec.Header())
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, no-cache" {
		t.Fatalf("cache = %q", got)
	}
	again := f.call(t, "bob", http.MethodGet, "/api/themes/magic", nil, map[string]string{"If-None-Match": rec.Header().Get("ETag")})
	if again.Code != http.StatusNotModified {
		t.Fatalf("revalidation = %d", again.Code)
	}
}

// These walk out before any route matches, so the router answers, not a
// handler: all that matters is that no pack is served.
func TestAnIDThatWalksOutMatchesNoRoute(t *testing.T) {
	f := newFixture(t)
	f.mustUpload(t, "alice", "magic")
	for _, path := range []string{"/api/themes/..%2fmagic/files/rooms/lounge.webp", "/api/themes/..%2fmagic"} {
		rec := f.call(t, "alice", http.MethodGet, path, nil, nil)
		if rec.Code != http.StatusNotFound || bytes.Contains(rec.Body.Bytes(), []byte("RIFF")) || bytes.Contains(rec.Body.Bytes(), []byte("schema")) {
			t.Errorf("%s = %d %q", path, rec.Code, rec.Body)
		}
	}
}

func TestMissingFilesAre404AndNotCached(t *testing.T) {
	f := newFixture(t)
	f.mustUpload(t, "alice", "magic")
	secret := filepath.Join(f.dir, "secret.png")
	if err := os.WriteFile(secret, pngImage, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/themes/magic/files/missing.png",
		"/api/themes/magic/files/theme.json",
		"/api/themes/magic/files/../secret.png",
		"/api/themes/magic/files/..%2fsecret.png",
		"/api/themes/magic/files/rooms/..%2f..%2fsecret.png",
		"/api/themes/magic/files/rooms%5clounge.webp",
		"/api/themes/magic/files/rooms//lounge.webp",
		"/api/themes/magic/files/",
		"/api/themes/nothing/files/rooms/lounge.webp",
		"/api/themes/MAGIC/files/rooms/lounge.webp",
		"/api/themes/nothing",
	} {
		rec := f.call(t, "alice", http.MethodGet, path, nil, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d %s", path, rec.Code, rec.Body)
			continue
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: cache = %q", path, got)
		}
		if bytes.Contains(rec.Body.Bytes(), pngImage[:4]) {
			t.Errorf("%s served something: %q", path, rec.Body)
		}
	}
}
