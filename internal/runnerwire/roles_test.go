package runnerwire

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestJobRoleFieldsRoundTrip(t *testing.T) {
	job := Job{RunID: "r1", Kind: JobVerify, Role: "tester", Tools: []string{"get_node", "ask_user"},
		TestFiles: []string{"internal/x/x_test.go"}, Acceptance: []AcceptanceItem{{ID: "a1", Text: "returns 200"}},
		Verify: &VerifySpec{Phase: VerifyRed, Head: "abc123"}}
	raw, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"role":"tester"`, `"tools":["get_node","ask_user"]`, `"testFiles"`, `"acceptance":[{"id":"a1"`, `"verify":{"phase":"red","head":"abc123"}`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("job JSON lacks %s: %s", key, raw)
		}
	}
	var back Job
	if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, job) {
		t.Fatalf("round trip = %+v, %v", back, err)
	}
	result := RunResult{Status: RunSucceeded, Verify: &VerifyResult{Phase: VerifyGreen, ExitCode: 0, Passed: true},
		Checks: []CheckResult{{ID: "a1", Pass: false, Note: "missing 404"}}, GuardAck: true}
	raw, _ = json.Marshal(result)
	var backResult RunResult
	if err := json.Unmarshal(raw, &backResult); err != nil || !reflect.DeepEqual(backResult, result) {
		t.Fatalf("result round trip = %+v, %v", backResult, err)
	}
}

// An old hub's job (no role fields) decodes as a legacy job, and a legacy
// job encodes without them, so an old runner sees exactly what it did.
func TestOldJobDecodesWithoutRole(t *testing.T) {
	var job Job
	if err := json.Unmarshal([]byte(`{"runId":"r","project":"p","nodeId":"n","nodeTitle":"t","prompt":"x","agent":"claude","model":"","repoId":"repo","requestedBy":"bea"}`), &job); err != nil {
		t.Fatal(err)
	}
	if job.Role != "" || job.Tools != nil || job.Verify != nil || job.TestFiles != nil {
		t.Fatalf("old job = %+v", job)
	}
	raw, _ := json.Marshal(Job{RunID: "r"})
	for _, key := range []string{"role", "tools", "testFiles", "acceptance", "verify"} {
		if strings.Contains(string(raw), `"`+key+`"`) {
			t.Errorf("legacy job JSON carries %s: %s", key, raw)
		}
	}
	raw, _ = json.Marshal(Repo{ID: "r", Name: "n"})
	if strings.Contains(string(raw), "verify") {
		t.Errorf("repo without a test command says verify: %s", raw)
	}
}

func TestGuardReportViolation(t *testing.T) {
	if (GuardReport{TestFiles: []string{"a_test.go"}}).Violation() {
		t.Fatal("no touched files is a violation")
	}
	if !(GuardReport{Touched: []string{"a_test.go"}}).Violation() {
		t.Fatal("a touched test file is not a violation")
	}
	if !(GuardReport{Sensitive: []string{"package.json"}}).Violation() {
		t.Fatal("a sensitive path is not a violation")
	}
}

func TestRunResultClampsRoleFields(t *testing.T) {
	checks := make([]CheckResult, MaxCheckResults+5)
	for i := range checks {
		checks[i] = CheckResult{ID: "a1", Note: strings.Repeat("n", MaxCheckNote+50)}
	}
	r := RunResult{Checks: checks, Verify: &VerifyResult{Output: strings.Repeat("x", MaxVerifyOutput) + "TAIL"}}
	r.ClampRoleFields()
	if len(r.Checks) != MaxCheckResults {
		t.Fatalf("checks = %d", len(r.Checks))
	}
	if len(r.Checks[0].Note) > MaxCheckNote {
		t.Fatalf("note = %d bytes", len(r.Checks[0].Note))
	}
	if len(r.Verify.Output) > MaxVerifyOutput || !strings.HasSuffix(r.Verify.Output, "TAIL") {
		t.Fatalf("output = %d bytes, tail kept = %v", len(r.Verify.Output), strings.HasSuffix(r.Verify.Output, "TAIL"))
	}
}
