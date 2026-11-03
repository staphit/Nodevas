package runs

import (
	"context"
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
)

type evidenceCall struct{ project, node, runID, kind, outcome, summary string }

// B-10: every pipeline evidence event is a run_evidence row carrying its
// run and pipeline, with an outcome, and the journal note names the run.
func TestEvidenceRowsCarryTheirRun(t *testing.T) {
	ctx := context.Background()
	f := newRoleFixture(t, true)
	var calls []evidenceCall
	f.d.SetEvidence(func(project, nodeID, runID, kind, outcome, summary string) {
		calls = append(calls, evidenceCall{project, nodeID, runID, kind, outcome, summary})
	})
	wf := &engine.Workflow{Preset: engine.PresetCustom, Stages: engine.Stages{Tester: true, CI: true}, MaxGreenTries: 3, MaxTesterRounds: 2}
	first := f.stagedStart(t, wf, nil)
	verify := f.complete(t, first, stepOutcome{files: []runnerwire.FileChange{testFile("pkg/x_test.go")}, head: head1})
	exec := f.complete(t, verify, stepOutcome{result: red()})

	rows, err := f.s.EvidenceForRun(ctx, verify.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Kind != runnerwire.PhaseVerifyRed || rows[0].Outcome != EvidencePass ||
		rows[0].RunID != verify.ID || rows[0].PipelineID != first.PipelineID || rows[0].NodeID != "n1" || rows[0].At == "" {
		t.Fatalf("verify red evidence = %+v", rows)
	}
	if len(calls) == 0 || calls[0].runID != verify.ID || calls[0].outcome != EvidencePass ||
		!strings.Contains(calls[0].summary, "(run "+verify.ID[:8]+")") {
		t.Fatalf("journal hook = %+v, want the run named in the note", calls)
	}

	green := f.complete(t, exec, stepOutcome{files: []runnerwire.FileChange{codeFile("pkg/x.go", 3)}, head: head2})
	f.complete(t, green, stepOutcome{result: runnerwire.RunResult{Verify: &runnerwire.VerifyResult{Phase: runnerwire.VerifyGreen, Passed: true}}})
	_ = f.d.PR(ctx, f.runner.ID, exec.ID, runnerwire.PRInfo{URL: "https://github.com/o/r/pull/7", Number: 7, State: "open", CI: runnerwire.CIFailure, HeadSHA: head2})
	rows, err = f.s.EvidenceForRun(ctx, exec.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ci *Evidence
	for i := range rows {
		if rows[i].Kind == runnerwire.PhaseCI {
			ci = &rows[i]
		}
	}
	if ci == nil || ci.Outcome != EvidenceFail || ci.PipelineID != first.PipelineID {
		t.Fatalf("ci evidence on the executor run = %+v", rows)
	}
}
