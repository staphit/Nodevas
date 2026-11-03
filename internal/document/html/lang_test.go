package html

import (
	"strings"
	"testing"

	"nodevas/internal/document"
)

// The page says what language it is written in, so a screen reader picks
// the right voice and a spellchecker the right dictionary: English for an
// English document, Traditional Chinese for a Chinese one, whatever the
// exporting reader's interface is.
func TestRenderHTMLNamesTheDocumentsLanguage(t *testing.T) {
	for _, tc := range []struct {
		name, markdown, want string
	}{
		{"English", "# Release plan\n\nShip the build on Friday, then tell the team.\n", `<html lang="en">`},
		{"Chinese", "# 發布計畫\n\n週五發布版本，然後通知團隊。\n", `<html lang="zh-Hant">`},
		{"Chinese with code", "# 部署\n\n執行 `go build ./...` 之後再部署到伺服器。\n\n```\ngo test ./internal/store/...\n```\n", `<html lang="zh-Hant">`},
		{"English naming a Chinese word", "# Glossary\n\nThe board is called 看板 in the Chinese interface; everything else is in English.\n", `<html lang="en">`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := RenderHTML(document.Parse(tc.markdown), document.Options{Title: "x"})
			if !strings.Contains(out, tc.want) {
				t.Fatalf("want %s in:\n%s", tc.want, out[:min(len(out), 80)])
			}
		})
	}
}
