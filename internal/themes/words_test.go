package themes

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// A pack can word the story war room its own way: the interface's words, what
// an agent says in the dialogue box by its state, and what a character is
// called on the name plate. All three are optional.

func goodWords() map[string]any {
	return map[string]any{
		"narrator": map[string]any{"en": "Chronicler", "zh-TW": "史官"},
		"unnamed":  map[string]any{"en": "A stranger", "zh-TW": "陌生人"},
		"task":     map[string]any{"en": "a quest", "zh-TW": "一項委託"},
		"nobody":   map[string]any{"en": "Nobody is here."},
	}
}

func goodLines() map[string]any {
	return map[string]any{
		"working":     map[string]any{"en": "I am casting on {node}.", "zh-TW": "我正在對 {node} 施法。"},
		"asking-user": map[string]any{"en": "A word, if you will."},
		"idle":        map[string]any{"en": "The hearth is warm.", "zh-TW": "爐火正暖。"},
	}
}

func goodTitle() map[string]any {
	return map[string]any{"en": "The Sage", "zh-TW": "賢者"}
}

// everyKey is a text under each of keys, so a case can show none is refused.
func everyKey(keys map[string]bool) map[string]any {
	texts := map[string]any{}
	for key := range keys {
		texts[key] = map[string]any{"en": "Text for " + key, "zh-TW": "文字"}
	}
	return texts
}

func TestWordsAreKeptAsGiven(t *testing.T) {
	for name, words := range map[string]map[string]any{
		"a few":             goodWords(),
		"one of them":       {"narrator": map[string]any{"en": "Chronicler"}},
		"every word":        everyKey(wordKeys),
		"the longest texts": {"empty": map[string]any{"en": strings.Repeat("a", 120), "zh-TW": strings.Repeat("魔", 120)}},
	} {
		t.Run(name, func(t *testing.T) {
			doc := manifestDoc()
			doc["words"] = words
			if got := storedForm(t, doc)["words"]; !reflect.DeepEqual(got, any(words)) {
				t.Fatalf("words = %v, want %v", got, words)
			}
		})
	}
}

func TestLinesAreKeptAsGiven(t *testing.T) {
	for name, lines := range map[string]map[string]any{
		"a few":             goodLines(),
		"one of them":       {"idle": map[string]any{"en": "Resting."}},
		"every state":       everyKey(lineStates),
		"the longest texts": {"failed": map[string]any{"en": strings.Repeat("a", 200), "zh-TW": strings.Repeat("魔", 200)}},
		// Nothing here reads a placeholder: the app does.
		"a placeholder of its own": {"working": map[string]any{"en": "On {node}, {nothing} and {."}},
	} {
		t.Run("the pack's, "+name, func(t *testing.T) {
			doc := manifestDoc()
			doc["lines"] = lines
			if got := storedForm(t, doc)["lines"]; !reflect.DeepEqual(got, any(lines)) {
				t.Fatalf("lines = %v, want %v", got, lines)
			}
		})
		t.Run("a character's, "+name, func(t *testing.T) {
			doc := manifestDoc()
			characterOf(doc)["lines"] = lines
			stored := storedForm(t, doc)
			if got := characterOf(stored)["lines"]; !reflect.DeepEqual(got, any(lines)) {
				t.Fatalf("lines = %v, want %v", got, lines)
			}
			if value, has := stored["lines"]; has {
				t.Fatalf("the pack gained lines: %v", value)
			}
		})
	}
}

func TestATitleIsKeptAsGiven(t *testing.T) {
	for name, title := range map[string]map[string]any{
		"both languages":    goodTitle(),
		"English alone":     {"en": "The Sage"},
		"the longest texts": {"en": strings.Repeat("a", 40), "zh-TW": strings.Repeat("魔", 40)},
	} {
		t.Run(name, func(t *testing.T) {
			doc := manifestDoc()
			characterOf(doc)["title"] = title
			if got := characterOf(storedForm(t, doc))["title"]; !reflect.DeepEqual(got, any(title)) {
				t.Fatalf("title = %v, want %v", got, title)
			}
		})
	}
}

// Schema 1 from before any of them: still a pack, and nothing is written in
// their place.
func TestAPackWithNoWordingStoresNone(t *testing.T) {
	for name, edit := range map[string]func(map[string]any){
		"left out": func(map[string]any) {},
		"null": func(d map[string]any) {
			d["words"], d["lines"] = nil, nil
			characterOf(d)["lines"], characterOf(d)["title"] = nil, nil
		},
		"nothing in them yet": func(d map[string]any) {
			d["words"], d["lines"] = map[string]any{}, map[string]any{}
			characterOf(d)["lines"] = map[string]any{}
		},
	} {
		t.Run(name, func(t *testing.T) {
			doc := manifestDoc()
			edit(doc)
			stored := storedForm(t, doc)
			for _, key := range []string{"words", "lines"} {
				if value, has := stored[key]; has {
					t.Fatalf("%s = %v", key, value)
				}
			}
			for _, key := range []string{"lines", "title"} {
				if value, has := characterOf(stored)[key]; has {
					t.Fatalf("the character's %s = %v", key, value)
				}
			}
		})
	}
}

// spoiledTexts are texts no wording may hold, whatever its limit: limit is the
// longest the place they go allows.
func spoiledTexts(limit int) map[string]any {
	return map[string]any{
		"no English text":                         map[string]any{"zh-TW": "文字"},
		"an empty English text":                   map[string]any{"en": "", "zh-TW": "文字"},
		"nothing in it":                           map[string]any{},
		"long English text":                       map[string]any{"en": strings.Repeat("a", limit+1)},
		"long Chinese text":                       map[string]any{"en": "Text", "zh-TW": strings.Repeat("魔", limit+1)},
		"a line end in a text":                    map[string]any{"en": "One\nTwo"},
		"a tab in a text":                         map[string]any{"en": "One\tTwo"},
		"a null byte in a text":                   map[string]any{"en": "Text\x00"},
		"an escape in a text":                     map[string]any{"en": "\x1b[31mText"},
		"a delete in a text":                      map[string]any{"en": "Text\x7f"},
		"a C1 control in a text":                  map[string]any{"en": "Text\u0085"},
		"a control character in the Chinese text": map[string]any{"en": "Text", "zh-TW": "文\r字"},
		"unknown language":                        map[string]any{"en": "Text", "fr": "Texte"},
		"plain text in place of the object":       "Text",
		"a text that is no string":                map[string]any{"en": 7},
		"a list in place of the object":           []any{"Text"},
	}
}

func TestSpoiledWordsRefuseTheWholeZip(t *testing.T) {
	s, database := newTestStore(t)
	cases := map[string]any{
		"unknown word":   map[string]any{"greeting": map[string]any{"en": "Hello"}},
		"a capital word": map[string]any{"Narrator": map[string]any{"en": "Chronicler"}},
		"a line's state": map[string]any{"idle": map[string]any{"en": "Resting."}},
		"a word in another case": func() any {
			words := goodWords()
			words["runnercount"] = map[string]any{"en": "Towers"}
			return words
		}(),
		"a list in place of the object": []any{"narrator"},
		"text in place of the object":   "Chronicler",
	}
	for name, text := range spoiledTexts(maxWordRunes) {
		cases[name] = map[string]any{"narrator": text}
	}
	for name, words := range cases {
		t.Run(name, func(t *testing.T) {
			doc := manifestDoc()
			doc["words"] = words
			expectRefused(t, importErr(t, s, doc, goodFiles()), http.StatusBadRequest)
			expectNoTrace(t, s, database)
		})
	}
}

func TestSpoiledLinesRefuseTheWholeZip(t *testing.T) {
	s, database := newTestStore(t)
	cases := map[string]any{
		"unknown state":   map[string]any{"sleeping": map[string]any{"en": "Zzz."}},
		"a capital state": map[string]any{"Idle": map[string]any{"en": "Resting."}},
		"a scene's name":  map[string]any{"read": map[string]any{"en": "Reading."}},
		"a word's key":    map[string]any{"narrator": map[string]any{"en": "Chronicler"}},
		"an eleventh state among the ten": func() any {
			lines := everyKey(lineStates)
			lines["asking_user"] = map[string]any{"en": "A word."}
			return lines
		}(),
		"a list in place of the object": []any{"idle"},
		"text in place of the object":   "Resting.",
	}
	for name, text := range spoiledTexts(maxLineRunes) {
		cases[name] = map[string]any{"idle": text}
	}
	for name, lines := range cases {
		t.Run("the pack's, "+name, func(t *testing.T) {
			doc := manifestDoc()
			doc["lines"] = lines
			expectRefused(t, importErr(t, s, doc, goodFiles()), http.StatusBadRequest)
			expectNoTrace(t, s, database)
		})
		t.Run("a character's, "+name, func(t *testing.T) {
			doc := manifestDoc()
			characterOf(doc)["lines"] = lines
			expectRefused(t, importErr(t, s, doc, goodFiles()), http.StatusBadRequest)
			expectNoTrace(t, s, database)
		})
	}
}

func TestASpoiledTitleRefusesTheWholeZip(t *testing.T) {
	s, database := newTestStore(t)
	for name, title := range spoiledTexts(maxTitleRunes) {
		t.Run(name, func(t *testing.T) {
			doc := manifestDoc()
			characterOf(doc)["title"] = title
			expectRefused(t, importErr(t, s, doc, goodFiles()), http.StatusBadRequest)
			expectNoTrace(t, s, database)
		})
	}
	// A title is a character's: the pack has none of its own.
	t.Run("a title on the pack", func(t *testing.T) {
		doc := manifestDoc()
		doc["title"] = goodTitle()
		expectRefused(t, importErr(t, s, doc, goodFiles()), http.StatusBadRequest)
		expectNoTrace(t, s, database)
	})
}

// Each limit is its own: a line may run longer than a word, and a word longer
// than a title.
func TestEachWordingHasItsOwnLimit(t *testing.T) {
	of := func(runes int) map[string]any {
		return map[string]any{"en": strings.Repeat("a", runes), "zh-TW": strings.Repeat("魔", runes)}
	}
	for _, tc := range []struct {
		name  string
		limit int
		put   func(doc map[string]any, text map[string]any)
	}{
		{"a word", 120, func(d map[string]any, text map[string]any) { d["words"] = map[string]any{"more": text} }},
		{"a line", 200, func(d map[string]any, text map[string]any) { d["lines"] = map[string]any{"limit": text} }},
		{"a character's line", 200, func(d map[string]any, text map[string]any) {
			characterOf(d)["lines"] = map[string]any{"limit": text}
		}},
		{"a title", 40, func(d map[string]any, text map[string]any) { characterOf(d)["title"] = text }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := manifestDoc()
			tc.put(doc, of(tc.limit))
			if _, err := parseManifest(marshalDoc(t, doc)); err != nil {
				t.Fatalf("%d characters are refused: %v", tc.limit, err)
			}
			doc = manifestDoc()
			tc.put(doc, of(tc.limit+1))
			_, err := parseManifest(marshalDoc(t, doc))
			expectRefused(t, err, http.StatusBadRequest)
		})
	}
}

// All of it reaches the manifest a browser is served, beside what was there
// before.
func TestTheStoredManifestKeepsItsWording(t *testing.T) {
	s, database := newTestStore(t)
	doc := manifestDoc()
	doc["words"], doc["lines"] = goodWords(), goodLines()
	ownLines := map[string]any{"idle": map[string]any{"en": "I am reading by the fire.", "zh-TW": "我在爐邊看書。"}}
	characterOf(doc)["title"], characterOf(doc)["lines"] = goodTitle(), ownLines
	if err := importErr(t, s, doc, goodFiles()); err != nil {
		t.Fatal(err)
	}
	stored := asDoc(t, readPackFile(t, s, "magic", ManifestFile))
	if got := stored["words"]; !reflect.DeepEqual(got, any(goodWords())) {
		t.Fatalf("words = %v", got)
	}
	if got := stored["lines"]; !reflect.DeepEqual(got, any(goodLines())) {
		t.Fatalf("lines = %v", got)
	}
	character := characterOf(stored)
	if got := character["title"]; !reflect.DeepEqual(got, any(goodTitle())) {
		t.Fatalf("title = %v", got)
	}
	if got := character["lines"]; !reflect.DeepEqual(got, any(ownLines)) {
		t.Fatalf("the character's lines = %v", got)
	}
	if stored["id"] != "magic" || sceneOf(stored)["room"] != "lounge" {
		t.Fatalf("the rest of the manifest = %v", stored)
	}
	expectNoTrace(t, s, database, "magic")
}
