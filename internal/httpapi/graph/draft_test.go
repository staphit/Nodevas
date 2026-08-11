package graph

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// draftContext is ginContext plus the :id the draft routes take from the path.
func draftContext(response http.ResponseWriter, request *http.Request, id string) *gin.Context {
	c := ginContext(response, request)
	c.Params = gin.Params{{Key: "id", Value: id}}
	return c
}

func TestDraftRoundTripsThroughCreateReadAndDelete(t *testing.T) {
	api, _ := graphTestAPI(t)

	request := httptest.NewRequest(http.MethodPut, "/api/drafts/alpha",
		strings.NewReader(`{"content":"half a sentence"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.putDraft(draftContext(response, request, "alpha"))
	if response.Code != http.StatusOK {
		t.Fatalf("save status = %d, body = %s", response.Code, response.Body)
	}

	readRequest := httptest.NewRequest(http.MethodGet, "/api/drafts/alpha", nil)
	readResponse := httptest.NewRecorder()
	api.getDraft(draftContext(readResponse, readRequest, "alpha"))
	if readResponse.Code != http.StatusOK {
		t.Fatalf("read status = %d, body = %s", readResponse.Code, readResponse.Body)
	}
	var payload struct {
		Exists  bool   `json:"exists"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(readResponse.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Exists || payload.Content != "half a sentence" {
		t.Fatalf("draft = %+v", payload)
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/drafts/alpha", nil)
	deleteResponse := httptest.NewRecorder()
	api.deleteDraft(draftContext(deleteResponse, deleteRequest, "alpha"))
	if deleteResponse.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", deleteResponse.Code, deleteResponse.Body)
	}

	goneResponse := httptest.NewRecorder()
	api.getDraft(draftContext(goneResponse,
		httptest.NewRequest(http.MethodGet, "/api/drafts/alpha", nil), "alpha"))
	if goneResponse.Code != http.StatusOK {
		t.Fatalf("read-after-delete status = %d, body = %s", goneResponse.Code, goneResponse.Body)
	}
	payload.Exists = true
	if err := json.Unmarshal(goneResponse.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Exists {
		t.Fatal("the deleted draft still reports itself as existing")
	}
}

// A draft nobody ever saved is the normal case on first open, so it is a 200
// saying "no", not an error the editor has to special-case.
func TestReadingADraftThatWasNeverSavedIsNotAnError(t *testing.T) {
	api, _ := graphTestAPI(t)

	request := httptest.NewRequest(http.MethodGet, "/api/drafts/alpha", nil)
	response := httptest.NewRecorder()
	api.getDraft(draftContext(response, request, "alpha"))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	var payload struct {
		Exists bool `json:"exists"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Exists {
		t.Fatal("a draft that was never written reports as existing")
	}
}

func TestDraftIdentifiersThatAreNotNodeIDsAreRejected(t *testing.T) {
	api, _ := graphTestAPI(t)

	request := httptest.NewRequest(http.MethodPut, "/api/drafts/..%2F..%2Fescape",
		strings.NewReader(`{"content":"x"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.putDraft(draftContext(response, request, "../../escape"))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body)
	}
}
