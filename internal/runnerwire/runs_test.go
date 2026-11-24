package runnerwire

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestContinueJobShape(t *testing.T) {
	if EventUser != "user" {
		t.Fatalf("EventUser = %q", EventUser)
	}
	if DeclinePREnded != "pr_ended" || DeclinePRMoved != "pr_moved" || DeclineBranchMoved != "branch_moved" {
		t.Fatalf("decline codes = %q %q %q", DeclinePREnded, DeclinePRMoved, DeclineBranchMoved)
	}
	job := Job{
		RunID:           "r2",
		Global:          true,
		Slug:            "fix-ui",
		ContinueOf:      "r1",
		WorkRoot:        "r1",
		ContinueTask:    "make the button bigger",
		ContinueSummary: "made it 2px bigger",
		ContinuePR:      "https://github.com/org/repo/pull/42",
	}
	raw, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"continueOf":"r1"`,
		`"workRoot":"r1"`,
		`"continueTask":"make the button bigger"`,
		`"continueSummary":"made it 2px bigger"`,
		`"continuePR":"https://github.com/org/repo/pull/42"`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("Job JSON lacks %s:\n%s", want, raw)
		}
	}
	// Job carries no group field (§6.2, §10.2).
	if strings.Contains(string(raw), `"groupId"`) {
		t.Errorf("Job unexpectedly carries groupId: %s", raw)
	}
	var back Job
	if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, job) {
		t.Fatalf("round trip = %+v, %v", back, err)
	}

	// Accept carries ViewOnly; Run carries continue/group/end fields.
	acc := Accept{RunID: "r2", Base: "main", Branch: "agent/fix-ui-r1", ViewOnly: true}
	rawAcc, err := json.Marshal(acc)
	if err != nil || !strings.Contains(string(rawAcc), `"viewOnly":true`) {
		t.Fatalf("Accept = %s, %v", rawAcc, err)
	}
	run := Run{
		ID: "r2", ContinueOf: "r1", WorkRoot: "r1", ContinuedBy: "r3",
		ViewOnly: true, GroupID: "g1", ResetAt: "2026-11-18T12:00:00Z",
		EndReason: EndReasonQuietWaiting, PushRefused: true,
	}
	rawRun, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"continueOf":"r1"`, `"workRoot":"r1"`, `"continuedBy":"r3"`,
		`"viewOnly":true`, `"groupId":"g1"`, `"resetAt":"2026-11-18T12:00:00Z"`,
		`"endReason":"quiet_waiting"`, `"pushRefused":true`,
	} {
		if !strings.Contains(string(rawRun), want) {
			t.Errorf("Run JSON lacks %s:\n%s", want, rawRun)
		}
	}
	var runBack Run
	if err := json.Unmarshal(rawRun, &runBack); err != nil || !reflect.DeepEqual(runBack, run) {
		t.Fatalf("Run round trip = %+v, %v", runBack, err)
	}
}

func TestValidSessionID(t *testing.T) {
	for id, want := range map[string]bool{
		"a":                                    true,
		"0":                                    true,
		"abc-123_456.789:0":                    true,
		"550e8400-e29b-41d4-a716-446655440000": true,
		strings.Repeat("a", 128):               true,
		"":                                     false,
		strings.Repeat("a", 129):               false,
		"-abc":                                 false,
		"_abc":                                 false,
		".abc":                                 false,
		":abc":                                 false,
		"abc def":                              false,
		"abc/def":                              false,
		"abc\x00":                              false,
		"../etc/passwd":                        false,
		"sess;rm -rf /":                        false,
	} {
		if got := ValidSessionID(id); got != want {
			t.Errorf("ValidSessionID(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestRunResultPushRefusedWire(t *testing.T) {
	res := RunResult{
		Status:      RunSucceeded,
		Summary:     "done",
		EndReason:   EndReasonEnded,
		PushRefused: true,
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"endReason":"ended"`, `"pushRefused":true`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("RunResult JSON lacks %s:\n%s", want, raw)
		}
	}
	var back RunResult
	if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, res) {
		t.Fatalf("RunResult round trip = %+v, %v", back, err)
	}
	// Plain result omits endReason and pushRefused.
	plain, _ := json.Marshal(RunResult{Status: RunSucceeded})
	if strings.Contains(string(plain), "endReason") || strings.Contains(string(plain), "pushRefused") {
		t.Errorf("plain RunResult leaks optional fields: %s", plain)
	}
}
