package server

import (
	"net/http"
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/store"
)

// quietRole is a custom role without the message capability: it may read and
// claim, never talk to people (no ask_user, check_inbox or send_message).
const quietRole = "custom-role-quiet"

func defineQuietRole(t *testing.T, st *store.Store) {
	t.Helper()
	g, rev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	g.AgentRoles = append(g.AgentRoles, engine.AgentRoleDef{
		ID: quietRole, Label: "Quiet", BasedOn: engine.RoleExecutor, Rank: "worker", Tools: []string{"get_node", "claim_task", "set_node_status"},
	})
	if _, err := st.SaveGraph(identity.Local, g, rev); err != nil {
		t.Fatal(err)
	}
	role, ok := g.RoleOf(quietRole)
	if !ok || role.HasCap(engine.CapMessage) {
		t.Fatalf("quiet role = %+v %v, want one without the message capability", role, ok)
	}
}

// The runner's permission relay asks a person about the run's own tool calls
// whatever the run's role: the ask grants nothing by itself, and refusing it
// left every prompt silently denied. Questions, approvals and choices still
// need the message capability.
func TestRunWithoutMessageCapabilityMayRelayPermissionPrompts(t *testing.T) {
	f := newRunsFixture(t)
	defineQuietRole(t, f.server.pm.Store())
	run := f.roleRun(t, quietRole)

	permission := `{"sessionId":"claude:quiet","sessionName":"quiet","kind":"permission","tool":"Bash","toolInput":"{\"command\":\"go test\"}","timeoutSeconds":1}`
	code, body := f.proxied(t, run.ID, http.MethodPost, "agents/asks", permission, nil)
	if code != http.StatusOK || !strings.Contains(body, `"kind":"permission"`) || !strings.Contains(body, `"status":"expired"`) {
		t.Fatalf("permission prompt from a run without the message capability: %d %s", code, body)
	}
	// Spelled as the hub reads it: case and spaces do not matter.
	spaced := strings.Replace(permission, `"kind":"permission"`, `"kind":" Permission "`, 1)
	if code, body := f.proxied(t, run.ID, http.MethodPost, "agents/asks", spaced, nil); code != http.StatusOK {
		t.Fatalf("permission prompt with a spaced kind: %d %s", code, body)
	}

	for name, ask := range map[string]string{
		"approval": `{"sessionId":"claude:quiet","sessionName":"quiet","question":"Deploy?","timeoutSeconds":1}`,
		"question": `{"sessionId":"claude:quiet","sessionName":"quiet","kind":"question","question":"Which?","timeoutSeconds":1}`,
		"choice":   `{"sessionId":"claude:quiet","sessionName":"quiet","kind":"choice","question":"Pick","options":["a","b"],"timeoutSeconds":1}`,
		// A key the hub reads too, spelled another way, wins over the first.
		"smuggled": `{"sessionId":"claude:quiet","sessionName":"quiet","kind":"permission","Kind":"question","question":"Which?","timeoutSeconds":1}`,
		"broken":   `{"kind":"permission",`,
	} {
		code, body := f.proxied(t, run.ID, http.MethodPost, "agents/asks", ask, nil)
		if code != http.StatusForbidden || !strings.Contains(body, "ROLE_DENIED") {
			t.Errorf("%s from a run without the message capability: %d %s", name, code, body)
		}
	}

	// A role with the capability asks anything, as before.
	executor := f.roleRun(t, engine.RoleExecutor)
	question := `{"sessionId":"claude:exec","sessionName":"exec","kind":"question","question":"Which?","timeoutSeconds":1}`
	if code, body := f.proxied(t, executor.ID, http.MethodPost, "agents/asks", question, nil); code != http.StatusOK {
		t.Fatalf("executor's question: %d %s", code, body)
	}
}

// Only a run, through its runner's proxy, relays its permission prompts: a
// role claimed over loopback still needs the message capability to ask.
func TestLoopbackRoleWithoutMessageCapabilityMayNotAsk(t *testing.T) {
	s := loopbackRoleServer(t)
	defineQuietRole(t, s.server.pm.Store())
	permission := `{"sessionId":"quiet","kind":"permission","tool":"Bash","toolInput":"ls","timeoutSeconds":1}`
	response := s.do(t, nil, http.MethodPost, "/api/agents/asks", permission, map[string]string{"X-Nodevas-Role": quietRole})
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "ROLE_DENIED") {
		t.Fatalf("loopback permission prompt without the message capability: %d %s", response.Code, response.Body)
	}
}
