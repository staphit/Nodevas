package server

import (
	"testing"
	"time"

	"nodevas/internal/realtime"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

func runRecord(project, id string) runs.Record {
	var r runs.Record
	r.Project, r.ID = project, id
	return r
}

// A run's new events reach its project with the last tool its agent called
// in them, so an open office redraws the desk without fetching the run; a
// batch without one says nothing about tools, and the push is otherwise the
// run-events it always was.
func TestRunEventsCarryTheLastTool(t *testing.T) {
	httpServer, server := hubTestServer(t)
	peer := joinRoom(t, httpServer, "main")
	forRun := func(event realtime.Event) bool { return event.ID == "run-1" }

	server.broadcastRunEvents(runRecord("main", "run-1"), runs.ToolSeen{Tool: "Bash", At: "2026-11-05T09:00:00.000Z"})
	event := peer.await(runnerwire.RunEventsEvent, forRun)
	if event.Tool != "Bash" || event.ToolAt != "2026-11-05T09:00:00.000Z" || event.Kind != "" {
		t.Fatalf("run-events = %+v, want tool Bash at 09:00", event)
	}

	server.broadcastRunEvents(runRecord("main", "run-1"), runs.ToolSeen{})
	if event := peer.await(runnerwire.RunEventsEvent, forRun); event.Tool != "" || event.ToolAt != "" {
		t.Fatalf("a batch without a tool: %+v", event)
	}

	// Another project's watchers hear nothing of it, as before.
	server.broadcastRunEvents(runRecord("other", "run-2"), runs.ToolSeen{Tool: "Read", At: "2026-11-05T09:00:01.000Z"})
	peer.awaitNone(runnerwire.RunEventsEvent, 200*time.Millisecond)
}

// The tools a run's agent calls are as private as the run: its dispatcher,
// its runner's owner and administrators hear them; another reader of the
// project hears only that the run has new events, as the runs API would
// show them nothing more (canSee).
func TestRunEventsToolsReachOnlyWhoMaySeeTheRun(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, runsHello(1))
	run := f.startedRun(t, conn)
	bea := dialHubAs(t, f.liveURL, f.bea)
	cal := dialHubAs(t, f.liveURL, f.cal)
	ann := dialHubAs(t, f.liveURL, f.ann)
	bea.await("hello", nil)
	cal.await("hello", nil)
	ann.await("hello", nil)

	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeEvents, RunID: run.ID, Events: []runnerwire.RunEvent{
		{Seq: 1, Type: runnerwire.EventToolCall, Tool: "mcp__stripe__create_refund", At: "2026-11-05T09:00:00.000Z"},
	}})
	forRun := func(event realtime.Event) bool { return event.ID == run.ID }
	if event := bea.await(runnerwire.RunEventsEvent, forRun); event.Tool != "mcp__stripe__create_refund" {
		t.Fatalf("the dispatcher's push = %+v, want the tool", event)
	}
	if event := ann.await(runnerwire.RunEventsEvent, forRun); event.Tool != "mcp__stripe__create_refund" {
		t.Fatalf("the administrator's push = %+v, want the tool", event)
	}
	if event := cal.await(runnerwire.RunEventsEvent, forRun); event.Tool != "" || event.ToolAt != "" {
		t.Fatalf("another reader's push = %+v, want no tool", event)
	}
}
