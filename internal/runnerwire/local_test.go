package runnerwire

import "testing"

// The local runner's name is a fixed id, not display text: hubs already store
// it, runs copy it into runner_name, and clients translate exactly this value
// into the viewer's language. Changing it would leave every existing local
// runner, and every run it took, shown untranslated.
func TestLocalRunnerNameIsAFixedID(t *testing.T) {
	if LocalRunnerName != "this machine" {
		t.Fatalf("LocalRunnerName = %q; existing hubs and the web expect %q", LocalRunnerName, "this machine")
	}
}
