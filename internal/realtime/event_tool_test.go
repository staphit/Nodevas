package realtime

import (
	"encoding/json"
	"strings"
	"testing"
)

// run-events carries the run's last tool as "tool" and "toolAt"; every other
// event, and a run-events without one, keeps the wire shape it had.
func TestEventCarriesToolOnlyWhenSet(t *testing.T) {
	raw, err := json.Marshal(Event{Type: "run-events", ID: "run-1", Tool: "Edit", ToolAt: "2026-11-05T09:00:00.000Z"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(raw), `{"type":"run-events","id":"run-1","tool":"Edit","toolAt":"2026-11-05T09:00:00.000Z"}`; got != want {
		t.Fatalf("event = %s, want %s", got, want)
	}
	raw, err = json.Marshal(Event{Type: "run-events", ID: "run-1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "tool") {
		t.Fatalf("an event without a tool carries one: %s", raw)
	}
}
