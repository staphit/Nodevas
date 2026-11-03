package server

import (
	"context"
	"testing"
	"time"

	"nodevas/internal/agents"
	"nodevas/internal/realtime"
)

// A new ask's event says what kind it is and which run asks, so a browser
// can word the toast ("wants to run Bash") before it fetches the ask.
func TestNewAskEventCarriesItsKindAndRun(t *testing.T) {
	httpServer, server := hubTestServer(t)
	peer := joinRoom(t, httpServer, "main")

	done := make(chan agents.Ask, 1)
	go func() {
		ask, _ := server.agents.CreateAndWait(context.Background(), agents.AskInput{
			Project: server.pm.Store().Root(), SessionID: "s1", SessionName: "claude-1",
			ActorID: "local", Agent: "claude", Kind: agents.AskKindPermission, Tool: "Bash",
			ToolInput: `{"command":"ls"}`, RunID: "run-9",
		}, 300*time.Millisecond)
		done <- ask
	}()
	event := peer.await(agents.AskEvent, func(realtime.Event) bool { return true })
	ask := <-done
	if event.ID != ask.ID || event.Kind != agents.AskKindPermission || event.RunID != "run-9" {
		t.Fatalf("event %+v, ask %+v", event, ask)
	}
}
