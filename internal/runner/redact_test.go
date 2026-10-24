package runner

import (
	"context"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

// A key the agent printed or read never leaves in a run's events or result.
func TestRunOutputIsRedacted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	j := newJobs(ctx, &Client{Config: &Config{}})
	key := "sk-ant-api03-" + strings.Repeat("Zx9", 12)
	token := "ghp_" + strings.Repeat("a", 36)
	r := &run{job: testJob("run-r", "ok:")}
	j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventLog, Tool: "Bash", Input: "echo " + key, Text: "found " + token})
	j.finishWith(r, runnerwire.RunResult{Status: runnerwire.RunFailed, Summary: "the key is " + key, Error: "push: " + token},
		nil, &runnerwire.PRInfo{Error: "gh said " + token})
	j.mu.Lock()
	defer j.mu.Unlock()
	all := r.events[0].Text + r.events[0].Input + r.finish.Summary + r.finish.Error + r.pr.Error
	if strings.Contains(all, key) || strings.Contains(all, token) || strings.Count(all, "[redacted]") != 5 {
		t.Fatalf("not redacted: %q", all)
	}
	if msg := commitMessage("done; used "+key, testJob("run-r", "ok:")); strings.Contains(msg, key) {
		t.Fatalf("commit message: %q", msg)
	}
}
