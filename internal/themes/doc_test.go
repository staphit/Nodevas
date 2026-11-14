package themes

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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
			// Both styles are shown: a figure in a room, and a whole picture
			// that moves and carries a sign, and one that is a clip with a
			// click clip.
			var inRoom, whole, moving, filmed, clicked, signed, titled, voiced bool
			for _, character := range manifest.Characters {
				titled = titled || character.Title != nil
				voiced = voiced || len(character.Lines) > 0
				for _, scene := range character.Scenes {
					inRoom = inRoom || scene.Room != ""
					whole = whole || scene.Room == ""
					moving = moving || scene.Frames != nil
					filmed = filmed || scene.Video != ""
					clicked = clicked || scene.ClickVideo != ""
					signed = signed || (scene.Sign != nil && character.Sign != "")
				}
			}
			if !inRoom || !whole || !moving || !filmed || !clicked || !signed {
				t.Fatalf("the guide's manifest shows a room %v, a whole picture %v, frames %v, a clip %v, a click clip %v, a sign %v",
					inRoom, whole, moving, filmed, clicked, signed)
			}
			if manifest.Skin == nil || len(manifest.Skin.Colors) == 0 || len(manifest.Places) == 0 {
				t.Fatalf("the guide's manifest shows a skin %v and places %v", manifest.Skin, manifest.Places)
			}
			if len(manifest.Words) == 0 || len(manifest.Lines) == 0 || !titled || !voiced {
				t.Fatalf("the guide's manifest shows words %v, lines %v, a character's title %v and its lines %v",
					manifest.Words, manifest.Lines, titled, voiced)
			}
			var archive bytes.Buffer
			writer := zip.NewWriter(&archive)
			entries := map[string][]byte{ManifestFile: match[1]}
			for name := range manifest.files() {
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

// The guides list what a skin may colour, which places a pack may name, and
// which words and lines it may write: every one an import accepts must be
// there, and so must the rules for them.
func TestTheGuidesNameEveryColourPlaceWordAndState(t *testing.T) {
	stated := []string{"`light`", "`dark`", "`sans`", "`serif`", "`#rgb`", "`#rrggbb`", "`#rrggbbaa`", strconv.Itoa(maxPlaceRunes),
		strconv.Itoa(maxWordRunes), strconv.Itoa(maxLineRunes), strconv.Itoa(maxTitleRunes), "`{node}`"}
	for _, names := range []map[string]bool{skinColors, placeIDs, wordKeys, lineStates} {
		for name := range names {
			stated = append(stated, "`"+name+"`")
		}
	}
	for _, guide := range []string{"theme-packs.md", "theme-packs.zh-TW.md"} {
		text, err := os.ReadFile(filepath.Join("..", "..", "docs", guide))
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range stated {
			if !strings.Contains(string(text), value) {
				t.Errorf("%s does not state %s", guide, value)
			}
		}
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
