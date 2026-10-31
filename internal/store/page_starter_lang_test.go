package store

import (
	"strings"
	"testing"
)

// A new HTML page says the language of its title, not always Chinese.
func TestNewHTMLPageNamesItsTitlesLanguage(t *testing.T) {
	for title, want := range map[string]string{
		"Meeting notes": `<html lang="en">`,
		"會議記錄":          `<html lang="zh-Hant">`,
	} {
		if got := starterPageContent(title, PageFormatHTML); !strings.Contains(got, want) {
			t.Errorf("page %q starts:\n%s", title, got)
		}
	}
}
