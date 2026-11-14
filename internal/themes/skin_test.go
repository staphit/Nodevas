package themes

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// A pack can colour the whole app with a skin, and give the six places of the
// war room its own names. Both are optional.

func goodSkin() map[string]any {
	return map[string]any{
		"scheme": "dark",
		"font":   "serif",
		"colors": map[string]any{"bg": "#0d0e1c", "surface": "#15142b", "text": "#efe6d0", "accent": "#d9b86c"},
	}
}

func goodPlaces() map[string]any {
	return map[string]any{
		"work":     map[string]any{"en": "Study", "zh-TW": "書房"},
		"meeting":  map[string]any{"en": "Council", "zh-TW": "議事廳"},
		"entrance": map[string]any{"en": "Gate", "zh-TW": "城門"},
		"lounge":   map[string]any{"en": "Hearth", "zh-TW": "爐邊"},
		"away":     map[string]any{"en": "Wilds", "zh-TW": "野外"},
		"runners":  map[string]any{"en": "Crystal tower", "zh-TW": "水晶塔"},
	}
}

func TestASkinIsKeptAsGiven(t *testing.T) {
	for name, skin := range map[string]map[string]any{
		"every part":       goodSkin(),
		"the scheme alone": {"scheme": "light"},
		"sans":             {"scheme": "light", "font": "sans"},
		"every colour, in each form of hex": {"scheme": "dark", "colors": map[string]any{
			"bg": "#fff", "surface": "#FFF", "surface-2": "#15142b", "border": "#15142B", "border-strong": "#AbCdEf",
			"hover": "#15142b80", "text": "#EFE6D0FF", "text-2": "#000", "accent": "#d9b86c", "accent-text": "#00000000",
		}},
	} {
		t.Run(name, func(t *testing.T) {
			doc := manifestDoc()
			doc["skin"] = skin
			if got := storedForm(t, doc)["skin"]; !reflect.DeepEqual(got, any(skin)) {
				t.Fatalf("skin = %v, want %v", got, skin)
			}
		})
	}
	// Left out, not written empty: the reader tells "none" by the key's absence.
	t.Run("no colours", func(t *testing.T) {
		for _, colors := range []any{map[string]any{}, nil} {
			doc := manifestDoc()
			doc["skin"] = map[string]any{"scheme": "dark", "font": "", "colors": colors}
			if got, want := storedForm(t, doc)["skin"], map[string]any{"scheme": "dark"}; !reflect.DeepEqual(got, any(want)) {
				t.Fatalf("skin = %v, want %v", got, want)
			}
		}
	})
}

func TestPlacesAreKeptAsGiven(t *testing.T) {
	longest := strings.Repeat("魔", 40)
	for name, places := range map[string]map[string]any{
		"all six":            goodPlaces(),
		"one of them":        {"lounge": map[string]any{"en": "Hearth", "zh-TW": "爐邊"}},
		"English alone":      {"work": map[string]any{"en": "Study"}, "away": map[string]any{"en": "Wilds"}},
		"the longest names":  {"runners": map[string]any{"en": strings.Repeat("a", 40), "zh-TW": longest}},
		"a name in no ASCII": {"meeting": map[string]any{"en": "Salón de té", "zh-TW": "議事廳"}},
	} {
		t.Run(name, func(t *testing.T) {
			doc := manifestDoc()
			doc["places"] = places
			if got := storedForm(t, doc)["places"]; !reflect.DeepEqual(got, any(places)) {
				t.Fatalf("places = %v, want %v", got, places)
			}
		})
	}
}

// Schema 1 from before either field: still a pack, and nothing is written in
// their place.
func TestAPackWithNoSkinAndNoPlacesStoresNeither(t *testing.T) {
	for name, edit := range map[string]func(map[string]any){
		"left out":      func(map[string]any) {},
		"null":          func(d map[string]any) { d["skin"], d["places"] = nil, nil },
		"no places yet": func(d map[string]any) { d["places"] = map[string]any{} },
	} {
		t.Run(name, func(t *testing.T) {
			doc := manifestDoc()
			edit(doc)
			stored := storedForm(t, doc)
			for _, key := range []string{"skin", "places"} {
				if value, has := stored[key]; has {
					t.Fatalf("%s = %v", key, value)
				}
			}
		})
	}
}

func TestASpoiledSkinRefusesTheWholeZip(t *testing.T) {
	s, database := newTestStore(t)
	colour := func(value any) any {
		skin := goodSkin()
		skin["colors"].(map[string]any)["accent"] = value
		return skin
	}
	with := func(key string, value any) any {
		skin := goodSkin()
		skin[key] = value
		return skin
	}
	for _, tc := range []struct {
		name string
		skin any
	}{
		{"no scheme", map[string]any{"font": "serif"}},
		{"nothing in it", map[string]any{}},
		{"an empty scheme", with("scheme", "")},
		{"a scheme of its own", with("scheme", "sepia")},
		{"a capital scheme", with("scheme", "Dark")},
		{"a scheme that is no string", with("scheme", true)},
		{"a font of its own", with("font", "Comic Sans MS")},
		{"a font family", with("font", "sans-serif")},
		{"a capital font", with("font", "Serif")},
		{"a font that is no string", with("font", []any{"serif"})},
		{"unknown field", with("radius", "4px")},
		{"colours that are a list", with("colors", []any{"#fff"})},
		{"unknown colour", with("colors", map[string]any{"shadow": "#000"})},
		{"a colour's CSS name", with("colors", map[string]any{"--bg": "#000"})},
		{"a capital colour", with("colors", map[string]any{"BG": "#000"})},
		{"a third text colour", with("colors", map[string]any{"text-3": "#000"})},
		{"a named colour", colour("red")},
		{"rgb()", colour("rgb(0,0,0)")},
		{"two digits", colour("#12")},
		{"four digits", colour("#1234")},
		{"five digits", colour("#12345")},
		{"seven digits", colour("#1234567")},
		{"nine digits", colour("#123456789")},
		{"no hash", colour("d9b86c")},
		{"the hash alone", colour("#")},
		{"a letter past f", colour("#d9b86g")},
		{"a variable", colour("var(--x)")},
		{"a second declaration", colour("#fff;background:url(x)")},
		{"a url", colour("url(https://example.com/x.png)")},
		{"a space before", colour(" #fff")},
		{"a space after", colour("#fff ")},
		{"a space inside", colour("#ff f")},
		{"a line end after", colour("#fff\n")},
		{"a tab after", colour("#fff\t")},
		{"a closing brace", colour("#fff}")},
		{"a comment", colour("#fff/**/")},
		{"digits that are not ASCII", colour("#１２３")},
		{"empty", colour("")},
		{"a number", colour(0xffffff)},
		{"a scheme in place of the object", "dark"},
		{"a list in place of the object", []any{"dark"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := manifestDoc()
			doc["skin"] = tc.skin
			expectRefused(t, importErr(t, s, doc, goodFiles()), http.StatusBadRequest)
			expectNoTrace(t, s, database)
		})
	}
}

func TestSpoiledPlacesRefuseTheWholeZip(t *testing.T) {
	s, database := newTestStore(t)
	named := func(name map[string]any) any { return map[string]any{"lounge": name} }
	for _, tc := range []struct {
		name   string
		places any
	}{
		{"unknown place", map[string]any{"attic": map[string]any{"en": "Attic"}}},
		{"a capital place", map[string]any{"Lounge": map[string]any{"en": "Hearth"}}},
		{"a seventh place among the six", func() any {
			places := goodPlaces()
			places["kitchen"] = map[string]any{"en": "Kitchen"}
			return places
		}()},
		{"no English name", named(map[string]any{"zh-TW": "爐邊"})},
		{"an empty English name", named(map[string]any{"en": "", "zh-TW": "爐邊"})},
		{"nothing in it", named(map[string]any{})},
		{"long English name", named(map[string]any{"en": strings.Repeat("a", 41)})},
		{"long Chinese name", named(map[string]any{"en": "Hearth", "zh-TW": strings.Repeat("魔", 41)})},
		{"a line end in a name", named(map[string]any{"en": "Hearth\nGate"})},
		{"a tab in a name", named(map[string]any{"en": "Hearth\tGate"})},
		{"a null byte in a name", named(map[string]any{"en": "Hearth\x00"})},
		{"an escape in a name", named(map[string]any{"en": "\x1b[31mHearth"})},
		{"a delete in a name", named(map[string]any{"en": "Hearth\x7f"})},
		{"a C1 control in a name", named(map[string]any{"en": "Hearth\u0085"})},
		{"a control character in the Chinese name", named(map[string]any{"en": "Hearth", "zh-TW": "爐\r邊"})},
		{"unknown language", named(map[string]any{"en": "Hearth", "fr": "Foyer"})},
		{"a name that is plain text", map[string]any{"lounge": "Hearth"}},
		{"a name that is no string", named(map[string]any{"en": 7})},
		{"a list in place of the object", []any{"lounge"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := manifestDoc()
			doc["places"] = tc.places
			expectRefused(t, importErr(t, s, doc, goodFiles()), http.StatusBadRequest)
			expectNoTrace(t, s, database)
		})
	}
}

// Both reach the manifest a browser is served, beside what was there before.
func TestTheStoredManifestKeepsASkinAndPlaces(t *testing.T) {
	s, database := newTestStore(t)
	doc := manifestDoc()
	doc["skin"], doc["places"] = goodSkin(), goodPlaces()
	if err := importErr(t, s, doc, goodFiles()); err != nil {
		t.Fatal(err)
	}
	stored := asDoc(t, readPackFile(t, s, "magic", ManifestFile))
	if got := stored["skin"]; !reflect.DeepEqual(got, any(goodSkin())) {
		t.Fatalf("skin = %v", got)
	}
	if got := stored["places"]; !reflect.DeepEqual(got, any(goodPlaces())) {
		t.Fatalf("places = %v", got)
	}
	if stored["id"] != "magic" || sceneOf(stored)["room"] != "lounge" {
		t.Fatalf("the rest of the manifest = %v", stored)
	}
	expectNoTrace(t, s, database, "magic")
}
