package runs

import "strings"

// SimpleInstruction is what a run asked for in Simple mode (easy start;
// CreateRunRequest.Simple, CreateGlobalRunRequest.Simple) is also told. It
// is the hub's own constant, never the request's text, so the flag cannot
// carry anything else into a prompt, and it lifts no gate: the dispatch
// checks are exactly a run's without it. "index.html at the top of the
// folder" is what the result preview looks for first.
const SimpleInstruction = "The person who asked for this is not a programmer.\n" +
	"- Unless the task says otherwise, make something that opens directly in a web browser, with no build or install step: one index.html at the top of the folder, with its styles and scripts inside or next to it.\n" +
	"- When you finish, say in plain words what you made and how to open it. Use no technical terms.\n" +
	"- Answer in the language the task is written in."

// withSimple is extra with SimpleInstruction after it when simple is set,
// extra unchanged otherwise.
func withSimple(extra string, simple bool) string {
	if !simple {
		return extra
	}
	return strings.TrimSpace(strings.TrimSpace(extra) + "\n\n" + SimpleInstruction)
}
