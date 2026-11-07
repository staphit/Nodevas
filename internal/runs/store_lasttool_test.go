package runs

// Last tool: a run remembers the last tool its agent called (or "thinking")
// and when, from the events it stores, so the building can draw what the
// agent is doing without reading its event log.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

func TestAppendEventsRecordsTheLastToolOfABatch(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := createRun(t, s, "n1")
	// The hub's clock, three hours ahead of the runner's: the time kept is
	// when the hub heard of the tool, on the clock a session's tool is also
	// stamped with, not the runner's own (which may be minutes off).
	hub := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return hub }
	hubAt := func() string { return hub.Format(stampLayout) }

	seen, err := s.appendEvents(ctx, r.ID, []runnerwire.RunEvent{
		{Seq: 1, Type: runnerwire.EventToolCall, Tool: "Read", At: "2026-11-05T09:00:00.000Z"},
		{Seq: 2, Type: runnerwire.EventToolResult, ToolID: "t1", At: "2026-11-05T09:00:01.000Z"},
		{Seq: 3, Type: runnerwire.EventToolCall, Tool: " Edit ", At: "2026-11-05T09:00:02.000Z"},
		{Seq: 4, Type: runnerwire.EventText, Text: "done", At: "2026-11-05T09:00:03.000Z"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen.added != 4 || seen.tool != (ToolSeen{Tool: "Edit", At: hubAt()}) {
		t.Fatalf("appended %+v, want 4 events and Edit at the hub's 12:00", seen)
	}
	got, err := s.Get(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastTool != "Edit" || got.LastToolAt != hubAt() {
		t.Fatalf("run's last tool = %q at %q, want Edit at the hub's 12:00", got.LastTool, got.LastToolAt)
	}

	// A thinking event is the agent thinking, after the tool before it.
	hub = hub.Add(time.Minute)
	if _, err := s.AppendEvents(ctx, r.ID, []runnerwire.RunEvent{
		{Seq: 5, Type: runnerwire.EventThinking, Text: "hmm", At: "2026-11-05T09:01:00.000Z"},
	}); err != nil {
		t.Fatal(err)
	}
	thoughtAt := hubAt()
	if got, _ := s.Get(ctx, r.ID); got.LastTool != LastToolThinking || got.LastToolAt != thoughtAt {
		t.Fatalf("after thinking: %q at %q", got.LastTool, got.LastToolAt)
	}

	// A batch with neither leaves the run as it was; so does a resend of an
	// earlier tool call.
	hub = hub.Add(time.Minute)
	seen, err = s.appendEvents(ctx, r.ID, []runnerwire.RunEvent{
		{Seq: 3, Type: runnerwire.EventToolCall, Tool: "Bash", At: "2026-11-05T09:02:00.000Z"},
		{Seq: 6, Type: runnerwire.EventText, Text: "more", At: "2026-11-05T09:02:01.000Z"},
		{Seq: 7, Type: runnerwire.EventToolCall, Tool: "   ", At: "2026-11-05T09:02:02.000Z"},
	})
	if err != nil || seen.tool != (ToolSeen{}) {
		t.Fatalf("batch without a tool: %+v, %v", seen, err)
	}
	if got, _ := s.Get(ctx, r.ID); got.LastTool != LastToolThinking || got.LastToolAt != thoughtAt {
		t.Fatalf("a batch without a tool changed the run: %q at %q", got.LastTool, got.LastToolAt)
	}

	// The wire carries it; a run that never called one omits it.
	if got, _ := s.Get(ctx, r.ID); !strings.Contains(mustJSON(t, got.Run), `"lastTool":"thinking"`) {
		t.Fatalf("run JSON lacks lastTool: %s", mustJSON(t, got.Run))
	}
	fresh := createRun(t, s, "n2")
	if body := mustJSON(t, fresh.Run); strings.Contains(body, "lastTool") {
		t.Fatalf("a run with no tool carries one: %s", body)
	}
}

// Past MaxRunEvents nothing more is stored, but what the agent is doing still
// is: the run's last tool keeps up with it.
func TestAppendEventsRecordsTheLastToolPastTheCap(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := createRun(t, s, "n1")
	if _, err := s.db.ExecContext(ctx, `INSERT INTO run_events (run_id, seq, type, at, payload)
SELECT ?, g, 'text', '', '{}' FROM generate_series(1, ?::bigint) AS g`, r.ID, runnerwire.MaxRunEvents); err != nil {
		t.Fatal(err)
	}
	hub := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return hub }
	n, err := s.AppendEvents(ctx, r.ID, []runnerwire.RunEvent{
		{Seq: runnerwire.MaxRunEvents + 1, Type: runnerwire.EventToolCall, Tool: "Grep", At: "2026-11-05T09:55:00.000Z"},
	})
	if err != nil || n != 0 {
		t.Fatalf("append past the cap = %d, %v, want 0", n, err)
	}
	if got, _ := s.Get(ctx, r.ID); got.LastTool != "Grep" || got.LastToolAt != hub.Format(stampLayout) {
		t.Fatalf("past the cap: %q at %q, want Grep", got.LastTool, got.LastToolAt)
	}
}

// The dispatcher announces a batch's last tool with its run-events push, and
// nothing when the batch had none.
func TestDispatcherAnnouncesTheLastTool(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := startRun(t, s)
	hub := time.Date(2026, 11, 5, 11, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return hub }
	var announced []ToolSeen
	d := NewDispatcher(s, nil, nil, func(run Record, seen ToolSeen) {
		if run.Project != "." || run.ID != r.ID {
			t.Errorf("announced %s/%s", run.Project, run.ID)
		}
		announced = append(announced, seen)
	})
	if err := d.Events(ctx, "r1", r.ID, []runnerwire.RunEvent{
		{Seq: 1, Type: runnerwire.EventToolCall, Tool: "WebFetch", At: "2026-11-05T10:55:00.000Z"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.Events(ctx, "r1", r.ID, []runnerwire.RunEvent{
		{Seq: 2, Type: runnerwire.EventText, Text: "read it", At: "2026-11-05T11:00:01.000Z"},
	}); err != nil {
		t.Fatal(err)
	}
	want := []ToolSeen{{Tool: "WebFetch", At: hub.Format(stampLayout)}, {}}
	if len(announced) != 2 || announced[0] != want[0] || announced[1] != want[1] {
		t.Fatalf("announced %+v, want %+v", announced, want)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
