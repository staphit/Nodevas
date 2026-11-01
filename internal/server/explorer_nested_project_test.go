package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// A project nested in another's folder is restricted on its own: reading the
// parent, whose Files tab walks that folder, must not read the child.
func TestExplorerHidesNestedRestrictedProject(t *testing.T) {
	f := newMembersFixture(t)
	if response := f.do(t, &f.ann, http.MethodPost, "/api/projects/open", `{"name":"main/payroll","create":true}`, nil); response.Code != http.StatusOK {
		t.Fatalf("create main/payroll: %d %s", response.Code, response.Body)
	}
	response := f.do(t, &f.ann, http.MethodPost, "/api/nodes?project=main/payroll", `{"title":"Salaries","body":"SECRET-SALARY-TABLE"}`, nil)
	var node struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &node); err != nil || node.ID == "" {
		t.Fatalf("create the node: %d %s", response.Code, response.Body)
	}
	if response := f.do(t, &f.ann, http.MethodPut, "/api/projects/members",
		`{"project":"main/payroll","user":"cal","role":"viewer"}`, nil); response.Code != http.StatusOK {
		t.Fatalf("restrict main/payroll: %d %s", response.Code, response.Body)
	}
	if code := f.code(t, f.dan, http.MethodGet, "/api/graph?project=main/payroll", ""); code != http.StatusForbidden {
		t.Fatalf("dan reading main/payroll: %d", code)
	}

	listing := f.do(t, &f.dan, http.MethodGet, "/api/explorer/workspace/files?project=main", "", nil)
	if listing.Code != http.StatusOK {
		t.Fatalf("dan listing main: %d %s", listing.Code, listing.Body)
	}
	if strings.Contains(listing.Body.String(), "payroll") {
		t.Errorf("main's listing shows the restricted child: %s", listing.Body)
	}
	for _, path := range []string{"payroll/nodes/" + node.ID + ".md", "payroll/graph.yaml"} {
		response := f.do(t, &f.dan, http.MethodGet, "/api/explorer/workspace/file?project=main&path="+path, "", nil)
		if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "SECRET-SALARY-TABLE") ||
			strings.Contains(response.Body.String(), "Salaries") {
			t.Errorf("dan reading %s through main: %d %s", path, response.Code, response.Body)
		}
	}

	// cal, a member, reads the child through its own Files tab.
	own := f.do(t, &f.cal, http.MethodGet, "/api/explorer/workspace/file?project=main/payroll&path=nodes/"+node.ID+".md", "", nil)
	if own.Code != http.StatusOK || !strings.Contains(own.Body.String(), "SECRET-SALARY-TABLE") {
		t.Errorf("cal reading main/payroll's own file: %d %s", own.Code, own.Body)
	}
}
