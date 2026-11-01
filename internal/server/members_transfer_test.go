package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// A transfer names its two projects in the body, past the project gate,
// which sees only the request's own (?project=). Each is checked as the gate
// would check it: the selection is read from the source, written into the
// target, and taken out of the source on a cut.
func TestTransferChecksBothProjects(t *testing.T) {
	f := newMembersFixture(t)
	if response := f.setMember(t, f.bea, "cal", "viewer"); response.Code != http.StatusOK {
		t.Fatalf("restricting other: %d %s", response.Code, response.Body)
	}
	transfer := func(b browser, body string) (int, string) {
		t.Helper()
		response := f.do(t, &b, http.MethodPost, "/api/nodes/transfer?project=main", body, nil)
		return response.Code, errorCode(t, response)
	}
	newNode := func(b browser, title string) string {
		t.Helper()
		response := f.do(t, &b, http.MethodPost, "/api/nodes?project=main", `{"title":"`+title+`"}`, nil)
		var out struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil || out.ID == "" {
			t.Fatalf("create %s: %d %s", title, response.Code, response.Body)
		}
		return out.ID
	}

	// dan is no member of other: he neither copies nor cuts out of it, nor
	// pastes into it.
	for _, mode := range []string{"copy", "cut"} {
		code, reason := transfer(f.dan, `{"source":"other","target":"main","ids":["secret-plan"],"mode":"`+mode+`"}`)
		if code != http.StatusForbidden || reason != "PROJECT_ACCESS_DENIED" {
			t.Errorf("dan taking from other (%s): %d %s", mode, code, reason)
		}
	}
	dans := newNode(f.dan, "Planted by dan")
	if code, reason := transfer(f.dan, `{"source":"main","target":"other","ids":["`+dans+`"]}`); code != http.StatusForbidden || reason != "PROJECT_ACCESS_DENIED" {
		t.Errorf("dan pasting into other: %d %s", code, reason)
	}

	// cal views other: he may not cut out of it or paste into it.
	if code, reason := transfer(f.cal, `{"source":"other","target":"main","ids":["secret-plan"],"mode":"cut"}`); code != http.StatusForbidden || reason != "PROJECT_READ_ONLY" {
		t.Errorf("cal cutting from other: %d %s", code, reason)
	}
	cals := newNode(f.cal, "Planted by cal")
	if code, reason := transfer(f.cal, `{"source":"main","target":"other","ids":["`+cals+`"]}`); code != http.StatusForbidden || reason != "PROJECT_READ_ONLY" {
		t.Errorf("cal pasting into other: %d %s", code, reason)
	}

	other := f.do(t, &f.bea, http.MethodGet, "/api/graph?project=other", "", nil)
	if !strings.Contains(other.Body.String(), "secret-plan") {
		t.Errorf("the restricted project lost its node: %s", other.Body)
	}
	if strings.Contains(other.Body.String(), "Planted by") {
		t.Errorf("nodes were planted in the restricted project: %s", other.Body)
	}
	if main := f.do(t, &f.dan, http.MethodGet, "/api/graph?project=main", "", nil); strings.Contains(main.Body.String(), "Secret plan") {
		t.Errorf("the restricted node was copied where dan reads it: %s", main.Body)
	}

	// What each may do still works: cal reads other, so he copies out of it
	// into main, which he may change; bea owns other and cuts from it.
	if code, reason := transfer(f.cal, `{"source":"other","target":"main","ids":["secret-plan"]}`); code != http.StatusOK {
		t.Errorf("cal copying from other: %d %s", code, reason)
	}
	if code, reason := transfer(f.bea, `{"source":"other","target":"main","ids":["secret-plan"],"mode":"cut"}`); code != http.StatusOK {
		t.Errorf("bea cutting from other: %d %s", code, reason)
	}
}
