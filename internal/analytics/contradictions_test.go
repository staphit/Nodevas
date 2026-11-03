package analytics

import (
	"context"
	"testing"
	"time"
)

// B-29: rules C2 (a person asked for changes after a reviewer pass), C3 (a
// person approved over a reviewer fail and no revise followed), C4 (a QA
// fail on the same node within 14 days of a QA pass). A QA fail is never
// contradicted.
func TestContradictionRules(t *testing.T) {
	f := newFixture(t)
	// C2
	f.pipeline("P2", "p", "n2", "approved", 1, at(9, 0))
	f.run(seedRun{id: "rv2", node: "n2", pipeline: "P2", kind: "review", role: "reviewer", created: at(9, 0), seconds: 60})
	f.verdict("rv2", "p", "n2", "P2", "reviewer", 1, "pass", "none", "", "claude", "sonnet", at(9, 1))
	f.vote("P2", 1, "human", "changes", at(9, 30))
	// C3: a person approved over a fail, and no revise came after it.
	f.pipeline("P3", "p", "n3", "approved", 1, at(10, 0))
	f.run(seedRun{id: "rv3", node: "n3", pipeline: "P3", kind: "review", role: "reviewer", created: at(10, 0), seconds: 60})
	f.verdict("rv3", "p", "n3", "P3", "reviewer", 1, "fail", "low", `["style"]`, "claude", "sonnet", at(10, 1))
	f.vote("P3", 1, "human", "approve", at(10, 30))
	// Not C3: the fail led to a revise.
	f.pipeline("P4", "p", "n4", "approved", 1, at(11, 0))
	f.run(seedRun{id: "rv4", node: "n4", pipeline: "P4", kind: "review", role: "reviewer", created: at(11, 0), seconds: 60})
	f.verdict("rv4", "p", "n4", "P4", "reviewer", 1, "fail", "low", `["style"]`, "claude", "sonnet", at(11, 1))
	f.run(seedRun{id: "fix4", node: "n4", pipeline: "P4", kind: "revise", role: "executor", created: at(11, 5), seconds: 60})
	f.vote("P4", 1, "human", "approve", at(11, 30))
	// C4: a QA pass, then a QA fail on the node 10 days later.
	day0 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	f.run(seedRun{id: "qa1", node: "goal1", nodeType: "goal", kind: "judge", role: "qa", created: day0, seconds: 60})
	f.verdict("qa1", "p", "goal1", "", "qa", 1, "pass", "none", "", "claude", "sonnet", day0)
	f.run(seedRun{id: "qa2", node: "goal1", nodeType: "goal", kind: "judge", role: "qa", created: day0.AddDate(0, 0, 10), seconds: 60})
	f.verdict("qa2", "p", "goal1", "", "qa", 2, "fail", "medium", `["criteria_unmet"]`, "claude", "sonnet", day0.AddDate(0, 0, 10))
	// Not C4: the fail came 15 days later.
	f.run(seedRun{id: "qa3", node: "goal2", nodeType: "goal", kind: "judge", role: "qa", created: day0, seconds: 60})
	f.verdict("qa3", "p", "goal2", "", "qa", 1, "pass", "none", "", "claude", "sonnet", day0)
	f.run(seedRun{id: "qa4", node: "goal2", nodeType: "goal", kind: "judge", role: "qa", created: day0.AddDate(0, 0, 15), seconds: 60})
	f.verdict("qa4", "p", "goal2", "", "qa", 2, "fail", "medium", `["criteria_unmet"]`, "claude", "sonnet", day0.AddDate(0, 0, 15))

	got, err := Contradicted(context.Background(), f.db, []string{"rv2", "rv3", "rv4", "qa1", "qa2", "qa3", "qa4"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"rv2": true, "rv3": true, "rv4": false, "qa1": true, "qa2": false, "qa3": false, "qa4": false}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("contradicted[%s] = %v, want %v", id, got[id], w)
		}
	}
}
