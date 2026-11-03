package server

import (
	"net/http"
	"strings"
	"testing"
)

// An English project exported to HTML is marked English: lang was
// hard-coded to zh-Hant.
func TestHTMLExportNamesTheDocumentsLanguage(t *testing.T) {
	server, _ := readerLanguageFixture(t)
	response := askIn(t, server, "en-US,en;q=0.9", http.MethodPost, "/api/export?language=en",
		`{"format":"html","scope":"project"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("export = %d %s", response.Code, response.Body)
	}
	if body := response.Body.String(); !strings.Contains(body, `<html lang="en">`) {
		t.Fatalf("English export starts:\n%s", body[:min(len(body), 120)])
	}
}
