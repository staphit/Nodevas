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

// An agent that printed the owner's environment: the values of its secret
// variables and a password in a URL stay on this machine.
func TestRunOutputRedactsEnvironmentDumps(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	j := newJobs(ctx, &Client{Config: &Config{}})
	dump := "GEMINI_API_KEY=AIzaSyA1b2C3d4E5f6G7h8I9j0KlMnOpQrStUvW\nAWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG\n" +
		"DATABASE_URL=postgres://u:s3cr3t@db/x"
	r := &run{job: testJob("run-env", "ok:")}
	j.emit(r, runnerwire.RunEvent{Type: runnerwire.EventToolResult, Tool: "Bash", Input: "env", Text: dump})
	j.finishWith(r, runnerwire.RunResult{Status: runnerwire.RunFailed, Summary: dump, Error: dump}, nil, nil)
	j.mu.Lock()
	defer j.mu.Unlock()
	all := r.events[0].Text + r.finish.Summary + r.finish.Error
	for _, secret := range []string{"AIzaSy", "wJalrXUtnFEMI", "s3cr3t"} {
		if strings.Contains(all, secret) {
			t.Errorf("%q left the machine: %q", secret, all)
		}
	}
}

// The other ways a run's text leaves the machine: the commit the runner
// pushes (its body is the agent's summary), a verify job's test output, a
// judge's CHECK notes and the handoff note.
func TestRunOutputRedactsEveryResultText(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	j := newJobs(ctx, &Client{Config: &Config{}})
	secrets := []string{"wJalrXUtnFEMI", "hunter2xyz"}
	dump := "AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG\nDB_PASSWORD=hunter2xyz"
	msg := commitMessage("done\n\n"+dump, testJob("run-c", "ok:"))
	r := &run{job: testJob("run-v", "ok:")}
	j.finishWith(r, runnerwire.RunResult{Status: runnerwire.RunSucceeded,
		Verify:      &runnerwire.VerifyResult{Phase: runnerwire.VerifyRed, Output: "env\n" + dump},
		Checks:      parseChecks("CHECK a1: fail — " + strings.ReplaceAll(dump, "\n", " / ")),
		HandoffNote: dump,
	}, nil, nil)
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(r.finish.Checks) != 1 || r.finish.Verify == nil {
		t.Fatalf("finish = %+v", r.finish)
	}
	for what, text := range map[string]string{"commit message": msg, "verify output": r.finish.Verify.Output,
		"check note": r.finish.Checks[0].Note, "handoff note": r.finish.HandoffNote} {
		for _, secret := range secrets {
			if strings.Contains(text, secret) {
				t.Errorf("%s: %q left the machine: %q", what, secret, text)
			}
		}
	}
}
