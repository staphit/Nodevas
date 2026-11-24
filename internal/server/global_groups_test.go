package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"nodevas/internal/audit"
	"nodevas/internal/realtime"
	"nodevas/internal/runnerwire"
)

func globalGroupHello(maxConcurrent int) runnerwire.Envelope {
	env := globalRunsHello()
	env.Hello.MaxConcurrent = maxConcurrent
	env.Hello.Repos = []runnerwire.Repo{
		{ID: "repo-1", Name: "api", Remote: "https://github.com/org/api.git"},
		{ID: "repo-2", Name: "web", Remote: "https://github.com/org/web.git"},
		{ID: "repo-3", Name: "worker", Remote: "https://github.com/org/worker.git"},
	}
	return env
}

func (f *runsFixture) groupBody(edit func(*runnerwire.CreateGlobalGroupRequest)) string {
	body := runnerwire.CreateGlobalGroupRequest{
		RunnerID: f.runner.Runner.ID,
		RepoIDs:  []string{"repo-1", "repo-2", "repo-3"},
		Agent:    "claude",
		Base:     "main",
		Task:     "Bump deps\n\nSecret note: upgrade internal auth lib.",
		OpenPR:   true,
	}
	if edit != nil {
		edit(&body)
	}
	raw, _ := json.Marshal(body)
	return string(raw)
}

func TestGlobalGroupsLifecycleAndSecurity(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, globalGroupHello(1))
	pingPong(t, conn)
	bea := dialHubAs(t, f.liveURL, f.bea)
	cal := dialHubAs(t, f.liveURL, f.cal)

	// POST /api/global-runs/groups -> 201 {group, runs}
	resp := f.do(t, &f.bea, http.MethodPost, "/api/global-runs/groups", f.groupBody(nil), nil)
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST /api/global-runs/groups = %d %s", resp.Code, resp.Body)
	}
	if cache := resp.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("Cache-Control = %q", cache)
	}
	var created runnerwire.GlobalGroupResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Group.ID == "" || created.Group.RepoCount != 3 || created.Group.NoPR || len(created.Runs) != 3 {
		t.Fatalf("created = %+v", created)
	}
	if created.Runs[0].Status != runnerwire.RunOffered || created.Runs[1].Status != runnerwire.RunQueued ||
		created.Runs[2].Status != runnerwire.RunQueued {
		t.Fatalf("statuses = [%s, %s, %s]", created.Runs[0].Status, created.Runs[1].Status, created.Runs[2].Status)
	}
	for _, r := range created.Runs {
		if r.GroupID != created.Group.ID {
			t.Fatalf("run %s missing groupId", r.ID)
		}
	}
	job0 := expectOffer(t, conn, created.Runs[0].ID)
	if !strings.Contains(job0.Prompt, "This is one of 3 repositories changing together: api, web, worker") {
		t.Fatalf("job0 prompt missing sibling list:\n%s", job0.Prompt)
	}
	bea.await(runnerwire.GlobalRunsChangedEvent, func(e realtime.Event) bool { return e.ID == created.Runs[0].ID })
	awaitNoneOf(t, cal, 200*time.Millisecond, runnerwire.GlobalRunsChangedEvent, runnerwire.RunsChangedEvent)

	// GET /api/global-runs/groups/:id
	getResp := f.do(t, &f.bea, http.MethodGet, "/api/global-runs/groups/"+created.Group.ID, "", nil)
	if getResp.Code != http.StatusOK {
		t.Fatalf("GET group = %d %s", getResp.Code, getResp.Body)
	}
	var fetched runnerwire.GlobalGroupResponse
	if err := json.Unmarshal(getResp.Body.Bytes(), &fetched); err != nil || fetched.Group.ID != created.Group.ID || len(fetched.Runs) != 3 {
		t.Fatalf("fetched = %+v, %v", fetched, err)
	}

	// Other user cannot read, cancel, or resume the group (404), nor start on bea's runner.
	for _, tc := range [][2]string{
		{http.MethodGet, "/api/global-runs/groups/" + created.Group.ID},
		{http.MethodPost, "/api/global-runs/groups/" + created.Group.ID + "/cancel"},
		{http.MethodPost, "/api/global-runs/groups/" + created.Group.ID + "/resume"},
		{http.MethodPost, "/api/global-runs/groups"},
	} {
		body := ""
		if tc[0] == http.MethodPost {
			body = "{}"
			if tc[1] == "/api/global-runs/groups" {
				body = f.groupBody(nil)
			}
		}
		if res := f.do(t, &f.cal, tc[0], tc[1], body, nil); res.Code != http.StatusNotFound {
			t.Fatalf("cal %s %s = %d %s, want 404", tc[0], tc[1], res.Code, res.Body)
		}
	}

	// Agents are refused on every group route (personOnly -> 403 PERMISSION_DENIED).
	for _, tc := range [][2]string{
		{http.MethodPost, "/api/global-runs/groups"},
		{http.MethodGet, "/api/global-runs/groups/" + created.Group.ID},
		{http.MethodPost, "/api/global-runs/groups/" + created.Group.ID + "/cancel"},
		{http.MethodPost, "/api/global-runs/groups/" + created.Group.ID + "/resume"},
	} {
		body := ""
		if tc[0] == http.MethodPost {
			body = "{}"
		}
		if res := f.doAsAgent(t, &f.bea, tc[0], tc[1], body); res.Code != http.StatusForbidden ||
			!strings.Contains(res.Body.String(), "PERMISSION_DENIED") {
			t.Fatalf("agent %s %s = %d %s", tc[0], tc[1], res.Code, res.Body)
		}
	}

	// Usage limit holds the group; POST .../resume clears the hold and offers the next member.
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeAccept, Accept: &runnerwire.Accept{
		RunID: created.Runs[0].ID, Base: "main", Branch: "agent/bump-deps-0",
	}})
	f.waitGlobal(t, created.Runs[0].ID, "running", func(r runnerwire.Run) bool { return r.Status == runnerwire.RunRunning })
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: created.Runs[0].ID, Result: &runnerwire.RunResult{
		Status:      runnerwire.RunFailed,
		FailureKind: runnerwire.FailureQuota,
		Error:       "usage limit",
		ResetAt:     time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}})
	f.waitGlobal(t, created.Runs[0].ID, "failed", func(r runnerwire.Run) bool { return r.Status == runnerwire.RunFailed })

	heldResp := f.do(t, &f.bea, http.MethodGet, "/api/global-runs/groups/"+created.Group.ID, "", nil)
	var held runnerwire.GlobalGroupResponse
	_ = json.Unmarshal(heldResp.Body.Bytes(), &held)
	if held.Group.HeldUntil == "" {
		t.Fatalf("group not held after quota failure: %+v", held.Group)
	}
	if held.Runs[1].Status != runnerwire.RunQueued {
		t.Fatalf("run[1] status while held = %s, want queued", held.Runs[1].Status)
	}

	resumeResp := f.do(t, &f.bea, http.MethodPost, "/api/global-runs/groups/"+created.Group.ID+"/resume", "{}", nil)
	if resumeResp.Code != http.StatusOK {
		t.Fatalf("POST resume = %d %s", resumeResp.Code, resumeResp.Body)
	}
	var resumed struct {
		Group runnerwire.RunGroup `json:"group"`
	}
	if err := json.Unmarshal(resumeResp.Body.Bytes(), &resumed); err != nil || resumed.Group.HeldUntil != "" {
		t.Fatalf("resumed = %+v, %v", resumed, err)
	}
	expectOffer(t, conn, created.Runs[1].ID)

	// POST /api/global-runs/groups/:id/cancel cancels remaining active members (run[1] offered + run[2] queued).
	cancelResp := f.do(t, &f.bea, http.MethodPost, "/api/global-runs/groups/"+created.Group.ID+"/cancel", "{}", nil)
	if cancelResp.Code != http.StatusOK {
		t.Fatalf("POST cancel = %d %s", cancelResp.Code, cancelResp.Body)
	}
	var cancelled runnerwire.CancelAllResponse
	if err := json.Unmarshal(cancelResp.Body.Bytes(), &cancelled); err != nil || cancelled.Cancelled != 2 {
		t.Fatalf("cancelled = %+v, %v", cancelled, err)
	}

	// Audit entry exists and contains no secret task text.
	audited, err := f.server.audit.Query(context.Background(), audit.Filter{ActionPrefix: "run.global.group"})
	if err != nil || len(audited) != 1 || audited[0].Target != created.Group.ID {
		t.Fatalf("audit = %+v, %v", audited, err)
	}
	if detail := fmt.Sprint(audited[0].Detail); strings.Contains(detail, "Secret note") || strings.Contains(detail, "Bump deps") {
		t.Fatalf("audit leaked task text: %s", detail)
	}
}

func TestGlobalGroupsRefusals(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, globalGroupHello(2))
	pingPong(t, conn)

	// Interactive mode -> 400 GROUP_NEEDS_BACKGROUND.
	res := f.do(t, &f.bea, http.MethodPost, "/api/global-runs/groups", f.groupBody(func(r *runnerwire.CreateGlobalGroupRequest) {
		r.Mode = runnerwire.ModeInteractive
	}), nil)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "GROUP_NEEDS_BACKGROUND") {
		t.Fatalf("interactive = %d %s, want 400 GROUP_NEEDS_BACKGROUND", res.Code, res.Body)
	}

	// Duplicate repoIds -> 400 field repoIds.
	res = f.do(t, &f.bea, http.MethodPost, "/api/global-runs/groups", f.groupBody(func(r *runnerwire.CreateGlobalGroupRequest) {
		r.RepoIDs = []string{"repo-1", "repo-1"}
	}), nil)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"field":"repoIds"`) {
		t.Fatalf("duplicate = %d %s", res.Code, res.Body)
	}

	// Disallowed onBusy on group -> 400 field onBusy.
	for _, badOnBusy := range []string{runnerwire.OnBusyQueue, runnerwire.OnBusyTakeOver, runnerwire.OnBusyContinue} {
		res = f.do(t, &f.bea, http.MethodPost, "/api/global-runs/groups", f.groupBody(func(r *runnerwire.CreateGlobalGroupRequest) {
			r.OnBusy = badOnBusy
		}), nil)
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"field":"onBusy"`) {
			t.Fatalf("onBusy=%q = %d %s", badOnBusy, res.Code, res.Body)
		}
	}

	// Budget too small -> 429 with room.
	if code, _ := f.budget(t, &f.bea, http.MethodPut, `{"dailyRuns":2}`); code != http.StatusOK {
		t.Fatalf("set budget = %d", code)
	}
	res = f.do(t, &f.bea, http.MethodPost, "/api/global-runs/groups", f.groupBody(nil), nil)
	var budgetErr struct {
		Code     string `json:"code"`
		Exceeded string `json:"exceeded"`
		Room     int    `json:"room"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &budgetErr); err != nil ||
		res.Code != http.StatusTooManyRequests || budgetErr.Exceeded != "runs" || budgetErr.Room != 2 {
		t.Fatalf("budget = %d %s (parsed %+v)", res.Code, res.Body, budgetErr)
	}
}
