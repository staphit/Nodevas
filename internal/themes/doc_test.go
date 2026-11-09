package themes

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var docManifest = regexp.MustCompile("(?s)```json\n(.*?)```")

// The guides tell people to copy this manifest and these limits: they must
// stay what an import accepts.
func TestTheGuidesManifestImports(t *testing.T) {
	for _, guide := range []string{"theme-packs.md", "theme-packs.zh-TW.md"} {
		t.Run(guide, func(t *testing.T) {
			text, err := os.ReadFile(filepath.Join("..", "..", "docs", guide))
			if err != nil {
				t.Fatal(err)
			}
			match := docManifest.FindSubmatch(text)
			if match == nil {
				t.Fatal("no json block in the guide")
			}
			manifest, err := parseManifest(match[1])
			if err != nil {
				t.Fatalf("the guide's manifest is refused: %v", err)
			}
			var archive bytes.Buffer
			writer := zip.NewWriter(&archive)
			entries := map[string][]byte{ManifestFile: match[1]}
			for name := range manifest.images() {
				entries[name] = webpImage
			}
			for name, data := range entries {
				entry, err := writer.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := entry.Write(data); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := readArchive(bytes.NewReader(archive.Bytes()), int64(archive.Len()), DefaultLimits()); err != nil {
				t.Fatalf("a zip of the guide's layout is refused: %v", err)
			}
		})
	}
}

func TestTheGuidesStateTheLimits(t *testing.T) {
	limits := DefaultLimits()
	stated := map[string]bool{
		"200 MiB": limits.MaxZipBytes == 200<<20,
		"16 MiB":  limits.MaxFileBytes == 16<<20,
		"4 MiB":   limits.MaxManifestBytes == 4<<20,
		"500 MiB": limits.MaxTotalBytes == 500<<20,
		"2 GiB":   limits.MaxServerBytes == 2<<30,
		"2000":    limits.MaxFiles == 2000,
	}
	for _, guide := range []string{"theme-packs.md", "theme-packs.zh-TW.md"} {
		text, err := os.ReadFile(filepath.Join("..", "..", "docs", guide))
		if err != nil {
			t.Fatal(err)
		}
		for value, same := range stated {
			if !same {
				t.Errorf("the limit stated as %s changed: update the guides", value)
			}
			if !strings.Contains(string(text), value) {
				t.Errorf("%s does not state %s", guide, value)
			}
		}
	}
}
