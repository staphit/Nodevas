package themes

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

// A pack can carry animated whole pictures: a scene with no room under it, an
// image that is a strip of frames, and a sign floating over the character.

const signImage = "signs/mage.png"

func characterOf(doc map[string]any) map[string]any {
	return doc["characters"].(map[string]any)["claude"].(map[string]any)
}

// signedPack is a good pack whose character has a sign, and its files.
func signedPack() (map[string]any, map[string][]byte) {
	doc, files := manifestDoc(), goodFiles()
	characterOf(doc)["sign"] = signImage
	files[signImage] = pngImage
	return doc, files
}

// storedForm is doc as an import stores and serves it: parsed, then written
// out again from what was validated.
func storedForm(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	manifest, err := parseManifest(marshalDoc(t, doc))
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	return asDoc(t, marshalDoc(t, manifest))
}

func asDoc(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestAWholePictureNeedsNoRoom(t *testing.T) {
	s, database := newTestStore(t)
	doc, files := manifestDoc(), goodFiles()
	delete(doc, "rooms")
	delete(sceneOf(doc), "room")
	delete(files, "rooms/lounge.webp")
	if err := importErr(t, s, doc, files); err != nil {
		t.Fatalf("a whole picture with no rooms: %v", err)
	}
	stored := asDoc(t, readPackFile(t, s, "magic", ManifestFile))
	if rooms, ok := stored["rooms"].(map[string]any); !ok || len(rooms) != 0 {
		t.Fatalf("rooms = %v", stored["rooms"])
	}
	if room, has := sceneOf(stored)["room"]; has {
		t.Fatalf("a room was written for a scene that has none: %v", room)
	}
	expectNoTrace(t, s, database, "magic")

	// An empty room is no room, and a pack can mix both kinds of scene.
	doc = manifestDoc()
	whole := map[string]any{}
	for key, value := range sceneOf(doc) {
		whole[key] = value
	}
	whole["room"] = ""
	characterOf(doc)["scenes"].(map[string]any)["idle"] = whole
	scenes := characterOf(storedForm(t, doc))["scenes"].(map[string]any)
	if _, has := scenes["idle"].(map[string]any)["room"]; has {
		t.Fatalf("an empty room was kept: %v", scenes["idle"])
	}
	if scenes["read"].(map[string]any)["room"] != "lounge" {
		t.Fatalf("the room of a scene that has one was lost: %v", scenes["read"])
	}

	// A room that is named must still be one of the pack's.
	doc = manifestDoc()
	delete(doc, "rooms")
	expectRefused(t, importErr(t, s, doc, files), http.StatusBadRequest)
	expectNoTrace(t, s, database, "magic")
}

func TestFramesAreKeptAsGiven(t *testing.T) {
	for _, tc := range []struct {
		name   string
		frames map[string]any
		want   map[string]any
	}{
		{"the fewest frames, and nothing played", map[string]any{"count": 2}, map[string]any{"count": 2.0}},
		{"every part", map[string]any{"count": 4, "blink": 1, "click": []any{2, 3}},
			map[string]any{"count": 4.0, "blink": 1.0, "click": []any{2.0, 3.0}}},
		{"the most frames and clicks, at the last frame",
			map[string]any{"count": 16, "blink": 15, "click": []any{1, 15, 1, 15}},
			map[string]any{"count": 16.0, "blink": 15.0, "click": []any{1.0, 15.0, 1.0, 15.0}}},
		{"an empty click list", map[string]any{"count": 3, "blink": 1, "click": []any{}}, map[string]any{"count": 3.0, "blink": 1.0}},
		// Left out, not null: the reader tells "none" by the key's absence.
		{"parts given as null", map[string]any{"count": 3, "blink": nil, "click": nil}, map[string]any{"count": 3.0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := manifestDoc()
			sceneOf(doc)["frames"] = tc.frames
			if got := sceneOf(storedForm(t, doc))["frames"]; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("frames = %v, want %v", got, tc.want)
			}
		})
	}
	t.Run("a scene with none", func(t *testing.T) {
		if frames, has := sceneOf(storedForm(t, manifestDoc()))["frames"]; has {
			t.Fatalf("frames = %v", frames)
		}
	})
}

func TestSpoiledFramesRefuseTheWholeZip(t *testing.T) {
	s, database := newTestStore(t)
	for _, tc := range []struct {
		name   string
		frames any
	}{
		{"count 1", map[string]any{"count": 1}},
		{"count 17", map[string]any{"count": 17}},
		{"count 0", map[string]any{"count": 0}},
		{"negative count", map[string]any{"count": -4}},
		{"fractional count", map[string]any{"count": 2.5}},
		{"no count", map[string]any{"blink": 1}},
		{"nothing in it", map[string]any{}},
		{"blink 0", map[string]any{"count": 4, "blink": 0}},
		{"blink equal to count", map[string]any{"count": 4, "blink": 4}},
		{"negative blink", map[string]any{"count": 4, "blink": -1}},
		{"blink that is no number", map[string]any{"count": 4, "blink": "1"}},
		{"five clicks", map[string]any{"count": 8, "click": []any{1, 2, 3, 4, 5}}},
		{"click 0", map[string]any{"count": 4, "click": []any{1, 0}}},
		{"click equal to count", map[string]any{"count": 4, "click": []any{4}}},
		{"click that is no list", map[string]any{"count": 4, "click": 2}},
		{"unknown field", map[string]any{"count": 4, "loop": true}},
		{"a number in place of the object", 4},
		{"a list in place of the object", []any{4, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := manifestDoc()
			sceneOf(doc)["frames"] = tc.frames
			expectRefused(t, importErr(t, s, doc, goodFiles()), http.StatusBadRequest)
			expectNoTrace(t, s, database)
		})
	}
}

func TestACharactersSignIsOneOfThePacksImages(t *testing.T) {
	s, database := newTestStore(t)

	t.Run("named but not in the zip", func(t *testing.T) {
		doc, files := signedPack()
		delete(files, signImage)
		expectRefused(t, importErr(t, s, doc, files), http.StatusBadRequest)
		expectNoTrace(t, s, database)
	})
	t.Run("in the zip but not named", func(t *testing.T) {
		_, files := signedPack()
		expectRefused(t, importErr(t, s, manifestDoc(), files), http.StatusBadRequest)
		expectNoTrace(t, s, database)
	})
	for name, path := range map[string]string{
		"an SVG":              "signs/mage.svg",
		"a path that walks":   "../mage.png",
		"a capital extension": "signs/mage.PNG",
	} {
		t.Run(name, func(t *testing.T) {
			doc, files := signedPack()
			characterOf(doc)["sign"] = path
			delete(files, signImage)
			files[path] = pngImage
			expectRefused(t, importErr(t, s, doc, files), http.StatusBadRequest)
			expectNoTrace(t, s, database)
		})
	}
	t.Run("not a string", func(t *testing.T) {
		doc, files := signedPack()
		characterOf(doc)["sign"] = []any{signImage}
		expectRefused(t, importErr(t, s, doc, files), http.StatusBadRequest)
		expectNoTrace(t, s, database)
	})
	t.Run("not the image its extension says", func(t *testing.T) {
		doc, files := signedPack()
		files[signImage] = []byte("<svg/>")
		expectRefused(t, importErr(t, s, doc, files), http.StatusBadRequest)
		expectNoTrace(t, s, database)
	})
	// No scene says where it goes: it is just not drawn there.
	t.Run("named and in the zip", func(t *testing.T) {
		doc, files := signedPack()
		if err := importErr(t, s, doc, files); err != nil {
			t.Fatal(err)
		}
		if got := string(readPackFile(t, s, "magic", signImage)); got != string(pngImage) {
			t.Fatalf("sign image = %q", got)
		}
		stored := asDoc(t, readPackFile(t, s, "magic", ManifestFile))
		if got := characterOf(stored)["sign"]; got != signImage {
			t.Fatalf("stored sign = %v", got)
		}
		if position, has := sceneOf(stored)["sign"]; has {
			t.Fatalf("a scene was given a sign position: %v", position)
		}
		expectNoTrace(t, s, database, "magic")
	})
}

func TestACharacterWithoutASignStoresNone(t *testing.T) {
	if sign, has := characterOf(storedForm(t, manifestDoc()))["sign"]; has {
		t.Fatalf("sign = %v", sign)
	}
}

func TestASceneSaysWhereTheSignGoes(t *testing.T) {
	// The canvas is 1536 by 864.
	for name, position := range map[string][]any{
		"inside":                        {250, 180, 96},
		"fractions of a pixel":          {250.5, 180.25, 96.5},
		"the top left corner":           {0, 0, 1},
		"the far corner, canvas wide":   {1536, 864, 1536},
		"narrower than a pixel is wide": {10, 10, 0.5},
	} {
		t.Run(name, func(t *testing.T) {
			doc, _ := signedPack()
			sceneOf(doc)["sign"] = position
			got, ok := sceneOf(storedForm(t, doc))["sign"].([]any)
			if !ok || len(got) != 3 {
				t.Fatalf("sign = %v", sceneOf(storedForm(t, doc))["sign"])
			}
			for i, value := range got {
				if want := marshalDoc(t, position[i]); string(marshalDoc(t, value)) != string(want) {
					t.Fatalf("sign = %v, want %v", got, position)
				}
			}
		})
	}
}

func TestASpoiledSceneSignRefusesTheWholeZip(t *testing.T) {
	s, database := newTestStore(t)
	for _, tc := range []struct {
		name string
		sign any
	}{
		{"two numbers", []any{250, 180}},
		{"four numbers", []any{250, 180, 96, 96}},
		{"no numbers", []any{}},
		{"left of the canvas", []any{-1, 180, 96}},
		{"right of the canvas", []any{1537, 180, 96}},
		{"above the canvas", []any{250, -0.5, 96}},
		{"below the canvas", []any{250, 864.5, 96}},
		{"zero width", []any{250, 180, 0}},
		{"negative width", []any{250, 180, -96}},
		{"wider than the canvas", []any{250, 180, 1537}},
		{"a number too large to hold", json.RawMessage(`[250, 180, 1e999]`)},
		{"not numbers", []any{"250", 180, 96}},
		{"an image path, as a character's is", signImage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, files := signedPack()
			sceneOf(doc)["sign"] = tc.sign
			expectRefused(t, importErr(t, s, doc, files), http.StatusBadRequest)
			expectNoTrace(t, s, database)
		})
	}
	// A place for a sign the character does not have.
	t.Run("in a character with no sign", func(t *testing.T) {
		doc := manifestDoc()
		sceneOf(doc)["sign"] = []any{250, 180, 96}
		expectRefused(t, importErr(t, s, doc, goodFiles()), http.StatusBadRequest)
		expectNoTrace(t, s, database)
	})
}

// Everything a whole picture adds reaches the manifest a browser is served.
func TestTheStoredManifestKeepsAnAnimatedWholePicture(t *testing.T) {
	s, _ := newTestStore(t)
	doc, files := signedPack()
	delete(doc, "rooms")
	delete(files, "rooms/lounge.webp")
	scene := sceneOf(doc)
	delete(scene, "room")
	scene["frames"] = map[string]any{"count": 4, "blink": 1, "click": []any{2, 3}}
	scene["sign"] = []any{250, 180.5, 96}
	if err := importErr(t, s, doc, files); err != nil {
		t.Fatal(err)
	}
	stored := asDoc(t, readPackFile(t, s, "magic", ManifestFile))
	want := map[string]any{
		"image": "figures/claude/read.png",
		"x":     100.0, "y": 200.0, "w": 300.0, "h": 500.0,
		"box":    []any{120.0, 210.0, 380.0, 700.0},
		"frames": map[string]any{"count": 4.0, "blink": 1.0, "click": []any{2.0, 3.0}},
		"sign":   []any{250.0, 180.5, 96.0},
	}
	if got := sceneOf(stored); !reflect.DeepEqual(got, want) {
		t.Fatalf("scene = %v, want %v", got, want)
	}
	if got := characterOf(stored)["sign"]; got != signImage {
		t.Fatalf("character sign = %v", got)
	}
}
