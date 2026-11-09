package themes

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
)

// Each case takes a good pack and spoils it one way; the whole zip is refused
// with a two-language 400 and nothing is left behind.
func TestASpoiledManifestRefusesTheWholeZip(t *testing.T) {
	s, database := newTestStore(t)
	cases := []struct {
		name string
		edit func(doc map[string]any, files map[string][]byte)
	}{
		{"schema 2", func(d map[string]any, _ map[string][]byte) { d["schema"] = 2 }},
		{"no schema", func(d map[string]any, _ map[string][]byte) { delete(d, "schema") }},
		// An id comes from the name, so a zip cannot choose one for itself.
		{"an id in theme.json", func(d map[string]any, _ map[string][]byte) { d["id"] = "magic" }},
		{"a name with nothing to make an id of", func(d map[string]any, _ map[string][]byte) { d["name"] = map[string]any{"en": "!!!"} }},
		{"a name of 41 letters", func(d map[string]any, _ map[string][]byte) { d["name"] = map[string]any{"en": strings.Repeat("a", 41)} }},
		{"a name that is not ASCII", func(d map[string]any, _ map[string][]byte) { d["name"] = map[string]any{"en": "魔法 world"} }},
		// "none" is the web preference's built-in people, "default" a route.
		{"the reserved name none", func(d map[string]any, _ map[string][]byte) { d["name"] = map[string]any{"en": "None"} }},
		{"the reserved name default", func(d map[string]any, _ map[string][]byte) { d["name"] = map[string]any{"en": "default"} }},
		{"empty version", func(d map[string]any, _ map[string][]byte) { d["version"] = "" }},
		{"long version", func(d map[string]any, _ map[string][]byte) { d["version"] = strings.Repeat("1", 65) }},
		{"long author", func(d map[string]any, _ map[string][]byte) { d["author"] = strings.Repeat("a", 65) }},
		{"no English name", func(d map[string]any, _ map[string][]byte) { d["name"] = map[string]any{"zh-TW": "魔法"} }},
		{"long English name", func(d map[string]any, _ map[string][]byte) { d["name"] = map[string]any{"en": strings.Repeat("a", 65)} }},
		{"long Chinese name", func(d map[string]any, _ map[string][]byte) {
			d["name"] = map[string]any{"en": "a", "zh-TW": strings.Repeat("魔", 65)}
		}},
		{"unknown language", func(d map[string]any, _ map[string][]byte) { d["name"] = map[string]any{"en": "a", "fr": "b"} }},
		{"unknown top-level field", func(d map[string]any, _ map[string][]byte) { d["script"] = "alert(1)" }},
		{"unknown scene field", func(d map[string]any, _ map[string][]byte) { sceneOf(d)["onload"] = "alert(1)" }},
		{"fallback that is no boolean", func(d map[string]any, _ map[string][]byte) {
			d["characters"].(map[string]any)["claude"].(map[string]any)["fallback"] = "yes"
		}},
		// Two would leave which one stands in for an unknown agent to map order.
		{"two fallback characters", func(d map[string]any, _ map[string][]byte) {
			characters := d["characters"].(map[string]any)
			characters["claude"].(map[string]any)["fallback"] = true
			characters["other"] = map[string]any{"fallback": true}
		}},
		{"canvas of one number", func(d map[string]any, _ map[string][]byte) { d["canvas"] = []any{100} }},
		{"canvas of three numbers", func(d map[string]any, _ map[string][]byte) { d["canvas"] = []any{100, 100, 100} }},
		{"canvas of zero", func(d map[string]any, _ map[string][]byte) { d["canvas"] = []any{0, 100} }},
		{"canvas too large", func(d map[string]any, _ map[string][]byte) { d["canvas"] = []any{100, 8193} }},
		{"canvas not an integer", func(d map[string]any, _ map[string][]byte) { d["canvas"] = []any{100.5, 100} }},
		{"bad room id", func(d map[string]any, _ map[string][]byte) {
			d["rooms"] = map[string]any{"Lounge": "rooms/lounge.webp"}
		}},
		{"bad scene name", func(d map[string]any, _ map[string][]byte) {
			character := d["characters"].(map[string]any)["claude"].(map[string]any)
			character["scenes"] = map[string]any{"__proto__": sceneOf(d)}
		}},
		{"bad character id", func(d map[string]any, _ map[string][]byte) {
			d["characters"] = map[string]any{"__proto__": d["characters"].(map[string]any)["claude"]}
		}},
		{"scene in an unknown room", func(d map[string]any, _ map[string][]byte) { sceneOf(d)["room"] = "attic" }},
		{"rectangle past the right edge", func(d map[string]any, _ map[string][]byte) { sceneOf(d)["x"] = 1300 }},
		{"rectangle past the bottom edge", func(d map[string]any, _ map[string][]byte) { sceneOf(d)["y"] = 400 }},
		{"rectangle left of the canvas", func(d map[string]any, _ map[string][]byte) { sceneOf(d)["x"] = -1 }},
		{"rectangle above the canvas", func(d map[string]any, _ map[string][]byte) { sceneOf(d)["y"] = -1 }},
		{"rectangle overflowing an int", func(d map[string]any, _ map[string][]byte) {
			sceneOf(d)["x"] = int64(1<<63 - 1)
		}},
		{"zero width", func(d map[string]any, _ map[string][]byte) { sceneOf(d)["w"] = 0 }},
		{"negative height", func(d map[string]any, _ map[string][]byte) { sceneOf(d)["h"] = -5 }},
		{"fractional x", func(d map[string]any, _ map[string][]byte) { sceneOf(d)["x"] = 1.5 }},
		{"box of three numbers", func(d map[string]any, _ map[string][]byte) { sceneOf(d)["box"] = []any{1, 2, 3} }},
		{"no box", func(d map[string]any, _ map[string][]byte) { delete(sceneOf(d), "box") }},
		{"box inside out", func(d map[string]any, _ map[string][]byte) { sceneOf(d)["box"] = []any{380, 210, 120, 700} }},
		{"box upside down", func(d map[string]any, _ map[string][]byte) { sceneOf(d)["box"] = []any{120, 700, 380, 210} }},
		{"box outside the canvas", func(d map[string]any, _ map[string][]byte) { sceneOf(d)["box"] = []any{120, 210, 380, 900} }},
		{"box left of the canvas", func(d map[string]any, _ map[string][]byte) { sceneOf(d)["box"] = []any{-1, 210, 380, 700} }},
		{"too many agents", func(d map[string]any, _ map[string][]byte) {
			matchOf(d)["agents"] = repeated("a", 17)
		}},
		{"too many models", func(d map[string]any, _ map[string][]byte) {
			matchOf(d)["models"] = repeated("m", 17)
		}},
		{"long agent match", func(d map[string]any, _ map[string][]byte) {
			matchOf(d)["agents"] = []any{strings.Repeat("a", 65)}
		}},
		{"manifest names a missing room image", func(_ map[string]any, f map[string][]byte) { delete(f, "rooms/lounge.webp") }},
		{"manifest names a missing scene image", func(_ map[string]any, f map[string][]byte) { delete(f, "figures/claude/read.png") }},
		{"manifest path differing by case from the file", func(d map[string]any, _ map[string][]byte) {
			d["rooms"] = map[string]any{"lounge": "Rooms/lounge.webp"}
		}},
		{"unreferenced image", func(_ map[string]any, f map[string][]byte) { f["extra.webp"] = webpImage }},
		{"manifest naming an SVG", func(d map[string]any, f map[string][]byte) {
			sceneOf(d)["image"] = "a.svg"
			delete(f, "figures/claude/read.png")
			f["a.svg"] = []byte(`<svg onload="alert(1)"/>`)
		}},
		{"unreferenced SVG", func(_ map[string]any, f map[string][]byte) { f["a.svg"] = []byte("<svg/>") }},
		{"unreferenced HTML", func(_ map[string]any, f map[string][]byte) { f["index.html"] = []byte("<script>") }},
		{"HTML with an image's bytes", func(d map[string]any, f map[string][]byte) {
			sceneOf(d)["image"] = "a.html"
			delete(f, "figures/claude/read.png")
			f["a.html"] = pngImage
		}},
		{"capitalised extension", func(d map[string]any, f map[string][]byte) {
			sceneOf(d)["image"] = "a.PNG"
			delete(f, "figures/claude/read.png")
			f["a.PNG"] = pngImage
		}},
		{"png holding jpeg bytes", func(_ map[string]any, f map[string][]byte) { f["figures/claude/read.png"] = jpegImage }},
		{"webp holding png bytes", func(_ map[string]any, f map[string][]byte) { f["rooms/lounge.webp"] = pngImage }},
		{"webp that is RIFF only", func(_ map[string]any, f map[string][]byte) {
			f["rooms/lounge.webp"] = []byte("RIFF\x10\x00\x00\x00WAVEfmt ")
		}},
		{"jpg holding HTML", func(d map[string]any, f map[string][]byte) {
			sceneOf(d)["image"] = "a.jpg"
			delete(f, "figures/claude/read.png")
			f["a.jpg"] = []byte("<!DOCTYPE html><script>alert(1)</script>")
		}},
		{"png holding an SVG", func(_ map[string]any, f map[string][]byte) { f["figures/claude/read.png"] = []byte("<svg/>") }},
		{"an empty image", func(_ map[string]any, f map[string][]byte) { f["figures/claude/read.png"] = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, files := manifestDoc(), goodFiles()
			tc.edit(doc, files)
			_, err := doImport(context.Background(), s, "alice", packZip(t, doc, files))
			expectRefused(t, err, http.StatusBadRequest)
			expectNoTrace(t, s, database)
		})
	}
}

func matchOf(doc map[string]any) map[string]any {
	character := doc["characters"].(map[string]any)["claude"].(map[string]any)
	return character["match"].(map[string]any)
}

func repeated(value string, count int) []any {
	out := make([]any, count)
	for i := range out {
		out[i] = value
	}
	return out
}

// Caps of a manifest: one past each is refused, and the cap itself is not.
func TestManifestCounts(t *testing.T) {
	s, database := newTestStore(t)
	roomsOf := func(n int) (map[string]any, map[string][]byte) {
		doc, files := manifestDoc(), goodFiles()
		rooms := map[string]any{"lounge": "rooms/lounge.webp"}
		for i := 1; i < n; i++ {
			rooms[fmt.Sprintf("r%d", i)] = "rooms/lounge.webp"
		}
		doc["rooms"] = rooms
		return doc, files
	}
	doc, files := roomsOf(257)
	expectRefused(t, importErr(t, s, doc, files), http.StatusBadRequest)
	expectNoTrace(t, s, database)
	doc, files = roomsOf(256)
	if err := importErr(t, s, doc, files); err != nil {
		t.Fatalf("256 rooms: %v", err)
	}

	doc = manifestDoc()
	doc["name"] = map[string]any{"en": "characters"}
	one := doc["characters"].(map[string]any)["claude"]
	characters := map[string]any{}
	for i := range 65 {
		characters[fmt.Sprintf("c%d", i)] = one
	}
	doc["characters"] = characters
	expectRefused(t, importErr(t, s, doc, goodFiles()), http.StatusBadRequest)

	doc = manifestDoc()
	doc["name"] = map[string]any{"en": "scenes"}
	scene := sceneOf(doc)
	scenes := map[string]any{}
	for i := range 257 {
		scenes[fmt.Sprintf("s%d", i)] = scene
	}
	doc["characters"].(map[string]any)["claude"].(map[string]any)["scenes"] = scenes
	expectRefused(t, importErr(t, s, doc, goodFiles()), http.StatusBadRequest)
}

func importErr(t *testing.T, s *Store, doc any, files map[string][]byte) error {
	t.Helper()
	_, err := doImport(context.Background(), s, "alice", packZip(t, doc, files))
	return err
}

// hostilePaths are names no entry and no manifest path may have.
func hostilePaths() []string {
	names := []string{
		"../evil.webp",
		"a/../../evil.webp",
		"a/../evil.webp",
		"/etc/evil.webp",
		"C:/evil.webp",
		"C:evil.webp",
		"a\\evil.webp",
		"..\\evil.webp",
		"\\\\server\\share\\evil.webp",
		"./evil.webp",
		"a/./evil.webp",
		"a//evil.webp",
		"a\x00b.webp",
		"a\nb.webp",
		"evil.webp:stream.webp",
		"a./evil.webp",
		"a /evil.webp",
		strings.Repeat("a", 196) + ".webp",
	}
	if runtime.GOOS == "windows" {
		names = append(names, "con.webp", "aux/evil.webp")
	}
	return names
}

func TestAHostilePathInTheZipIsRefused(t *testing.T) {
	s, database := newTestStore(t)
	for _, name := range hostilePaths() {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			// Named by the manifest too, so that nothing but the path is wrong.
			doc, files := manifestDoc(), goodFiles()
			sceneOf(doc)["image"] = name
			delete(files, "figures/claude/read.png")
			files[name] = pngImage
			_, err := doImport(context.Background(), s, "alice", packZip(t, doc, files))
			expectRefused(t, err, http.StatusBadRequest)
			expectNoTrace(t, s, database)
		})
	}
}

func TestAHostilePathInTheManifestIsRefused(t *testing.T) {
	s, database := newTestStore(t)
	for _, name := range hostilePaths() {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			doc := manifestDoc()
			doc["rooms"].(map[string]any)["lounge"] = name
			_, err := doImport(context.Background(), s, "alice", packZip(t, doc, goodFiles()))
			expectRefused(t, err, http.StatusBadRequest)
			expectNoTrace(t, s, database)
		})
	}
}

func TestAHostileDirectoryEntryIsRefusedAndAHarmlessOneIgnored(t *testing.T) {
	s, database := newTestStore(t)
	files := goodFiles()
	good := func(extra ...zipEntry) []byte {
		entries := []zipEntry{
			{name: ManifestFile, data: marshalDoc(t, manifestDoc())},
			{name: "rooms/lounge.webp", data: files["rooms/lounge.webp"]},
			{name: "figures/claude/read.png", data: files["figures/claude/read.png"]},
		}
		return buildZip(t, append(entries, extra...)...)
	}
	_, err := doImport(context.Background(), s, "alice", good(zipEntry{name: "../outside/"}))
	expectRefused(t, err, http.StatusBadRequest)
	expectNoTrace(t, s, database)

	if _, err := doImport(context.Background(), s, "alice", good(zipEntry{name: "rooms/"}, zipEntry{name: "unused/"})); err != nil {
		t.Fatalf("directory entries: %v", err)
	}
	if _, err := os.Stat(s.dir + "/magic/unused"); !os.IsNotExist(err) {
		t.Fatalf("a directory entry was created: %v", err)
	}
}

func TestANonRegularEntryIsRefusedByItsMode(t *testing.T) {
	s, database := newTestStore(t)
	for name, mode := range map[string]fs.FileMode{
		"symlink":   fs.ModeSymlink | 0o777,
		"device":    fs.ModeDevice | 0o644,
		"character": fs.ModeDevice | fs.ModeCharDevice | 0o644,
		"pipe":      fs.ModeNamedPipe | 0o644,
		"socket":    fs.ModeSocket | 0o644,
	} {
		t.Run(name, func(t *testing.T) {
			archive := buildZip(t,
				zipEntry{name: ManifestFile, data: marshalDoc(t, manifestDoc())},
				zipEntry{name: "rooms/lounge.webp", data: webpImage},
				// A wanted name and an image's bytes, so that the mode alone is wrong.
				zipEntry{name: "figures/claude/read.png", data: pngImage, mode: mode},
			)
			_, err := doImport(context.Background(), s, "alice", archive)
			expectRefused(t, err, http.StatusBadRequest)
			expectNoTrace(t, s, database)
		})
	}
	t.Run("symlinked manifest", func(t *testing.T) {
		archive := buildZip(t,
			zipEntry{name: ManifestFile, data: marshalDoc(t, manifestDoc()), mode: fs.ModeSymlink | 0o777},
			zipEntry{name: "rooms/lounge.webp", data: webpImage},
			zipEntry{name: "figures/claude/read.png", data: pngImage},
		)
		_, err := doImport(context.Background(), s, "alice", archive)
		expectRefused(t, err, http.StatusBadRequest)
		expectNoTrace(t, s, database)
	})
}

func TestNamesThatCollideAreRefused(t *testing.T) {
	s, database := newTestStore(t)
	manifest := marshalDoc(t, manifestDoc())
	base := []zipEntry{
		{name: "rooms/lounge.webp", data: webpImage},
		{name: "figures/claude/read.png", data: pngImage},
	}
	for name, extra := range map[string][]zipEntry{
		"differ by case":     {{name: "ROOMS/LOUNGE.WEBP", data: webpImage}},
		"exact duplicate":    {{name: "rooms/lounge.webp", data: webpImage}},
		"two manifests":      {{name: ManifestFile, data: manifest}},
		"manifest by case":   {{name: "THEME.JSON", data: manifest}},
		"file and directory": {{name: "rooms/lounge.webp/inner.png", data: pngImage}},
	} {
		t.Run(name, func(t *testing.T) {
			entries := append([]zipEntry{{name: ManifestFile, data: manifest}}, base...)
			_, err := doImport(context.Background(), s, "alice", buildZip(t, append(entries, extra...)...))
			expectRefused(t, err, http.StatusBadRequest)
			expectNoTrace(t, s, database)
		})
	}
}

func TestAManifestThatIsNotOneIsRefused(t *testing.T) {
	s, database := newTestStore(t)
	whole := marshalDoc(t, manifestDoc())
	for name, entries := range map[string][]zipEntry{
		"none":           {{name: "rooms/lounge.webp", data: webpImage}},
		"in a folder":    {{name: "x/" + ManifestFile, data: whole}},
		"not JSON":       {{name: ManifestFile, data: []byte("<html>")}},
		"an array":       {{name: ManifestFile, data: []byte("[]")}},
		"two values":     {{name: ManifestFile, data: append(append([]byte{}, whole...), whole...)}},
		"over the limit": {{name: ManifestFile, data: append(append([]byte{}, whole...), bytes.Repeat([]byte(" "), 4<<20)...)}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := doImport(context.Background(), s, "alice", buildZip(t, entries...))
			expectRefused(t, err, http.StatusBadRequest)
			expectNoTrace(t, s, database)
		})
	}
}

func TestSomethingThatIsNotAZipIsRefused(t *testing.T) {
	s, database := newTestStore(t)
	for name, data := range map[string][]byte{
		"text":  []byte("hello"),
		"empty": {},
		"png":   pngImage,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := doImport(context.Background(), s, "alice", data)
			expectRefused(t, err, http.StatusBadRequest)
			expectNoTrace(t, s, database)
		})
	}
}

// countingReaderAt counts what is read of the archive.
type countingReaderAt struct {
	r    io.ReaderAt
	read int64
}

func (c *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.r.ReadAt(p, off)
	c.read += int64(n)
	return n, err
}

// bombZip is a good pack whose room image inflates to size bytes while its
// headers claim to hold claimed.
func bombZip(t *testing.T, claimed uint64, size int) []byte {
	t.Helper()
	var deflated bytes.Buffer
	compressor, err := flate.NewWriter(&deflated, flate.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compressor.Write(webpImage); err != nil {
		t.Fatal(err)
	}
	if _, err := compressor.Write(make([]byte, size)); err != nil {
		t.Fatal(err)
	}
	if err := compressor.Close(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for _, entry := range []zipEntry{
		{name: ManifestFile, data: marshalDoc(t, manifestDoc())},
		{name: "figures/claude/read.png", data: pngImage},
	} {
		w, err := writer.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	w, err := writer.CreateRaw(&zip.FileHeader{
		Name: "rooms/lounge.webp", Method: zip.Deflate, CRC32: 1,
		CompressedSize64: uint64(deflated.Len()), UncompressedSize64: claimed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(deflated.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// A zip bomb is stopped by the bytes it really inflates to, whatever its
// header claims, well before the end of its data.
func TestAZipBombWhoseHeaderLiesStopsEarly(t *testing.T) {
	s, database := newTestStore(t)
	const inflated = 64 << 20
	for name, claimed := range map[string]uint64{
		"claims little":                      100,
		"claims a lot":                       1 << 40,
		"claims a size under the file limit": 1 << 20,
	} {
		t.Run(name, func(t *testing.T) {
			archive := bombZip(t, claimed, inflated)
			counted := &countingReaderAt{r: bytes.NewReader(archive)}
			_, err := s.Import(context.Background(), "alice", counted, int64(len(archive)))
			expectRefused(t, err, http.StatusBadRequest)
			expectNoTrace(t, s, database)
			if counted.read >= int64(len(archive))*3/4 {
				t.Fatalf("read %d of %d bytes before stopping", counted.read, len(archive))
			}
		})
	}
}

func TestSizeLimits(t *testing.T) {
	s, database := newTestStore(t)
	limits := DefaultLimits()
	sized := func(n int) []byte {
		return append(append([]byte{}, pngImage...), make([]byte, n-len(pngImage))...)
	}
	t.Run("a file over the limit", func(t *testing.T) {
		files := goodFiles()
		files["figures/claude/read.png"] = sized(int(limits.MaxFileBytes) + 1)
		expectRefused(t, importErr(t, s, manifestDoc(), files), http.StatusBadRequest)
		expectNoTrace(t, s, database)
	})
	t.Run("a file at the limit", func(t *testing.T) {
		doc, files := manifestDoc(), goodFiles()
		doc["name"] = map[string]any{"en": "atlimit"}
		files["figures/claude/read.png"] = sized(int(limits.MaxFileBytes))
		if err := importErr(t, s, doc, files); err != nil {
			t.Fatalf("at the limit: %v", err)
		}
	})
	if _, err := database.Exec(`DELETE FROM theme_packs`); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(s.dir); err != nil {
		t.Fatal(err)
	}

	t.Run("more uncompressed than the total allows", func(t *testing.T) {
		small := limits
		small.MaxTotalBytes = 1 << 20
		s.SetLimits(small)
		defer s.SetLimits(limits)
		files := goodFiles()
		files["rooms/lounge.webp"] = append(append([]byte{}, webpImage...), make([]byte, 600<<10)...)
		files["figures/claude/read.png"] = sized(600 << 10)
		expectRefused(t, importErr(t, s, manifestDoc(), files), http.StatusBadRequest)
		expectNoTrace(t, s, database)
	})
	t.Run("an upload over the zip limit", func(t *testing.T) {
		_, err := s.Import(context.Background(), "alice", bytes.NewReader(nil), limits.MaxZipBytes+1)
		expectRefused(t, err, http.StatusRequestEntityTooLarge)
		expectNoTrace(t, s, database)
	})
}

// manyFilesDoc names n images, one a scene.
func manyFilesDoc(n int) (map[string]any, map[string][]byte) {
	doc := manifestDoc()
	scene := sceneOf(doc)
	characters := map[string]any{}
	files := map[string][]byte{"rooms/lounge.webp": webpImage}
	for i := 0; len(files) < n; i++ {
		scenes := map[string]any{}
		for j := 0; j < 256 && len(files) < n; j++ {
			name := fmt.Sprintf("figures/%d/%d.png", i, j)
			one := map[string]any{}
			for key, value := range scene {
				one[key] = value
			}
			one["image"] = name
			scenes[fmt.Sprintf("s%d", j)] = one
			files[name] = pngImage
		}
		characters[fmt.Sprintf("c%d", i)] = map[string]any{"scenes": scenes}
	}
	doc["characters"] = characters
	return doc, files
}

func TestTooManyFiles(t *testing.T) {
	s, database := newTestStore(t)
	// 2000 with theme.json is the most there may be.
	doc, files := manyFilesDoc(2000)
	expectRefused(t, importErr(t, s, doc, files), http.StatusBadRequest)
	expectNoTrace(t, s, database)
	doc, files = manyFilesDoc(1999)
	if err := importErr(t, s, doc, files); err != nil {
		t.Fatalf("2000 files: %v", err)
	}
}

// A zip can be all directory: millions of entries in 200 MiB, each one memory
// before a single name is looked at. That is refused while reading it.
func TestAZipThatIsAllDirectoryIsRefusedWhileReadingIt(t *testing.T) {
	s, database := newTestStore(t)
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for i := range 300_000 {
		if _, err := writer.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("%07d", i), Method: zip.Store}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	archive := out.Bytes()
	counted := &countingReaderAt{r: bytes.NewReader(archive)}
	_, err := s.Import(context.Background(), "alice", counted, int64(len(archive)))
	expectRefused(t, err, http.StatusBadRequest)
	expectNoTrace(t, s, database)
	if limit := int64(maxDirectoryBytes + 1<<20); counted.read > limit {
		t.Fatalf("read %d bytes of directory, limit %d", counted.read, limit)
	}
}
