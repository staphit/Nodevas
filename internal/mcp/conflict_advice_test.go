package mcp

import (
	"strings"
	"testing"
)

// A 409 tells the agent what to do next. "Re-read it" fits a revision
// conflict only: a thread at its turn limit waits for a person, and a
// held message someone already decided has nothing left to do.
func TestConflictAdviceFitsTheConflict(t *testing.T) {
	loop := decodeError(409, []byte(`{"code":"LOOP_DETECTED","error":"agent-to-agent thread limit reached; waiting for human input"}`))
	if strings.Contains(loop.Message, "re-read") || !strings.Contains(loop.Message, "person") {
		t.Errorf("loop advice: %q", loop.Message)
	}
	held := decodeError(409, []byte(`{"code":"NOT_HELD","error":"message is not held: it is delivered"}`))
	if strings.Contains(held.Message, "re-read") || !strings.Contains(held.Message, "already") {
		t.Errorf("not-held advice: %q", held.Message)
	}
	revision := decodeError(409, []byte(`{"error":"node changed"}`))
	if !strings.Contains(revision.Message, "re-read") {
		t.Errorf("revision advice: %q", revision.Message)
	}
}
