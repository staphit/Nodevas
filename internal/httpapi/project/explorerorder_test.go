package project

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	projectdomain "nodevas/internal/project"
	"nodevas/internal/realtime"
	"nodevas/internal/store"
)

func orderTestManager(t *testing.T) *projectdomain.ProjectManager {
	t.Helper()
	pm, err := projectdomain.NewManagerAt(t.TempDir(), realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pm.Close)
	return pm
}

func putProjectOrder(t *testing.T, api *API, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(
		http.MethodPut, "/api/workspaces/order", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.PutProjectOrder(securityTestContext(response, request))
	return response
}

func TestGetProjectOrderEmptyThenStored(t *testing.T) {
	pm := orderTestManager(t)
	api := New(pm)

	request := httptest.NewRequest(http.MethodGet, "/api/workspaces/order", nil)
	response := httptest.NewRecorder()
	api.GetProjectOrder(securityTestContext(response, request))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	// The client sorts by this list, so an absent order must arrive as an empty
	// array rather than null.
	if got := response.Body.String(); !bytes.Contains([]byte(got), []byte(`"projectOrder":[]`)) {
		t.Fatalf("empty order body = %s", got)
	}

	if response := putProjectOrder(t,
		api, `{"projectOrder":["Story","Story/sub","Game mechanic"]}`); response.Code != http.StatusOK {
		t.Fatalf("put status = %d, body = %s", response.Code, response.Body.String())
	}
	if stored := store.LoadProjectOrder(pm.Workspace()); len(stored) != 3 ||
		stored[2] != "Game mechanic" {
		t.Fatalf("stored = %v", stored)
	}

	response = httptest.NewRecorder()
	api.GetProjectOrder(securityTestContext(response, request))
	var payload struct {
		ProjectOrder []string `json:"projectOrder"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.ProjectOrder) != 3 || payload.ProjectOrder[0] != "Story" {
		t.Fatalf("read back = %v", payload.ProjectOrder)
	}
}

func TestPutProjectOrderRejectsBadEntries(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "duplicate", body: `{"projectOrder":["Story","Story"]}`},
		{name: "duplicate ignoring case", body: `{"projectOrder":["Story","story"]}`},
		{name: "traversal", body: `{"projectOrder":["../secret"]}`},
		{name: "absolute", body: `{"projectOrder":["/etc"]}`},
		{name: "empty name", body: `{"projectOrder":[""]}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			pm := orderTestManager(t)
			response := putProjectOrder(t, New(pm), testCase.body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if stored := store.LoadProjectOrder(pm.Workspace()); len(stored) != 0 {
				t.Fatalf("rejected write still stored %v", stored)
			}
		})
	}
}

// A project the workspace does not hold is accepted: the order outlives the
// projects it names, and the client is what resolves it against the real tree.
func TestPutProjectOrderAcceptsUnknownProject(t *testing.T) {
	pm := orderTestManager(t)
	response := putProjectOrder(t, New(pm), `{"projectOrder":["never-existed"]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}
