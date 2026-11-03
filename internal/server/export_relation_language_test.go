package server

import (
	"net/http"
	"strings"
	"testing"
)

// The graph section of a project export is labelled in the reader's
// language: an English export read "## 關係圖" and "**起點**：…" under an
// <html lang="en">.
func TestProjectExportLabelsTheGraphInTheReadersLanguage(t *testing.T) {
	server, _ := readerLanguageFixture(t)
	for _, format := range []string{"md", "html", "txt"} {
		t.Run(format, func(t *testing.T) {
			english := askIn(t, server, "en-US,en;q=0.9", http.MethodPost, "/api/export?language=en",
				`{"format":"`+format+`","scope":"project"}`)
			if english.Code != http.StatusOK {
				t.Fatalf("export = %d %s", english.Code, english.Body)
			}
			if body := english.Body.String(); hasCJK(body) || !strings.Contains(body, "Graph") ||
				!strings.Contains(body, "Start nodes") || !strings.Contains(body, "Requires") {
				t.Fatalf("English %s export reads:\n%s", format, body)
			}
			chinese := askIn(t, server, "zh-TW,zh;q=0.9", http.MethodPost, "/api/export",
				`{"format":"`+format+`","scope":"project"}`)
			if chinese.Code != http.StatusOK {
				t.Fatalf("export = %d %s", chinese.Code, chinese.Body)
			}
			if body := chinese.Body.String(); !strings.Contains(body, "關係圖") || !strings.Contains(body, "起點") {
				t.Fatalf("Chinese %s export reads:\n%s", format, body)
			}
		})
	}
}
