package agents

import (
	"net/http"
	"strings"
	"testing"

	"nodevas/internal/agents"
)

// An ask about a peer's message forwards the person's decision to that
// peer as a verified approval. The answer and note it carries are for the
// asker, the peer's account and admins, as the ask listing shows them:
// another member listing the project's mail sees the decision alone.
func TestApprovalForwardShowsTheAnswerOnlyToItsParties(t *testing.T) {
	router := testRouter(t)
	heartbeat(t, router, "alice-agent", "claude:a1", "alice-bot")
	heartbeat(t, router, "bob-agent", "claude:b1", "bob-bot")
	rec := call(t, router, "bob-agent", http.MethodPost, "/api/agents/messages", `{"fromSession":"bob-bot","to":"alice-bot","message":"may I push?"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	var sent struct {
		Deliveries []agents.DeliveryStatus `json:"deliveries"`
	}
	decodeJSON(t, rec, &sent)

	ask, asked := askOverHTTP(t, router, `{"sessionId":"claude:a1","sessionName":"alice-bot","kind":"question","question":"Let bob push?",`+
		`"aboutMsgId":"`+sent.Deliveries[0].MessageID+`","timeoutSeconds":5}`)
	if rec := call(t, router, "alice", http.MethodPost, "/api/agents/asks/"+ask.ID+"/answer", `{"decision":"answer","answer":"SECRET-ANSWER","note":"SECRET-NOTE"}`); rec.Code != http.StatusOK {
		t.Fatalf("answer = %d %s", rec.Code, rec.Body)
	}
	decidedAsk(t, asked)

	for actor, sees := range map[string]bool{"alice": true, "bob": true, "root": true, "carol": false} {
		var listed struct {
			Messages []agents.Message `json:"messages"`
		}
		decodeJSON(t, call(t, router, actor, http.MethodGet, "/api/agents/messages", ""), &listed)
		var forward *agents.Message
		for i, m := range listed.Messages {
			if strings.Contains(m.Body, "<nodevas-approval") {
				forward = &listed.Messages[i]
			}
		}
		if forward == nil {
			t.Fatalf("%s does not see the forwarded decision: %+v", actor, listed.Messages)
		}
		if !strings.Contains(forward.Body, `decision="answered"`) {
			t.Errorf("%s does not see the decision: %q", actor, forward.Body)
		}
		if leaked := strings.Contains(forward.Body, "SECRET-ANSWER") || strings.Contains(forward.Body, "SECRET-NOTE"); leaked != sees {
			t.Errorf("%s sees the answer = %v, want %v: %q", actor, leaked, sees, forward.Body)
		}
	}

	// The peer's agent still reads all of it.
	rec = call(t, router, "bob-agent", http.MethodPost, "/api/agents/inbox", `{"session":"bob-bot"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "SECRET-ANSWER") || !strings.Contains(rec.Body.String(), "SECRET-NOTE") {
		t.Fatalf("bob's inbox = %d %s, want the answer and note", rec.Code, rec.Body)
	}
}
