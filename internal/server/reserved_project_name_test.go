package server

import (
	"encoding/json"
	"net/http"
	"runtime"
	"strings"
	"testing"
)

// Windows keeps CON, NUL, AUX... for devices, so no folder can have those
// names. A project named one was refused as "unsafe project path: <absolute
// path> is outside project root", which is neither the reason nor something
// the reader should see; the refusal says the name is reserved instead.
func TestWindowsReservedProjectNamesAreRefusedAsReserved(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("device names are only reserved on Windows")
	}
	for _, language := range []struct{ header, reserved string }{
		{"en-US,en;q=0.9", "reserved"},
		{"zh-TW,zh;q=0.9", "保留"},
	} {
		t.Run(language.header, func(t *testing.T) {
			server, pm := readerLanguageFixture(t)
			workspace := pm.Workspace()
			for _, request := range []struct{ path, body, name string }{
				{"/api/projects/open", `{"name":"CON","create":true}`, "CON"},
				{"/api/projects/open", `{"name":"nul","create":true}`, "nul"},
				{"/api/projects/folder", `{"name":"aux"}`, "aux"},
				{"/api/projects/copy", `{"name":"other","newName":"PRN"}`, "PRN"},
				{"/api/projects/move", `{"name":"other","newName":"com1"}`, "com1"},
			} {
				response := askIn(t, server, language.header, http.MethodPost, request.path, request.body)
				if response.Code != http.StatusBadRequest {
					t.Fatalf("%s %s = %d %s", request.path, request.body, response.Code, response.Body)
				}
				var refusal struct {
					Error string `json:"error"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &refusal); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(refusal.Error, request.name) ||
					!strings.Contains(refusal.Error, language.reserved) {
					t.Errorf("%s %s refused with %q: want the name and why it is refused",
						request.path, request.body, refusal.Error)
				}
				if strings.Contains(refusal.Error, workspace) || strings.Contains(refusal.Error, `:\`) {
					t.Errorf("%s %s refusal shows a local path: %q", request.path, request.body, refusal.Error)
				}
			}
		})
	}
}
