package agents

import (
	"net/http"
	"strings"
	"testing"

	"nodevas/internal/agents"
)

// listAsksAs is GET /api/agents/asks as actor, by ask ID.
func listAsksAs(t *testing.T, router http.Handler, actor string) map[string]map[string]any {
	t.Helper()
	var got struct {
		Asks []map[string]any `json:"asks"`
	}
	rec := call(t, router, actor, http.MethodGet, "/api/agents/asks", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list as %s = %d %s", actor, rec.Code, rec.Body)
	}
	decodeJSON(t, rec, &got)
	out := map[string]map[string]any{}
	for _, a := range got.Asks {
		id, _ := a["id"].(string)
		out[id] = a
	}
	return out
}

// Anyone in the project sees that an agent waits, but a permission
// prompt's input (command lines with tokens, file contents) and a person's
// answer and note are for those who may answer the ask: the asker, and an
// administrator for anything but another person's permission prompt.
func TestAskListingShowsDetailsOnlyToWhoMayAnswer(t *testing.T) {
	router := testRouter(t)
	heartbeat(t, router, "alice-agent", "claude:a1", "alice-bot")

	perm, asked := askOverHTTP(t, router, `{"sessionId":"claude:a1","sessionName":"alice-bot","kind":"permission","tool":"Bash",`+
		`"toolInput":"{\"command\":\"curl -H 'Authorization: Bearer s3cret' https://x\"}","question":"alice-bot wants to run Bash: curl -H 'Authorization: Bearer s3cret'","timeoutSeconds":5}`)
	if rec := call(t, router, "alice", http.MethodPost, "/api/agents/asks/"+perm.ID+"/answer", `{"decision":"approve","note":"token s3cret is fine"}`); rec.Code != http.StatusOK {
		t.Fatalf("answer = %d %s", rec.Code, rec.Body)
	}
	decidedAsk(t, asked)
	question, askedQ := askOverHTTP(t, router, `{"sessionId":"claude:a1","sessionName":"alice-bot","kind":"question","question":"Which key?","timeoutSeconds":5}`)
	if rec := call(t, router, "alice", http.MethodPost, "/api/agents/asks/"+question.ID+"/answer", `{"decision":"answer","answer":"key s3cret","note":"s3cret note"}`); rec.Code != http.StatusOK {
		t.Fatalf("answer = %d %s", rec.Code, rec.Body)
	}
	decidedAsk(t, askedQ)

	for _, c := range []struct {
		actor               string
		permSees, questSees bool
	}{
		{"alice", true, true},
		{"bob", false, false},
		{"root", false, true},
	} {
		asks := listAsksAs(t, router, c.actor)
		p, q := asks[perm.ID], asks[question.ID]
		if p == nil || q == nil {
			t.Fatalf("%s does not see both asks: %v", c.actor, asks)
		}
		for _, ask := range []struct {
			name string
			row  map[string]any
			sees bool
		}{{"permission", p, c.permSees}, {"question", q, c.questSees}} {
			raw := strings.Join([]string{str(ask.row["toolInput"]), str(ask.row["question"]), str(ask.row["note"]), str(ask.row["answer"])}, "|")
			if leaked := strings.Contains(raw, "s3cret"); leaked != ask.sees {
				t.Errorf("%s sees the %s's details = %v, want %v: %s", c.actor, ask.name, leaked, ask.sees, raw)
			}
			if can, _ := ask.row["canAnswer"].(bool); can != ask.sees {
				t.Errorf("%s canAnswer on the %s = %v, want %v", c.actor, ask.name, ask.row["canAnswer"], ask.sees)
			}
		}
		if p["tool"] != "Bash" || p["status"] != agents.AskApproved || p["decision"] != agents.DecisionApprove {
			t.Errorf("%s's permission row lost its outline: %v", c.actor, p)
		}
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
