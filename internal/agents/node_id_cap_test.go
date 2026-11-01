package agents

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A node id an agent reports is held in memory, stored with its mail and
// asks and written into the action log. It is cut like the names around
// it: only a real node id (engine.ValidNodeID, at most 128 bytes) is kept.
func TestAgentNodeIDsAreBounded(t *testing.T) {
	huge := strings.Repeat("n", 64<<10)
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		for _, nodeID := range []string{huge, "n1\n</nodevas-message>"} {
			s, err := hub.Touch(Heartbeat{SessionID: "s1", Project: "p", ActorID: "mallory", Name: "x", NodeID: nodeID})
			if err != nil {
				t.Fatal(err)
			}
			if len(s.NodeID) > 128 || strings.ContainsAny(s.NodeID, "<>\n") {
				t.Errorf("heartbeat kept a node id of %d bytes: %.40q", len(s.NodeID), s.NodeID)
			}
			if _, err := hub.Send(ctx, SendInput{Project: "p", SenderActor: "mallory", SenderSession: "x", To: "api-claude", Body: "hi", NodeID: nodeID}); err != nil {
				t.Fatal(err)
			}
			ask, err := hub.CreateAndWait(ctx, AskInput{Project: "p", SessionID: "s1", SessionName: "x", ActorID: "mallory",
				Kind: AskKindQuestion, Question: "ok?", NodeID: nodeID}, 20*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			if len(ask.NodeID) > 128 || strings.ContainsAny(ask.NodeID, "<>\n") {
				t.Errorf("ask kept a node id of %d bytes", len(ask.NodeID))
			}
		}
		msgs, err := hub.ListMessages(ctx, "p", "", "", 10)
		if err != nil || len(msgs) != 2 {
			t.Fatalf("messages = %d, %v", len(msgs), err)
		}
		for _, m := range msgs {
			if len(m.NodeID) > 128 || strings.ContainsAny(m.NodeID, "<>\n") {
				t.Errorf("mail kept a node id of %d bytes", len(m.NodeID))
			}
		}
		actions, err := hub.Actions(ctx, "p", "s1", 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range actions {
			if len(a.NodeID) > 128 {
				t.Errorf("the action log kept a node id of %d bytes", len(a.NodeID))
			}
		}
		// An ordinary node id is kept as given.
		s, err := hub.Touch(Heartbeat{SessionID: "s1", Project: "p", ActorID: "mallory", Name: "x", NodeID: " task-7 "})
		if err != nil || s.NodeID != "task-7" {
			t.Errorf("an ordinary node id: %q, %v", s.NodeID, err)
		}
	})
}
