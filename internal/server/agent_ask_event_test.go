package server

import (
	"context"
	"testing"
	"time"

	"nodevas/internal/agents"
	"nodevas/internal/realtime"
)

// A new question names its ask to the project's browsers, so whoever can
// answer is told; its expiry is only a panel reload.
func TestAgentEventsReachTheProjectRoom(t *testing.T) {
	httpServer, server := hubTestServer(t)
	peer := joinRoom(t, httpServer, "main")
	if _, err := server.agents.CreateAndWait(context.Background(), agents.AskInput{
		Project: server.pm.Store().Root(), SessionID: "s1", Question: "Ship it?",
	}, 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	peer.await("agents-changed", nil)
}

func TestNewAskIsAnnounced(t *testing.T) {
	httpServer, server := hubTestServer(t)
	peer := joinRoom(t, httpServer, "main")

	done := make(chan agents.Ask, 1)
	go func() {
		ask, _ := server.agents.CreateAndWait(context.Background(), agents.AskInput{
			Project: server.pm.Store().Root(), SessionID: "s1", SessionName: "claude-1",
			ActorID: "local", Agent: "claude", Question: "Ship it?",
		}, 300*time.Millisecond)
		done <- ask
	}()
	event := peer.await(agents.AskEvent, nil)
	ask := <-done
	if ask.ID == "" || event.ID != ask.ID || ask.Status != agents.AskExpired {
		t.Fatalf("event %+v, ask %+v", event, ask)
	}
	peer.await("agents-changed", func(realtime.Event) bool { return true })
	peer.awaitNone(agents.AskEvent, 200*time.Millisecond)
}
