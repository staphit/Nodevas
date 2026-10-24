package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"

	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// consensusHello is routingHello with room for parallel reviews and a third
// model to review with.
func consensusHello() runnerwire.Envelope {
	env := routingHello(3)
	env.Hello.Agents[1].Models = append(env.Hello.Agents[1].Models, runnerwire.Model{ID: "o5"})
	return env
}

// reviewOffers reads n offers and keys them by agent/model.
func reviewOffers(t *testing.T, conn *websocket.Conn, n int) map[string]runnerwire.Job {
	t.Helper()
	out := map[string]runnerwire.Job{}
	for range n {
		job := nextOffer(t, conn)
		if job.Kind != runnerwire.JobReview {
			t.Fatalf("offer = %+v, want a review", job)
		}
		out[job.Agent+"/"+job.Model] = job
	}
	return out
}

func (f *runsFixture) humanReview(t *testing.T, b browser, id, verdict, comment string) (int, runnerwire.PipelineState) {
	t.Helper()
	body, _ := json.Marshal(runnerwire.HumanReviewRequest{Verdict: verdict, Comment: comment})
	response := f.do(t, &b, http.MethodPost, "/api/runs/pipelines/"+id+"/review", string(body), nil)
	var out runnerwire.PipelineState
	_ = json.Unmarshal(response.Body.Bytes(), &out)
	return response.Code, out
}

func countVotes(p runnerwire.PipelineState, round int, verdict string) int {
	n := 0
	for _, v := range p.Votes {
		if v.Round == round && v.Verdict == verdict {
			n++
		}
	}
	return n
}

// Three reviewers, two needed: round 1 has one approval and two change
// requests, so both reviews go to one revise; round 2 has two approvals and a
// failed review (an abstention), which carries it.
func TestRunPipelineConsensus(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, consensusHello())
	impl := f.dispatch(t, f.bea, f.createBody(func(r *runnerwire.CreateRunRequest) {
		r.Agent, r.Model = "", ""
		r.Pipeline = &runnerwire.Pipeline{
			Implement: runnerwire.AgentModel{Agent: "claude", Model: "opus"},
			Reviewers: []runnerwire.AgentModel{{Agent: "codex"}, {Agent: "claude", Model: "sonnet"}, {Agent: "codex", Model: "o5"}},
			Quorum:    2,
			MaxRounds: 2,
		}
	}))
	if job := nextOffer(t, conn); job.RunID != impl.ID {
		t.Fatalf("implement job = %+v", job)
	}
	implement(t, conn, impl.ID)

	round1 := reviewOffers(t, conn, 3)
	for _, key := range []string{"codex/gpt-5", "claude/sonnet", "codex/o5"} {
		if _, ok := round1[key]; !ok {
			t.Fatalf("round 1 reviews = %v, missing %s", round1, key)
		}
	}
	review(t, conn, round1["codex/gpt-5"].RunID, runnerwire.VerdictApprove, "Fine.\nVERDICT: approve")
	review(t, conn, round1["claude/sonnet"].RunID, runnerwire.VerdictChanges, "Rename the helper.\nVERDICT: changes")
	pingPong(t, conn)
	// Two votes in, one pending: nothing moves yet.
	if _, p := f.pipeline(t, f.bea, impl.PipelineID); p.Status != runnerwire.PipelineRunning || p.Round != 1 ||
		countVotes(p, 1, "") != 1 || countVotes(p, 1, runnerwire.VerdictApprove) != 1 {
		t.Fatalf("pipeline mid-round = %+v", p)
	}
	review(t, conn, round1["codex/o5"].RunID, runnerwire.VerdictChanges, "Add a test.\nVERDICT: changes")

	revise := nextOffer(t, conn)
	if revise.Kind != runnerwire.JobRevise || revise.Agent != "claude" || revise.Model != "opus" ||
		!strings.Contains(revise.Review, "Review 1 of 2, by claude/sonnet:\nRename the helper.") ||
		!strings.Contains(revise.Review, "Review 2 of 2, by codex/o5:\nAdd a test.") ||
		strings.Contains(revise.Review, "Fine.") {
		t.Fatalf("revise job = %+v", revise)
	}
	accept(t, conn, revise.RunID, revise.Branch)
	finish(t, conn, revise.RunID, runnerwire.RunResult{Status: runnerwire.RunSucceeded, Summary: "Renamed, tested."})

	round2 := reviewOffers(t, conn, 3)
	review(t, conn, round2["codex/gpt-5"].RunID, runnerwire.VerdictApprove, "Good.\nVERDICT: approve")
	accept(t, conn, round2["codex/o5"].RunID, "x")
	finish(t, conn, round2["codex/o5"].RunID, runnerwire.RunResult{Status: runnerwire.RunFailed, Error: "rate limited"})
	review(t, conn, round2["claude/sonnet"].RunID, runnerwire.VerdictApprove, "Good now.\nVERDICT: approve")
	pingPong(t, conn)

	p := f.waitPipeline(t, impl.PipelineID, runnerwire.PipelineApproved)
	if p.Round != 2 || len(p.Votes) != 6 || countVotes(p, 2, runnerwire.VerdictApprove) != 2 ||
		countVotes(p, 2, runnerwire.VerdictAbstain) != 1 || countVotes(p, 1, runnerwire.VerdictChanges) != 2 {
		t.Fatalf("approved pipeline = %+v", p)
	}
	if events := f.runEvents(t, round2["claude/sonnet"].RunID); !strings.Contains(events, "2 of 3 approved, 2 needed") {
		t.Fatalf("approval note missing: %s", events)
	}
}

// Without enough approvals and with nobody asking for changes, there is
// nothing to revise: the pipeline fails.
func TestRunPipelineConsensusWithoutChanges(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, consensusHello())
	impl := f.dispatch(t, f.bea, f.createBody(func(r *runnerwire.CreateRunRequest) {
		r.Agent, r.Model = "", ""
		r.Pipeline = &runnerwire.Pipeline{
			Implement: runnerwire.AgentModel{Agent: "claude", Model: "opus"},
			Reviewers: []runnerwire.AgentModel{{Agent: "codex"}, {Agent: "claude", Model: "sonnet"}},
			MaxRounds: 2,
		}
	}))
	expectOffer(t, conn, impl.ID)
	implement(t, conn, impl.ID)
	reviews := reviewOffers(t, conn, 2)
	review(t, conn, reviews["codex/gpt-5"].RunID, runnerwire.VerdictApprove, "Fine.\nVERDICT: approve")
	accept(t, conn, reviews["claude/sonnet"].RunID, "x")
	finish(t, conn, reviews["claude/sonnet"].RunID, runnerwire.RunResult{Status: runnerwire.RunFailed, Error: "no verdict"})
	pingPong(t, conn)
	f.waitPipeline(t, impl.PipelineID, runnerwire.PipelineFailed)
}

// A person reviews after the agents' quorum, or alone: changes start a revise
// round with the comment, approve ends it.
func TestRunPipelineHumanReview(t *testing.T) {
	f := newRunsFixture(t)
	// Observers (the W7 autopilot) hear every pipeline state on the way.
	var mu sync.Mutex
	var seen []string
	f.server.runs.Observe(func(u runs.Update) {
		if u.Pipeline != nil {
			mu.Lock()
			seen = append(seen, fmt.Sprintf("%s/%d", u.Pipeline.Status, u.Pipeline.Round))
			mu.Unlock()
		}
	})
	conn := f.connect(t, routingHello(2))
	impl := f.dispatch(t, f.bea, f.createBody(func(r *runnerwire.CreateRunRequest) {
		r.Agent, r.Model = "", ""
		r.Pipeline = &runnerwire.Pipeline{
			Implement: runnerwire.AgentModel{Agent: "claude", Model: "opus"},
			Reviewers: []runnerwire.AgentModel{{Agent: "codex"}},
			Human:     true,
			MaxRounds: 2,
		}
	}))
	expectOffer(t, conn, impl.ID)
	if code, _ := f.humanReview(t, f.bea, impl.PipelineID, runnerwire.VerdictApprove, ""); code != http.StatusConflict {
		t.Fatalf("reviewing before the agents = %d", code)
	}
	implement(t, conn, impl.ID)
	job := nextOffer(t, conn)
	review(t, conn, job.RunID, runnerwire.VerdictApprove, "Fine.\nVERDICT: approve")
	pingPong(t, conn)
	p := f.waitPipeline(t, impl.PipelineID, runnerwire.PipelineWaitingHuman)
	if p.Round != 1 || p.HumanVerdict != "" {
		t.Fatalf("waiting pipeline = %+v", p)
	}

	if code, _ := f.humanReview(t, f.cal, impl.PipelineID, runnerwire.VerdictApprove, ""); code != http.StatusNotFound {
		t.Fatalf("cal reviewing bea's pipeline = %d", code)
	}
	if code, _ := f.humanReview(t, f.bea, impl.PipelineID, runnerwire.VerdictChanges, "  "); code != http.StatusBadRequest {
		t.Fatalf("changes without a comment = %d", code)
	}
	if code, _ := f.humanReview(t, f.bea, impl.PipelineID, "maybe", ""); code != http.StatusBadRequest {
		t.Fatalf("an unknown verdict = %d", code)
	}
	code, p := f.humanReview(t, f.bea, impl.PipelineID, runnerwire.VerdictChanges, "Use a map.")
	if code != http.StatusOK || p.Status != runnerwire.PipelineRunning || p.Round != 2 {
		t.Fatalf("changes = %d %+v", code, p)
	}
	revise := nextOffer(t, conn)
	if revise.Kind != runnerwire.JobRevise || !strings.HasPrefix(revise.Review, "Review by bea (a person):\nUse a map.") {
		t.Fatalf("revise job = %+v", revise)
	}
	accept(t, conn, revise.RunID, revise.Branch)
	finish(t, conn, revise.RunID, runnerwire.RunResult{Status: runnerwire.RunSucceeded})
	job = nextOffer(t, conn)
	review(t, conn, job.RunID, runnerwire.VerdictApprove, "Still fine.\nVERDICT: approve")
	pingPong(t, conn)
	f.waitPipeline(t, impl.PipelineID, runnerwire.PipelineWaitingHuman)

	code, p = f.humanReview(t, f.bea, impl.PipelineID, runnerwire.VerdictApprove, "")
	if code != http.StatusOK || p.Status != runnerwire.PipelineApproved || p.HumanVerdict != runnerwire.VerdictApprove {
		t.Fatalf("approve = %d %+v", code, p)
	}
	if countVotes(p, 1, runnerwire.VerdictChanges) != 1 || countVotes(p, 2, runnerwire.VerdictApprove) != 2 {
		t.Fatalf("votes = %+v", p.Votes)
	}
	if code, _ := f.humanReview(t, f.bea, impl.PipelineID, runnerwire.VerdictApprove, ""); code != http.StatusConflict {
		t.Fatalf("a second approval = %d", code)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, want := range []string{"running/1", "waiting_human/1", "running/2", "waiting_human/2", "approved/2"} {
		if !slices.Contains(seen, want) {
			t.Errorf("observers never heard %s: %v", want, seen)
		}
	}
}

// A person alone, and a pipeline with no review at all.
func TestRunPipelineHumanOnlyAndImplementOnly(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, routingHello(1))
	human := f.dispatch(t, f.bea, f.createBody(func(r *runnerwire.CreateRunRequest) {
		r.Agent, r.Model = "", ""
		r.Pipeline = &runnerwire.Pipeline{
			Implement: runnerwire.AgentModel{Agent: "claude", Model: "opus"}, Human: true, MaxRounds: 1,
		}
	}))
	expectOffer(t, conn, human.ID)
	implement(t, conn, human.ID)
	pingPong(t, conn)
	f.waitPipeline(t, human.PipelineID, runnerwire.PipelineWaitingHuman)
	if code, p := f.humanReview(t, f.bea, human.PipelineID, runnerwire.VerdictChanges, "Not like this."); code != http.StatusOK ||
		p.Status != runnerwire.PipelineChangesLeft {
		t.Fatalf("changes in the last round = %d %+v", code, p)
	}

	only := f.dispatch(t, f.bea, f.createBody(func(r *runnerwire.CreateRunRequest) {
		r.Agent, r.Model = "", ""
		r.Pipeline = &runnerwire.Pipeline{Implement: runnerwire.AgentModel{Agent: "claude", Model: "opus"}, MaxRounds: 1}
	}))
	expectOffer(t, conn, only.ID)
	implement(t, conn, only.ID)
	pingPong(t, conn)
	if p := f.waitPipeline(t, only.PipelineID, runnerwire.PipelineApproved); len(p.RunIDs) != 1 || len(p.Votes) != 0 {
		t.Fatalf("implement-only pipeline = %+v", p)
	}
}

func TestRunPipelineReviewerValidation(t *testing.T) {
	f := newRunsFixture(t)
	f.connect(t, consensusHello())
	for name, pipeline := range map[string]runnerwire.Pipeline{
		"quorum over the reviewers": {Implement: runnerwire.AgentModel{Agent: "claude"},
			Reviewers: []runnerwire.AgentModel{{Agent: "codex"}}, Quorum: 2, MaxRounds: 1},
		"too many reviewers": {Implement: runnerwire.AgentModel{Agent: "claude"},
			Reviewers: make([]runnerwire.AgentModel, runnerwire.MaxReviewers+1), MaxRounds: 1},
		"a reviewer the runner lacks": {Implement: runnerwire.AgentModel{Agent: "claude"},
			Reviewers: []runnerwire.AgentModel{{Agent: "gemini"}}, MaxRounds: 1},
	} {
		body := f.createBody(func(r *runnerwire.CreateRunRequest) { r.Pipeline = &pipeline })
		if response := f.do(t, &f.bea, http.MethodPost, "/api/runs", body, nil); response.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, response.Code, response.Body)
		}
	}
}

// A plan run is one headless run of kind plan, on a runner that offers it.
func TestRunPlanDispatch(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, routingHello(1))
	plan := f.createBody(func(r *runnerwire.CreateRunRequest) { r.Kind = runnerwire.JobPlan })
	if response := f.do(t, &f.bea, http.MethodPost, "/api/runs", plan, nil); response.Code != http.StatusConflict {
		t.Fatalf("plan on a runner without plan jobs = %d %s", response.Code, response.Body)
	}
	_ = conn.CloseNow()

	hello := routingHello(1)
	hello.Hello.Capabilities = append(hello.Hello.Capabilities, runnerwire.CapPlan)
	conn = f.connect(t, hello)
	for name, body := range map[string]string{
		"an unknown kind": f.createBody(func(r *runnerwire.CreateRunRequest) { r.Kind = runnerwire.JobReview }),
		"a plan pipeline": f.createBody(func(r *runnerwire.CreateRunRequest) {
			r.Kind = runnerwire.JobPlan
			r.Pipeline = &runnerwire.Pipeline{Implement: runnerwire.AgentModel{Agent: "claude"}, MaxRounds: 1}
		}),
		"an interactive plan": f.createBody(func(r *runnerwire.CreateRunRequest) {
			r.Kind, r.Mode = runnerwire.JobPlan, runnerwire.ModeInteractive
		}),
	} {
		if response := f.do(t, &f.bea, http.MethodPost, "/api/runs", body, nil); response.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, response.Code, response.Body)
		}
	}
	run := f.dispatch(t, f.bea, plan)
	if run.Kind != runnerwire.JobPlan {
		t.Fatalf("plan run = %+v", run)
	}
	job := nextOffer(t, conn)
	if job.Kind != runnerwire.JobPlan || job.RunID != run.ID || job.Branch != "" || job.PRURL != "" ||
		!strings.Contains(job.Prompt, "Fix the login bug") {
		t.Fatalf("plan job = %+v", job)
	}
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeAccept, Accept: &runnerwire.Accept{RunID: run.ID, Base: "main"}})
	finish(t, conn, run.ID, runnerwire.RunResult{Status: runnerwire.RunSucceeded, Summary: "Created 3 tasks."})
	pingPong(t, conn)
	if done := f.waitStatus(t, run.ID, runnerwire.RunSucceeded); done.Summary != "Created 3 tasks." || done.PRURL != "" {
		t.Fatalf("finished plan = %+v", done)
	}
}
