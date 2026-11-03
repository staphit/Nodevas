package server

import (
	"context"
	"testing"
	"time"

	"nodevas/internal/agents"
)

// A prompt the hub allows on an earlier "allow for this run" waits on
// nobody: an agy run makes one per shell command, so it must not make every
// browser in the project reload its agent panel.
func TestAPromptAllowedForTheRunReloadsNoPanel(t *testing.T) {
	httpServer, server := hubTestServer(t)
	peer := joinRoom(t, httpServer, "main")
	ctx := context.Background()
	root := server.pm.Store().Root()
	if _, err := server.agents.Touch(agents.Heartbeat{SessionID: "r1", Project: root, ActorID: "local", Name: "app-agy-r1", RunID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	in := agents.AskInput{
		Project: root, SessionID: "r1", SessionName: "app-agy-r1", ActorID: "local", Agent: "antigravity",
		Kind: agents.AskKindPermission, Tool: "run_command", ToolInput: `{"CommandLine":"go test ./..."}`,
	}
	done := make(chan agents.Ask, 1)
	go func() {
		ask, _ := server.agents.CreateAndWait(ctx, in, 5*time.Second)
		done <- ask
	}()
	first := peer.await(agents.AskEvent, nil)
	if _, err := server.agents.AnswerWith(ctx, root, first.ID, agents.AnswerInput{Decision: agents.DecisionApproveRun}, agents.Viewer{ID: "local", Admin: true}); err != nil {
		t.Fatal(err)
	}
	<-done
	peer.sync("drained")

	again, err := server.agents.CreateAndWait(ctx, in, 5*time.Second)
	if err != nil || again.Status != agents.AskApproved || again.Note != agents.AllowedForRunNote {
		t.Fatalf("the same command again = %+v, %v; want allowed for this run", again, err)
	}
	peer.awaitNone("agents-changed", 300*time.Millisecond)
}
