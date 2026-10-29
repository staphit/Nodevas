package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"nodevas/internal/runner"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// A run's error is what its runner (or the hub) said, stored in English. The
// run detail and the run list hand it to a Chinese reader in Chinese when it
// is one of the hub's or runner's own sentences; anything else (an agent's
// own words, a reason the hub does not know) stays as written.
func TestRunErrorSpeaksTheReadersLanguage(t *testing.T) {
	_, refused := runner.AutoAcceptDecision(runnerwire.AutoAcceptPolicy{Enabled: true, RequireStepUp: true},
		runner.Offer{
			Job:  runnerwire.Job{Agent: "claude", RepoID: "repo-1", Mode: runnerwire.ModeHeadless},
			Repo: runnerwire.Repo{ID: "repo-1", Name: "visual-editor"},
		})
	cases := []struct{ reason, chinese string }{
		{runner.ReasonNoTerminal, "runner 需要有人在它的終端機前接受（或執行 `nodevas runner autoaccept on`）"},
		{runner.ReasonTimedOut, "runner 那端沒有人接受"},
		{runner.ReasonDeclined, "在 runner 那端被拒絕"},
		{runner.ReasonInputGone, "runner 的終端機輸入已關閉"},
		{runner.ReasonCancelled, "在有人接受前就取消了"},
		{"this runner already ran or refused this run", "這個 runner 已經執行過或拒絕過這次執行"},
		{runs.ReasonLost, "runner 重新啟動或遺失了這次執行"},
		{refused + " (and nobody is at the runner's terminal to accept)",
			"自動接受需要最近在 hub 上驗證過身分；repo visual-editor 不在這個 runner 的自動接受清單上；" +
				"agent claude 不在這個 runner 的自動接受清單上（而且 runner 的終端機前沒有人可以接受）"},
		{"not now", "not now"},
	}

	f := newRunsFixture(t)
	conn := f.connect(t, runsHello(1))
	ids := make(map[string]int, len(cases))
	for i, tc := range cases {
		run := f.dispatch(t, f.bea, f.runBody(""))
		expectOffer(t, conn, run.ID)
		sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeDecline, RunID: run.ID, Reason: tc.reason})
		f.waitStatus(t, run.ID, runnerwire.RunDeclined)
		ids[run.ID] = i
	}

	for language, chinese := range map[string]bool{"en-US,en;q=0.9": false, "zh-TW,zh;q=0.9": true} {
		want := func(i int) string {
			if chinese {
				return cases[i].chinese
			}
			return cases[i].reason
		}
		header := map[string]string{"Accept-Language": language}
		for id, i := range ids {
			response := f.do(t, &f.bea, http.MethodGet, "/api/runs/"+id, "", header)
			var run runnerwire.Run
			if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil || response.Code != http.StatusOK {
				t.Fatalf("%s GET run = %d %s", language, response.Code, response.Body)
			}
			if run.Error != want(i) {
				t.Errorf("%s run detail error = %q, want %q", language, run.Error, want(i))
			}
		}
		response := f.do(t, &f.bea, http.MethodGet, "/api/runs", "", header)
		var list runnerwire.RunListResponse
		if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil || response.Code != http.StatusOK {
			t.Fatalf("%s GET runs = %d %s", language, response.Code, response.Body)
		}
		seen := 0
		for _, run := range list.Runs {
			i, ok := ids[run.ID]
			if !ok {
				continue
			}
			seen++
			if run.Error != want(i) {
				t.Errorf("%s run list error = %q, want %q", language, run.Error, want(i))
			}
		}
		if seen != len(ids) {
			t.Errorf("%s run list has %d of the %d runs", language, seen, len(ids))
		}
	}
}
