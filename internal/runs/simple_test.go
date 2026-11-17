package runs

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
)

// Simple mode's instruction (easy start, D5): a fixed server constant the
// request's simple flag appends to an implement run's extra instructions. It
// lifts no gate.

func TestSimpleInstructionText(t *testing.T) {
	want := "The person who asked for this is not a programmer.\n" +
		"- Unless the task says otherwise, make something that opens directly in a web browser, with no build or install step: one index.html at the top of the folder, with its styles and scripts inside or next to it.\n" +
		"- When you finish, say in plain words what you made and how to open it. Use no technical terms.\n" +
		"- Answer in the language the task is written in."
	if SimpleInstruction != want {
		t.Fatalf("SimpleInstruction = %q", SimpleInstruction)
	}
}

func TestWithSimple(t *testing.T) {
	if got := withSimple("x", true); got != "x\n\n"+SimpleInstruction {
		t.Fatalf("withSimple(x) = %q", got)
	}
	if got := withSimple("", true); got != SimpleInstruction {
		t.Fatalf("withSimple(\"\") = %q", got)
	}
	if got := withSimple("  x \n", true); got != "x\n\n"+SimpleInstruction {
		t.Fatalf("withSimple(spaced) = %q", got)
	}
	for _, in := range []string{"", "x", "  x \n"} {
		if got := withSimple(in, false); got != in {
			t.Fatalf("withSimple(%q, false) = %q", in, got)
		}
	}
}

func TestStartSimpleAppendsInstruction(t *testing.T) {
	f := newRoleFixture(t, true)
	r, err := f.roleStart(t, nil, nil, func(req *StartRequest) {
		req.Body.Prompt, req.Body.Simple = "Use blue.", true
	})
	if err != nil {
		t.Fatal(err)
	}
	want := BuildPrompt("Title", "Do the thing.", "", "Use blue.\n\n"+SimpleInstruction)
	if r.JobPrompt != want || !strings.HasSuffix(r.JobPrompt, "Use blue.\n\n"+SimpleInstruction) {
		t.Fatalf("job prompt = %q", r.JobPrompt)
	}
	// The dispatcher's own prompt is recorded as typed: the instruction is
	// not part of the task.
	if r.Prompt != "Use blue." {
		t.Fatalf("recorded prompt = %q", r.Prompt)
	}
	if job := f.conn.last(t, r.ID); !strings.HasSuffix(job.Prompt, SimpleInstruction) {
		t.Fatalf("offered prompt = %q", job.Prompt)
	}
	// With no prompt of its own the instruction stands alone.
	r, err = f.roleStart(t, nil, nil, func(req *StartRequest) { req.Body.Simple = true })
	if err != nil || r.JobPrompt != BuildPrompt("Title", "Do the thing.", "", SimpleInstruction) {
		t.Fatalf("job prompt = %q, %v", r.JobPrompt, err)
	}
}

func TestStartWithoutSimpleUnchanged(t *testing.T) {
	f := newRoleFixture(t, true)
	r, err := f.roleStart(t, nil, nil, func(req *StartRequest) { req.Body.Prompt = "Use blue." })
	if err != nil {
		t.Fatal(err)
	}
	if want := BuildPrompt("Title", "Do the thing.", "", "Use blue."); r.JobPrompt != want {
		t.Fatalf("job prompt = %q, want %q", r.JobPrompt, want)
	}
	if strings.Contains(r.JobPrompt, "not a programmer") {
		t.Fatalf("job prompt carries the simple instruction: %q", r.JobPrompt)
	}
}

func TestStartSimplePlanAppendsNothing(t *testing.T) {
	f := newRoleFixture(t, true)
	r, err := f.roleStart(t, nil, nil, func(req *StartRequest) {
		req.Body.Kind, req.Body.Simple, req.Body.Prompt = runnerwire.JobPlan, true, "Small steps."
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := BuildPrompt("Title", "Do the thing.", "", "Small steps."); r.JobPrompt != want {
		t.Fatalf("plan job prompt = %q", r.JobPrompt)
	}
}

func TestStartSimpleLiftsNoGate(t *testing.T) {
	simple := func(req *StartRequest) { req.Body.Simple = true }
	// No criteria on a Standard project: still ACCEPTANCE_REQUIRED.
	f := newRoleFixture(t, true)
	_, err := f.roleStart(t, standardStages(), nil, simple)
	if se := wantStartRefusal(t, err, StartPrecondition, "acceptance"); se.Reason != CodeAcceptanceRequired {
		t.Fatalf("reason = %q", se.Reason)
	}
	// No test command: still refused.
	f = newRoleFixture(t, false)
	_, err = f.roleStart(t, standardStages(), criteria("x"), simple)
	wantStartRefusal(t, err, StartPrecondition, "nodevas runner repo set")
	// An override with a role, or with a pipeline, is still invalid.
	f = newRoleFixture(t, true)
	_, err = f.roleStart(t, standardStages(), criteria("x"), func(req *StartRequest) {
		req.Body.Simple, req.Body.Override, req.Body.Role = true, "Simple mode: started without checks", engine.RoleExecutor
	})
	wantStartRefusal(t, err, StartInvalid, "override")
	_, err = f.roleStart(t, standardStages(), criteria("x"), func(req *StartRequest) {
		req.Body.Simple, req.Body.Override, req.Body.Pipeline = true, "Simple mode: started without checks", stagedPipeline()
	})
	wantStartRefusal(t, err, StartInvalid, "override")
	// With the override alone it goes, as one plain implement run that
	// carries the instruction.
	r, err := f.roleStart(t, standardStages(), nil, func(req *StartRequest) {
		req.Body.Simple, req.Body.Override = true, "Simple mode: started without checks"
	})
	if err != nil || r.PipelineID != "" || !strings.HasSuffix(r.JobPrompt, SimpleInstruction) {
		t.Fatalf("override run = %+v, %v", r.Run, err)
	}
}

func TestStartGlobalSimple(t *testing.T) {
	f, log := newGlobalFixture(t, globalHello())
	ctx := context.Background()
	req := f.globalRequest("Make a birthday page\nFor my sister, with balloons.")
	req.Body.Simple = true
	r, err := f.d.StartGlobal(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if want := BuildPrompt("Make a birthday page", "For my sister, with balloons.", "", SimpleInstruction); r.JobPrompt != want {
		t.Fatalf("job prompt = %q", r.JobPrompt)
	}
	plain, err := f.d.StartGlobal(ctx, f.globalRequest("Make a birthday page"))
	if err != nil || plain.JobPrompt != BuildPrompt("Make a birthday page", "", "", "") {
		t.Fatalf("plain job prompt = %q, %v", plain.JobPrompt, err)
	}
	if len(log.audited) != 2 {
		t.Fatalf("audit = %+v", log.audited)
	}
	entry := log.audited[0]
	detail := fmt.Sprint(entry.Detail)
	// The slug comes from the title, as for any global run; the task's text
	// and the instruction never reach the audit.
	if entry.Action != "run.global.start" || entry.Detail["simple"] != true ||
		strings.Contains(detail, "balloons") || strings.Contains(detail, "Make a") || strings.Contains(detail, "programmer") {
		t.Fatalf("audit entry = %+v", entry)
	}
	if log.audited[1].Detail["simple"] != false {
		t.Fatalf("plain audit = %+v", log.audited[1])
	}
}
