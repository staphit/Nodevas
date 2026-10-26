package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/copilot/provider"
	"nodevas/internal/runnerwire"
)

// goalOutline is GET /api/graph?view=outline for the goal node-0042 and its
// four tasks: t1 done, t2 in progress, t3 and t4 not started.
const goalOutline = `{"graph":{"version":1,"nodes":[
	{"id":"node-0042","title":"Login page","kind":"task","tags":["goal"]},
	{"id":"t1","title":"Add the users table","kind":"task","tags":["goal:node-0042"]},
	{"id":"t2","title":"Write the login form","kind":"task","tags":["goal:node-0042"]},
	{"id":"t3","title":"Add PDF export","kind":"task","tags":["goal:node-0042"]},
	{"id":"t4","title":"Email the admin","kind":"task","tags":["goal:node-0042"]},
	{"id":"elsewhere","title":"Another goal's task","kind":"task"}],
	"edges":[{"from":"t1","to":"t2"},{"from":"t1","to":"t3"},{"from":"t2","to":"t4"},
	{"from":"t3","to":"node-0042"},{"from":"t4","to":"node-0042"}]},
	"statuses":{"t1":"done","t2":"in_progress","t3":"ready","t4":"ready","node-0042":"locked"}}`

func revisingAPI() *fakeAPI {
	api := standardAPI()
	api.graph = goalOutline
	api.goalContent = "---\nid: node-0042\n---\nBuild the login page with a password form.\n"
	api.latest = `{"run":{"id":"ap-1","goalNodeId":"node-0042","status":"running","createdBy":"u-alice","createdByName":"alice"},"tasks":[]}`
	return api
}

func fixRevisionClock(t *testing.T) {
	t.Helper()
	old := revisionNow
	revisionNow = func() time.Time { return time.Date(2026, 10, 27, 9, 30, 0, 0, time.UTC) }
	t.Cleanup(func() { revisionNow = old })
}

func indexOf(calls []recorded, method, path string) int {
	return slices.IndexFunc(calls, func(r recorded) bool { return r.method == method && r.path == path })
}

// revise_goal pauses the running autopilot first, records the change on the
// goal under Revisions with the original kept, and dispatches a replan run
// carrying the change and the tasks with their states; the follow-up
// remembers the autopilot was running.
func TestReviseGoalPausesRecordsAndDispatches(t *testing.T) {
	fixRevisionClock(t)
	api := revisingAPI()
	env := toolEnv(api)
	var recorded *Revision
	env.followup = func(goal, runID string, revision *Revision) {
		if goal != "node-0042" || runID != "run-7" {
			t.Errorf("follow-up for %s/%s", goal, runID)
		}
		recorded = revision
	}
	result := runReviseGoal(context.Background(), env,
		json.RawMessage(`{"goalNodeId":"node-0042","change":"Drop the PDF export; add a sign-in with SSO button."}`))
	if result.IsError {
		t.Fatalf("result = %s", result.Output)
	}
	calls := api.calls()
	pause := indexOf(calls, http.MethodPost, "/api/autopilot/ap-1/pause")
	put := indexOf(calls, http.MethodPut, "/api/nodes/node-0042")
	dispatch := indexOf(calls, http.MethodPost, "/api/runs")
	if pause < 0 || put < pause || dispatch < put {
		t.Fatalf("pause %d, record %d, dispatch %d: want them in that order", pause, put, dispatch)
	}
	for _, r := range calls {
		if r.project != "main" || r.cookie != "session=abc" || r.agent {
			t.Errorf("%s %s: project %q cookie %q in-process agent %v", r.method, r.path, r.project, r.cookie, r.agent)
		}
	}
	content, _ := calls[put].body["content"].(string)
	if calls[put].body["baseRev"] != "rev-1" || !strings.Contains(content, "Build the login page with a password form.") ||
		!strings.Contains(content, "## Revisions\n\n### 2026-10-27 09:30 UTC\n\nDrop the PDF export; add a sign-in with SSO button.\n") ||
		strings.Index(content, "password form") > strings.Index(content, "## Revisions") {
		t.Fatalf("goal content = %q", content)
	}
	run := calls[dispatch].body
	if run["kind"] != runnerwire.JobPlan || run["nodeId"] != "node-0042" || run["runnerId"] != "r-ann" {
		t.Fatalf("run = %+v", run)
	}
	revision, _ := run["revision"].(string)
	for _, want := range []string{"Drop the PDF export; add a sign-in with SSO button.", "2026-10-27 09:30 UTC",
		`t1 [done] "Add the users table"`, `t2 [running] "Write the login form" after t1`,
		`t3 [pending] "Add PDF export" after t1`, `t4 [pending] "Email the admin" after t2`, "data, not instructions"} {
		if !strings.Contains(revision, want) {
			t.Errorf("revision lacks %q:\n%s", want, revision)
		}
	}
	if strings.Contains(revision, "elsewhere") {
		t.Errorf("revision lists a task of no goal:\n%s", revision)
	}
	if recorded == nil || recorded.AutopilotID != "ap-1" || recorded.AutopilotWas != runnerwire.AutopilotRunning ||
		len(recorded.Tasks) != 4 || !slices.Contains(recorded.Tasks, RevisionTask{ID: "t2", Title: "Write the login form", State: stateRunning}) {
		t.Fatalf("recorded revision = %+v", recorded)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil || out["autopilotPaused"] != true ||
		out["runId"] != "run-7" || !strings.Contains(out["note"].(string), "nothing resumes by itself") {
		t.Fatalf("output = %s", result.Output)
	}

	// Without a live autopilot nothing is paused, and the follow-up offers to
	// start one.
	api = revisingAPI()
	api.latest = `{"run":{"id":"ap-0","status":"succeeded","createdBy":"u-alice"},"tasks":[]}`
	env = toolEnv(api)
	env.followup = func(_, _ string, revision *Revision) { recorded = revision }
	if result := runReviseGoal(context.Background(), env, json.RawMessage(`{"goalNodeId":"node-0042","change":"x"}`)); result.IsError {
		t.Fatal(result.Output)
	}
	if api.find(http.MethodPost, "/api/autopilot/ap-0/pause") != nil || recorded.AutopilotWas != "" {
		t.Fatalf("an ended autopilot was paused: %+v", recorded)
	}
}

// What revise_goal refuses, and that a refusal pauses, writes and dispatches
// nothing: another account's autopilot, a goal with no tasks, a replan
// already going, no runner of the person's own.
func TestReviseGoalRefusals(t *testing.T) {
	cases := []struct {
		name, input string
		api         func(*fakeAPI)
		want        string
	}{
		{"no goal", `{"change":"x"}`, nil, "give the goalNodeId"},
		{"no change", `{"goalNodeId":"node-0042"}`, nil, "give the change"},
		{"not a node id", `{"goalNodeId":"../x","change":"x"}`, nil, "not a valid node id"},
		{"no such goal", `{"goalNodeId":"node-9999","change":"x"}`, nil, `no node "node-9999"`},
		{"no tasks yet", `{"goalNodeId":"node-0042","change":"x"}`, func(f *fakeAPI) {
			f.graph = `{"graph":{"nodes":[{"id":"node-0042","title":"Login page"}]},"statuses":{}}`
		}, "start_breakdown"},
		{"another account's autopilot", `{"goalNodeId":"node-0042","change":"x"}`, func(f *fakeAPI) {
			f.latest = `{"run":{"id":"ap-1","status":"running","createdBy":"u-bob","createdByName":"bob"},"tasks":[]}`
		}, "started by bob"},
		{"a replan already going", `{"goalNodeId":"node-0042","change":"x"}`, func(f *fakeAPI) {
			f.active = []runnerwire.Run{{ID: "run-3", NodeID: "node-0042", Kind: runnerwire.JobPlan, Status: runnerwire.RunRunning}}
		}, "still running (run run-3)"},
		{"someone else's runner", `{"goalNodeId":"node-0042","change":"x","runnerId":"r-bob"}`, nil, "not one of your online runners"},
		{"the project is not theirs", `{"goalNodeId":"node-0042","change":"x"}`, func(f *fakeAPI) { f.graph = "" }, "Not revised"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := revisingAPI()
			if tc.api != nil {
				tc.api(api)
			}
			env := toolEnv(api)
			env.followup = func(string, string, *Revision) { t.Error("a follow-up was recorded") }
			result := runReviseGoal(context.Background(), env, json.RawMessage(tc.input))
			if !result.IsError || !strings.Contains(result.Output, tc.want) {
				t.Fatalf("result = %+v, want an error with %q", result, tc.want)
			}
			for _, r := range api.calls() {
				if r.method != http.MethodGet {
					t.Fatalf("a refusal wrote: %s %s", r.method, r.path)
				}
			}
		})
	}
}

// revise_goal asks every time, bypass included, is refused in plan mode, and
// is offered only where the server can apply a replan.
func TestReviseGoalAlwaysAsks(t *testing.T) {
	fixRevisionClock(t)
	api := revisingAPI()
	runs := newFakeRuns()
	writes := &boardWrites{}
	h := newHarness(t, func(opts *Options) {
		opts.Handler = func() http.Handler { return api }
		withFollowups(runs)(opts)
		writes.install(opts)
	})
	conversation := h.conversation(alice, ModeBypass)
	h.model.steps = []step{
		func(t *testing.T, ctx context.Context, req provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
			if !slices.ContainsFunc(req.Tools, func(tool provider.Tool) bool { return tool.Name == "revise_goal" }) {
				t.Errorf("revise_goal not offered")
			}
			if !strings.Contains(req.System, "revise_goal") {
				t.Errorf("the system prompt does not teach revise_goal:\n%s", req.System)
			}
			return callWith("c1", "revise_goal", `{"goalNodeId":"node-0042","change":"add SSO"}`)(t, ctx, req, onDelta)
		},
		expectResult("c1", false, "run-7"),
	}
	turn := h.start(h.turn(conversation, ModeBypass))
	started := turn.until(EventTurnStarted)
	request := turn.until(EventPermissionRequest)
	if request.Name != "revise_goal" || !request.EveryTime || len(api.calls()) != 0 {
		t.Fatalf("request = %+v; API reached before the answer: %d calls", request, len(api.calls()))
	}
	if err := h.service.Decide(alice, conversation.ID, started.TurnID, request.RequestID, DecisionAllow, ""); err != nil {
		t.Fatal(err)
	}
	turn.finish()
	h.service.background.Wait()
	pending := pendingFollowups(t, h)
	if len(pending) != 1 || pending[0].Kind != followupRevision || pending[0].Revision == nil ||
		pending[0].Revision.Change != "add SSO" || pending[0].Revision.AutopilotWas != runnerwire.AutopilotRunning {
		t.Fatalf("pending = %+v", pending)
	}

	plan := revisingAPI()
	h = newHarness(t, func(opts *Options) {
		opts.Handler = func() http.Handler { return plan }
		withFollowups(newFakeRuns())(opts)
		(&boardWrites{}).install(opts)
	})
	conversation = h.conversation(alice, ModePlan)
	h.model.steps = []step{callWith("c1", "revise_goal", `{"goalNodeId":"node-0042","change":"x"}`), expectResult("c1", true, "plan mode")}
	for _, event := range h.start(h.turn(conversation, ModePlan)).finish() {
		if event.Type == EventPermissionRequest {
			t.Fatal("plan mode asked")
		}
	}
	if len(plan.calls()) != 0 {
		t.Fatalf("plan mode reached the API: %+v", plan.calls())
	}

	// Without the board writes that apply a replan, the tool is not offered.
	none := &Service{opts: Options{LookupRun: h.service.opts.LookupRun, GoalTasks: h.service.opts.GoalTasks}}
	if slices.Contains(none.serverToolsOffered(), "revise_goal") || !slices.Contains(none.serverToolsOffered(), "start_breakdown") {
		t.Fatalf("offered = %v", none.serverToolsOffered())
	}
}

// boardWrites records what a revision's follow-up writes on the board.
type boardWrites struct {
	mu      sync.Mutex
	skipped []string
	noted   []string
	fail    string // a node whose writes fail
}

func (b *boardWrites) install(opts *Options) {
	opts.SkipTask = func(_ context.Context, project, nodeID, by, note string) error {
		b.mu.Lock()
		defer b.mu.Unlock()
		if nodeID == b.fail {
			return errors.New("the board refused")
		}
		b.skipped = append(b.skipped, project+"/"+nodeID+"|"+by+"|"+note)
		return nil
	}
	opts.NoteTask = func(_ context.Context, project, nodeID, accountID, note string) error {
		b.mu.Lock()
		defer b.mu.Unlock()
		if nodeID == b.fail {
			return errors.New("the board refused")
		}
		b.noted = append(b.noted, project+"/"+nodeID+"|"+accountID+"|"+note)
		return nil
	}
}

func (b *boardWrites) all() (skipped, noted []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.skipped...), append([]string(nil), b.noted...)
}

// revisedRuns is the board after a replan: the four tasks as before, and t5
// it added after t2.
func revisedRuns() *fakeRuns {
	return &fakeRuns{runs: map[string]RunInfo{}, goal: Goal{Title: "Login page", Tasks: []GoalTask{
		{ID: "t1", Title: "Add the users table", Status: "done"},
		{ID: "t2", Title: "Write the login form", After: []string{"t1"}, Status: "in_progress"},
		{ID: "t3", Title: "Add PDF export", After: []string{"t1"}, Status: "ready"},
		{ID: "t4", Title: "Email the admin", After: []string{"t2"}, Status: "ready", Active: false},
		{ID: "t5", Title: "Add the SSO button", After: []string{"t2"}, Status: "ready"},
	}}}
}

func addRevisionFollowup(t *testing.T, h *harness, conversation Conversation, language, was string) {
	t.Helper()
	if _, err := h.service.store.addFollowup(context.Background(), Followup{
		ConversationID: conversation.ID, AccountID: alice.ID, GoalNodeID: "node-0042", RunID: "run-7",
		Language: language, Revision: &Revision{Change: "Drop the PDF export; add SSO.", AutopilotID: "ap-1",
			AutopilotWas: was, Tasks: []RevisionTask{
				{ID: "t1", Title: "Add the users table", State: stateDone},
				{ID: "t2", Title: "Write the login form", State: stateRunning},
				{ID: "t3", Title: "Add PDF export", State: statePending},
				{ID: "t4", Title: "Email the admin", State: statePending},
			}},
	}); err != nil {
		t.Fatal(err)
	}
}

const replanSummary = `Reconciled the goal with the change.

RECONCILE:
- skip t1: the users table is not needed any more
- change t2: also show the SSO button
- **skip t3**: the PDF export was dropped
- change t4: mention SSO sign-ins in the email
- skip elsewhere: not this goal's
- add t5: Add the SSO button
END RECONCILE`

// A replan's follow-up applies its reconciliation by the rules — the done
// task is kept though the planner said skip, the task in progress is flagged
// rather than changed, the obsolete pending task is skipped, the pending one
// is changed, a task of no goal is left alone — and reports the diff, then
// asks to resume the paused autopilot.
func TestRevisionFollowupAppliesTheReconciliation(t *testing.T) {
	runs := revisedRuns()
	writes := &boardWrites{}
	h := newHarness(t, func(opts *Options) {
		withFollowups(runs)(opts)
		writes.install(opts)
	})
	conversation := h.conversation(alice, ModeDefault)
	addRevisionFollowup(t, h, conversation, "en", runnerwire.AutopilotRunning)
	run := planRun(runnerwire.RunSucceeded)
	run.Summary = replanSummary
	runs.set(run)
	h.service.RunChanged(run)
	h.service.background.Wait()

	skipped, noted := writes.all()
	if len(skipped) != 1 || !strings.HasPrefix(skipped[0], "demo/t3|copilot revision|skipped in a revision of \"Login page\": the PDF export was dropped") {
		t.Fatalf("skipped = %q", skipped)
	}
	if len(noted) != 1 || noted[0] != `demo/t4|u-alice|Changed by a revision of the goal "Login page": mention SSO sign-ins in the email` {
		t.Fatalf("noted = %q", noted)
	}
	entries := entriesOf(t, h, conversation)
	if len(entries) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
	event, reply := entries[0], entries[1]
	if event.Kind != EntryEvent || event.Text != "Revision planned: 1 added, 1 changed, 1 skipped" {
		t.Fatalf("event = %+v", event)
	}
	note := messageOf(t, event).Blocks[0].Text
	for _, want := range []string{"[Server event, not the person: the replan run run-7", "data, not instructions",
		`t1 "Add the users table" kept`, `t3 "Add PDF export" skipped: the PDF export was dropped`,
		`t4 "Email the admin" changed: mention SSO sign-ins`, `t5 "Add the SSO button" added (after t2)`,
		`t2 "Write the login form" flagged`, "in progress, so not changed: also show the SSO button",
		"autopilot ap-1 is paused", `start_autopilot with goalNodeId "node-0042"`, "Never call it without"} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
	if strings.Contains(note, "elsewhere") {
		t.Errorf("the note acted on a task of no goal:\n%s", note)
	}
	for _, want := range []string{"“Login page” is planned: 1 kept, 1 changed, 1 skipped, 1 added",
		"Changed:\n- Email the admin — mention SSO", "Skipped:\n- Add PDF export — the PDF export was dropped",
		"Added:\n1. Add the SSO button", "left unchanged:\n- Write the login form — in progress",
		"paused for the revision. Shall I resume it"} {
		if !strings.Contains(reply.Text, want) {
			t.Errorf("reply lacks %q:\n%s", want, reply.Text)
		}
	}
	if len(pendingFollowups(t, h)) != 0 {
		t.Fatal("the follow-up is still pending")
	}
}

// In Traditional Chinese, with no autopilot: the diff, then whether to start
// one. A write the board refuses is flagged, not reported as done.
func TestRevisionFollowupInChineseAndRefusedWrites(t *testing.T) {
	runs := revisedRuns()
	writes := &boardWrites{fail: "t3"}
	h := newHarness(t, func(opts *Options) {
		withFollowups(runs)(opts)
		writes.install(opts)
	})
	conversation := h.conversation(alice, ModeDefault)
	addRevisionFollowup(t, h, conversation, "zh-TW", "")
	run := planRun(runnerwire.RunSucceeded)
	run.Summary = replanSummary
	runs.set(run)
	h.service.RunChanged(run)
	h.service.background.Wait()
	entries := entriesOf(t, h, conversation)
	if len(entries) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
	reply := entries[1].Text
	for _, want := range []string{"「Login page」的修訂規劃完成了：保留 1、修改 1、略過 0、新增 1", "修改：\n- Email the admin",
		"Add PDF export — could not be skipped", "要開始自動推進嗎"} {
		if !strings.Contains(reply, want) {
			t.Errorf("reply lacks %q:\n%s", want, reply)
		}
	}
	if entries[0].Text != "修訂規劃完成：新增 1、修改 1、略過 0" {
		t.Errorf("shown = %q", entries[0].Text)
	}
}

// A failed replan changes nothing on the board; the follow-up says the change
// is recorded, the autopilot stays paused, and offers to try again. A
// replan that is not the conversation owner's says nothing and writes
// nothing.
func TestRevisionFollowupFailedOrNotTheOwners(t *testing.T) {
	runs := revisedRuns()
	writes := &boardWrites{}
	h := newHarness(t, func(opts *Options) {
		withFollowups(runs)(opts)
		writes.install(opts)
	})
	conversation := h.conversation(alice, ModeDefault)
	addRevisionFollowup(t, h, conversation, "en", runnerwire.AutopilotRunning)
	run := planRun(runnerwire.RunFailed)
	run.Error, run.Summary = "the agent crashed", replanSummary
	runs.set(run)
	h.service.RunChanged(run)
	h.service.background.Wait()
	if skipped, noted := writes.all(); len(skipped)+len(noted) != 0 {
		t.Fatalf("a failed replan wrote: %q %q", skipped, noted)
	}
	entries := entriesOf(t, h, conversation)
	if len(entries) != 2 || entries[0].Text != "Revision failed" ||
		!strings.Contains(entries[1].Text, "the agent crashed. The change is recorded on the goal and no task was changed; the autopilot stays paused. Shall I try again?") ||
		!strings.Contains(messageOf(t, entries[0]).Blocks[0].Text, `revise_goal with goalNodeId "node-0042"`) {
		t.Fatalf("entries = %+v", entries)
	}

	runs = revisedRuns()
	writes = &boardWrites{}
	h = newHarness(t, func(opts *Options) {
		withFollowups(runs)(opts)
		writes.install(opts)
	})
	conversation = h.conversation(alice, ModeDefault)
	addRevisionFollowup(t, h, conversation, "en", runnerwire.AutopilotRunning)
	run = planRun(runnerwire.RunSucceeded)
	run.CreatedBy, run.Summary = bob.ID, replanSummary
	runs.set(run)
	h.service.RunChanged(run)
	h.service.background.Wait()
	if skipped, noted := writes.all(); len(skipped)+len(noted) != 0 || len(entriesOf(t, h, conversation)) != 0 {
		t.Fatalf("another account's replan wrote %q %q or spoke", skipped, noted)
	}
}

// start_autopilot on a goal whose autopilot is paused resumes it with the
// revised tasks (adopt) instead of starting a second one; only its creator
// may.
func TestStartAutopilotResumesAPausedOne(t *testing.T) {
	ctx := context.Background()
	api := standardAPI()
	api.latest = `{"run":{"id":"ap-1","status":"paused","createdBy":"u-alice","policy":{"maxParallel":2,"review":{"mode":"human"}}},"tasks":[]}`
	result := runStartAutopilot(ctx, toolEnv(api), json.RawMessage(`{"goalNodeId":"node-0042","maxParallel":4}`))
	if result.IsError || !strings.Contains(result.Output, `"resumed":true`) || !strings.Contains(result.Output, "not applied") {
		t.Fatalf("result = %s", result.Output)
	}
	if api.find(http.MethodPost, "/api/autopilot/ap-1/adopt") == nil || api.find(http.MethodPost, "/api/autopilot") != nil {
		t.Fatalf("calls = %+v", api.calls())
	}

	api = standardAPI()
	api.latest = `{"run":{"id":"ap-1","status":"paused","createdBy":"u-bob","createdByName":"bob"},"tasks":[]}`
	result = runStartAutopilot(ctx, toolEnv(api), json.RawMessage(`{"goalNodeId":"node-0042"}`))
	if !result.IsError || !strings.Contains(result.Output, "started by bob") || api.find(http.MethodPost, "/api/autopilot/ap-1/adopt") != nil {
		t.Fatalf("result = %+v", result)
	}
}

func TestAppendRevision(t *testing.T) {
	first, changed := AppendRevision("---\nid: g\n---\nThe requirement.\n", "2026-10-27 09:30 UTC", "Drop PDF.")
	if !changed || first != "---\nid: g\n---\nThe requirement.\n\n## Revisions\n\n### 2026-10-27 09:30 UTC\n\nDrop PDF.\n" {
		t.Fatalf("first = %q", first)
	}
	second, changed := AppendRevision(first, "2026-10-28 10:00 UTC", "Add SSO.")
	if !changed || strings.Count(second, "## Revisions") != 1 ||
		!strings.HasSuffix(second, "Drop PDF.\n\n### 2026-10-28 10:00 UTC\n\nAdd SSO.\n") {
		t.Fatalf("second = %q", second)
	}
	if again, changed := AppendRevision(second, "2026-10-28 10:05 UTC", " Add SSO. "); changed || again != second {
		t.Fatalf("a retry recorded the change twice: %q", again)
	}
	if empty, changed := AppendRevision("", "s", "x"); !changed || empty != "## Revisions\n\n### s\n\nx\n" {
		t.Fatalf("empty = %q", empty)
	}
}
