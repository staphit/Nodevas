package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// A member who may not read a restricted project cannot learn its name: not
// from the explorer's order, not from the events every socket gets when an
// administrator copies or moves it, and not from how a request naming it is
// refused compared with one naming nothing.
func TestRestrictedProjectNamesStayHidden(t *testing.T) {
	f := newMembersFixture(t)
	if response := f.setMember(t, f.bea, "cal", "viewer"); response.Code != http.StatusOK {
		t.Fatalf("restrict other: %d %s", response.Code, response.Body)
	}

	// The explorer order, which an administrator saves for the whole tree.
	if response := f.do(t, &f.ann, http.MethodPut, "/api/workspaces/order",
		`{"projectOrder":["other","main","later"]}`, nil); response.Code != http.StatusOK {
		t.Fatalf("save the order: %d %s", response.Code, response.Body)
	}
	order := f.do(t, &f.dan, http.MethodGet, "/api/workspaces/order", "", nil)
	if order.Code != http.StatusOK || strings.Contains(order.Body.String(), `"other"`) ||
		!strings.Contains(order.Body.String(), `"main"`) || !strings.Contains(order.Body.String(), `"later"`) {
		t.Errorf("dan's order: %d %s", order.Code, order.Body)
	}
	for _, reader := range []browser{f.ann, f.cal} {
		if response := f.do(t, &reader, http.MethodGet, "/api/workspaces/order", "", nil); !strings.Contains(response.Body.String(), `"other"`) {
			t.Errorf("a reader of other lost it from the order: %s", response.Body)
		}
	}

	// A refusal of a restricted project reads like one of a missing project.
	restricted := f.do(t, &f.dan, http.MethodGet, "/api/graph?project=other", "", nil)
	missing := f.do(t, &f.dan, http.MethodGet, "/api/graph?project=nosuch", "", nil)
	if restricted.Code != missing.Code || errorCode(t, restricted) != errorCode(t, missing) {
		t.Errorf("restricted %d %s, missing %d %s", restricted.Code, restricted.Body, missing.Code, missing.Body)
	}
	transferRestricted := f.do(t, &f.dan, http.MethodPost, "/api/nodes/transfer?project=main",
		`{"source":"other","target":"main","ids":["secret-plan"]}`, nil)
	transferMissing := f.do(t, &f.dan, http.MethodPost, "/api/nodes/transfer?project=main",
		`{"source":"nosuch","target":"main","ids":["secret-plan"]}`, nil)
	if transferRestricted.Code != transferMissing.Code || errorCode(t, transferRestricted) != errorCode(t, transferMissing) {
		t.Errorf("transfer: restricted %d %s, missing %d %s", transferRestricted.Code, transferRestricted.Body,
			transferMissing.Code, transferMissing.Body)
	}
	// An administrator is still told the project does not exist.
	if response := f.do(t, &f.ann, http.MethodGet, "/api/graph?project=nosuch", "", nil); response.Code != http.StatusBadRequest {
		t.Errorf("ann asking for a missing project: %d %s", response.Code, response.Body)
	}

	// Every socket hears that the catalog changed, and nothing more.
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/ws", f.server.pm.Hub().Handle)
	live := httptest.NewServer(router)
	t.Cleanup(live.Close)
	peer := dialHub(t, live)
	peer.await("hello", nil)
	if response := f.do(t, &f.ann, http.MethodPost, "/api/projects/copy",
		`{"name":"other","newName":"acquisition-target"}`, nil); response.Code != http.StatusOK {
		t.Fatalf("copy other: %d %s", response.Code, response.Body)
	}
	if response := f.do(t, &f.ann, http.MethodPost, "/api/projects/move",
		`{"name":"acquisition-target","newName":"merger-talks"}`, nil); response.Code != http.StatusOK {
		t.Fatalf("move the copy: %d %s", response.Code, response.Body)
	}
	if code := f.code(t, f.dan, http.MethodGet, "/api/graph?project=merger-talks", ""); code != http.StatusForbidden {
		t.Fatalf("dan reading the restricted copy: %d", code)
	}
	f.server.pm.Hub().Broadcast("marker", "")
	timeout := time.After(5 * time.Second)
	for done := false; !done; {
		select {
		case event, ok := <-peer.events:
			if !ok {
				t.Fatal("connection closed")
			}
			if strings.Contains(event.ID, "acquisition") || strings.Contains(event.ID, "merger") {
				t.Errorf("a socket heard %q with id %q", event.Type, event.ID)
			}
			done = event.Type == "marker"
		case <-timeout:
			t.Fatal("no marker")
		}
	}
}
