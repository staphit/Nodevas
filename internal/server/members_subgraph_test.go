package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// A viewer may preview a draft (the dry run changes nothing) but may neither
// create it nor undo or redo a batch; a non-member reaches none of it, and an
// editor may not undo somebody else's batch.
func TestSubgraphCheckIsOpenToViewersAndTheRestIsNot(t *testing.T) {
	f := newMembersFixture(t)
	if response := f.setMember(t, f.bea, "cal", "viewer"); response.Code != http.StatusOK {
		t.Fatalf("bea adding cal: %d %s", response.Code, response.Body)
	}
	draft := `{"nodes":[{"key":"a","title":"A"},{"key":"b","title":"B","dependsOn":["a"]}],"batch":{"via":"copilot-draft:d1"}}`

	response := f.do(t, &f.bea, http.MethodPost, "/api/graph/subgraph?project=other", draft, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("bea's commit: %d %s", response.Code, response.Body)
	}
	var committed struct {
		BatchID  string `json:"batchId"`
		Undoable bool   `json:"undoable"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &committed); err != nil || committed.BatchID == "" || !committed.Undoable {
		t.Fatalf("commit body = %s (%v)", response.Body, err)
	}
	undo := "/api/graph/batches/" + committed.BatchID + "/undo?project=other"
	redo := "/api/graph/batches/" + committed.BatchID + "/redo?project=other"

	// cal, a viewer: checks, never writes.
	response = f.do(t, &f.cal, http.MethodPost, "/api/graph/subgraph/check?project=other", draft, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("cal's check: %d %s", response.Code, response.Body)
	}
	for _, path := range []string{"/api/graph/subgraph?project=other", undo, redo} {
		response = f.do(t, &f.cal, http.MethodPost, path, draft, nil)
		if response.Code != http.StatusForbidden || errorCode(t, response) != "PROJECT_READ_ONLY" {
			t.Fatalf("cal posting %s: %d %s", path, response.Code, response.Body)
		}
	}

	// dan is not a member: not even the dry run.
	response = f.do(t, &f.dan, http.MethodPost, "/api/graph/subgraph/check?project=other", draft, nil)
	if response.Code != http.StatusForbidden || errorCode(t, response) != "PROJECT_ACCESS_DENIED" {
		t.Fatalf("dan's check: %d %s", response.Code, response.Body)
	}

	// cal as an editor may write, but bea's batch is not his to undo.
	if response := f.setMember(t, f.bea, "cal", "editor"); response.Code != http.StatusOK {
		t.Fatalf("promoting cal: %d %s", response.Code, response.Body)
	}
	response = f.do(t, &f.cal, http.MethodPost, undo, "", nil)
	if response.Code != http.StatusNotFound || errorCode(t, response) != "BATCH_NOT_FOUND" {
		t.Fatalf("cal undoing bea's batch: %d %s", response.Code, response.Body)
	}

	// bea undoes it; ann, the administrator, may redo it.
	response = f.do(t, &f.bea, http.MethodPost, undo, "", nil)
	var undone struct {
		Trashed []string `json:"trashed"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &undone); response.Code != http.StatusOK || err != nil || len(undone.Trashed) != 2 {
		t.Fatalf("bea's undo: %d %s", response.Code, response.Body)
	}
	response = f.do(t, &f.ann, http.MethodPost, redo, "", nil)
	var redone struct {
		Restored []string `json:"restored"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &redone); response.Code != http.StatusOK || err != nil || len(redone.Restored) != 2 {
		t.Fatalf("ann's redo: %d %s", response.Code, response.Body)
	}
}
