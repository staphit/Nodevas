package runs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

func TestStartErrorCarriesBusy(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Single-run busy payload maps StartBusy -> 409 BUSY with holders, others, choices, continue.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/runs", nil)
	startFailed(c, &runs.StartError{
		Code: runs.StartBusy,
		Msg:  "someone is already working on this",
		Busy: &runnerwire.BusyResponse{
			Holders: []runnerwire.RunHolder{{
				RunID: "r1", Title: "Fix login", Agent: "claude", Model: "opus",
				Status: runnerwire.RunRunning, Since: "2026-11-18T00:00:00Z", By: "ann",
				MayStop: true, SameNode: true,
			}},
			Others:   1,
			Choices:  []string{runnerwire.OnBusyQueue, runnerwire.OnBusyBoth},
			Continue: &runnerwire.BusyContinue{RunID: "r1", Agent: "claude", Model: "opus", OpenPR: false},
		},
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", w.Code, w.Body.String())
	}
	var got struct {
		Code     string                   `json:"code"`
		Error    string                   `json:"error"`
		Holders  []runnerwire.RunHolder   `json:"holders"`
		Others   int                      `json:"others"`
		Choices  []string                 `json:"choices"`
		Continue *runnerwire.BusyContinue `json:"continue"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, w.Body.String())
	}
	if got.Code != "BUSY" || got.Others != 1 || len(got.Holders) != 1 || got.Holders[0].RunID != "r1" ||
		len(got.Choices) != 2 || got.Continue == nil || got.Continue.RunID != "r1" {
		t.Fatalf("body = %+v", got)
	}

	// Group busy payload carries repos[].
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodPost, "/api/global-runs/groups", nil)
	startFailed(c2, &runs.StartError{
		Code: runs.StartBusy,
		Msg:  "some folders are busy",
		Busy: &runnerwire.BusyResponse{
			Repos: []runnerwire.RepoBusy{{RepoID: "repo1", Holders: []runnerwire.RunHolder{{RunID: "r1"}}, Others: 0}},
		},
	})
	if w2.Code != http.StatusConflict {
		t.Fatalf("group status = %d, want 409", w2.Code)
	}
	var groupGot struct {
		Code  string                `json:"code"`
		Repos []runnerwire.RepoBusy `json:"repos"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &groupGot); err != nil || groupGot.Code != "BUSY" || len(groupGot.Repos) != 1 || groupGot.Repos[0].RepoID != "repo1" {
		t.Fatalf("group body = %+v, %v", groupGot, err)
	}

	// All newly registered routes coexist cleanly on Gin and return 501 stubs.
	router := gin.New()
	api := New(nil, nil, nil)
	api.Register(router.Group("/api"))
	stubs := [][2]string{
		{http.MethodPost, "/api/global-runs/r1/follow-up"},
		{http.MethodGet, "/api/global-runs/r1/thread"},
		{http.MethodPost, "/api/global-runs/r1/end"},
		{http.MethodPost, "/api/runs/r1/end"},
		{http.MethodPost, "/api/global-runs/groups"},
		{http.MethodGet, "/api/global-runs/groups/g1"},
		{http.MethodPost, "/api/global-runs/groups/g1/cancel"},
		{http.MethodPost, "/api/global-runs/groups/g1/resume"},
		{http.MethodPost, "/api/global-runs/groups/g1/follow-up"},
		{http.MethodGet, "/api/runs/busy"},
		{http.MethodGet, "/api/global-runs/busy"},
		{http.MethodGet, "/api/runs/r1/overlaps"},
		{http.MethodGet, "/api/global-runs/r1/overlaps"},
		{http.MethodPost, "/api/runs/contests"},
		{http.MethodPost, "/api/global-runs/contests"},
		{http.MethodGet, "/api/contests/c1"},
		{http.MethodPost, "/api/contests/c1/judge"},
		{http.MethodPost, "/api/contests/c1/pick"},
		{http.MethodPost, "/api/contests/c1/cancel"},
	}
	for _, tc := range stubs {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc[0], tc[1], nil)
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s = %d, want 501", tc[0], tc[1], rec.Code)
		}
	}
}
