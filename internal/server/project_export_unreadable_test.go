package server

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

// Exporting a project whose graph.yaml does not parse is the reader's file
// to fix, not the server failing: 422 with the same sentence the board
// shows, naming the line, in the reader's language.
func TestProjectExportOfAnUnreadableGraphSaysWhichLine(t *testing.T) {
	for _, language := range []string{"en-US,en;q=0.9", "zh-TW,zh;q=0.9"} {
		t.Run(language, func(t *testing.T) {
			server, pm := readerLanguageFixture(t)
			if err := os.WriteFile(pm.Store().GraphPath(),
				[]byte("version: 1\nnodes:\n  - id: [broken\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			response := askIn(t, server, language, http.MethodGet, "/api/projects/export?project=main", "")
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("export = %d %s", response.Code, response.Body)
			}
			var refusal struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &refusal); err != nil {
				t.Fatal(err)
			}
			chinese := strings.HasPrefix(language, "zh")
			line := "Line 3 "
			if chinese {
				line = "第 3 行"
			}
			if !strings.Contains(refusal.Error, "graph.yaml") || !strings.Contains(refusal.Error, line) {
				t.Fatalf("%q does not name the file and line", refusal.Error)
			}
			if hasCJK(refusal.Error) != chinese {
				t.Fatalf("export refusal for %s reads %q", language, refusal.Error)
			}
		})
	}
}
