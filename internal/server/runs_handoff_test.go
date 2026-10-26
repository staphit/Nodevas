package server

import (
	"net/http"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

// A run stopped at a usage limit with no fallback left waits for the reset,
// visible on the run, and a person can retry it now; the retry continues
// from the handoff branch.
func TestRunLimitWaitAndRetryNow(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, routingHello(4))
	run := f.dispatch(t, f.bea, f.createBody(func(*runnerwire.CreateRunRequest) {}))
	expectOffer(t, conn, run.ID)
	accept(t, conn, run.ID, "agent/a")
	f.waitStatus(t, run.ID, runnerwire.RunRunning)
	finish(t, conn, run.ID, runnerwire.RunResult{Status: runnerwire.RunFailed, FailureKind: runnerwire.FailureQuota,
		Error: "Claude AI usage limit reached", HandoffBranch: "nodevas/handoff/" + run.ID, HandoffNote: "did half",
		ResetAt: time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)})
	pingPong(t, conn)
	f.waitEvents(t, run.ID, "waiting for the usage limit to reset")

	failed, _ := f.getRun(t, f.bea, run.ID)
	if failed.HandoffBranch != "nodevas/handoff/"+run.ID || failed.HandoffNote != "did half" {
		t.Fatalf("failed run = %+v", failed)
	}
	var wait runnerwire.Run
	for _, r := range f.activeRuns(t, f.bea) {
		if r.RetryOf == run.ID {
			wait = r
		}
	}
	if wait.ID == "" || wait.Status != runnerwire.RunQueued || wait.RetryAt == "" ||
		wait.ContinuesFrom != "nodevas/handoff/"+run.ID {
		t.Fatalf("waiting run = %+v", wait)
	}

	// Only who may see it may retry it.
	if code := f.do(t, &f.cal, http.MethodPost, "/api/runs/"+wait.ID+"/retry-now", "", nil).Code; code != http.StatusNotFound {
		t.Fatalf("someone else's retry now = %d", code)
	}
	if code := f.do(t, &f.bea, http.MethodPost, "/api/runs/"+wait.ID+"/retry-now", "", nil).Code; code != http.StatusOK {
		t.Fatalf("retry now = %d", code)
	}
	job := nextOffer(t, conn)
	if job.RunID != wait.ID || job.HandoffBranch != "nodevas/handoff/"+run.ID || job.HandoffNote != "did half" {
		t.Fatalf("offer = %+v", job)
	}
	if code := f.do(t, &f.bea, http.MethodPost, "/api/runs/"+wait.ID+"/retry-now", "", nil).Code; code != http.StatusConflict {
		t.Fatalf("retry now on an offered run = %d", code)
	}
}
