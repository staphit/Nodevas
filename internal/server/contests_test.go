package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"nodevas/internal/realtime"
	"nodevas/internal/runnerwire"
)

func contestRunnerHello(maxConcurrent int) runnerwire.Envelope {
	return runnerwire.Envelope{
		Type: runnerwire.TypeHello,
		Hello: &runnerwire.Info{
			Version: "test", Hostname: "desk", OS: "darwin", Arch: "arm64",
			Capabilities: []string{
				runnerwire.CapHeadless,
				runnerwire.CapInteractive,
				runnerwire.CapGlobalRuns,
				runnerwire.CapGlobalRunsOn,
				runnerwire.CapRunWorkspace,
				runnerwire.CapWorkspaceActions,
				runnerwire.CapCoord,
			},
			MaxConcurrent: maxConcurrent,
			Agents: []runnerwire.Agent{
				{Kind: "claude", Installed: true, Models: []runnerwire.Model{{ID: "opus", Default: true}, {ID: "sonnet"}}},
				{Kind: "codex", Installed: true, Models: []runnerwire.Model{{ID: "gpt-5", Default: true}}},
				{Kind: "gemini", Installed: true, Models: []runnerwire.Model{{ID: "pro", Default: true}}},
			},
			Repos: []runnerwire.Repo{
				{ID: "repo-1", Name: "visual-editor", Remote: "https://github.com/org/visual-editor.git"},
			},
		},
	}
}

func (f *runsFixture) createNodeContest(t *testing.T, b browser, reviewer *runnerwire.ContestEntrySpec) runnerwire.ContestResponse {
	t.Helper()
	raw, _ := json.Marshal(runnerwire.CreateContestRequest{
		NodeID: runsTestNode,
		RepoID: "repo-1",
		Base:   "main",
		Prompt: "Keep changes minimal.",
		Entries: []runnerwire.ContestEntrySpec{
			{RunnerID: f.runner.Runner.ID, Agent: "claude", Model: "opus"},
			{RunnerID: f.runner.Runner.ID, Agent: "codex", Model: "gpt-5"},
		},
		Reviewer: reviewer,
	})
	resp := f.do(t, &b, http.MethodPost, "/api/runs/contests", string(raw), nil)
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST /api/runs/contests = %d %s", resp.Code, resp.Body)
	}
	var out runnerwire.ContestResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func (f *runsFixture) createGlobalContest(t *testing.T, b browser, reviewer *runnerwire.ContestEntrySpec) runnerwire.ContestResponse {
	t.Helper()
	raw, _ := json.Marshal(runnerwire.CreateGlobalContestRequest{
		RunnerID: f.runner.Runner.ID,
		RepoID:   "repo-1",
		Base:     "main",
		Task:     "Fix login\n\nSafari button does nothing.",
		Entries: []runnerwire.ContestEntrySpec{
			{Agent: "claude", Model: "opus"},
			{Agent: "codex", Model: "gpt-5"},
		},
		Reviewer: reviewer,
	})
	resp := f.do(t, &b, http.MethodPost, "/api/global-runs/contests", string(raw), nil)
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST /api/global-runs/contests = %d %s", resp.Code, resp.Body)
	}
	var out runnerwire.ContestResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func completeContestEntry(t *testing.T, f *runsFixture, conn *websocket.Conn, run runnerwire.Run, commit, summary string, global bool) {
	t.Helper()
	sendEnvelope(t, conn, runnerwire.Envelope{
		Type: runnerwire.TypeAccept,
		Accept: &runnerwire.Accept{
			RunID: run.ID, Base: "main", Branch: "agent/fix-login-" + run.ID[:8],
		},
	})
	if global {
		f.waitGlobal(t, run.ID, "running", func(r runnerwire.Run) bool { return r.Status == runnerwire.RunRunning })
	} else {
		f.waitStatus(t, run.ID, runnerwire.RunRunning)
	}
	sendEnvelope(t, conn, runnerwire.Envelope{
		Type:  runnerwire.TypeChanges,
		RunID: run.ID,
		Changes: &runnerwire.RunChanges{
			Commit:    commit,
			Base:      "main",
			Additions: 4,
			Deletions: 1,
			Files:     []runnerwire.FileChange{{Path: "login.ts", Status: runnerwire.FileModified, Additions: 4, Deletions: 1}},
			Patch:     "diff --git a/login.ts b/login.ts\n--- a/login.ts\n+++ b/login.ts\n@@ -1 +1 @@\n-a\n+b\n",
		},
	})
	sendEnvelope(t, conn, runnerwire.Envelope{
		Type:  runnerwire.TypeFinish,
		RunID: run.ID,
		Result: &runnerwire.RunResult{
			Status: runnerwire.RunSucceeded, Summary: summary,
		},
	})
	if global {
		f.waitGlobal(t, run.ID, "succeeded", func(r runnerwire.Run) bool { return r.Status == runnerwire.RunSucceeded })
	} else {
		f.waitStatus(t, run.ID, runnerwire.RunSucceeded)
	}
}

func TestPickCreatorOnlyNotAdmin(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, contestRunnerHello(4))
	pingPong(t, conn)

	created := f.createNodeContest(t, f.bea, nil)
	expectOffer(t, conn, created.Runs[0].ID)
	expectOffer(t, conn, created.Runs[1].ID)
	completeContestEntry(t, f, conn, created.Runs[0], "1111111111111111111111111111111111111111", "Summary A", false)
	completeContestEntry(t, f, conn, created.Runs[1], "2222222222222222222222222222222222222222", "Summary B", false)

	// Wait until contest reaches ready.
	var detail runnerwire.ContestResponse
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp := f.do(t, &f.bea, http.MethodGet, "/api/contests/"+created.Contest.ID, "", nil)
		if resp.Code == http.StatusOK {
			_ = json.Unmarshal(resp.Body.Bytes(), &detail)
			if detail.Contest.State == runnerwire.ContestReady {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("contest never became ready: %+v", detail)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if detail.Compare == nil || len(detail.Compare.Entries) != 2 || detail.Compare.Scores != nil {
		t.Fatalf("compare = %+v", detail.Compare)
	}

	// Admin (ann) can GET the contest.
	if resp := f.do(t, &f.ann, http.MethodGet, "/api/contests/"+created.Contest.ID, "", nil); resp.Code != http.StatusOK {
		t.Fatalf("admin GET contest = %d %s, want 200", resp.Code, resp.Body)
	}

	// Admin (ann) is REFUSED pick and judge (403 PERMISSION_DENIED) — creator only.
	pickBody, _ := json.Marshal(runnerwire.PickRequest{
		RunID: created.Runs[0].ID, Apply: runnerwire.ContestApplyKeep, DiscardOthers: false,
	})
	if resp := f.do(t, &f.ann, http.MethodPost, "/api/contests/"+created.Contest.ID+"/pick", string(pickBody), nil); resp.Code != http.StatusForbidden ||
		!strings.Contains(resp.Body.String(), "PERMISSION_DENIED") {
		t.Fatalf("admin pick = %d %s, want 403 PERMISSION_DENIED", resp.Code, resp.Body)
	}
	judgeBody, _ := json.Marshal(runnerwire.JudgeContestRequest{
		RunnerID: f.runner.Runner.ID, Agent: "gemini", Model: "pro",
	})
	if resp := f.do(t, &f.ann, http.MethodPost, "/api/contests/"+created.Contest.ID+"/judge", string(judgeBody), nil); resp.Code != http.StatusForbidden ||
		!strings.Contains(resp.Body.String(), "PERMISSION_DENIED") {
		t.Fatalf("admin judge = %d %s, want 403 PERMISSION_DENIED", resp.Code, resp.Body)
	}

	// Unrelated member (cal) gets 404 on every contest route.
	for _, tc := range [][2]string{
		{http.MethodGet, "/api/contests/" + created.Contest.ID},
		{http.MethodPost, "/api/contests/" + created.Contest.ID + "/judge"},
		{http.MethodPost, "/api/contests/" + created.Contest.ID + "/pick"},
		{http.MethodPost, "/api/contests/" + created.Contest.ID + "/cancel"},
	} {
		body := ""
		if tc[0] == http.MethodPost {
			body = "{}"
		}
		if resp := f.do(t, &f.cal, tc[0], tc[1], body, nil); resp.Code != http.StatusNotFound {
			t.Fatalf("cal %s %s = %d %s, want 404", tc[0], tc[1], resp.Code, resp.Body)
		}
	}

	// Agents are refused on every contest route (personOnly -> 403 PERMISSION_DENIED).
	for _, tc := range [][2]string{
		{http.MethodPost, "/api/runs/contests"},
		{http.MethodPost, "/api/global-runs/contests"},
		{http.MethodGet, "/api/contests/" + created.Contest.ID},
		{http.MethodPost, "/api/contests/" + created.Contest.ID + "/judge"},
		{http.MethodPost, "/api/contests/" + created.Contest.ID + "/pick"},
		{http.MethodPost, "/api/contests/" + created.Contest.ID + "/cancel"},
	} {
		body := ""
		if tc[0] == http.MethodPost {
			body = "{}"
		}
		if resp := f.doAsAgent(t, &f.bea, tc[0], tc[1], body); resp.Code != http.StatusForbidden ||
			!strings.Contains(resp.Body.String(), "PERMISSION_DENIED") {
			t.Fatalf("agent %s %s = %d %s, want 403 PERMISSION_DENIED", tc[0], tc[1], resp.Code, resp.Body)
		}
	}

	// Creator (bea) picks -> 202 Accepted.
	resp := f.do(t, &f.bea, http.MethodPost, "/api/contests/"+created.Contest.ID+"/pick", string(pickBody), nil)
	if resp.Code != http.StatusAccepted {
		t.Fatalf("creator pick = %d %s, want 202", resp.Code, resp.Body)
	}
	var picked runnerwire.PickResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &picked); err != nil ||
		picked.Contest.State != runnerwire.ContestDecided || picked.Contest.WinnerRunID != created.Runs[0].ID {
		t.Fatalf("picked = %+v, %v", picked, err)
	}
}

func TestCancelByCreatorOrAdmin(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, contestRunnerHello(4))
	pingPong(t, conn)

	// Contest 1: cancelled by admin (ann).
	c1 := f.createNodeContest(t, f.bea, nil)
	expectOffer(t, conn, c1.Runs[0].ID)
	expectOffer(t, conn, c1.Runs[1].ID)

	resp := f.do(t, &f.ann, http.MethodPost, "/api/contests/"+c1.Contest.ID+"/cancel", "{}", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("admin cancel = %d %s, want 200", resp.Code, resp.Body)
	}
	var cancelled1 runnerwire.ContestResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &cancelled1); err != nil ||
		cancelled1.Contest.State != runnerwire.ContestCancelled || len(cancelled1.Runs) != 2 {
		t.Fatalf("cancelled1 = %+v, %v", cancelled1, err)
	}
	for _, r := range cancelled1.Runs {
		if r.Status != runnerwire.RunCancelled {
			t.Fatalf("run %s status = %s, want cancelled", r.ID, r.Status)
		}
	}

	// Contest 2: cancelled by creator (bea).
	c2 := f.createGlobalContest(t, f.bea, nil)
	resp = f.do(t, &f.bea, http.MethodPost, "/api/contests/"+c2.Contest.ID+"/cancel", "{}", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("creator cancel = %d %s, want 200", resp.Code, resp.Body)
	}
	var cancelled2 runnerwire.ContestResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &cancelled2); err != nil ||
		cancelled2.Contest.State != runnerwire.ContestCancelled {
		t.Fatalf("cancelled2 = %+v, %v", cancelled2, err)
	}
}

func TestContestGlobalLifecycleAndHTTP(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, contestRunnerHello(4))
	pingPong(t, conn)
	beaWS := dialHubAs(t, f.liveURL, f.bea)
	calWS := dialHubAs(t, f.liveURL, f.cal)

	// Global contest works without any project query/header, emits global-runs-changed only to bea.
	created := f.createGlobalContest(t, f.bea, nil)
	expectOffer(t, conn, created.Runs[0].ID)
	expectOffer(t, conn, created.Runs[1].ID)
	beaWS.await(runnerwire.GlobalRunsChangedEvent, func(e realtime.Event) bool { return e.ID == created.Runs[0].ID })
	awaitNoneOf(t, calWS, 200*time.Millisecond, runnerwire.GlobalRunsChangedEvent, runnerwire.RunsChangedEvent)

	// Manual /judge while still running -> 409 conflict.
	judgeBody, _ := json.Marshal(runnerwire.JudgeContestRequest{Agent: "gemini", Model: "pro"})
	if resp := f.do(t, &f.bea, http.MethodPost, "/api/contests/"+created.Contest.ID+"/judge", string(judgeBody), nil); resp.Code != http.StatusConflict {
		t.Fatalf("judge while running = %d %s, want 409", resp.Code, resp.Body)
	}

	completeContestEntry(t, f, conn, created.Runs[0], "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "Global A", true)
	completeContestEntry(t, f, conn, created.Runs[1], "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "Global B", true)

	// Wait for ready, then trigger manual /judge -> 202.
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp := f.do(t, &f.bea, http.MethodGet, "/api/contests/"+created.Contest.ID, "", nil)
		var d runnerwire.ContestResponse
		_ = json.Unmarshal(resp.Body.Bytes(), &d)
		if d.Contest.State == runnerwire.ContestReady {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("global contest never became ready: %+v", d)
		}
		time.Sleep(20 * time.Millisecond)
	}

	resp := f.do(t, &f.bea, http.MethodPost, "/api/contests/"+created.Contest.ID+"/judge", string(judgeBody), nil)
	if resp.Code != http.StatusAccepted {
		t.Fatalf("POST /judge = %d %s, want 202", resp.Code, resp.Body)
	}
	var judged struct {
		Contest runnerwire.Contest `json:"contest"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &judged); err != nil ||
		judged.Contest.State != runnerwire.ContestJudging || judged.Contest.JudgeRunID == "" {
		t.Fatalf("judged = %+v, %v", judged, err)
	}
	job := expectOffer(t, conn, judged.Contest.JudgeRunID)
	if !job.Global || job.Kind != runnerwire.JobCompare || !job.NoPR {
		t.Fatalf("global judge offer = %+v", job)
	}

	// Second /judge while judging -> 409 conflict.
	if resp := f.do(t, &f.bea, http.MethodPost, "/api/contests/"+created.Contest.ID+"/judge", string(judgeBody), nil); resp.Code != http.StatusConflict {
		t.Fatalf("second judge = %d %s, want 409", resp.Code, resp.Body)
	}

	// Reviewer finishes with scores -> GET /api/contests/:id shows scores, and /judge again is 409.
	sendEnvelope(t, conn, runnerwire.Envelope{
		Type:   runnerwire.TypeAccept,
		Accept: &runnerwire.Accept{RunID: judged.Contest.JudgeRunID, Base: "main"},
	})
	f.waitGlobal(t, judged.Contest.JudgeRunID, "running", func(r runnerwire.Run) bool { return r.Status == runnerwire.RunRunning })
	sendEnvelope(t, conn, runnerwire.Envelope{
		Type:  runnerwire.TypeFinish,
		RunID: judged.Contest.JudgeRunID,
		Result: &runnerwire.RunResult{
			Status:  runnerwire.RunSucceeded,
			Summary: "Review done.\n```json\n" + `{"scores":[{"entry":"A","score":9,"why":"clean"},{"entry":"B","score":6,"why":"extra files"}],"pick":"A"}` + "\n```",
		},
	})
	f.waitGlobal(t, judged.Contest.JudgeRunID, "succeeded", func(r runnerwire.Run) bool { return r.Status == runnerwire.RunSucceeded })

	var scoredDetail runnerwire.ContestResponse
	deadline = time.Now().Add(5 * time.Second)
	for {
		resp := f.do(t, &f.bea, http.MethodGet, "/api/contests/"+created.Contest.ID, "", nil)
		_ = json.Unmarshal(resp.Body.Bytes(), &scoredDetail)
		if scoredDetail.Contest.State == runnerwire.ContestReady && scoredDetail.Compare != nil && scoredDetail.Compare.Scores != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("contest scores never landed: %+v", scoredDetail)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if scoredDetail.Compare.Scores.Pick != "A" || len(scoredDetail.Compare.Scores.Scores) != 2 {
		t.Fatalf("scores = %+v", scoredDetail.Compare.Scores)
	}
	if resp := f.do(t, &f.bea, http.MethodPost, "/api/contests/"+created.Contest.ID+"/judge", string(judgeBody), nil); resp.Code != http.StatusConflict {
		t.Fatalf("re-judge after scored = %d %s, want 409", resp.Code, resp.Body)
	}

	// Node contest refuses wrong ?project= header (project scope enforced directly).
	nc := f.createNodeContest(t, f.bea, nil)
	if resp := f.do(t, &f.bea, http.MethodGet, "/api/contests/"+nc.Contest.ID+"?project=other", "", nil); resp.Code != http.StatusNotFound {
		t.Fatalf("node contest wrong project = %d %s, want 404", resp.Code, resp.Body)
	}
	// Global contest works even with ?project=other or no active project.
	if resp := f.do(t, &f.bea, http.MethodGet, "/api/contests/"+created.Contest.ID+"?project=other", "", nil); resp.Code != http.StatusOK {
		t.Fatalf("global contest with ?project=other = %d %s, want 200", resp.Code, resp.Body)
	}

	// HTTP refusal codes: 400 CONTEST_SHAPE (interactive), 429 BUDGET_EXCEEDED with room, 412 RUNNER_OUTDATED.
	shapeResp := f.do(t, &f.bea, http.MethodPost, "/api/global-runs/contests",
		`{"runnerId":"`+f.runner.Runner.ID+`","repoId":"repo-1","task":"Fix","mode":"interactive","entries":[{"agent":"claude"},{"agent":"codex"}]}`, nil)
	if shapeResp.Code != http.StatusBadRequest || !strings.Contains(shapeResp.Body.String(), "CONTEST_SHAPE") {
		t.Fatalf("interactive contest = %d %s, want 400 CONTEST_SHAPE", shapeResp.Code, shapeResp.Body)
	}

	if code, _ := f.budget(t, &f.bea, http.MethodPut, `{"dailyRuns":6}`); code != http.StatusOK {
		t.Fatalf("set budget = %d", code)
	}
	// Already used 5 runs (2 global entries + 1 judge + 2 node entries) -> room = 1, contest needs 2 -> 429 {room:1}.
	budgetResp := f.do(t, &f.bea, http.MethodPost, "/api/global-runs/contests",
		`{"runnerId":"`+f.runner.Runner.ID+`","repoId":"repo-1","task":"Over budget","entries":[{"agent":"claude"},{"agent":"codex"}]}`, nil)
	var budgetErr struct {
		Code     string `json:"code"`
		Exceeded string `json:"exceeded"`
		Room     int    `json:"room"`
	}
	if err := json.Unmarshal(budgetResp.Body.Bytes(), &budgetErr); err != nil ||
		budgetResp.Code != http.StatusTooManyRequests || budgetErr.Exceeded != "runs" || budgetErr.Room != 1 {
		t.Fatalf("budget refusal = %d %s (parsed %+v)", budgetResp.Code, budgetResp.Body, budgetErr)
	}

	// Runner without CapCoord -> 412 RUNNER_OUTDATED.
	oldRunner := f.create(t, f.bea, "bea-old")
	oldConn := runnerSocket(t, f.liveURL, oldRunner.Token)
	oldHello := contestRunnerHello(2)
	oldHello.Hello.Capabilities = []string{runnerwire.CapHeadless, runnerwire.CapGlobalRuns, runnerwire.CapGlobalRunsOn}
	sendEnvelope(t, oldConn, oldHello)
	if welcome := readEnvelopeForTest(t, oldConn); welcome.Type != runnerwire.TypeWelcome {
		t.Fatalf("old welcome = %+v", welcome)
	}
	outdatedResp := f.do(t, &f.bea, http.MethodPost, "/api/global-runs/contests",
		`{"runnerId":"`+oldRunner.Runner.ID+`","repoId":"repo-1","task":"Old runner","entries":[{"agent":"claude"},{"agent":"codex"}]}`, nil)
	if outdatedResp.Code != http.StatusPreconditionFailed || !strings.Contains(outdatedResp.Body.String(), "RUNNER_OUTDATED") {
		t.Fatalf("outdated runner = %d %s, want 412 RUNNER_OUTDATED", outdatedResp.Code, outdatedResp.Body)
	}
}
