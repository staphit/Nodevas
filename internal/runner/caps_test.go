package runner

import (
	"slices"
	"testing"

	"nodevas/internal/runnerwire"
)

func TestCapabilitiesAdvertiseContinue(t *testing.T) {
	if !slices.Contains(capabilities(nil), runnerwire.CapContinue) {
		t.Fatalf("capabilities(nil) = %v, want %q", capabilities(nil), runnerwire.CapContinue)
	}
	if !slices.Contains(capabilities(&Config{}), runnerwire.CapContinue) {
		t.Fatalf("capabilities(&Config{}) = %v, want %q", capabilities(&Config{}), runnerwire.CapContinue)
	}
	// CapContinue covers both headless follow-ups and interactive End/view-only,
	// so turning off either setting still advertises it.
	off := false
	cfg := &Config{Interactive: &off, GlobalRuns: &off}
	if !slices.Contains(capabilities(cfg), runnerwire.CapContinue) {
		t.Fatalf("capabilities(off cfg) = %v, want %q", capabilities(cfg), runnerwire.CapContinue)
	}
}
