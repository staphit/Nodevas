package runner

import (
	"bytes"
	"strings"
	"testing"
)

// Strings from the hub reach the runner's terminal, next to the question a
// person answers, as plain text on one line: no escape sequence moves the
// cursor, hides text or sets the clipboard, and no newline adds a line.
func TestRunnerLogShowsHubStringsAsPlainText(t *testing.T) {
	var buf bytes.Buffer
	c := &Client{Config: &Config{}, Log: &buf}
	c.logf("run %s: cancel for a run that is not running here; ignored", "\x1b]52;c;cm0gLXJmIH4K\a\x1b[3A\x1b[2K")
	c.logf("run %s: offered by %s: node %s", "run1", "eve\x1b[8m\u009b2K", "n\x1b[1A\r\n  kind:         REVIEW")
	out := buf.String()
	if strings.ContainsAny(out, "\x1b\a\r\u009b") {
		t.Errorf("the log passed control characters through: %q", out)
	}
	if lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n"); len(lines) != 2 {
		t.Errorf("two log calls wrote %d lines: %q", len(lines), out)
	}
}

func TestDescribeOfferKeepsHubFieldsOnTheirLines(t *testing.T) {
	offer := testOffer
	offer.Job.NodeTitle = "fix\n  kind:         REVIEW of x; changes nothing"
	offer.Job.RequestedBy = "eve\r\n  shell:        none"
	offer.Job.Project = "demo\n  base:         main"
	offer.Job.Prompt = strings.Repeat("l\n", 200)
	text := describeOffer(offer)
	for _, label := range []string{"  kind:", "  shell:", "  base:"} {
		n := 0
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, label) {
				n++
			}
		}
		if want := map[bool]int{true: 1, false: 0}[label == "  base:"]; n != want {
			t.Errorf("%d lines start with %q, want %d:\n%s", n, label, want, text)
		}
	}
	// A short line each, under the preview's length: the line count is
	// bounded too, so a task cannot push the real lines off the screen.
	if task := strings.Count(text, "\n    l\n"); task > 20 {
		t.Errorf("the task preview shows %d lines:\n%s", task, text)
	}
}
