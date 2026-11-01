package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// Over the socket too: subscribing to a restricted project and to one that
// does not exist get the same refusal, for a member who may not read it.
func TestSubscribeDoesNotTellRestrictedNamesFromMissingOnes(t *testing.T) {
	f := newMembersFixture(t)
	if response := f.setMember(t, f.bea, "cal", "viewer"); response.Code != http.StatusOK {
		t.Fatalf("restrict other: %d %s", response.Code, response.Body)
	}
	live := httptest.NewServer(f.handler)
	t.Cleanup(live.Close)

	restricted := f.dialAs(t, live, f.dan)
	restricted.send(map[string]any{"type": "subscribe", "project": "other"})
	restricted.await("access-denied", nil)
	missing := f.dialAs(t, live, f.dan)
	missing.send(map[string]any{"type": "subscribe", "project": "nosuch"})
	missing.await("access-denied", nil)

	// A project dan may read is watched as before.
	open := f.dialAs(t, live, f.dan)
	open.send(map[string]any{"type": "subscribe", "project": "main"})
	open.await("presence", nil)

	// An administrator asking for a missing project stays in the catch-all
	// room, as before.
	ann := f.dialAs(t, live, f.ann)
	ann.send(map[string]any{"type": "subscribe", "project": "nosuch"})
	f.server.hub.Broadcast("marker", "")
	if seenBefore(t, ann, "access-denied", "marker") {
		t.Fatal("an administrator was refused a missing project")
	}
}

// The catalog's workspace entry counts only the projects the caller may
// read: the full count would tell a member that restricted projects exist,
// how many, and when one is added or removed.
func TestWorkspaceCountsOnlyReadableProjects(t *testing.T) {
	f := newMembersFixture(t)
	if response := f.setMember(t, f.bea, "cal", "viewer"); response.Code != http.StatusOK {
		t.Fatalf("restrict other: %d %s", response.Code, response.Body)
	}
	catalog := func(b browser) (listed, counted int) {
		t.Helper()
		response := f.do(t, &b, http.MethodGet, "/api/projects", "", nil)
		var out struct {
			Workspaces []struct {
				Active   bool `json:"active"`
				Projects int  `json:"projects"`
			} `json:"workspaces"`
			Projects []struct {
				IsFolder bool `json:"isFolder"`
			} `json:"projects"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil || response.Code != http.StatusOK {
			t.Fatalf("GET /api/projects: %d %s", response.Code, response.Body)
		}
		for _, p := range out.Projects {
			if !p.IsFolder {
				listed++
			}
		}
		if len(out.Workspaces) != 1 || !out.Workspaces[0].Active {
			t.Fatalf("workspaces = %+v", out.Workspaces)
		}
		return listed, out.Workspaces[0].Projects
	}
	annListed, annCounted := catalog(f.ann)
	danListed, danCounted := catalog(f.dan)
	if annCounted != annListed || danCounted != danListed || danListed != annListed-1 {
		t.Fatalf("ann lists %d and counts %d; dan lists %d and counts %d",
			annListed, annCounted, danListed, danCounted)
	}
}
