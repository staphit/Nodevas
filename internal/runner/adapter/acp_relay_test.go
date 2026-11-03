package adapter

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

// waitSent waits until the runner has written a message with JSON-RPC id id
// to the agent's stdin, and returns it.
func waitSent(t *testing.T, stdin *recordingStdin, id float64) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		stdin.mu.Lock()
		text := stdin.buf.String()
		stdin.mu.Unlock()
		for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
			var m map[string]any
			if json.Unmarshal([]byte(line), &m) == nil && m["id"] == id && m["method"] == nil {
				return m
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no answer to request %v", id)
	return nil
}

// sentTo reports whether anything answering id was written yet.
func sentTo(stdin *recordingStdin, id string) bool {
	stdin.mu.Lock()
	defer stdin.mu.Unlock()
	return strings.Contains(stdin.buf.String(), `"id":`+id+`,`)
}

const acpPermissionRequest = `{"jsonrpc":"2.0","id":3,"method":"session/request_permission","params":{"sessionId":"s1",` +
	`"toolCall":{"toolCallId":"c1","title":"git status","kind":"execute","rawInput":{"command":"git status"}},` +
	`"options":[{"optionId":"once","kind":"allow_once","name":"Allow"},{"optionId":"always","kind":"allow_always","name":"Always"},` +
	`{"optionId":"no","kind":"reject_once","name":"Reject"}]}}`

func relaySession(asker PermissionAsker, job runnerwire.Job, mode string) (Conversation, *recordingStdin) {
	conv := OpenCodeACP{}.NewConversation(job, RunEnv{Workdir: `C:\work\repo`, Branch: "agent/n-42",
		PermissionMode: mode, PermissionRelay: true, Permissions: asker})
	stdin := newRecordingStdin()
	conv.Begin(stdin)
	conv.Parse([]byte(`{"jsonrpc":"2.0","id":0,"result":{"protocolVersion":1}}`))
	conv.Parse([]byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":` +
		`{"sessionUpdate":"tool_call","toolCallId":"c1","title":"bash","kind":"execute","status":"pending","rawInput":{}}}}`))
	return conv, stdin
}

// With permissionRelay the runner asks the hub, without holding up the
// agent's stdout, and answers the parked request with the person's choice.
func TestACPSessionRelaysPermission(t *testing.T) {
	job := runnerwire.Job{RunID: "run-1", NodeID: "n-42", Prompt: "x"}
	for _, tc := range []struct {
		name     string
		decision PermissionDecision
		option   string
	}{
		{"allow", PermissionDecision{Allow: true}, "once"},
		// allow_always lets OpenCode remember its own rule for the tool, so
		// only the hub's whole-tool scope gets it; one command, one call, or
		// a scope the hub did not name is allow_once.
		{"allow the tool for the run", PermissionDecision{Allow: true, ForRun: true, RunScope: "tool"}, "always"},
		{"allow the command for the run", PermissionDecision{Allow: true, ForRun: true, RunScope: "command"}, "once"},
		{"allow for the run, once", PermissionDecision{Allow: true, ForRun: true, RunScope: "once"}, "once"},
		{"allow for the run, no scope", PermissionDecision{Allow: true, ForRun: true}, "once"},
		{"allow for the run, unknown scope", PermissionDecision{Allow: true, ForRun: true, RunScope: "everything"}, "once"},
		{"deny", PermissionDecision{Reason: "no"}, "no"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asker := &fakeAsker{decision: tc.decision, wait: make(chan struct{})}
			conv, stdin := relaySession(asker, job, "")
			parsed := make(chan []runnerwire.RunEvent, 1)
			go func() { parsed <- conv.Parse([]byte(acpPermissionRequest)) }()
			var events []runnerwire.RunEvent
			select {
			case events = <-parsed:
			case <-time.After(2 * time.Second):
				t.Fatal("Parse waited for the person: the reader is blocked")
			}
			if !strings.Contains(eventsText(events), "Nodevas") {
				t.Errorf("events = %+v", events)
			}
			if sentTo(stdin, "3") {
				t.Fatal("answered before the person did")
			}
			close(asker.wait)
			answer := waitSent(t, stdin, 3)
			outcome := answer["result"].(map[string]any)["outcome"].(map[string]any)
			if outcome["outcome"] != "selected" || outcome["optionId"] != tc.option {
				t.Errorf("answer = %v, want option %s", answer, tc.option)
			}
			asked := asker.requests()
			if len(asked) != 1 || asked[0].Tool != "bash" || !strings.Contains(asked[0].Input, "git status") {
				t.Errorf("asked %+v", asked)
			}
			// The decision shows on the next line the agent sends.
			later := conv.Parse([]byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":` +
				`{"sessionUpdate":"plan","entries":[]}}}`))
			if !strings.Contains(eventsText(later), "git status") {
				t.Errorf("no decision event in %+v", later)
			}
			conv.Parse([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":1,"message":"stop"}}`))
			stdin.messages(t)
		})
	}
}

// A session that ends while the person has not answered stops waiting, and
// nothing is written to the closed stdin.
func TestACPSessionRelayEndsWithTheRun(t *testing.T) {
	asker := &fakeAsker{decision: PermissionDecision{Allow: true}, wait: make(chan struct{})}
	conv, stdin := relaySession(asker, runnerwire.Job{RunID: "run-1", NodeID: "n-42", Prompt: "x"}, "")
	conv.Parse([]byte(acpPermissionRequest))
	ender, ok := conv.(interface{ End() })
	if !ok {
		t.Fatal("the ACP session cannot be ended by the runner")
	}
	ender.End()
	sent := stdin.messages(t)
	for _, m := range sent {
		if m["id"] == float64(3) {
			t.Errorf("answered after the end: %v", m)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(asker.requests()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	close(asker.wait) // no-op for an ask already cancelled
}

// A read-only run, the owner's read-only setting or relay off: refused as
// before, nobody asked.
func TestACPSessionRelayNotApplicable(t *testing.T) {
	review := runnerwire.Job{RunID: "run-1", NodeID: "n-42", Prompt: "x", Kind: runnerwire.JobReview}
	for name, conv := range map[string]func(*fakeAsker) (Conversation, *recordingStdin){
		"review": func(a *fakeAsker) (Conversation, *recordingStdin) { return relaySession(a, review, "") },
		"owner read-only": func(a *fakeAsker) (Conversation, *recordingStdin) {
			return relaySession(a, runnerwire.Job{RunID: "run-1", Prompt: "x"}, "read-only")
		},
	} {
		asker := &fakeAsker{decision: PermissionDecision{Allow: true}}
		c, stdin := conv(asker)
		c.Parse([]byte(acpPermissionRequest))
		answer := waitSent(t, stdin, 3)
		if got := answer["result"].(map[string]any)["outcome"].(map[string]any)["optionId"]; got != "no" {
			t.Errorf("%s: answer %v", name, answer)
		}
		if len(asker.requests()) != 0 {
			t.Errorf("%s: asked", name)
		}
	}
}

// With the relay OpenCode's shell asks instead of being denied, in edit
// only: read-only runs and the owner's read-only setting keep the deny, and
// the web stays denied.
func TestOpenCodeACPCommandRelay(t *testing.T) {
	job := runnerwire.Job{RunID: "run-1", NodeID: "n-42", Prompt: "x"}
	for _, tc := range []struct {
		name  string
		mode  string
		relay bool
		job   runnerwire.Job
		bash  string
	}{
		{"off", "edit", false, job, "deny"},
		{"on", "edit", true, job, "ask"},
		{"on default", "", true, job, "ask"},
		{"owner read-only", "read-only", true, job, "deny"},
		{"review", "edit", true, runnerwire.Job{RunID: "run-1", Prompt: "x", Kind: runnerwire.JobReview}, "deny"},
		{"full", "full", true, job, "allow"},
	} {
		env := RunEnv{Binary: "/usr/bin/opencode", Workdir: "/w", MCP: MCPServer{Command: "/opt/nodevas", Args: []string{"mcp"}},
			PermissionMode: tc.mode, PermissionRelay: tc.relay}
		cmd, err := OpenCodeACP{}.Command(tc.job, env)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := envValue(cmd.Env, openCodeConfigEnv)
		var config struct {
			Permission map[string]string `json:"permission"`
		}
		if err := json.Unmarshal([]byte(raw), &config); err != nil {
			t.Fatal(err)
		}
		if config.Permission["bash"] != tc.bash || (tc.mode != "full" && config.Permission["webfetch"] != "deny") {
			t.Errorf("%s: permission = %v", tc.name, config.Permission)
		}
	}
}

func eventsText(events []runnerwire.RunEvent) string {
	var parts []string
	for _, ev := range events {
		parts = append(parts, ev.Text)
	}
	return strings.Join(parts, "\n")
}
