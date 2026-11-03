package server

import (
	"net/http"
	"strings"
	"testing"
)

// Other spellings of a restricted project's folder (a trailing dot or
// space, which Windows drops; another case, which Windows and macOS ignore)
// are the same project to its members list: a non-member reads and writes
// nothing through them.
func TestProjectPathAliasesShareMembership(t *testing.T) {
	f := newMembersFixture(t)
	if response := f.setMember(t, f.bea, "cal", "viewer"); response.Code != http.StatusOK {
		t.Fatalf("restrict other: %d %s", response.Code, response.Body)
	}
	for _, alias := range []string{"other.", "other..", "OTHER", "Other", "other%20", "other.%20"} {
		for _, path := range []string{"/api/graph?project=" + alias, "/api/nodes/secret-plan?project=" + alias} {
			response := f.do(t, &f.dan, http.MethodGet, path, "", nil)
			if response.Code == http.StatusOK || strings.Contains(response.Body.String(), "Secret plan") {
				t.Errorf("dan GET %s: %d %s", path, response.Code, response.Body)
			}
		}
		response := f.do(t, &f.dan, http.MethodPost, "/api/projects/open", `{"name":"`+strings.ReplaceAll(alias, "%20", " ")+`"}`, nil)
		if response.Code == http.StatusOK {
			t.Errorf("dan opening %q: %d %s", alias, response.Code, response.Body)
		}
	}
	// cal, a member, still reads it by its name.
	if code := f.code(t, f.cal, http.MethodGet, "/api/graph?project=other", ""); code != http.StatusOK {
		t.Fatalf("cal reading other: %d", code)
	}
}
