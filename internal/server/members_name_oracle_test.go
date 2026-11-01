package server

import (
	"net/http"
	"testing"
)

// The routes that belong to an account rather than a project still read
// ?project=. Naming a restricted project there must be answered as naming
// one that does not exist, for a member who may not read it and for a
// client that is not signed in at all.
func TestAccountRoutesDoNotTellRestrictedNamesFromMissingOnes(t *testing.T) {
	f := newMembersFixture(t)
	if response := f.setMember(t, f.bea, "cal", "viewer"); response.Code != http.StatusOK {
		t.Fatalf("restrict other: %d %s", response.Code, response.Body)
	}

	for _, path := range []string{"/api/runs/budget", "/api/analytics/overview", "/api/copilot/conversations"} {
		restricted := f.do(t, &f.dan, http.MethodGet, path+"?project=other", "", nil)
		missing := f.do(t, &f.dan, http.MethodGet, path+"?project=nosuch", "", nil)
		if restricted.Code != missing.Code || errorCode(t, restricted) != errorCode(t, missing) {
			t.Errorf("%s: restricted %d %s, missing %d %s", path,
				restricted.Code, restricted.Body, missing.Code, missing.Body)
		}
		// Naming a project the member may read keeps working. (The copilot
		// is not set up in this fixture: its routes answer 404 there.)
		if response := f.do(t, &f.dan, http.MethodGet, path+"?project=main", "", nil); response.Code == http.StatusForbidden {
			t.Errorf("%s for main: %d %s", path, response.Code, response.Body)
		}
	}

	// Signed out: the status route is how a browser learns it must sign in.
	restricted := f.do(t, nil, http.MethodGet, "/api/auth/status?project=other", "", nil)
	missing := f.do(t, nil, http.MethodGet, "/api/auth/status?project=nosuch", "", nil)
	if restricted.Code != missing.Code || restricted.Body.String() != missing.Body.String() {
		t.Errorf("signed out: restricted %d %s, missing %d %s",
			restricted.Code, restricted.Body, missing.Code, missing.Body)
	}

	// A browser still showing a project it lost sends its name on every
	// request; its account routes keep working.
	header := map[string]string{"X-Nodevas-Project": "other"}
	if response := f.do(t, &f.dan, http.MethodGet, "/api/auth/totp", "", header); response.Code != http.StatusOK {
		t.Errorf("dan's two-factor settings with a stale project: %d %s", response.Code, response.Body)
	}
}
