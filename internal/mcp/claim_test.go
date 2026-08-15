package mcp

import (
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

// The loop the tool descriptions promise, driven end to end over the protocol:
// see what is ready, take it, report it, and find the next task unblocked.
func TestAnAgentCanWorkTheQueueFromEndToEnd(t *testing.T) {
	url, _ := liveServer(t)
	session := mcpSession(t, url, "")

	var queue readyTasksOutput
	call(t, session, "get_ready_tasks", nil, &queue)
	if len(queue.Tasks) != 1 {
		t.Fatalf("tasks = %+v, want one", queue.Tasks)
	}
	first := queue.Tasks[0].ID

	var claimed claimTaskOutput
	call(t, session, "claim_task", map[string]any{"id": first}, &claimed)
	if claimed.Owner != "mcp:test" {
		t.Fatalf("owner = %q, want the actor this server runs as", claimed.Owner)
	}
	if claimed.Expires == "" {
		t.Fatal("the claim carries no expiry, so nothing could ever reclaim it")
	}

	// While held, the task is out of the queue: another agent asking now sees
	// nothing to do rather than the same task.
	call(t, session, "get_ready_tasks", nil, &queue)
	if len(queue.Tasks) != 0 {
		t.Fatalf("tasks = %+v, want the claimed task gone", queue.Tasks)
	}

	var reported setStatusOutput
	call(t, session, "set_node_status", map[string]any{
		"id": first, "status": "done", "note": "worked out the shape",
	}, &reported)
	if reported.Status != string(engine.StatusDone) {
		t.Fatalf("status = %q, want done", reported.Status)
	}

	// Finishing it released what it was blocking.
	call(t, session, "get_ready_tasks", nil, &queue)
	if len(queue.Tasks) != 1 || queue.Tasks[0].ID != "build" {
		t.Fatalf("tasks = %+v, want build unblocked", queue.Tasks)
	}
}

// An agent told only "already claimed" will retry against the node somebody
// else is holding. The refusal has to carry the next move.
func TestARefusedClaimSaysWhatToDoInstead(t *testing.T) {
	url, pm := liveServer(t)
	if _, err := pm.Store().ClaimNode(identity.Local, "design", "another-agent", 0, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	session := mcpSession(t, url, "")

	result := call(t, session, "claim_task", map[string]any{"id": "design"}, nil)
	if !result.IsError {
		t.Fatal("a claimed node was handed to a second agent")
	}
	message := errorText(result)
	if !strings.Contains(message, "another-agent") {
		t.Fatalf("error = %q, want it to name the holder", message)
	}
	if !strings.Contains(message, "pick a different task") {
		t.Fatalf("error = %q, want it to say what to do next", message)
	}
}

func TestReportingOnAnUnclaimedTaskIsRefused(t *testing.T) {
	url, _ := liveServer(t)
	session := mcpSession(t, url, "")

	result := call(t, session, "set_node_status", map[string]any{
		"id": "design", "status": "done",
	}, nil)
	if !result.IsError {
		t.Fatal("an unclaimed task was reported on")
	}
	if !strings.Contains(errorText(result), "claim_task") {
		t.Fatalf("error = %q, want it to name the tool to call first", errorText(result))
	}
}

// The vocabulary is narrowed on purpose: a model handed the full lifecycle
// invents one nobody asked for, and "ready" already has its own tool.
func TestOnlyAnOutcomeCanBeReported(t *testing.T) {
	url, _ := liveServer(t)
	session := mcpSession(t, url, "")
	call(t, session, "claim_task", map[string]any{"id": "design"}, nil)

	result := call(t, session, "set_node_status", map[string]any{
		"id": "design", "status": "in_progress",
	}, nil)
	if !result.IsError {
		t.Fatal("in_progress was accepted as a reported outcome")
	}
	if !strings.Contains(errorText(result), "release_task") {
		t.Fatalf("error = %q, want it to point at the tool for giving work back", errorText(result))
	}
}

func TestGivingATaskBackPutsItInTheQueueAgain(t *testing.T) {
	url, _ := liveServer(t)
	session := mcpSession(t, url, "")
	call(t, session, "claim_task", map[string]any{"id": "design"}, nil)

	var released releaseTaskOutput
	call(t, session, "release_task", map[string]any{"id": "design"}, &released)

	var queue readyTasksOutput
	call(t, session, "get_ready_tasks", nil, &queue)
	if len(queue.Tasks) != 1 || queue.Tasks[0].ID != "design" {
		t.Fatalf("tasks = %+v, want design back in the queue", queue.Tasks)
	}
}

// An agent that could name the claim owner could report on work it does not
// hold, which is the whole thing claiming exists to prevent.
//
// Two independent defences, tested separately because either one alone would
// be enough to make the other look like it worked.
func TestAnAgentCannotNameItselfAsSomebodyElse(t *testing.T) {
	url, pm := liveServer(t)
	if _, err := pm.Store().ClaimNode(identity.Local, "design", "the-other-agent", 0, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	session := mcpSession(t, url, "")

	// The tool schema is closed, so the argument cannot even be sent.
	rejected := call(t, session, "set_node_status", map[string]any{
		"id": "design", "status": "done", "owner": "the-other-agent",
	}, nil)
	if !rejected.IsError || !strings.Contains(errorText(rejected), "additional properties") {
		t.Fatalf("the schema accepted an owner argument: %q", errorText(rejected))
	}

	// And with it left out, the owner the client sends is its own, so the
	// server refuses on the ownership check rather than the schema.
	refused := call(t, session, "set_node_status", map[string]any{
		"id": "design", "status": "done",
	}, nil)
	if !refused.IsError {
		t.Fatal("an agent reported on a claim it does not hold")
	}
	if !strings.Contains(errorText(refused), "the-other-agent") {
		t.Fatalf("error = %q, want the refusal to name the real holder", errorText(refused))
	}
}
