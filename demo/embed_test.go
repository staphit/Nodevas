package demo

import (
	"io/fs"
	"regexp"
	"slices"
	"testing"
)

var cjk = regexp.MustCompile(`\p{Han}`)

// The examples a person opens are in their interface language: Traditional
// Chinese has its own copies of every example, under the same names.
func TestExamplesComeInTraditionalChinese(t *testing.T) {
	names := Names()
	if slices.Contains(names, "zh-TW") {
		t.Fatalf("Names() = %v, the zh-TW copies are not an example of their own", names)
	}
	zh := ExamplesFor("zh-TW")
	for _, name := range names {
		data, err := fs.ReadFile(zh, name+"/graph.yaml")
		if err != nil {
			t.Fatalf("%s has no zh-TW copy: %v", name, err)
		}
		if !cjk.Match(data) {
			t.Fatalf("%s zh-TW graph.yaml has no Chinese text", name)
		}
	}
}

// Any other language, or none, gets the English originals.
func TestExamplesDefaultToEnglish(t *testing.T) {
	for _, lang := range []string{"", "en", "fr", "../zh-TW"} {
		data, err := fs.ReadFile(ExamplesFor(lang), "daily-work-schedule/graph.yaml")
		if err != nil {
			t.Fatalf("%q: %v", lang, err)
		}
		if cjk.Match(data) {
			t.Fatalf("%q got the Chinese copy", lang)
		}
	}
}
