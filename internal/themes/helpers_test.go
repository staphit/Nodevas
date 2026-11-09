package themes

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"nodevas/internal/db"
	"nodevas/internal/locale"
	"nodevas/internal/testpg"
)

// The three image types by their leading bytes, with a little body.
var (
	webpImage = []byte("RIFF\x10\x00\x00\x00WEBPVP8 body")
	pngImage  = []byte("\x89PNG\r\n\x1a\nbody")
	jpegImage = []byte("\xff\xd8\xff\xe0body")
)

func newTestStore(t *testing.T) (*Store, *db.DB) {
	t.Helper()
	database := testpg.New(t)
	return NewStore(database, filepath.Join(t.TempDir(), ".vised", "themes")), database
}

// manifestDoc is a good manifest as loose JSON, so that a case can put
// anything at all into it.
func manifestDoc() map[string]any {
	return map[string]any{
		"schema":  1,
		"id":      "magic",
		"name":    map[string]any{"en": "Magic world", "zh-TW": "魔法世界"},
		"version": "1.0.0",
		"author":  "someone",
		"canvas":  []any{1536, 864},
		"rooms":   map[string]any{"lounge": "rooms/lounge.webp"},
		"characters": map[string]any{
			"claude": map[string]any{
				"match": map[string]any{"agents": []any{"claude"}, "models": []any{"claude*"}},
				"scenes": map[string]any{
					"read": map[string]any{
						"room": "lounge", "image": "figures/claude/read.png",
						"x": 100, "y": 200, "w": 300, "h": 500, "box": []any{120, 210, 380, 700},
					},
				},
			},
		},
	}
}

// sceneOf is the manifest's one scene, for a case to spoil.
func sceneOf(doc map[string]any) map[string]any {
	character := doc["characters"].(map[string]any)["claude"].(map[string]any)
	return character["scenes"].(map[string]any)["read"].(map[string]any)
}

func goodFiles() map[string][]byte {
	return map[string][]byte{
		"rooms/lounge.webp":       webpImage,
		"figures/claude/read.png": pngImage,
	}
}

// zipEntry is one entry of a hand-built zip.
type zipEntry struct {
	name string
	data []byte
	mode fs.FileMode // 0 leaves the header's mode alone
}

func buildZip(t testing.TB, entries ...zipEntry) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		if entry.mode != 0 {
			header.SetMode(entry.mode)
		}
		w, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func marshalDoc(t testing.TB, doc any) []byte {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// packZip is a zip of doc as theme.json and files, in a fixed order.
func packZip(t testing.TB, doc any, files map[string][]byte) []byte {
	t.Helper()
	entries := []zipEntry{{name: ManifestFile, data: marshalDoc(t, doc)}}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entries = append(entries, zipEntry{name: name, data: files[name]})
	}
	return buildZip(t, entries...)
}

func doImport(ctx context.Context, s *Store, owner string, archive []byte) (Pack, error) {
	return s.Import(ctx, owner, bytes.NewReader(archive), int64(len(archive)))
}

// mustImport imports a good pack with the given id as owner.
func mustImport(t testing.TB, s *Store, owner, id string) Pack {
	t.Helper()
	doc := manifestDoc()
	doc["id"] = id
	pack, err := doImport(context.Background(), s, owner, packZip(t, doc, goodFiles()))
	if err != nil {
		t.Fatalf("import %s: %v", id, err)
	}
	return pack
}

// statusOf is the HTTP status err asks for, 0 when it asks for none.
func statusOf(err error) int {
	var carried interface{ Status() int }
	if errors.As(err, &carried) {
		return carried.Status()
	}
	return 0
}

// expectRefused checks that err is a refusal with status, worded in both
// languages.
func expectRefused(t *testing.T, err error, status int) {
	t.Helper()
	if err == nil {
		t.Fatal("accepted")
	}
	if got := statusOf(err); got != status {
		t.Fatalf("status = %d, want %d: %v", got, status, err)
	}
	var message *locale.Message
	if !errors.As(err, &message) {
		t.Fatalf("not a two-language error: %v", err)
	}
	if english, chinese := locale.Render(err, locale.English), locale.Render(err, locale.Chinese); english == chinese {
		t.Fatalf("one wording only: %q", english)
	}
}

// expectNoTrace checks that a refused import changed nothing: not a row, and
// nothing in the themes directory but the packs in keep.
func expectNoTrace(t *testing.T, s *Store, database *db.DB, keep ...string) {
	t.Helper()
	var rows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM theme_packs`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != len(keep) {
		t.Fatalf("%d rows, want %d", rows, len(keep))
	}
	if got, want := dirNames(t, s.dir), keep; !equalStrings(got, want) {
		t.Fatalf("themes directory holds %v, want %v", got, want)
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// readPackFile reads through the same door the routes use.
func readPackFile(t *testing.T, s *Store, id, rel string) []byte {
	t.Helper()
	file, _, err := s.Open(context.Background(), id, rel)
	if err != nil {
		t.Fatalf("open %s/%s: %v", id, rel, err)
	}
	defer file.Close()
	var out bytes.Buffer
	if _, err := out.ReadFrom(file); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
