package packs

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pngBytes is the smallest thing DetectKind takes for a PNG: the signature
// and a little more. The validator reads magic bytes; it does not decode.
var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)

const svgText = `<?xml version="1.0"?>
<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10"/></svg>`

const minimalManifest = `{"format":1,"id":"neon","name":{"en":"Neon"},"layout":"rooms",` +
	`"palette":{"ground":"#0e1726","text":"#fff"},"sprites":{"agent":"images/a.png"}}`

// entry is one file of a test zip.
type entry struct {
	name    string
	content []byte
	mode    fs.FileMode
	method  uint16
}

func file(name, content string) entry { return entry{name: name, content: []byte(content)} }

func buildZip(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		header := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.method != 0 {
			header.Method = e.method
		}
		if e.mode != 0 {
			header.SetMode(e.mode)
		}
		out, err := w.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write(e.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func minimalZip(t *testing.T) []byte {
	return buildZip(t, file("pack.json", minimalManifest), entry{name: "images/a.png", content: pngBytes})
}

func manifestWith(t *testing.T, manifest string, extra ...entry) []byte {
	return buildZip(t, append([]entry{file("pack.json", manifest)}, extra...)...)
}

func TestParseZipRules(t *testing.T) {
	png := entry{name: "images/a.png", content: pngBytes}
	many := []entry{file("pack.json", minimalManifest), png}
	for i := 0; i < MaxFiles; i++ {
		many = append(many, file(fmt.Sprintf("junk/%d.txt", i), "x"))
	}
	bomb := entry{name: "zeros.bin", content: make([]byte, MaxUnpacked+1)}

	cases := []struct {
		name string
		zip  func(t *testing.T) []byte
		// want is a fragment of the refusal; "" means the pack is accepted.
		want string
	}{
		{"valid minimal pack", minimalZip, ""},
		{"no sprites at all", func(t *testing.T) []byte {
			return manifestWith(t, `{"format":1,"id":"plain","name":{"en":"Plain"},"layout":"path"}`)
		}, ""},
		{"inside a top-level folder", func(t *testing.T) []byte {
			return buildZip(t,
				entry{name: "neon/", mode: fs.ModeDir | 0o755},
				file("neon/pack.json", minimalManifest),
				entry{name: "neon/images/a.png", content: pngBytes},
				file("__MACOSX/neon/._pack.json", "finder metadata"))
		}, ""},
		{"two top-level folders", func(t *testing.T) []byte {
			return buildZip(t, file("a/pack.json", minimalManifest), file("b/pack.json", minimalManifest))
		}, "one top-level folder"},
		{"no pack.json", func(t *testing.T) []byte {
			return buildZip(t, png)
		}, "no pack.json in images/"},
		{"too many files", func(t *testing.T) []byte { return buildZip(t, many...) }, "more than 200 files"},
		{"zip bomb", func(t *testing.T) []byte {
			return buildZip(t, file("pack.json", minimalManifest), png, bomb)
		}, "unpacks to more than 20 MB"},
		{"image over 2 MB", func(t *testing.T) []byte {
			big := append(append([]byte{}, pngBytes...), make([]byte, MaxImage)...)
			return manifestWith(t, minimalManifest, entry{name: "images/a.png", content: big})
		}, "larger than 2 MB"},
		{"dot-dot path", func(t *testing.T) []byte {
			return buildZip(t, file("pack.json", minimalManifest), png, file("images/../../evil", "x"))
		}, "uses .."},
		{"absolute path", func(t *testing.T) []byte {
			return buildZip(t, file("pack.json", minimalManifest), png, file("/etc/evil", "x"))
		}, "absolute path"},
		{"drive letter path", func(t *testing.T) []byte {
			return buildZip(t, file("pack.json", minimalManifest), png, file("C:/evil", "x"))
		}, "absolute path"},
		{"backslash", func(t *testing.T) []byte {
			return buildZip(t, file("pack.json", minimalManifest), png, file(`images\b.png`, "x"))
		}, "backslash"},
		{"symlink entry", func(t *testing.T) []byte {
			return buildZip(t, file("pack.json", minimalManifest), png,
				entry{name: "images/link.png", content: []byte("/etc/passwd"), mode: fs.ModeSymlink | 0o777})
		}, "is a link"},
		{"non-image disguised as png", func(t *testing.T) []byte {
			return manifestWith(t, minimalManifest, file("images/a.png", "<html><script>alert(1)</script></html>"))
		}, "not a PNG, WebP or SVG"},
		{"binary containing <svg", func(t *testing.T) []byte {
			return manifestWith(t, minimalManifest, file("images/a.png", "\x00\x01<svg>"))
		}, "not a PNG, WebP or SVG"},
		{"svg is fine", func(t *testing.T) []byte {
			return manifestWith(t, strings.Replace(minimalManifest, "images/a.png", "images/a.svg", 1),
				file("images/a.svg", svgText))
		}, ""},
		{"webp is fine", func(t *testing.T) []byte {
			return manifestWith(t, minimalManifest, file("images/a.png", "RIFF\x10\x00\x00\x00WEBPVP8 "))
		}, ""},
		{"unknown sprite key", func(t *testing.T) []byte {
			return manifestWith(t, strings.Replace(minimalManifest, `"agent"`, `"monster"`, 1), png)
		}, `"monster" is not a rooms sprite`},
		{"url in a name", func(t *testing.T) []byte {
			return manifestWith(t, strings.Replace(minimalManifest, `"Neon"`, `"see https://example.com"`, 1), png)
		}, "contains a URL"},
		{"protocol-relative url", func(t *testing.T) []byte {
			return manifestWith(t, strings.Replace(minimalManifest, `"Neon"`, `"//example.com/x"`, 1), png)
		}, "contains a URL"},
		{"escaped url", func(t *testing.T) []byte {
			return manifestWith(t, strings.Replace(minimalManifest, `"Neon"`, `"https\u003a//example.com"`, 1), png)
		}, "contains a URL"},
		{"url as sprite path", func(t *testing.T) []byte {
			return manifestWith(t, strings.Replace(minimalManifest, "images/a.png", "https://example.com/a.png", 1), png)
		}, "contains a URL"},
		{"bad colour", func(t *testing.T) []byte {
			return manifestWith(t, strings.Replace(minimalManifest, "#0e1726", "red", 1), png)
		}, "#rgb or #rrggbb"},
		{"css in a colour", func(t *testing.T) []byte {
			return manifestWith(t, strings.Replace(minimalManifest, "#0e1726", "#000;background:red", 1), png)
		}, "#rgb or #rrggbb"},
		{"css in a palette name", func(t *testing.T) []byte {
			return manifestWith(t, strings.Replace(minimalManifest, `"ground"`, `"ground;background:red"`, 1), png)
		}, "palette name"},
		{"sprite inside a reserved name", func(t *testing.T) []byte {
			return manifestWith(t, strings.Replace(minimalManifest, "images/a.png", "install.json/a.png", 1),
				entry{name: "install.json/a.png", content: pngBytes})
		}, "uses for itself"},
		{"bad id", func(t *testing.T) []byte {
			return manifestWith(t, strings.Replace(minimalManifest, `"neon"`, `"Neon Station"`, 1), png)
		}, "lowercase letters"},
		{"id too long", func(t *testing.T) []byte {
			return manifestWith(t, strings.Replace(minimalManifest, `"neon"`, `"`+strings.Repeat("a", 41)+`"`, 1), png)
		}, "lowercase letters"},
		{"no en name", func(t *testing.T) []byte {
			return manifestWith(t, strings.Replace(minimalManifest, `"en"`, `"zh-TW"`, 1), png)
		}, `"en" entry`},
		{"wrong format", func(t *testing.T) []byte {
			return manifestWith(t, strings.Replace(minimalManifest, `"format":1`, `"format":2`, 1), png)
		}, "format must be 1"},
		{"unknown layout", func(t *testing.T) []byte {
			return manifestWith(t, strings.Replace(minimalManifest, `"rooms"`, `"grid"`, 1), png)
		}, "layout must be"},
		{"unknown field", func(t *testing.T) []byte {
			return manifestWith(t, strings.Replace(minimalManifest, `"format":1,`, `"format":1,"script":"x",`, 1), png)
		}, "unknown field"},
		{"pack.json does not parse", func(t *testing.T) []byte {
			return manifestWith(t, `{"format":1,`, png)
		}, "does not parse"},
		{"missing sprite file", func(t *testing.T) []byte {
			return manifestWith(t, minimalManifest)
		}, "images/a.png is not in the zip"},
		{"sprite path with a space", func(t *testing.T) []byte {
			return manifestWith(t, strings.Replace(minimalManifest, "images/a.png", "images/a b.png", 1),
				entry{name: "images/a b.png", content: pngBytes})
		}, "may use only letters"},
		{"sprite path with dot-dot", func(t *testing.T) []byte {
			return manifestWith(t, strings.Replace(minimalManifest, "images/a.png", "../a.png", 1), png)
		}, "uses .."},
		{"sprite named like pack.json", func(t *testing.T) []byte {
			return manifestWith(t, strings.Replace(minimalManifest, "images/a.png", "Pack.JSON", 1),
				entry{name: "Pack.JSON", content: pngBytes})
		}, "uses for itself"},
		{"not a zip", func(t *testing.T) []byte { return []byte("plain text") }, "not a readable zip"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseZip(tc.zip(t))
			if tc.want == "" {
				if err != nil {
					t.Fatalf("refused a valid pack: %v", err)
				}
				return
			}
			var refused *InvalidError
			if !errors.As(err, &refused) {
				t.Fatalf("err = %v, want an *InvalidError containing %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// The bomb case above proves the limit holds; this proves the zip that
// carries it is small, i.e. that the test exercises decompression rather than
// a large upload.
func TestZipBombIsSmallOnTheWire(t *testing.T) {
	data := buildZip(t, file("pack.json", minimalManifest), entry{name: "images/a.png", content: pngBytes},
		entry{name: "zeros.bin", content: make([]byte, 64<<20)})
	if len(data) > 1<<20 {
		t.Fatalf("bomb zip is %d bytes, want it well under a megabyte", len(data))
	}
	if _, err := parseZip(data); err == nil || !strings.Contains(err.Error(), "unpacks to more than") {
		t.Fatalf("err = %v", err)
	}
}

func TestInstallListOpenRemove(t *testing.T) {
	dir := t.TempDir()
	s := New(func() string { return dir })

	pack, err := s.Install(minimalZip(t), "ann")
	if err != nil {
		t.Fatal(err)
	}
	if pack.InstalledBy != "ann" || pack.InstalledAt == 0 {
		t.Fatalf("installed = %+v", pack)
	}
	wantURL := FileURL("neon", "images/a.png", pack.InstalledAt)
	if pack.Sprites["agent"] != wantURL {
		t.Fatalf("sprite url = %q, want %q", pack.Sprites["agent"], wantURL)
	}

	list, err := s.List()
	if err != nil || len(list) != 1 || list[0].ID != "neon" || list[0].Palette["ground"] != "#0e1726" {
		t.Fatalf("list = %+v, %v", list, err)
	}

	content, kind, err := s.Open("neon", "images/a.png")
	if err != nil || kind != KindPNG || !bytes.Equal(content, pngBytes) {
		t.Fatalf("open = %v %q %v", len(content), kind, err)
	}
	for _, path := range []string{"pack.json", "install.json", "images/b.png", "../neon/pack.json"} {
		if _, _, err := s.Open("neon", path); !errors.Is(err, ErrNotFound) {
			t.Fatalf("open %q = %v, want ErrNotFound", path, err)
		}
	}

	// Reinstalling replaces the pack and moves its ?v= on.
	second, err := s.Install(manifestWith(t,
		strings.Replace(minimalManifest, `"Neon"`, `"Neon two"`, 1),
		entry{name: "images/a.png", content: pngBytes}), "bea")
	if err != nil {
		t.Fatal(err)
	}
	if second.InstalledAt <= pack.InstalledAt {
		t.Fatalf("reinstall installedAt = %d, want after %d", second.InstalledAt, pack.InstalledAt)
	}
	list, _ = s.List()
	if len(list) != 1 || list[0].Name["en"] != "Neon two" || list[0].InstalledBy != "bea" {
		t.Fatalf("after reinstall = %+v", list)
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".*"))
	if len(leftovers) != 0 {
		t.Fatalf("staging folders left behind: %v", leftovers)
	}

	if err := s.Remove("neon"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("neon"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second remove = %v, want ErrNotFound", err)
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Fatalf("after remove = %+v", list)
	}
}

// A refused zip must leave the installed pack exactly as it was.
func TestInvalidInstallChangesNothing(t *testing.T) {
	dir := t.TempDir()
	s := New(func() string { return dir })
	if _, err := s.Install(minimalZip(t), "ann"); err != nil {
		t.Fatal(err)
	}
	bad := manifestWith(t, minimalManifest, file("images/a.png", "not an image"))
	if _, err := s.Install(bad, "ann"); err == nil {
		t.Fatal("installed an invalid pack")
	}
	if _, _, err := s.Open("neon", "images/a.png"); err != nil {
		t.Fatalf("the old pack is gone: %v", err)
	}
}

func TestListSkipsPacksThatNoLongerValidate(t *testing.T) {
	dir := t.TempDir()
	s := New(func() string { return dir })
	if _, err := s.Install(minimalZip(t), "ann"); err != nil {
		t.Fatal(err)
	}
	// Hand-copied, no install.json: listed with pack.json's time.
	copied := filepath.Join(dir, "plain")
	if err := os.MkdirAll(copied, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copied, "pack.json"),
		[]byte(`{"format":1,"id":"plain","name":{"en":"Plain"},"layout":"path"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Edited by hand into something invalid.
	broken := filepath.Join(dir, "broken")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "pack.json"),
		[]byte(`{"format":1,"id":"broken","name":{"en":"x"},"layout":"rooms","palette":{"ground":"url(x)"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A folder whose name is not its id.
	misnamed := filepath.Join(dir, "other")
	if err := os.MkdirAll(misnamed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(misnamed, "pack.json"),
		[]byte(`{"format":1,"id":"neon","name":{"en":"x"},"layout":"rooms"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A sprite that went missing.
	if err := os.Remove(filepath.Join(dir, "neon", "images", "a.png")); err != nil {
		t.Fatal(err)
	}

	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "plain" || list[0].InstalledAt == 0 || list[0].InstalledBy != "" {
		t.Fatalf("list = %+v, want only the hand-copied pack", list)
	}
}

func TestOpenRefusesALinkedSprite(t *testing.T) {
	dir := t.TempDir()
	s := New(func() string { return dir })
	if _, err := s.Install(minimalZip(t), "ann"); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "secret.png")
	if err := os.WriteFile(secret, pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	sprite := filepath.Join(dir, "neon", "images", "a.png")
	if err := os.Remove(sprite); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, sprite); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, err := s.Open("neon", "images/a.png"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("open through a link = %v, want ErrNotFound", err)
	}
}
