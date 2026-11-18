package runs

import (
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
)

// A draft plan run (P6) is only ever the copilot's start_breakdown: a
// person's plain plan run, replayed in-process by a copilot server tool.
func TestStartDraftOnlyForAPersonsPlainPlanRun(t *testing.T) {
	f := newRoleFixture(t, true)
	asCopilot := func(edit func(*StartRequest)) func(*StartRequest) {
		return func(req *StartRequest) {
			req.Body.Kind, req.Body.Draft = runnerwire.JobPlan, true
			req.Origin, req.FromCopilot = runnerwire.OriginPerson, true
			if edit != nil {
				edit(req)
			}
		}
	}
	refused := []struct {
		name string
		wf   *engine.Workflow
		edit func(*StartRequest)
		says string
	}{
		{"implement", nil, func(req *StartRequest) { req.Body.Kind = runnerwire.JobImplement }, "draft is only"},
		{"replan", nil, func(req *StartRequest) { req.Body.Revision = "the goal changed" }, "draft is only"},
		{"autopilot", nil, func(req *StartRequest) { req.Origin = runnerwire.OriginAutopilot }, "draft is only"},
		{"not from the copilot", nil, func(req *StartRequest) { req.FromCopilot = false }, "draft is only"},
		{"qa role", lightStages(), func(req *StartRequest) { req.Body.Role = engine.RoleQA }, "acts as the planner"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.roleStart(t, tc.wf, criteria("it works"), asCopilot(tc.edit))
			wantStartRefusal(t, err, StartInvalid, tc.says)
		})
	}
	for _, tc := range []struct {
		name string
		wf   *engine.Workflow
		role string
	}{{"no role", nil, ""}, {"default planner", lightStages(), ""}, {"planner", lightStages(), engine.RolePlanner}} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := f.roleStart(t, tc.wf, nil, asCopilot(func(req *StartRequest) { req.Body.Role = tc.role }))
			if err != nil {
				t.Fatal(err)
			}
			if !r.Draft || r.Kind != runnerwire.JobPlan || !r.Job().Draft {
				t.Fatalf("run = %+v", r.Run)
			}
			if tc.wf != nil && r.Role != engine.RolePlanner {
				t.Fatalf("role = %q", r.Role)
			}
		})
	}
	// Without draft nothing changes.
	r, err := f.roleStart(t, nil, nil, func(req *StartRequest) { req.Body.Kind = runnerwire.JobPlan })
	if err != nil || r.Draft {
		t.Fatalf("plain plan run = %+v, %v", r.Run, err)
	}
}
