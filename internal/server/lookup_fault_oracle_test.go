package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A lookup that goes through a file inside a restricted project is an
// answer about the name, not a disk fault: a member who may not read the
// project gets the same reply as for a name that does not exist, so the 503
// kept for real disk faults cannot tell which projects, or which files in
// them, are there.
func TestAPathThroughAFileIsNotAnOracle(t *testing.T) {
	f := newMembersFixture(t)
	if response := f.setMember(t, f.bea, "cal", "viewer"); response.Code != http.StatusOK {
		t.Fatalf("restrict other: %d %s", response.Code, response.Body)
	}
	for _, pair := range [][2]string{
		{"other/graph.yaml", "nosuch/graph.yaml"},
		{"other/graph.yaml/deeper", "nosuch/graph.yaml/deeper"},
	} {
		there := f.do(t, &f.dan, http.MethodGet, "/api/graph?project="+pair[0], "", nil)
		missing := f.do(t, &f.dan, http.MethodGet, "/api/graph?project="+pair[1], "", nil)
		if there.Code != missing.Code || errorCode(t, there) != errorCode(t, missing) {
			t.Errorf("%s: %d %s; %s: %d %s; want the same answer",
				pair[0], there.Code, there.Body, pair[1], missing.Code, missing.Body)
		}
	}

	live := httptest.NewServer(f.handler)
	t.Cleanup(live.Close)
	for _, name := range []string{"other/graph.yaml", "nosuch/graph.yaml"} {
		dan := f.dialAs(t, live, f.dan)
		dan.send(map[string]any{"type": "subscribe", "project": name})
		dan.await("access-denied", nil)
	}
}
